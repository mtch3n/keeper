//go:build integration

package integration

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/daemonapp"
	"github.com/mtchen/keeper/internal/mcpapp"
	"github.com/mtchen/keeper/internal/types"
)

//go:embed testdata/shop.sql
var shopSQL string

// TestThePluginReleasesNoSeededPersonalData establishes how much personal data
// keeper's MCP server releases to an agent, end to end: a real daemon over a
// real PostgreSQL 18 seeded with synthetic customers, orders, support tickets
// and employees, driven through the same MCP server the plugin runs, with every
// response searched for every seeded value.
//
// It measures two states. Straight after registration, with no catalog
// decision yet, nothing may leak — computed columns, views, JSON and free text
// included. After an operator accepts every catalog proposal, only what
// pattern detection cannot see may remain: a person's name inside free text,
// and a column called just "name". If either measurement grows, an agent is
// receiving personal data an operator was told it would not; if the write
// flow fails, nothing the plugin escalates ever reaches the Inbox.
func TestThePluginReleasesNoSeededPersonalData(t *testing.T) {
	f := NewOn(t, "postgres:18-alpine")
	shop := seedShop(t, f)
	sock := startDaemon(t)
	ctx := t.Context()

	op, err := client.DialHuman(ctx, sock)
	if err != nil {
		t.Fatalf("operator dial: %v", err)
	}
	t.Cleanup(func() { op.Close() })
	ro, rw := register(t, op, shop)

	agent := mcpAgent(t)
	truth := groundTruth(t, shop.superDSN)

	// --- straight after registration -------------------------------------
	listing := agent.call(t, "list_connections", map[string]any{})
	if strings.Contains(listing, shop.roPass) || strings.Contains(listing, shop.rwPass) {
		t.Fatal("list_connections carries a password")
	}
	leaked := map[string]int{}
	for _, q := range battery {
		for kind, n := range truth.leaks(agent.call(t, "query", map[string]any{"connection_id": ro, "sql": q.sql})) {
			leaked[q.name+": "+kind] += n
		}
	}
	t.Logf("no catalog decisions: %d leaked kinds %v", len(leaked), leaked)
	if len(leaked) != 0 {
		t.Errorf("with no catalog decisions, values leaked: %v", leaked)
	}

	// --- a write waits, shows its rows, and runs once approved ------------
	wrote := agent.call(t, "query", map[string]any{"connection_id": rw, "sql": "UPDATE orders SET status = 'refunded' WHERE id <= 3"})
	var tk struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal([]byte(wrote), &tk); err != nil || tk.Ticket == "" {
		t.Fatalf("a write did not return a ticket: %s", wrote)
	}
	items, err := op.ListApprovals(ctx)
	if err != nil {
		t.Fatalf("approvals: %v", err)
	}
	var changed int
	for _, it := range items {
		if it.TicketID == tk.Ticket && it.Write != nil && it.Write.Changes != nil {
			changed = len(it.Write.Changes.Rows)
		}
	}
	if changed != 3 {
		t.Errorf("the approval shows %d changed rows, want 3", changed)
	}
	if err := op.DecideApproval(ctx, tk.Ticket, client.DecisionApprove, nil); err != nil {
		t.Fatalf("approve: %v", err)
	}
	result := agent.call(t, "get_result", map[string]any{"ticket": tk.Ticket, "wait_ms": 10000})
	if !strings.Contains(result, `"ready"`) || len(truth.leaks(result)) != 0 {
		t.Errorf("approved write: %s", result)
	}

	// --- after an operator accepts every proposal ------------------------
	for _, id := range []string{ro, rw} {
		p, err := op.CatalogInit(ctx, id, 200)
		if err != nil {
			t.Fatalf("catalog init: %v", err)
		}
		entries := map[string]types.ColumnPolicy{}
		for k, v := range p.SafeToBulkAccept {
			entries[k] = v
		}
		for k, v := range p.NeedsReview {
			entries[k] = v
		}
		if _, err := op.PutCatalogColumns(ctx, id, entries); err != nil {
			t.Fatalf("accept proposals: %v", err)
		}
	}
	leaked = map[string]int{}
	for _, q := range battery {
		for kind, n := range truth.leaks(agent.call(t, "query", map[string]any{"connection_id": ro, "sql": q.sql})) {
			leaked[q.name+": "+kind] += n
		}
	}
	t.Logf("proposals accepted: leaked %v", leaked)
	for key := range leaked {
		// Pattern detection finds structured identifiers. A name written in
		// prose, and a column called "name", are what it cannot see.
		if key != "free text: name" && key != "employees: employee_name" {
			t.Errorf("after accepting every proposal, %s leaked", key)
		}
	}

	// Utility: a token from one answer still finds its row in the next.
	tokRes := agent.call(t, "query", map[string]any{"connection_id": ro, "sql": "SELECT email FROM customers WHERE id = 4"})
	var rows struct {
		Rows [][]any `json:"rows"`
	}
	_ = json.Unmarshal([]byte(tokRes), &rows)
	if len(rows.Rows) != 1 {
		t.Fatalf("token source: %s", tokRes)
	}
	token, _ := rows.Rows[0][0].(string)
	found := agent.call(t, "query", map[string]any{"connection_id": ro, "sql": "SELECT id FROM customers WHERE email = $1",
		"params": []map[string]any{{"token": token}}})
	if !strings.Contains(found, `[[4]]`) {
		t.Errorf("a token used as a parameter did not find its row: %s", found)
	}
}

// battery is what an agent sends, including the shapes that have leaked.
var battery = []struct{ name, sql string }{
	{"select *", "SELECT * FROM customers ORDER BY id LIMIT 5"},
	{"alias", "SELECT email AS e, ssn AS s FROM customers LIMIT 5"},
	{"cte", "WITH x AS (SELECT email, phone FROM customers) SELECT * FROM x LIMIT 5"},
	{"function wrapper", "SELECT upper(email), lower(full_name), replace(ssn, '-', '') FROM customers LIMIT 5"},
	{"concat", "SELECT 'mail:' || email AS m FROM customers LIMIT 5"},
	{"json_build_object", "SELECT json_build_object('e', email, 's', ssn) AS j FROM customers LIMIT 3"},
	{"string_agg", "SELECT string_agg(email, ',') FROM customers"},
	{"view", "SELECT * FROM customer_contacts LIMIT 5"},
	{"free text", "SELECT body FROM support_tickets ORDER BY id LIMIT 8"},
	{"jsonb", "SELECT metadata FROM support_tickets ORDER BY id LIMIT 5"},
	{"notes", "SELECT shipping_note FROM orders WHERE shipping_note <> 'Standard delivery' LIMIT 8"},
	{"misnamed columns", "SELECT ref, contact FROM legacy_records LIMIT 5"},
	{"employees", "SELECT name, work_email, salary, iban FROM employees LIMIT 5"},
	{"names and addresses", "SELECT full_name, address, dob FROM customers LIMIT 5"},
	{"error-message probe", "SELECT ssn::int FROM customers LIMIT 1"},
	{"substring probe", "SELECT substr(ssn, 1, 3) AS area FROM customers LIMIT 3"},
}

type shopDB struct {
	superDSN, host string
	port           int
	roPass, rwPass string
}

func seedShop(t *testing.T, f *Fixture) shopDB {
	t.Helper()
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE shop"); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, _ := url.Parse(f.SuperDSN)
	u.Path = "/shop"
	db := shopDB{superDSN: u.String(), host: u.Hostname(), roPass: "ro-" + strconv.FormatInt(time.Now().UnixNano(), 36), rwPass: "rw-" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	db.port, _ = strconv.Atoi(u.Port())

	conn, err := pgx.Connect(ctx, db.superDSN)
	if err != nil {
		t.Fatalf("connect shop: %v", err)
	}
	defer conn.Close(ctx)
	for _, stmt := range []string{
		shopSQL,
		fmt.Sprintf("CREATE ROLE shop_eval_ro LOGIN PASSWORD '%s'", db.roPass),
		fmt.Sprintf("CREATE ROLE shop_eval_rw LOGIN PASSWORD '%s'", db.rwPass),
		"GRANT USAGE ON SCHEMA public TO shop_eval_ro, shop_eval_rw",
		"GRANT SELECT ON ALL TABLES IN SCHEMA public TO shop_eval_ro, shop_eval_rw",
		"GRANT INSERT, UPDATE, DELETE ON orders TO shop_eval_rw",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db
}

// startDaemon runs keeperd in this process with its own home, socket and key,
// and no route to the OS keychain, so nothing of the machine's keeper is read.
func startDaemon(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "keeper.sock")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 3)
	}
	t.Setenv("KEEPER_HOME", filepath.Join(dir, "home"))
	t.Setenv("KEEPER_SOCKET", sock)
	t.Setenv("KEEPER_UI_PORT", "0")
	t.Setenv("KEEPER_MASTER_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/keeper-eval-bus")

	done := make(chan int, 1)
	go func() { done <- daemonapp.Run([]string{"--idle-exit=-1s", "--log-level=error"}) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("keeperd did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(func() {
		hc := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
		if resp, err := hc.Post("http://keeper/v1/daemon/shutdown", "application/json", nil); err == nil {
			resp.Body.Close()
		}
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("keeperd did not stop")
		}
	})
	return sock
}

func register(t *testing.T, op *client.Client, db shopDB) (ro, rw string) {
	t.Helper()
	ctx := t.Context()
	host, err := op.RegisterHost(ctx, client.RegisterHostParams{Name: "eval-pg", Address: db.host, Port: db.port, SSLMode: "disable"})
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	dir := t.TempDir()
	add := func(name, user, pass string, writes types.Writes) string {
		c, err := op.RegisterConnection(ctx, client.RegisterConnectionParams{
			Name: name, HostID: host.ID, Database: "shop", Writes: writes,
			Credential: client.Credential{User: user, Password: pass}, CatalogPath: filepath.Join(dir, name+".yaml"),
		})
		if err != nil {
			t.Fatalf("connection %s: %v", name, err)
		}
		return c.ID
	}
	return add("shop_ro", "shop_eval_ro", db.roPass, types.WritesOff), add("shop_rw", "shop_eval_rw", db.rwPass, types.WritesApprove)
}

type agentSession struct{ cs *mcp.ClientSession }

// mcpAgent connects to the plugin's MCP server over an in-memory transport.
func mcpAgent(t *testing.T) *agentSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := mcpapp.NewServer().Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "eval-agent", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return &agentSession{cs: cs}
}

// call returns everything the agent sees: structured content, or the text of
// an error.
func (a *agentSession) call(t *testing.T, tool string, args map[string]any) string {
	t.Helper()
	res, err := a.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		return string(b)
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

type seeded map[string]map[string]bool

func groundTruth(t *testing.T, dsn string) seeded {
	t.Helper()
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	out := seeded{}
	add := func(kind, v string) {
		if out[kind] == nil {
			out[kind] = map[string]bool{}
		}
		out[kind][strings.ToLower(v)] = true
	}
	rows, _ := conn.Query(ctx, "SELECT full_name, email, phone, ssn, dob::text, address, card_number FROM customers")
	for rows.Next() {
		var name, email, phone, ssn, dob, address, card string
		_ = rows.Scan(&name, &email, &phone, &ssn, &dob, &address, &card)
		add("name", name)
		add("email", email)
		add("phone", phone)
		add("ssn", ssn)
		add("dob", dob)
		add("address", address)
		add("card", card)
	}
	rows, _ = conn.Query(ctx, "SELECT name, work_email, salary::text, iban FROM employees")
	for rows.Next() {
		var name, mail, salary, iban string
		_ = rows.Scan(&name, &mail, &salary, &iban)
		add("employee_name", name)
		add("email", mail)
		add("salary", salary)
		add("iban", iban)
	}
	rows, _ = conn.Query(ctx, "SELECT metadata->>'ip' FROM support_tickets")
	for rows.Next() {
		var ip string
		_ = rows.Scan(&ip)
		add("ip", ip)
	}
	return out
}

// leaks counts, per kind, the seeded values a response contains.
func (s seeded) leaks(text string) map[string]int {
	low := strings.ToLower(text)
	out := map[string]int{}
	for kind, values := range s {
		for v := range values {
			if v != "" && strings.Contains(low, v) {
				out[kind]++
			}
		}
	}
	return out
}
