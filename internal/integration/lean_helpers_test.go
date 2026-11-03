//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
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

func parseBody2(body string) queryBody {
	var q queryBody
	_ = json.Unmarshal([]byte(body), &q)
	return q
}

func (e *leanEnv) setReads(t *testing.T, conn string, r types.Reads) {
	t.Helper()
	if _, err := e.op.PatchConnection(t.Context(), conn, client.PatchConnectionParams{Reads: r}); err != nil {
		t.Fatalf("set reads=%s: %v", r, err)
	}
}

func (e *leanEnv) setWrites(t *testing.T, conn string, w types.Writes) {
	t.Helper()
	if _, err := e.op.PatchConnection(t.Context(), conn, client.PatchConnectionParams{Writes: w}); err != nil {
		t.Fatalf("set writes=%s: %v", w, err)
	}
}

func (e *leanEnv) super(t *testing.T, stmts ...string) {
	t.Helper()
	admin, err := pgx.Connect(t.Context(), e.shop.superDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(t.Context())
	for _, s := range stmts {
		if _, err := admin.Exec(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func (e *leanEnv) superValue(t *testing.T, sql string) string {
	t.Helper()
	admin, err := pgx.Connect(t.Context(), e.shop.superDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(t.Context())
	var v string
	if err := admin.QueryRow(t.Context(), sql).Scan(&v); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return v
}

// status is the HTTP status of one unauthenticated-by-session request on the operator socket.
func (e *leanEnv) status(t *testing.T, method, path string) int {
	t.Helper()
	hc := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", e.sock)
	}}}
	req, err := http.NewRequestWithContext(t.Context(), method, "http://keeper"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (e *leanEnv) toolNames(t *testing.T) []string {
	t.Helper()
	res, err := e.agent.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func (e *leanEnv) explainOutcome(t *testing.T, conn, sql string) string {
	t.Helper()
	body := e.agent.call(t, "explain", map[string]any{"connection_id": conn, "sql": sql})
	var out struct {
		Outcome types.Outcome `json:"outcome"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	if out.Outcome == "" {
		return "error"
	}
	return string(out.Outcome)
}

// queryOutcome classifies what a query call returned the way an agent would read it.
func queryOutcome(body string) string {
	q := parseBody2(body)
	switch {
	case q.Ticket != "":
		return "ask"
	case strings.Contains(body, `"rows"`) || strings.Contains(body, `"executed_rows"`) || strings.Contains(body, `"row_count"`):
		return "run"
	default:
		return "refuse"
	}
}

func sameErrorCode(a, b string) bool {
	code := func(s string) string {
		i := strings.Index(s, "syntax")
		j := strings.Index(s, "keeper could not")
		return fmt.Sprint(i >= 0, j >= 0)
	}
	return code(a) == code(b)
}

func (e *leanEnv) rawGet(t *testing.T, path string) string {
	t.Helper()
	hc := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", e.sock)
	}}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://keeper"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func (e *leanEnv) registerSuper(t *testing.T) string {
	t.Helper()
	list, err := e.op.ListHosts(t.Context())
	if err != nil || len(list) == 0 {
		t.Fatalf("hosts: %v", err)
	}
	u, err := url.Parse(e.shop.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	c, err := e.op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: "shop_master", HostID: list[0].ID, Database: "shop",
		Credential: client.Credential{User: u.User.Username(), Password: pass}, CatalogPath: filepath.Join(t.TempDir(), "master.yaml"),
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return c.ID
}

func activityAll() client.ActivityFilter { return client.ActivityFilter{} }

func activityHasWrite(recs []types.AuditRecord, stmtType string) bool {
	for _, r := range recs {
		if r.StatementType == stmtType && r.ErrorCode == "" {
			return true
		}
	}
	return false
}

func (e *leanEnv) activity(t *testing.T) ([]types.AuditRecord, error) {
	t.Helper()
	return e.op.ListActivity(t.Context(), activityAll())
}
