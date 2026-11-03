//go:build integration

package integration

import (
	"encoding/json"
	"testing"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

type leanEnv struct {
	f     *Fixture
	shop  shopDB
	sock  string
	op    *client.Client
	agent *agentSession
	ro    string
	rw    string
	truth seeded
}

func newLeanEnv(t *testing.T) *leanEnv {
	t.Helper()
	f := NewOn(t, "postgres:18-alpine")
	shop := seedShop(t, f)
	sock := startDaemon(t)
	op, err := client.DialHuman(t.Context(), sock)
	if err != nil {
		t.Fatalf("operator dial: %v", err)
	}
	t.Cleanup(func() { op.Close() })
	ro, rw := register(t, op, shop)
	return &leanEnv{f: f, shop: shop, sock: sock, op: op, agent: mcpAgent(t), ro: ro, rw: rw, truth: groundTruth(t, shop.superDSN)}
}

func (e *leanEnv) declare(t *testing.T, conn string, entries map[string]types.ColumnPolicy) {
	t.Helper()
	if _, err := e.op.PutCatalogColumns(t.Context(), conn, entries); err != nil {
		t.Fatalf("declare columns: %v", err)
	}
}

func token(col string) types.ColumnPolicy {
	return types.ColumnPolicy{Policy: types.PolicyToken, Namespace: col}
}

func allowAll(cols ...string) map[string]types.ColumnPolicy {
	out := map[string]types.ColumnPolicy{}
	for _, c := range cols {
		out[c] = types.ColumnPolicy{Policy: types.PolicyAllow}
	}
	return out
}

type queryBody struct {
	Rows   [][]any `json:"rows"`
	Ticket string  `json:"ticket"`
	State  string  `json:"state"`
}

func parseBody(t *testing.T, body string) queryBody {
	t.Helper()
	var q queryBody
	if err := json.Unmarshal([]byte(body), &q); err != nil {
		t.Fatalf("not a query result: %s", body)
	}
	return q
}

func (e *leanEnv) query(t *testing.T, a *agentSession, conn, sql string, params ...map[string]any) string {
	t.Helper()
	args := map[string]any{"connection_id": conn, "sql": sql}
	if len(params) > 0 {
		args["params"] = params
	}
	return a.call(t, "query", args)
}

func (e *leanEnv) mintEmailToken(t *testing.T, a *agentSession, conn string) string {
	t.Helper()
	body := e.query(t, a, conn, "SELECT email FROM customers WHERE id = 4")
	q := parseBody(t, body)
	if len(q.Rows) != 1 {
		t.Fatalf("token source: %s", body)
	}
	tok, _ := q.Rows[0][0].(string)
	if tok == "" {
		t.Fatalf("no token in %s", body)
	}
	return tok
}

// approveAndWait decides a ticket as a person would and returns what the agent then receives.
func (e *leanEnv) approveAndWait(t *testing.T, a *agentSession, body string) string {
	t.Helper()
	q := parseBody(t, body)
	if q.Ticket == "" {
		t.Fatalf("no ticket in %s", body)
	}
	if err := e.op.DecideApproval(t.Context(), q.Ticket, client.DecisionApprove, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return a.call(t, "get_result", map[string]any{"ticket": q.Ticket, "wait_ms": 10000})
}
