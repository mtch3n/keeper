//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// TestReadOnlySessionRefusesAWriteCapableRole is the premise under pgdb's read
// pool: a session started with default_transaction_read_only=on cannot write,
// even when the role behind it holds INSERT, and the setting survives the
// DISCARD ALL that returns the connection to the pool.
//
// Both halves are the server's behaviour, not keeper's. The second one is the
// reason the parameter is sent in the startup packet rather than with SET:
// DISCARD ALL runs RESET ALL, which restores a parameter to its value at
// connection start. If that were not so, the guard would last exactly one
// statement and the pool would hand out writable sessions afterwards.
func TestReadOnlySessionRefusesAWriteCapableRole(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	cfg, err := pgx.ParseConfig(f.WriteDSN) // app_rw: INSERT on orders
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["default_transaction_read_only"] = "on"

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	const insert = `INSERT INTO orders (user_email, total, status) VALUES ('a@b.c', 1, 'new')`
	if _, err := conn.Exec(ctx, insert); err == nil {
		t.Fatal("a write-capable role wrote in a read-only session; the read pool's guard is not a guard")
	} else {
		t.Logf("CONFIRMED: INSERT refused for app_rw under default_transaction_read_only: %v", err)
	}

	if _, err := conn.Exec(ctx, "DISCARD ALL"); err != nil {
		t.Fatalf("DISCARD ALL: %v", err)
	}
	var after string
	if err := conn.QueryRow(ctx, `SELECT current_setting('default_transaction_read_only')`).Scan(&after); err != nil {
		t.Fatalf("read the setting back: %v", err)
	}
	if after != "on" {
		t.Fatalf("DISCARD ALL cleared the read-only default: %q — every pooled connection after the first would be writable", after)
	}
	if _, err := conn.Exec(ctx, insert); err == nil {
		t.Fatal("the write succeeded after DISCARD ALL")
	}
}

// TestKeeperReadsWithAWriteCapableCredential is the case this install is made
// of: the DSN an operator registers names the application's read-write login,
// because that is the login they have. SPEC §4.2 says write mode does not
// exist without a separate write credential, and this is what makes that true
// of the account and not only of the statement keeper meant to send.
func TestKeeperReadsWithAWriteCapableCredential(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	exec, err := pgdb.New(pgdb.Config{
		// app_rw registered as the read credential, and no write credential at
		// all — exactly what `keeper connection add` without --write-dsn stores.
		DSN: func(_ context.Context, _ string, role ports.Role) (string, error) {
			if role == ports.RoleWrite {
				return "", &types.Error{Code: types.CodeNoWriteCredential, Summary: "no write credential"}
			}
			return f.WriteDSN, nil
		},
	})
	if err != nil {
		t.Fatalf("pgdb.New: %v", err)
	}
	t.Cleanup(exec.Shutdown)

	var setting string
	res, err := exec.Run(ctx, "conn", `SELECT current_setting('default_transaction_read_only')`, nil, 1)
	if err != nil {
		t.Fatalf("read the session setting: %v", err)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("unexpected shape: %+v", res.Rows)
	}
	setting, _ = res.Rows[0][0].(string)
	if setting != "on" {
		t.Fatalf("keeper's read pool is not read-only: current_setting = %q", setting)
	}

	// keeper refuses the write from the plan, before the server is asked. The
	// session guard above is what holds if this check is ever wrong.
	_, err = exec.Run(ctx, "conn", `INSERT INTO orders (user_email, total, status) VALUES ('a@b.c', 1, 'new')`, nil, 10)
	var kerr *types.Error
	if !errors.As(err, &kerr) || kerr.Code != types.CodeApprovalRequired {
		t.Fatalf("an INSERT on the read path was not routed to approval: %v", err)
	}

	// And nothing reached the table by either route.
	res, err = exec.Run(ctx, "conn", `SELECT count(*) FROM orders WHERE user_email = 'a@b.c'`, nil, 1)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n, _ := res.Rows[0][0].(int64); n != 0 {
		t.Fatalf("the read path wrote %d row(s)", n)
	}
}
