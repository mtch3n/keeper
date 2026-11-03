//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestColumnGrantsBreakSelectStar is §5.5's measured claim, and the reason
// get_schema must return only readable columns and the error must name them.
func TestColumnGrantsBreakSelectStar(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	conn, err := pgx.Connect(ctx, f.ColumnGrantDSN)
	if err != nil {
		t.Fatalf("connect as the column-granted role: %v", err)
	}
	defer conn.Close(ctx)

	var n int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count(*) should succeed under column grants: %v", err)
	}

	rows, err := conn.Query(ctx, `SELECT * FROM users`)
	if err == nil {
		rows.Close()
		err = rows.Err()
	}
	if err == nil {
		t.Fatal("SELECT * succeeded under column grants; §5.5's premise does not hold")
	}
	// The whole statement is refused, not the column — which is why an agent
	// that writes SELECT * constantly needs to be told which regime it is in.
	t.Logf("CONFIRMED: count(*) succeeds, SELECT * fails for the whole statement: %v", err)

	if _, err := conn.Query(ctx, `SELECT id, email FROM users`); err != nil {
		t.Fatalf("naming the readable columns should succeed: %v", err)
	}
}

// TestPostgresErrorsCarryTheOffendingValue is why R6.4a discards every server
// error field. There is no result row here, so redaction never runs: the error
// itself is the egress channel.
func TestPostgresErrorsCarryTheOffendingValue(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	conn, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, `SELECT ssn::integer FROM users`)
	if err == nil {
		t.Fatal("expected a conversion error")
	}
	if !strings.Contains(err.Error(), "123-45-6789") {
		t.Logf("this server did not interpolate the value: %v", err)
		t.Skip("the premise is server-version dependent; nothing to assert")
	}
	// CONFIRMED: the raw error contains an SSN keeper never returned in a row.
	t.Logf("CONFIRMED: the raw PostgreSQL error carries the offending value. "+
		"R6.4a's allowlist is what stops it reaching an agent. Raw error: %v", err)
}

// TestAdvisoryLockSurvivesCommit is R7.9's reason for DISCARD ALL: a plain
// SELECT role can otherwise block production workers indefinitely, and
// statement_timeout never fires because the lock call returns instantly.
func TestAdvisoryLockSurvivesCommit(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	cfg, err := pgx.ParseConfig(f.ReadDSN)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	// DISCARD ALL invalidates pgx's prepared statement cache, so a cached-mode
	// connection fails its *next* query with "prepared statement does not
	// exist". That is R7.5b's reason for QueryExecModeDescribeExec, and writing
	// this test the other way reproduced it exactly.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var got bool
	if err := tx.QueryRow(ctx, `SELECT pg_advisory_lock(42) IS NULL`).Scan(&got); err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var held int
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid()`).Scan(&held); err != nil {
		t.Fatalf("inspect locks: %v", err)
	}
	if held == 0 {
		t.Fatal("the advisory lock did not survive COMMIT; R7.9's premise does not hold")
	}
	t.Logf("CONFIRMED: %d advisory lock(s) still held after COMMIT", held)

	if _, err := conn.Exec(ctx, `DISCARD ALL`); err != nil {
		t.Fatalf("discard all: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = pg_backend_pid()`).Scan(&held); err != nil {
		t.Fatalf("inspect locks: %v", err)
	}
	if held != 0 {
		t.Fatalf("DISCARD ALL left %d advisory lock(s) held", held)
	}
	t.Log("CONFIRMED: DISCARD ALL releases them, which is why a connection returns to the pool only after it succeeds")
}

// TestConcurrentAlterBlocksRatherThanRaces is R7.5b's measured claim: parse
// analysis takes an AccessShareLock and holds it to transaction end, so the
// database already supplies the Describe→Execute guarantee.
func TestConcurrentAlterBlocksRatherThanRaces(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	reader, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect reader: %v", err)
	}
	defer reader.Close(ctx)

	tx, err := reader.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT id FROM users LIMIT 0`)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	rows.Close()

	alterer, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect alterer: %v", err)
	}
	defer alterer.Close(ctx)

	if _, err := alterer.Exec(ctx, `SET lock_timeout = '500ms'`); err != nil {
		t.Fatalf("set lock_timeout: %v", err)
	}
	_, err = alterer.Exec(ctx, `ALTER TABLE users ADD COLUMN probe text`)
	if err == nil {
		t.Fatal("the ALTER succeeded while a read transaction held its lock; R7.5b's premise does not hold")
	}
	if !strings.Contains(err.Error(), "lock timeout") && !strings.Contains(err.Error(), "canceling statement") {
		t.Fatalf("the ALTER failed for the wrong reason: %v", err)
	}
	t.Log("CONFIRMED: a concurrent ALTER blocks on AccessShareLock until lock_timeout rather than racing the descriptor")
}
