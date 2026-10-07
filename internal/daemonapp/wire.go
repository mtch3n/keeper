package daemonapp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/mtchen/keeper/internal/audit"
	"github.com/mtchen/keeper/internal/catalog"
	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/detect"
	"github.com/mtchen/keeper/internal/pgaudit"
	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/pipeline"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/redact"
	"github.com/mtchen/keeper/internal/types"
	"github.com/mtchen/keeper/internal/vault"
)

// buildDeps constructs the real implementations behind ports and hands the
// daemon everything it needs.
//
// The daemon starts locked, so nothing here opens a database or reads the master
// key: the vault resolves its key source chain on Unlock (§4.3), and the pools
// and catalogs below are all lazy for the same reason.
func buildDeps(ctx context.Context, logger *slog.Logger) (daemon.Deps, *lateAuthority, func(), error) {
	dir, err := configDir()
	if err != nil {
		return daemon.Deps{}, nil, nil, err
	}

	v := vault.New(dir)

	// One patterns engine, shared: it screens every session intent and is the
	// patterns pass of every connection that lists it. Each connection's chain
	// is built from its own passes and terms when a statement needs it.
	patterns := detect.NewPatterns()
	detectors := detect.NewFactory(patterns, v)

	// The activity log is sealed under a key the vault derives, so it reads
	// only where the vault opens. The vault opens after wiring, before the
	// daemon serves, and the log asks for the key on first use.
	alog, err := audit.New(audit.Config{
		Dir:      filepath.Join(dir, "activity"),
		Key:      func() ([]byte, error) { return v.DeriveKey("keeper audit log v1") },
		Detector: patterns,
	})
	if err != nil {
		return daemon.Deps{}, nil, nil, fmt.Errorf("audit log: %w", err)
	}

	exec, err := pgdb.New(pgdb.Config{
		DSN:    v.DSN,
		Limits: limitsFrom(ctx, v),
		Idle:   idleFrom(ctx, v),
	})
	if err != nil {
		alog.Close()
		return daemon.Deps{}, nil, nil, fmt.Errorf("database: %w", err)
	}

	cats := newCatalogs(v, exec, detectors.For, dir)

	red, err := redact.New(redact.Config{
		Keys:      v,
		Detectors: detectors.For,
		Policies:  cats,
	})
	if err != nil {
		alog.Close()
		return daemon.Deps{}, nil, nil, fmt.Errorf("redaction: %w", err)
	}

	// The pipeline asks the daemon for connections and grants, and the daemon
	// asks the pipeline to run statements. Neither can be constructed first, so
	// the authority is a box the daemon drops itself into once it exists. It is
	// read on every statement and written exactly once, which is what the atomic
	// is for; a statement arriving before the daemon is wired gets a refusal
	// rather than a nil dereference.
	auth := &lateAuthority{vault: v}

	pipe, err := pipeline.New(pipeline.Deps{
		Catalogs:  cats.For,
		Redactor:  red,
		Executor:  exec,
		Audit:     alog,
		Authority: auth,
	})
	if err != nil {
		alog.Close()
		return daemon.Deps{}, nil, nil, fmt.Errorf("pipeline: %w", err)
	}

	deps := daemon.Deps{
		Vault:        v,
		Auditor:      pgaudit.New(),
		Catalogs:     cats.For,
		CatalogStore: openingStore{cats},
		Executor:     exec,
		Redactor:     red,
		Audit:        alog,
		Detector:     patterns,
		Pipeline:     &pipelineAdapter{p: pipe},
	}

	cleanup := func() {
		cats.closeAll()
		exec.Shutdown()
		alog.Close()
	}
	return deps, auth, cleanup, nil
}

// limitsFrom reads §4.5's per-connection settings out of the vault for the pool
// and the statement timeout. A locked vault or an unknown connection falls back
// to the defaults rather than failing: the caller is about to get a clearer
// error from the gate that actually needs the connection.
func limitsFrom(ctx context.Context, v *vault.Vault) func(string) types.Limits {
	return func(connID string) types.Limits {
		c, err := v.Connection(ctx, connID)
		if err != nil || c == nil {
			return types.DefaultLimits()
		}
		return c.Limits
	}
}

// idleFrom reads Settings' idle limit on every check, so a change applies at the
// next sweep without a restart.
func idleFrom(ctx context.Context, v *vault.Vault) func() time.Duration {
	return func() time.Duration {
		s, err := v.Settings(ctx)
		if err != nil {
			return time.Duration(types.DefaultSettings().ConnectionIdleMinutes) * time.Minute
		}
		return time.Duration(s.ConnectionIdleMinutes) * time.Minute
	}
}

// catalogs opens one catalog per connection, on first use, and keeps it.
//
// It cannot be eager: opening a catalog introspects the database, and doing
// that for every registered connection at start would make keeperd's start time
// the sum of every server's. It cannot be shared either — a (tableOID, attnum)
// pair means nothing without knowing which database produced it, and two
// databases reuse OIDs freely.
type catalogs struct {
	store *catalog.Store
	vault connectionLookup
	dir   string

	mu      sync.Mutex
	open    map[string]*catalog.Catalog
	opening singleflight.Group
}

// connectionLookup is the one thing catalogs needs from the vault.
type connectionLookup interface {
	Connection(ctx context.Context, id string) (*types.Connection, error)
}

func newCatalogs(v *vault.Vault, exec *pgdb.DB, detectors ports.DetectorFor, dir string) *catalogs {
	return &catalogs{
		store: catalog.NewStore(catalog.Dependencies{
			Introspector: exec,
			Sampler:      exec,
			Privileges:   exec,
			Fingerprints: exec,
			Detectors:    detectors,
		}),
		vault: v,
		dir:   dir,
		open:  map[string]*catalog.Catalog{},
	}
}

// For resolves the catalog for one connection, opening it if needed.
//
// Opening introspects the database, so it lasts as long as a connect can. It is
// serialized per connection and never across them: a single lock held over the
// open let one unreachable server stall every other connection behind it, and
// since a failed open is not kept, the queue never drained.
func (c *catalogs) For(connID string) (ports.Catalog, error) {
	if cat := c.cached(connID); cat != nil {
		return cat, nil
	}
	v, err, _ := c.opening.Do(connID, func() (any, error) {
		// A caller that missed the cache while another flight was finishing
		// arrives here after the catalog was stored.
		if cat := c.cached(connID); cat != nil {
			return cat, nil
		}
		cat, err := c.openOne(connID)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.open[connID] = cat
		c.mu.Unlock()
		return cat, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*catalog.Catalog), nil
}

func (c *catalogs) cached(connID string) *catalog.Catalog {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open[connID]
}

func (c *catalogs) openOne(connID string) (*catalog.Catalog, error) {
	ctx := context.Background()
	conn, err := c.vault.Connection(ctx, connID)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("catalog: no connection %q", connID)
	}

	committed := conn.CatalogPath
	if committed == "" {
		committed = filepath.Join(".keeper", "catalog.yaml")
	}
	return c.store.Open(ctx, connID, catalog.Paths{
		Committed: committed,
		Overlay:   filepath.Join(filepath.Dir(committed), "catalog.local.yaml"),
		Cache:     filepath.Join(c.dir, "catalog-"+safeFileName(connID)+".db"),
	})
}

// openingStore is the CatalogStore the daemon is given. catalog.Store answers
// only for a connection whose catalog is already open, and opening is For's
// job, so every method opens first. Handing the daemon the bare store made
// every catalog command on a fresh daemon fail as "not open" until some query
// happened to open that connection.
type openingStore struct{ c *catalogs }

var _ ports.CatalogStore = openingStore{}

func (s openingStore) Entries(ctx context.Context, connID string) (map[string]types.ColumnPolicy, error) {
	if _, err := s.c.For(connID); err != nil {
		return nil, err
	}
	return s.c.store.Entries(ctx, connID)
}

func (s openingStore) Put(ctx context.Context, connID string, entries map[string]types.ColumnPolicy) error {
	if _, err := s.c.For(connID); err != nil {
		return err
	}
	return s.c.store.Put(ctx, connID, entries)
}

func (s openingStore) Raise(ctx context.Context, connID, key string, p types.ColumnPolicy) error {
	if _, err := s.c.For(connID); err != nil {
		return err
	}
	return s.c.store.Raise(ctx, connID, key, p)
}

func (s openingStore) Init(ctx context.Context, connID string, sample int) (*ports.InitProposal, error) {
	if _, err := s.c.For(connID); err != nil {
		return nil, err
	}
	return s.c.store.Init(ctx, connID, sample)
}

func (s openingStore) SuggestGrants(ctx context.Context, connID string) ([]string, error) {
	if _, err := s.c.For(connID); err != nil {
		return nil, err
	}
	return s.c.store.SuggestGrants(ctx, connID)
}

func (s openingStore) Unclassified(ctx context.Context, connID string) ([]string, error) {
	if _, err := s.c.For(connID); err != nil {
		return nil, err
	}
	return s.c.store.Unclassified(ctx, connID)
}

// Lookup satisfies redact.PolicyLookup. The Redactor needs a column's namespace
// and partial form, and it knows which connection it is masking for because the
// pipeline attached it to the context (ports.Statement).
func (c *catalogs) Lookup(connID string, tableOID uint32, attNum uint16) (types.ColumnPolicy, bool) {
	cat, err := c.For(connID)
	if err != nil {
		return types.ColumnPolicy{}, false
	}
	return cat.Lookup(tableOID, attNum)
}

func (c *catalogs) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.open {
		c.store.Close(id)
	}
	clear(c.open)
}

// safeFileName keeps a connection id from escaping the config directory. Ids are
// generated, not typed, but a cache file name is not the place to rely on that.
func safeFileName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}

// lateAuthority is the box the daemon drops itself into. It exists because the
// pipeline and the daemon each need the other.
type lateAuthority struct {
	vault *vault.Vault
	d     atomic.Pointer[daemon.Daemon]
}

// Bind completes the cycle, once the daemon exists.
func (l *lateAuthority) Bind(d *daemon.Daemon) { l.d.Store(d) }

// Connection comes straight from the vault rather than through the daemon. It is
// the single source R4.5's denylist, the writes setting, §4.5's limits and
// §9.4's mode all read, which is what stops them drifting apart.
func (l *lateAuthority) Connection(ctx context.Context, connID string) (*types.Connection, error) {
	c, err := l.vault.Connection(ctx, connID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("no connection %q", connID)
	}
	return c, nil
}

func (l *lateAuthority) Grant(ctx context.Context, sessionID string, path types.PathRef) (*types.Grant, bool) {
	d := l.d.Load()
	if d == nil {
		return nil, false
	}
	return d.GrantFor(ctx, sessionID, path)
}

// pipelineAdapter fits *pipeline.Pipeline to daemon.Pipeline.
//
// The two interfaces describe the same work from opposite sides: the pipeline
// takes one Request and returns one Decision, because a statement is one
// decision; the daemon asks four narrower questions, because it owns tickets and
// needs to enqueue before it executes. Nothing is reinterpreted here — the
// adapter only unpacks the Decision.
type pipelineAdapter struct {
	p *pipeline.Pipeline
}

func (a *pipelineAdapter) request(sess types.Session, connID, sql string, params []ports.Param) pipeline.Request {
	return pipeline.Request{Session: sess, ConnID: connID, SQL: sql, Params: params}
}

func (a *pipelineAdapter) Explain(ctx context.Context, sess types.Session, connID, sql string, params []ports.Param) (*types.ExplainResult, error) {
	res, kerr, err := a.p.Explain(ctx, a.request(sess, connID, sql, params))
	if err != nil {
		return nil, err
	}
	if kerr != nil {
		return nil, kerr
	}
	return res, nil
}

func (a *pipelineAdapter) Query(ctx context.Context, sess types.Session, connID, sql string, params []ports.Param, maxRows int, authorization string) (*types.QueryResult, *types.Ticket, error) {
	req := a.request(sess, connID, sql, params)
	req.MaxRows = maxRows
	if id, ok := strings.CutPrefix(authorization, "ticket:"); ok {
		req.Approval = &pipeline.Approval{TicketID: id, Approver: sess.Client.Name}
	}
	dec, err := a.p.Query(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case dec.Error != nil:
		return nil, nil, dec.Error
	case dec.Result != nil:
		return dec.Result, nil, nil
	case dec.Escalation != nil:
		return nil, &types.Ticket{
			State:   types.TicketPending,
			Tier:    dec.Tier,
			Reason:  strings.Join(dec.Escalation.Facts.Reasons, ", "),
			AuditID: dec.AuditID,
		}, nil
	default:
		return nil, nil, fmt.Errorf("keeperd: pipeline returned neither a result nor an escalation")
	}
}

// Facts is built from the dry run rather than from a second Query, because a
// Query that escalated has already taken a write preview and asking twice would
// take two.
func (a *pipelineAdapter) Facts(ctx context.Context, sess types.Session, connID, sql string, params []ports.Param) (*types.ApprovalFacts, error) {
	f, kerr, err := a.p.Facts(ctx, a.request(sess, connID, sql, params))
	if err != nil {
		return nil, err
	}
	if kerr != nil {
		return nil, kerr
	}
	return f, nil
}

func (a *pipelineAdapter) PreviewWrite(ctx context.Context, sess types.Session, connID, sql string, params []ports.Param) (*types.WritePreview, error) {
	dec, err := a.p.Query(ctx, a.request(sess, connID, sql, params))
	if err != nil {
		return nil, err
	}
	if dec.Error != nil {
		return nil, dec.Error
	}
	if dec.Escalation == nil || dec.Escalation.Write == nil {
		return nil, fmt.Errorf("keeperd: no write preview for this statement")
	}
	return dec.Escalation.Write, nil
}

func (a *pipelineAdapter) CommitWrite(ctx context.Context, sess types.Session, connID, sql string, params []ports.Param, authorization string) (*types.QueryResult, error) {
	res, _, err := a.Query(ctx, sess, connID, sql, params, 0, authorization)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("keeperd: the approved write did not run")
	}
	return res, nil
}

// configDir is §4.3's ~/.config/keeper, overridable for tests and for a
// non-standard home.
func configDir() (string, error) {
	if v := os.Getenv("KEEPER_HOME"); v != "" {
		return v, os.MkdirAll(v, 0o700)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config directory: %w", err)
	}
	dir := filepath.Join(base, "keeper")
	return dir, os.MkdirAll(dir, 0o700)
}
