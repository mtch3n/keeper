// Package pgdb is keeper's only door to PostgreSQL.
//
// Two rules shape everything in it.
//
// One connection, one transaction (SPEC R7.5b, CONTRACT §4 rule 4). Parse,
// Describe, EXPLAIN and Execute run on a single pooled connection inside a
// single BEGIN READ ONLY, under one SET LOCAL statement_timeout, and the
// connection returns to the pool only after DISCARD ALL succeeds (R7.9).
// Nothing else is required to close the Describe→Execute window: parse analysis
// takes an AccessShareLock on every referenced relation and holds it to
// transaction end, so a concurrent ALTER TABLE blocks rather than races.
//
// No PostgreSQL error leaves this package (SPEC R6.4a, CONTRACT §4 rule 1).
// internal/pgdb is the only package that ever holds a *pgconn.PgError; see
// errors.go for the allowlist that replaces it.
package pgdb

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// DSNFunc resolves a connection id and a role to a connection string.
// internal/vault supplies it; internal/pgdb never reads a credential from
// anywhere else and never stores one.
type DSNFunc func(ctx context.Context, connID string, role ports.Role) (string, error)

// Config is everything the Executor cannot discover for itself.
type Config struct {
	// DSN is required.
	DSN DSNFunc
	// Limits supplies the per-connection operator settings of SPEC §4.5. A nil
	// func, or a zero StatementTimeout, falls back to types.DefaultLimits.
	Limits func(connID string) types.Limits
	// MaxConnsPerPool bounds each (connection, role) pool. Zero means 4.
	MaxConnsPerPool int32
	// IdleInTransactionTimeout is the SET LOCAL value a write preview runs
	// under, so a preview nobody answers cannot hold locks (SPEC R4.2d). Zero
	// means 30s.
	IdleInTransactionTimeout time.Duration
	// ApplicationName is what shows up in pg_stat_activity. Zero means "keeper".
	ApplicationName string
}

// DB implements ports.Executor.
type DB struct {
	dsn      DSNFunc
	limits   func(string) types.Limits
	maxConns int32
	idleInTx time.Duration
	appName  string

	mu    sync.Mutex
	pools map[poolKey]*pgxpool.Pool
}

type poolKey struct {
	connID string
	role   ports.Role
}

const discardTimeout = 5 * time.Second

// New builds an Executor. Pools are created lazily, one per connection id and
// role, the first time a statement needs one.
func New(cfg Config) (*DB, error) {
	if cfg.DSN == nil {
		return nil, fmt.Errorf("pgdb: Config.DSN is required")
	}
	db := &DB{
		dsn:      cfg.DSN,
		limits:   cfg.Limits,
		maxConns: cmp.Or(cfg.MaxConnsPerPool, int32(4)),
		idleInTx: cmp.Or(cfg.IdleInTransactionTimeout, 30*time.Second),
		appName:  cmp.Or(cfg.ApplicationName, "keeper"),
		pools:    map[poolKey]*pgxpool.Pool{},
	}
	return db, nil
}

var _ ports.Executor = (*DB)(nil)

func (db *DB) limitsFor(connID string) types.Limits {
	lim := types.DefaultLimits()
	if db.limits != nil {
		got := db.limits(connID)
		if got.StatementTimeout > 0 {
			lim.StatementTimeout = got.StatementTimeout
		}
		if got.MaxRowsCeiling > 0 {
			lim.MaxRowsCeiling = got.MaxRowsCeiling
		}
		if got.ScanSample > 0 {
			lim.ScanSample = got.ScanSample
		}
	}
	return lim
}

func (db *DB) pool(ctx context.Context, connID string, role ports.Role) (*pgxpool.Pool, error) {
	key := poolKey{connID: connID, role: role}

	db.mu.Lock()
	p, ok := db.pools[key]
	db.mu.Unlock()
	if ok {
		return p, nil
	}

	dsn, err := db.dsn(ctx, connID, role)
	if err != nil {
		return nil, convert(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// A malformed connection string is keeper's own configuration problem,
		// and the parse error can quote the string — which holds the password.
		return nil, keeperError(types.CodeInternal)
	}
	cfg.MaxConns = db.maxConns
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 5 * time.Minute

	// DISCARD ALL invalidates the server's prepared statements, so pgx must not
	// keep a cache that claims otherwise (SPEC R7.5b).
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	cfg.ConnConfig.StatementCacheCapacity = 0
	cfg.ConnConfig.DescriptionCacheCapacity = 0

	// Notices are an egress channel of exactly the same standing as rows and
	// carry interpolated values (SPEC R6.4a). keeper drops them at the socket
	// rather than deciding later what to do with them.
	cfg.ConnConfig.OnNotice = func(*pgconn.PgConn, *pgconn.Notice) {}
	cfg.ConnConfig.OnNotification = func(*pgconn.PgConn, *pgconn.Notification) {}

	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = db.appName

	p, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, convert(err)
	}

	db.mu.Lock()
	if existing, ok := db.pools[key]; ok {
		db.mu.Unlock()
		p.Close()
		return existing, nil
	}
	db.pools[key] = p
	db.mu.Unlock()
	return p, nil
}

// Close releases a connection's pools, for R4.1e's disable path.
func (db *DB) Close(connID string) {
	db.mu.Lock()
	var closing []*pgxpool.Pool
	for key, p := range db.pools {
		if key.connID == connID {
			closing = append(closing, p)
			delete(db.pools, key)
		}
	}
	db.mu.Unlock()
	for _, p := range closing {
		p.Close()
	}
}

// Shutdown closes every pool. The daemon calls it on exit.
func (db *DB) Shutdown() {
	db.mu.Lock()
	pools := slices.Collect(maps.Values(db.pools))
	clear(db.pools)
	db.mu.Unlock()
	for _, p := range pools {
		p.Close()
	}
}

// conn is one statement's whole world: one pooled connection, one transaction.
type conn struct {
	tx      pgx.Tx
	pg      *pgconn.PgConn
	typeMap *pgtype.Map
	limits  types.Limits
	// aborted records that the server rejected a statement inside this
	// transaction, so the envelope rolls back instead of committing.
	aborted bool
}

type txMode struct {
	readOnly  bool
	commit    bool
	idleGuard bool
}

var (
	readTx    = txMode{readOnly: true, commit: true}
	previewTx = txMode{idleGuard: true} // R4.2d step 1: always rolls back
	commitTx  = txMode{commit: true}    // R4.2d step 3: a fresh transaction
)

// withTx is the envelope of SPEC §7.9. Everything this package sends to
// PostgreSQL goes through it.
func (db *DB) withTx(ctx context.Context, connID string, role ports.Role, mode txMode, fn func(context.Context, *conn) error) error {
	pool, err := db.pool(ctx, connID, role)
	if err != nil {
		return err
	}

	pc, err := pool.Acquire(ctx)
	if err != nil {
		return convert(err)
	}
	defer db.finish(ctx, pc)

	opts := pgx.TxOptions{}
	if mode.readOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := pc.BeginTx(ctx, opts)
	if err != nil {
		return convert(err)
	}

	c := &conn{
		tx:      tx,
		pg:      pc.Conn().PgConn(),
		typeMap: pc.Conn().TypeMap(),
		limits:  db.limitsFor(connID),
	}

	if err := c.setLocalMillis(ctx, "statement_timeout", c.limits.StatementTimeout); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if mode.idleGuard {
		if err := c.setLocalMillis(ctx, "idle_in_transaction_session_timeout", db.idleInTx); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
	}

	ferr := fn(ctx, c)

	if ferr != nil || c.aborted || !mode.commit {
		_ = tx.Rollback(ctx)
		return ferr
	}
	if err := tx.Commit(ctx); err != nil {
		return convert(err)
	}
	return nil
}

// finish runs DISCARD ALL and only then returns the connection to the pool
// (SPEC R7.9). A session-level advisory lock survives COMMIT and ROLLBACK, and
// SELECT pg_advisory_lock(...) returns instantly so statement_timeout never
// fires on it — without DISCARD ALL a plain SELECT role can block production
// workers indefinitely. A connection whose DISCARD ALL failed is destroyed
// rather than reused: keeper cannot prove what session state it still carries.
func (db *DB) finish(ctx context.Context, pc *pgxpool.Conn) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), discardTimeout)
	defer cancel()

	// The simple protocol, deliberately: DISCARD ALL must not be wrapped in any
	// implicit block, and it takes no parameters.
	if _, err := pc.Conn().PgConn().Exec(dctx, "DISCARD ALL").ReadAll(); err != nil {
		raw := pc.Hijack()
		_ = raw.Close(dctx)
		return
	}
	pc.Release()
}

// setLocalMillis applies one SET LOCAL. The value is formatted from an integer,
// so no statement text is ever assembled from anything a caller supplied.
func (c *conn) setLocalMillis(ctx context.Context, name string, d time.Duration) error {
	ms := d.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	if _, err := c.tx.Exec(ctx, fmt.Sprintf("SET LOCAL %s = %d", name, ms)); err != nil {
		c.aborted = true
		return convert(err)
	}
	return nil
}

// describe runs Parse and Describe over the extended protocol. This is G1:
// PostgreSQL's own parser validates the syntax, and the protocol rejects
// multi-statement input inherently, so no keeper-authored lexer participates
// (SPEC R7.2). The unnamed statement is used, which describes without creating
// anything DISCARD ALL would have to clean up.
func (c *conn) describe(ctx context.Context, sql string) (*pgconn.StatementDescription, error) {
	sd, err := c.pg.Prepare(ctx, "", sql, nil)
	if err != nil {
		c.aborted = true
		return nil, convert(err)
	}
	return sd, nil
}

// columnsOf turns a RowDescription into keeper's output columns. TableOID and
// TableAttributeNumber are the server's own answer for which column each output
// came from; (0,0) means computed. Nothing here looks at the output name
// (SPEC R7.5a, CONTRACT §4 rule 2).
func (c *conn) columnsOf(fields []pgconn.FieldDescription) []types.ColumnMeta {
	out := make([]types.ColumnMeta, len(fields))
	for i, f := range fields {
		out[i] = types.ColumnMeta{
			Name:     f.Name,
			Type:     c.typeName(f.DataTypeOID),
			TableOID: f.TableOID,
			AttNum:   f.TableAttributeNumber,
		}
	}
	return out
}

func (c *conn) typeName(oid uint32) string {
	if n, ok := nameByOID[oid]; ok {
		return n
	}
	if t, ok := c.typeMap.TypeForOID(oid); ok {
		return t.Name
	}
	return "unknown"
}

// Describe implements ports.Executor. It runs Parse and Describe inside the same
// envelope execution would use, and reads no rows.
func (db *DB) Describe(ctx context.Context, connID, sql string, params []ports.Param) ([]types.ColumnMeta, error) {
	var cols []types.ColumnMeta
	err := db.withTx(ctx, connID, ports.RoleRead, readTx, func(ctx context.Context, c *conn) error {
		sd, err := c.describe(ctx, sql)
		if err != nil {
			return err
		}
		cols = c.columnsOf(sd.Fields)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cols, nil
}

// Plan implements ports.Executor. EXPLAIN (FORMAT JSON), never ANALYZE, inside
// the same BEGIN READ ONLY and statement_timeout as execution (SPEC R7.4a).
//
// The read credential plans first. PostgreSQL checks permissions at executor
// start, and EXPLAIN without ANALYZE still reaches executor start, so planning
// an UPDATE under a SELECT-only role is refused; the write credential is tried
// in that case. This cannot smuggle a read through the write credential: a plan
// with no ModifyTable node is executed by Run, which always uses the read role.
func (db *DB) Plan(ctx context.Context, connID, sql string, params []ports.Param) (*ports.PlanFacts, error) {
	facts, err := db.planAs(ctx, connID, ports.RoleRead, sql, params)
	if err != nil && isCode(err, types.CodePermissionDenied) {
		if writeFacts, writeErr := db.planAs(ctx, connID, ports.RoleWrite, sql, params); writeErr == nil {
			return writeFacts, nil
		}
	}
	return facts, err
}

func (db *DB) planAs(ctx context.Context, connID string, role ports.Role, sql string, params []ports.Param) (*ports.PlanFacts, error) {
	var facts *ports.PlanFacts
	err := db.withTx(ctx, connID, role, readTx, func(ctx context.Context, c *conn) error {
		sd, err := c.describe(ctx, sql)
		if err != nil {
			return err
		}
		f, err := db.explain(ctx, c, sql, params, len(sd.Fields) > 0)
		if err != nil {
			return err
		}
		facts = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	return facts, nil
}

// Run implements ports.Executor: Parse, Describe, EXPLAIN and Execute on one
// pooled connection inside one BEGIN READ ONLY, with DISCARD ALL after.
func (db *DB) Run(ctx context.Context, connID, sql string, params []ports.Param, maxRows int) (*ports.RawResult, error) {
	var res *ports.RawResult
	err := db.withTx(ctx, connID, ports.RoleRead, readTx, func(ctx context.Context, c *conn) error {
		sd, err := c.describe(ctx, sql)
		if err != nil {
			return err
		}
		facts, err := db.explain(ctx, c, sql, params, len(sd.Fields) > 0)
		if err != nil {
			return err
		}
		// The routing decision is keeper's, made from the plan (SPEC §4.2). A
		// statement that turns out to write never runs on the read path, even
		// though BEGIN READ ONLY would also refuse it.
		if facts.Writes || facts.StatementType == StmtDDL {
			return keeperError(types.CodeApprovalRequired)
		}
		r, err := c.fetch(ctx, sql, params, effectiveMaxRows(maxRows, c.limits))
		if err != nil {
			return err
		}
		res = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// PreviewWrite implements ports.Executor: SPEC R4.2d's first execution. The
// statement runs and the transaction rolls back; the command tag's row count is
// exact as of now and is not a lock. Any RETURNING rows come back raw and pass
// through G7 like any other output (R4.2e).
func (db *DB) PreviewWrite(ctx context.Context, connID, sql string, params []ports.Param) (*ports.RawResult, error) {
	return db.write(ctx, connID, sql, params, previewTx)
}

// CommitWrite implements ports.Executor: SPEC R4.2d's third step, a fresh
// transaction on a fresh connection.
func (db *DB) CommitWrite(ctx context.Context, connID, sql string, params []ports.Param) (*ports.RawResult, error) {
	return db.write(ctx, connID, sql, params, commitTx)
}

func (db *DB) write(ctx context.Context, connID, sql string, params []ports.Param, mode txMode) (*ports.RawResult, error) {
	var res *ports.RawResult
	err := db.withTx(ctx, connID, ports.RoleWrite, mode, func(ctx context.Context, c *conn) error {
		sd, err := c.describe(ctx, sql)
		if err != nil {
			return err
		}
		facts, err := db.explain(ctx, c, sql, params, len(sd.Fields) > 0)
		if err != nil {
			return err
		}
		if facts.StatementType == StmtDDL {
			return keeperError(types.CodeDDLRefused)
		}
		r, err := c.fetch(ctx, sql, params, c.limits.MaxRowsCeiling)
		if err != nil {
			return err
		}
		res = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// fetch executes the statement and reads at most maxRows rows.
func (c *conn) fetch(ctx context.Context, sql string, params []ports.Param, maxRows int) (*ports.RawResult, error) {
	start := time.Now()
	rows, err := c.tx.Query(ctx, sql, args(params)...)
	if err != nil {
		c.aborted = true
		return nil, convert(err)
	}
	defer rows.Close()

	res := &ports.RawResult{Columns: c.columnsOf(rows.FieldDescriptions())}
	for rows.Next() {
		if len(res.Rows) >= maxRows {
			// One row past the ceiling is what distinguishes "exactly maxRows"
			// from "truncated". SPEC §4.5.
			res.Truncated = true
			break
		}
		vals, err := rows.Values()
		if err != nil {
			c.aborted = true
			return nil, convert(err)
		}
		res.Rows = append(res.Rows, vals)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		c.aborted = true
		return nil, convert(err)
	}
	tag := rows.CommandTag()

	res.RowCount = len(res.Rows)
	res.CommandTag = tag.RowsAffected()
	res.Duration = time.Since(start)
	return res, nil
}

func effectiveMaxRows(requested int, lim types.Limits) int {
	ceiling := lim.MaxRowsCeiling
	if ceiling <= 0 {
		ceiling = types.DefaultLimits().MaxRowsCeiling
	}
	if requested <= 0 {
		return ceiling
	}
	return min(requested, ceiling)
}
