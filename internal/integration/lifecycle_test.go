//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

func TestLifecycle_CancellingAGetResultKeepsTheAgentsSession(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t, "GRANT UPDATE ON orders TO shop_eval_rw")
	waiting := e.query(t, e.agent, e.rw, "UPDATE orders SET shipping_note = 'x' WHERE id = 1")
	ticket := parseBody2(waiting).Ticket
	if ticket == "" {
		t.Fatalf("no ticket: %s", waiting)
	}

	ctx, cancel := context.WithCancel(t.Context())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	_, err := e.agent.cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_result", Arguments: map[string]any{"ticket": ticket, "wait_ms": 25000}})
	if err == nil {
		t.Fatal("the cancelled call returned")
	}

	if out := e.agent.call(t, "list_connections", nil); !strings.Contains(out, "connections") {
		t.Fatalf("list_connections after the cancel: %s", out)
	}
	if out := e.agent.call(t, "query", map[string]any{"connection_id": e.ro, "sql": "SELECT count(*) FROM orders"}); strings.Contains(out, "agent session") {
		t.Fatalf("query after the cancel: %s", out)
	}
	if out := e.agent.call(t, "get_result", map[string]any{"ticket": ticket}); !strings.Contains(out, "pending_approval") {
		t.Fatalf("the ticket did not survive the cancel: %s", out)
	}
}

func TestLifecycle_GetResultCarriesWhatQueryDoesAndTheRecordCountsTheRowsChanged(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t, "GRANT UPDATE ON orders TO shop_eval_rw")
	waiting := e.query(t, e.agent, e.rw, "UPDATE orders SET shipping_note = 'x' WHERE id = 1")
	got := e.approveAndWait(t, e.agent, waiting)
	for _, want := range []string{`"state":"ready"`, `"executed_rows":1`, `"previewed_rows":1`, `"audit_id"`} {
		if !strings.Contains(got, want) {
			t.Errorf("get_result lacks %s: %s", want, got)
		}
	}
	recs, err := e.op.ListActivity(t.Context(), client.ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var approved, escalated int
	for _, r := range recs.Records {
		switch {
		case r.Decision == types.DecisionApproved && r.StatementType == "UPDATE":
			approved++
			if r.RowCount != 1 {
				t.Errorf("the approved write's record counts %d rows, want 1", r.RowCount)
			}
		case r.Decision == "" && r.StatementType == "UPDATE":
			escalated++
		}
	}
	if approved != 1 || escalated != 0 {
		t.Errorf("update records: %d approved, %d escalated, want one approved record: the question and the run share one id, and the facts lookup is not a record", approved, escalated)
	}
}
