//go:build integration

package integration

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestAsk_ASK_C21_previewOnceCommitOnce(t *testing.T) {
	e := newLeanEnv(t)
	admin, err := pgx.Connect(t.Context(), e.shop.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(t.Context())
	for _, s := range []string{
		"CREATE SEQUENCE ask_c21",
		"GRANT USAGE, SELECT ON SEQUENCE ask_c21 TO shop_eval_rw",
		"CREATE FUNCTION ask_c21_count() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('ask_c21'); RETURN NEW; END $$",
		"CREATE TRIGGER ask_c21 BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION ask_c21_count()",
	} {
		if _, err := admin.Exec(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	executions := func() int64 {
		var v int64
		var called bool
		if err := admin.QueryRow(t.Context(), "SELECT last_value, is_called FROM ask_c21").Scan(&v, &called); err != nil {
			t.Fatal(err)
		}
		if !called {
			return 0
		}
		return v
	}
	waiting := e.query(t, e.agent, e.rw, "UPDATE orders SET total = total + 0 WHERE id = 1")
	if got := executions(); got != 1 {
		t.Errorf("the statement executed %d times before a person accepted, want one preview", got)
	}
	got := e.approveAndWait(t, e.agent, waiting)
	if !strings.Contains(got, `"ready"`) {
		t.Fatalf("not committed: %s", got)
	}
	if got := executions(); got != 2 {
		t.Errorf("after acceptance the statement had executed %d times, want one preview and one commit", got)
	}
}

func TestAsk_ASK_C22_failedPreviewOpensNoTicket(t *testing.T) {
	e := newLeanEnv(t)
	body := e.query(t, e.agent, e.rw, "UPDATE orders SET total = 1 / (total - total) WHERE id = 1")
	if parseBody2(body).Ticket != "" {
		t.Errorf("a failed preview opened a ticket: %s", body)
	}
	items, err := e.op.ListApprovals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("a failed preview reached the Inbox: %d items", len(items))
	}
	if strings.Contains(body, "division") {
		t.Errorf("the server's error text reached the agent: %s", body)
	}
}

func TestAsk_WriteThatCallsAVolatileFunctionIsNotPreviewed(t *testing.T) {
	e := newLeanEnv(t)
	executions := e.counter(t, "ask_volatile")
	e.super(t, "GRANT UPDATE ON orders TO shop_eval_rw")
	waiting := e.query(t, e.agent, e.rw, "UPDATE orders SET total = total + 0 * nextval('ask_volatile') WHERE id = 1")
	if parseBody2(waiting).Ticket == "" {
		t.Fatalf("no ticket: %s", waiting)
	}
	if got := executions(); got != 0 {
		t.Errorf("the statement executed %d times before a person accepted, want none", got)
	}
	items, err := e.op.ListApprovals(t.Context())
	if err != nil || len(items) != 1 || items[0].Write == nil {
		t.Fatalf("approvals = %+v, %v", items, err)
	}
	if !items[0].Write.Unpreviewed {
		t.Errorf("the approval does not say it was not previewed: %+v", items[0].Write)
	}
	got := e.approveAndWait(t, e.agent, waiting)
	if !strings.Contains(got, `"ready"`) {
		t.Fatalf("not committed: %s", got)
	}
	if got := executions(); got != 1 {
		t.Errorf("after acceptance the statement had executed %d times, want one commit", got)
	}
}

func TestAsk_ADestructiveCallInAWriteDoesNotRunBeforeADecision(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t, "GRANT UPDATE ON orders TO shop_eval_rw")
	other, err := pgx.Connect(t.Context(), fmt.Sprintf("postgres://shop_eval_rw:%s@%s:%d/shop", e.shop.rwPass, e.shop.host, e.shop.port))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(t.Context())
	var pid int
	if err := other.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	body := e.query(t, e.agent, e.rw, "UPDATE orders SET total = total WHERE id = 1 AND pg_terminate_backend("+strconv.Itoa(pid)+")")
	if parseBody2(body).Ticket == "" {
		t.Fatalf("no ticket: %s", body)
	}
	if _, err := other.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Errorf("the backend was terminated while the ticket was pending: %v", err)
	}
}
