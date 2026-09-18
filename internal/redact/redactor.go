package redact

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Errors this package returns. They are package sentinels rather than
// *types.Error values on purpose: CONTRACT §2 converts to the agent-facing error
// shape at the API boundary and nowhere else.
var (
	// ErrUnknownToken is a token this session cannot resolve — never minted,
	// expired, or minted by a session that has since gone. The daemon maps it to
	// types.CodeStaleToken (R3.4d).
	ErrUnknownToken = errors.New("redact: token is not bound in this session")
	// ErrCollision is R8.3c at mint time: the token this value would take is
	// already bound to a different value, and the first binding stands.
	ErrCollision = errors.New("redact: token already bound to a different value")
	// ErrNoConnection is returned when a statement needs a token key and the
	// context carries no connection to fetch one for.
	ErrNoConnection = errors.New("redact: statement context carries no connection id")
)

// PolicyLookup is the slice of ports.Catalog this package needs: the arguments a
// policy carries that types.ColumnMeta does not. ColumnMeta says *which* policy
// applies; the namespace a token hashes under, the component a partial keeps and
// the key paths of a json column live in the catalog entry, and they are
// resolved by the server's own column identity, never by output name (R7.5a).
type PolicyLookup interface {
	// Lookup resolves one column's policy arguments — namespace, partial form,
	// JSON paths — for a connection. The connection is a parameter because a
	// (tableOID, attnum) pair means nothing without it: two databases reuse OIDs
	// freely, and the daemon serves all of them.
	Lookup(connID string, tableOID uint32, attNum uint16) (types.ColumnPolicy, bool)
}

// Config constructs a [Redactor].
type Config struct {
	// Keys supplies the per-connection HMAC key. Required for token policies.
	Keys KeySource
	// Detector is the rules pass a scan column runs. Required: a scan column
	// with no detector would pass free text through unexamined.
	Detector ports.Detector
	// Policies resolves a column's policy arguments. Optional; without it a
	// token column has no namespace and falls back to redact.
	Policies PolicyLookup
	// Namespaces declares per-namespace normalization (R8.3b). A namespace not
	// listed gets the floor: NFC and trim, no case folding.
	Namespaces map[string]NamespaceRule
	// TTL is how long a binding survives without being used. Zero uses
	// DefaultTTL. The map is memory-only either way (CONTRACT §4 rule 9).
	TTL time.Duration
	// MinMatch is the shortest value the emission scan will look for as a
	// substring (R8.4c's minimum length threshold). Zero uses DefaultMinMatch.
	// Shorter values are matched against the whole cell instead.
	MinMatch int
	// Now is the clock, for tests.
	Now func() time.Time
}

// DefaultTTL and DefaultMinMatch are the values Config's zero fields take.
const (
	DefaultTTL      = time.Hour
	DefaultMinMatch = 4
)

// Redactor is G7. Every value keeper emits passes through [Redactor.Apply].
type Redactor struct {
	keys     KeySource
	detector ports.Detector
	policies PolicyLookup
	ns       atomic.Pointer[map[string]NamespaceRule]
	ttl      time.Duration
	minMatch int
	now      func() time.Time
	// hexLen is TokenHexLen in every build. Tests inside this package shorten it
	// to reach R8.3c's collision path; the keeper_short_token build tag does the
	// same for tests that run the daemon.
	hexLen int

	mu       sync.RWMutex
	sessions map[string]*session
}

var _ ports.Redactor = (*Redactor)(nil)

// New builds a Redactor.
func New(cfg Config) (*Redactor, error) {
	if cfg.Detector == nil {
		return nil, errors.New("redact: a detector is required; a scan column without one emits unexamined free text")
	}
	r := &Redactor{
		keys:     cfg.Keys,
		detector: cfg.Detector,
		policies: cfg.Policies,
		ttl:      cmpOr(cfg.TTL, DefaultTTL),
		minMatch: cmpOr(cfg.MinMatch, DefaultMinMatch),
		now:      cfg.Now,
		hexLen:   TokenHexLen,
		sessions: map[string]*session{},
	}
	if r.now == nil {
		r.now = time.Now
	}
	r.SetNamespaces(cfg.Namespaces)
	return r, nil
}

func cmpOr[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}

// SetNamespaces replaces the namespace rules, for a catalog reload. Rules are
// read on every hash, so the swap is atomic rather than locked.
func (r *Redactor) SetNamespaces(m map[string]NamespaceRule) {
	c := maps.Clone(m)
	if c == nil {
		c = map[string]NamespaceRule{}
	}
	r.ns.Store(&c)
}

func (r *Redactor) rule(namespace string) NamespaceRule {
	m := r.ns.Load()
	if m == nil {
		return NamespaceRule{}
	}
	return (*m)[namespace]
}

func (r *Redactor) sessionFor(id string) *session {
	r.mu.RLock()
	s, ok := r.sessions[id]
	r.mu.RUnlock()
	if ok {
		return s
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		return s
	}
	s = newSession()
	r.sessions[id] = s
	return s
}

// DropSession clears a session's reverse map. The daemon calls it when the
// socket closes; a daemon restart does the same for every session at once,
// which is why a token from yesterday no longer resolves (§8.4).
func (r *Redactor) DropSession(sessionID string) {
	r.mu.Lock()
	delete(r.sessions, sessionID)
	r.mu.Unlock()
}

// Mint stores a value in a session's reverse map and returns its token, for
// §8.7's local input flow. The raw value never reaches the agent: only the
// token and its namespace do (R8.7b).
func (r *Redactor) Mint(ctx context.Context, sessionID, connID, namespace, value string) (string, error) {
	if err := validNamespace(namespace); err != nil {
		return "", err
	}
	if connID == "" {
		return "", ErrNoConnection
	}
	key, version, err := r.key(ctx, connID)
	if err != nil {
		return "", err
	}
	// A human typing into a form is the least consistent source there is
	// (R8.3b), so the floor — NFC and trim — is applied to the value that will
	// be bound as a parameter, not only to the one that is hashed.
	stored := Normalize(value, NamespaceRule{})
	normalized := Normalize(value, r.rule(namespace))
	now := r.now()
	b := &binding{
		token:      FormatToken(namespace, version, hmacTag(key, namespace, normalized, r.hexLen)),
		value:      stored,
		normalized: normalized,
		namespace:  namespace,
		version:    version,
		policy:     types.PolicyToken,
		expires:    now.Add(r.ttl),
	}
	bound, collision := r.sessionFor(sessionID).bind(b, now)
	if collision {
		return "", fmt.Errorf("%w: %s", ErrCollision, bound.token)
	}
	return bound.token, nil
}

// Resolve turns a token back into the value it stands for, so the pipeline can
// bind it as a parameter (§6.2). keeper never substitutes the value into
// statement text.
//
// The policy it returns is the one that caused the value to be tokenized, and
// it is what R8.4b's output floor is computed from.
func (r *Redactor) Resolve(ctx context.Context, sessionID, token string) (string, types.Policy, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if _, _, _, ok := ParseToken(token); !ok {
		return "", "", ErrUnknownToken
	}
	r.mu.RLock()
	s, ok := r.sessions[sessionID]
	r.mu.RUnlock()
	if !ok {
		return "", "", ErrUnknownToken
	}
	b, ok := s.resolve(token, r.now(), r.ttl)
	if !ok {
		return "", "", ErrUnknownToken
	}
	return b.value, b.policy, nil
}

func (r *Redactor) key(ctx context.Context, connID string) ([]byte, int, error) {
	if r.keys == nil {
		return nil, 0, ErrNoKeySource
	}
	key, current, err := r.keys.TokenKey(ctx, connID, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("redact: token key: %w", err)
	}
	if len(key) == 0 {
		return nil, 0, ErrNoKeySource
	}
	return key, current, nil
}

// columnPlan is one output column's resolved treatment.
type columnPlan struct {
	name      string
	policy    types.Policy
	namespace string
	form      types.PartialForm
	paths     map[string]types.ColumnPolicy
	family    ports.TypeFamily
	basis     types.Basis
	// floor is R8.4b's inherited policy, kept so json key paths inherit it too.
	floor types.Policy

	key     []byte
	version int

	spans      int
	collisions int
}

// Apply rewrites rows in place according to the policies the pipeline resolved,
// and reports what it did.
//
// It runs in two passes, and the order is the point.
//
// Pass one applies each column's policy, which is also what mints token
// bindings into the session's reverse map. Pass two scans every string leaving
// the result — including leaves inside json, array and row values — against
// that map and re-tokenizes any resolved value it finds (R8.4a, R8.4c). Doing
// the scan second means a value tokenized in one column is also caught where it
// appears in cleartext in another column of the same result.
//
// A `drop` column's cells are set to nil here and its transform reports
// `drop`; removing the column from the response is [DropColumns], because the
// ports.Redactor signature cannot shorten the caller's column slice.
func (r *Redactor) Apply(ctx context.Context, sessionID string, cols []types.ColumnMeta, rows [][]any) (map[string]types.Transform, error) {
	st, _ := StatementFrom(ctx)
	now := r.now()
	sess := r.sessionFor(sessionID)

	plans, err := r.plan(ctx, st, cols)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		for j := range plans {
			if j >= len(row) {
				break
			}
			row[j] = r.applyCell(ctx, sess, &plans[j], row[j], now)
		}
	}

	// R8.4a. This runs whether or not this statement resolved a token
	// parameter: the map also holds values minted through §8.7 and values
	// tokenized earlier in the session, and every one of them is a value the
	// agent is not supposed to be able to read back.
	if !sess.empty() {
		rt := &retokenizer{idx: sess.buildIndex(r.minMatch, now)}
		if rt.active() {
			for _, row := range rows {
				for j := range row {
					if j < len(plans) && plans[j].policy == types.PolicyDrop {
						continue
					}
					// A re-tokenized value is counted in SpansRedacted
					// alongside the rules pass's hits. Both are "a value
					// replaced inside this column's text", and the frozen
					// Transform has no separate field; leaving it uncounted
					// would make the emission scan invisible in the response.
					before := rt.hits
					row[j] = rewriteStrings(row[j], rt.rewrite)
					if j < len(plans) {
						plans[j].spans += rt.hits - before
					}
				}
			}
		}
	}

	return transformsOf(plans), nil
}

func (r *Redactor) plan(ctx context.Context, st Statement, cols []types.ColumnMeta) ([]columnPlan, error) {
	// R8.4b: if any parameter was resolved from a token, every text-like or
	// unknown-typed output column inherits at least that token's source policy,
	// regardless of which relations the statement reads. Numeric, boolean,
	// date/time and uuid columns do not: a text value does not fit in a bigint,
	// which is what keeps count(*) and an unrelated numeric status readable.
	floor := types.PolicyAllow
	for _, p := range st.Params {
		if p.Policy.Rank() > floor.Rank() {
			floor = p.Policy
		}
	}

	plans := make([]columnPlan, len(cols))
	needKey := false
	for j, c := range cols {
		pl := &plans[j]
		pl.name = c.Name
		pl.policy = c.Policy
		pl.family = Family(c.Type)

		if r.policies != nil && c.TableOID != 0 && st.ConnectionID != "" {
			if cp, ok := r.policies.Lookup(st.ConnectionID, c.TableOID, c.AttNum); ok {
				pl.namespace = cp.Namespace
				pl.form = cp.Form
				pl.paths = cp.Paths
				pl.basis = types.BasisCatalog
			} else {
				pl.basis = types.BasisUnknown
			}
		} else if c.TableOID == 0 {
			pl.basis = types.BasisInherited
		} else {
			pl.basis = types.BasisUnknown
		}

		// A lookup that could not be made because no connection was attached is
		// a wiring fault, not a data condition. Letting it fall through to the
		// no-namespace rule below would turn every token column in the result
		// into a redaction — fail-closed, but silently, and it would look like a
		// policy decision rather than the missing context it is.
		if r.policies != nil && c.TableOID != 0 && st.ConnectionID == "" && pl.policy == types.PolicyToken {
			needKey = true
		}

		pl.floor = floor
		if floor != types.PolicyAllow && pl.policy != types.PolicyDrop && TextLike(pl.family) {
			if raised := types.Strictest(pl.policy, floor); raised != pl.policy {
				pl.policy = raised
				pl.basis = types.BasisParameter
			}
		}

		// A token needs a namespace and a partial needs a form. R5.2b and R5.2d
		// make their absence a catalog validation error, so reaching here means
		// something upstream let one through — or the floor above raised a
		// column that has no namespace of its own. Either way the value does
		// not survive: tokenizing under a borrowed namespace would put two
		// different classifications in one token space, and R7.6b already
		// resolves an un-namespaceable token to redact.
		if pl.policy == types.PolicyToken && pl.namespace == "" {
			pl.policy = types.PolicyRedact
		}
		if pl.policy == types.PolicyPartial && !pl.form.Valid() {
			pl.policy = types.PolicyRedact
		}
		if pl.policy == types.PolicyScan && pl.basis == types.BasisCatalog {
			pl.basis = types.BasisRules
		}
		if pl.policy == types.PolicyToken || pathsNeedKey(pl.paths, pl.policy) {
			needKey = true
		}
	}

	if needKey {
		if st.ConnectionID == "" {
			return nil, ErrNoConnection
		}
		key, version, err := r.key(ctx, st.ConnectionID)
		if err != nil {
			return nil, err
		}
		for j := range plans {
			plans[j].key = key
			plans[j].version = version
		}
	}
	return plans, nil
}

func pathsNeedKey(paths map[string]types.ColumnPolicy, colPolicy types.Policy) bool {
	if colPolicy == types.PolicyDrop {
		return false
	}
	for _, p := range paths {
		if p.Policy == types.PolicyToken {
			return true
		}
	}
	return false
}

func (r *Redactor) applyCell(ctx context.Context, sess *session, pl *columnPlan, v any, now time.Time) any {
	if pl.policy == types.PolicyDrop {
		return nil
	}
	if v == nil {
		return nil
	}
	if len(pl.paths) > 0 {
		if out, ok := r.applyPaths(ctx, sess, pl, v, now); ok {
			return out
		}
	}
	switch pl.policy {
	case types.PolicyAllow:
		return v
	case types.PolicyRedact:
		return RedactedMarker
	case types.PolicyToken:
		return r.tokenize(sess, pl, stringOf(v), now)
	case types.PolicyPartial:
		return Partial(pl.form, stringOf(v))
	case types.PolicyScan:
		return rewriteStrings(v, func(s string) string {
			out, n := r.redactSpans(ctx, s)
			pl.spans += n
			return out
		})
	default:
		// An unknown policy is not a reason to emit the value.
		return RedactedMarker
	}
}

// tokenize returns the token for a value, or [RedactedMarker] when the token is
// already bound to a different value.
//
// R8.3c: the first binding stands. The statement completes at tier 1 and the
// collision is counted; the binding is never moved, because a token already in
// the agent's context must keep meaning what it meant. Refusing the whole
// statement would prevent the aliasing too, and was an earlier draft's wording,
// but it fails a query over an event the agent did not cause and cannot fix.
func (r *Redactor) tokenize(sess *session, pl *columnPlan, value string, now time.Time) string {
	if pl.key == nil {
		return RedactedMarker
	}
	normalized := Normalize(value, r.rule(pl.namespace))
	b := &binding{
		token:      FormatToken(pl.namespace, pl.version, hmacTag(pl.key, pl.namespace, normalized, r.hexLen)),
		value:      value,
		normalized: normalized,
		namespace:  pl.namespace,
		version:    pl.version,
		policy:     types.PolicyToken,
		expires:    now.Add(r.ttl),
	}
	bound, collision := sess.bind(b, now)
	if collision {
		pl.collisions++
		return RedactedMarker
	}
	return bound.token
}

// redactSpans replaces the rules pass's matched spans and nothing else (R8.5a).
// One email in one row of `notes` must not mask every note in the result.
func (r *Redactor) redactSpans(ctx context.Context, s string) (string, int) {
	if s == "" {
		return s, 0
	}
	spans, err := r.detector.Scan(ctx, s)
	if err != nil {
		// A detector that failed examined nothing, and free text that was not
		// examined is not free text that is clean. The cell is masked and the
		// statement completes; R8.5h forbids blocking on a detector.
		return RedactedMarker, 1
	}
	if len(spans) == 0 {
		return s, 0
	}
	var b []byte
	at := 0
	n := 0
	for _, sp := range spans {
		if sp.Start < at || sp.End > len(s) || sp.End <= sp.Start {
			continue
		}
		b = append(b, s[at:sp.Start]...)
		b = append(b, RedactedSpan(sp.Type)...)
		at = sp.End
		n++
	}
	b = append(b, s[at:]...)
	return string(b), n
}

func stringOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(v)
	}
}

// transformsOf builds the response's transforms map.
//
// The map is keyed by output column name, which two columns of one result can
// share (SELECT a.email, b.email). Where that happens the entries merge to the
// strictest of the two and their counts sum: overstating what was masked is
// safe, inventing a key that is not a column name is not, and the frozen
// response shape offers no third option.
func transformsOf(plans []columnPlan) map[string]types.Transform {
	out := make(map[string]types.Transform, len(plans))
	for i := range plans {
		pl := &plans[i]
		t := types.Transform{
			Policy:        pl.policy,
			Basis:         pl.basis,
			SpansRedacted: pl.spans,
			Collisions:    pl.collisions,
		}
		if pl.policy == types.PolicyToken {
			t.Namespace = pl.namespace
		}
		if pl.policy == types.PolicyPartial {
			t.Form = pl.form
		}
		if prev, ok := out[pl.name]; ok {
			t = mergeTransform(prev, t)
		}
		out[pl.name] = t
	}
	return out
}

func mergeTransform(a, b types.Transform) types.Transform {
	out := a
	if b.Policy == types.PolicyDrop || b.Policy.Rank() > a.Policy.Rank() {
		out.Policy = b.Policy
		out.Namespace = b.Namespace
		out.Form = b.Form
		out.Basis = b.Basis
	}
	out.SpansRedacted = a.SpansRedacted + b.SpansRedacted
	out.Collisions = a.Collisions + b.Collisions
	out.SampleSize = max(a.SampleSize, b.SampleSize)
	return out
}

// DropColumns removes every column whose transform reports `drop`, from both
// the column list and every row, and returns the shortened pair.
//
// ports.Redactor.Apply cannot do this itself: its cols parameter is a slice
// header the caller owns, and shortening it here would not shorten the
// caller's. Apply nils the cells so nothing survives either way; this is what
// makes the column absent from the response (§8.2).
//
// The dropped column keeps its entry in transforms. An agent that selected a
// column and got no column back needs to be told why, and "policy: drop" is the
// answer that sends it to the catalog rather than into a retry loop.
func DropColumns(cols []types.ColumnMeta, rows [][]any, transforms map[string]types.Transform) ([]types.ColumnMeta, [][]any) {
	keep := make([]int, 0, len(cols))
	for j, c := range cols {
		if t, ok := transforms[c.Name]; ok && t.Policy == types.PolicyDrop {
			continue
		}
		keep = append(keep, j)
	}
	if len(keep) == len(cols) {
		return cols, rows
	}
	outCols := make([]types.ColumnMeta, 0, len(keep))
	for _, j := range keep {
		outCols = append(outCols, cols[j])
	}
	for i, row := range rows {
		nr := make([]any, 0, len(keep))
		for _, j := range keep {
			if j < len(row) {
				nr = append(nr, row[j])
			}
		}
		rows[i] = nr
	}
	return outCols, rows
}
