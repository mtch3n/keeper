package redact

import (
	"cmp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mtchen/keeper/internal/rules"
	"github.com/mtchen/keeper/internal/types"
)

// binding is one value the session may resolve, and the token it resolves from.
type binding struct {
	token      string
	value      string // the value as bound and as it would leave keeper
	normalized string // the value as hashed, per its namespace's rule
	namespace  string
	version    int
	policy     types.Policy // the policy that caused this value to be tokenized
	expires    time.Time
	// seq is the order the binding was created in. Two namespaces can bind the
	// same value to two different tokens; the emission scan has to pick one,
	// and picking the first one bound is both deterministic and consistent with
	// R8.3c's rule that the first binding is the one that stands.
	seq uint64
}

// session is one client connection's reverse map (§8.4).
//
// In memory, TTL'd, never persisted — CONTRACT §4 rule 9, and SPEC's reason for
// it: a persistent map is a growing local PII store and a target. It dies with
// the socket, which is also why R3.4d's error says what it says. A token the
// agent recorded yesterday is deterministic but no longer resolvable.
type session struct {
	mu      sync.Mutex
	byToken map[string]*binding
	nextSeq uint64

	// The emission index, rebuilt lazily. owners[i] is the binding that the
	// automaton's pattern i belongs to. A binding contributes both its bound
	// value and its normalized form when they differ, because the database
	// returns the former and the token was computed from the latter.
	matcher *rules.Matcher
	owners  []*binding
	// short holds values below the substring threshold, matched by whole-cell
	// equality instead. Dropping them entirely would be a hole; substring
	// matching them would rewrite every cell containing "Li".
	short map[string]*binding
	dirty bool
}

func newSession() *session {
	return &session{
		byToken: map[string]*binding{},
		short:   map[string]*binding{},
	}
}

// bind records a value under a token. It returns the existing binding when one
// is already present, and reports a collision when the token is taken by a
// different value.
//
// R8.3c: on collision the *first* binding stands for the life of the session.
// The binding never moves, so a token already in the agent's context keeps
// meaning what it meant when it was minted; the second value is the one that
// loses, and it is emitted as redact rather than aliased onto someone else.
func (s *session) bind(b *binding, now time.Time) (bound *binding, collision bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)

	if existing, ok := s.byToken[b.token]; ok {
		if existing.namespace == b.namespace && existing.normalized == b.normalized {
			if b.expires.After(existing.expires) {
				existing.expires = b.expires
			}
			return existing, false
		}
		return existing, true
	}
	b.seq = s.nextSeq
	s.nextSeq++
	s.byToken[b.token] = b
	s.dirty = true
	return b, false
}

// resolve returns the value a token stands for, refreshing its lifetime: a
// session still using a token has not finished with it.
func (s *session) resolve(token string, now time.Time, ttl time.Duration) (*binding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.byToken[token]
	if !ok || !b.expires.After(now) {
		return nil, false
	}
	b.expires = now.Add(ttl)
	return b, true
}

// sweepLocked drops expired bindings. now is passed in rather than read, so one
// statement sees one clock.
func (s *session) sweepLocked(now time.Time) {
	for tok, b := range s.byToken {
		if !b.expires.After(now) {
			delete(s.byToken, tok)
			s.dirty = true
		}
	}
}

// index is the emission-scan view of the map: an Aho-Corasick automaton over
// every resolved value, plus the short-value exact set.
type index struct {
	matcher *rules.Matcher
	owners  []*binding
	short   map[string]*binding
}

// buildIndex returns the scan index, rebuilding it if the map changed.
//
// Aho-Corasick and not a loop of strings.Contains: R8.4a scans every text-like
// cell of every result against every value in the map, and the loop is
// O(cells × values × cell length). One automaton is O(cells × cell length) no
// matter how many values the session has accumulated.
func (s *session) buildIndex(minMatch int, now time.Time) index {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	if s.dirty || s.matcher == nil {
		s.rebuildLocked(minMatch)
	}
	return index{matcher: s.matcher, owners: s.owners, short: s.short}
}

// rebuildLocked builds a fresh index. Fresh, not reused: buildIndex hands the
// slices and the map out to a scan that runs without the lock, and a later
// rebuild that truncated them in place would be a race with a reader.
func (s *session) rebuildLocked(minMatch int) {
	patterns := make([]string, 0, len(s.byToken)*2)
	owners := make([]*binding, 0, len(s.byToken)*2)
	short := make(map[string]*binding)
	seen := make(map[string]bool, len(s.byToken)*2)
	add := func(v string, b *binding) {
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		if len(v) < minMatch {
			// Whole-cell matching is case-insensitive, so two spellings of one
			// short value share a key. First bound wins, as everywhere else.
			if k := strings.ToLower(v); short[k] == nil {
				short[k] = b
			}
			return
		}
		patterns = append(patterns, v)
		owners = append(owners, b)
	}
	ordered := make([]*binding, 0, len(s.byToken))
	for _, b := range s.byToken {
		ordered = append(ordered, b)
	}
	slices.SortFunc(ordered, func(a, b *binding) int { return cmp.Compare(a.seq, b.seq) })
	for _, b := range ordered {
		add(b.value, b)
		add(b.normalized, b)
	}
	s.owners = owners
	s.short = short
	s.matcher = rules.NewMatcher(patterns, rules.MatcherOptions{Fold: true})
	s.dirty = false
}

// empty reports whether there is nothing to scan for.
func (s *session) empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byToken) == 0
}
