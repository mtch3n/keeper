package api_test

import (
	jsonv1 "encoding/json"
	json "encoding/json/v2"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func escalatingRig(t *testing.T, rels ...types.RelationRef) *rig {
	t.Helper()
	r := newRig(t)
	if len(rels) == 0 {
		rels = []types.RelationRef{{Schema: "public", Relation: "users"}}
	}
	r.pipe.escalate = true
	r.pipe.facts = types.ApprovalFacts{
		StatementType: "SELECT",
		Relations:     rels,
		EstimatedRows: 10,
		Reasons:       []string{"near_row_cap"},
	}
	return r
}

// R3.4b: get_result refuses a ticket issued to a different session. Without this
// the design permits an implementation where a guessed or borrowed ticket
// retrieves another session's rows.
func TestTicketIssuedToAnotherSessionIsRefused(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	b := r.agent()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	if tk.ID == "" || tk.State != types.TicketPending {
		t.Fatalf("expected a pending ticket, got %+v", tk)
	}
	if len(tk.ID) < 22 {
		t.Fatalf("ticket id carries too little entropy: %q", tk.ID)
	}

	resp, raw := b.do("GET", "/v1/tickets/"+tk.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("session B read session A's ticket: status %d body %s", resp.StatusCode, raw)
	}
	if code := decodeError(t, raw).Code; code != types.CodeTicketUnknown {
		t.Fatalf("want ticket_unknown so the ticket is not an oracle, got %q", code)
	}

	resp, raw = a.do("GET", "/v1/tickets/"+tk.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issuing session could not read its own ticket: %d %s", resp.StatusCode, raw)
	}
}

// R3.4d and R3.4e: when the connection closes the session goes, and so do its
// tickets, its queue items and its reverse map.
func TestClosingTheConnectionDropsEverythingScopedToIt(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	session := a.sessionID()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)

	if got := len(r.d.Approvals()); got != 1 {
		t.Fatalf("queue should hold the escalation: %d", got)
	}

	a.close()

	eventually(t, "the session's queue items to go", func() bool { return len(r.d.Approvals()) == 0 })
	eventually(t, "the reverse map to be dropped", func() bool {
		for _, s := range r.red.droppedSessions() {
			if s == session {
				return true
			}
		}
		return false
	})
	if len(r.d.Sessions()) != 0 {
		t.Fatalf("session outlived its connection")
	}
}

// R9.1: one queue, oldest first, always. Never by severity and never reordered
// while a client is reading it.
func TestApprovalQueueIsOldestFirst(t *testing.T) {
	r := escalatingRig(t)
	agents := []*client{r.agent(), r.agent(), r.agent()}
	var ids []string
	for _, a := range agents {
		var tk types.Ticket
		a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
		ids = append(ids, tk.ID)
		time.Sleep(2 * time.Millisecond)
	}

	cli := r.socket()
	var queue []types.ApprovalItem
	cli.mustJSON("GET", "/v1/approvals", nil, &queue)
	if len(queue) != 3 {
		t.Fatalf("want 3 items, got %d", len(queue))
	}
	for i, item := range queue {
		if item.TicketID != ids[i] {
			t.Fatalf("item %d out of order: want %s got %s", i, ids[i], item.TicketID)
		}
		if i > 0 && item.CreatedAt.Before(queue[i-1].CreatedAt) {
			t.Fatalf("item %d is older than the one before it", i)
		}
		if item.Session.Client.Workspace == "" {
			t.Fatalf("item %d carries no attribution: %+v", i, item.Session)
		}
	}

	// Deciding the middle one does not reorder the rest.
	cli.mustJSON("POST", "/v1/approvals/"+ids[1]+"/decide", map[string]any{"decision": "refuse", "actor": "ming"}, nil)
	queue = nil
	cli.mustJSON("GET", "/v1/approvals", nil, &queue)
	if len(queue) != 2 || queue[0].TicketID != ids[0] || queue[1].TicketID != ids[2] {
		t.Fatalf("queue reordered after a decision: %+v", queue)
	}
}

// R3.4a: a browser visiting an unrelated site must not be able to drive
// approvals or policy changes. Loopback binding alone is not the boundary.
func TestLoopbackRejectsForeignOrigin(t *testing.T) {
	r := newRig(t)
	b := r.browser()
	b.header.Set("Origin", "http://evil.example")

	resp, raw := b.do("PATCH", "/v1/connections/c1", map[string]any{"writes": "allow"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin drove a policy change: %d %s", resp.StatusCode, raw)
	}
	if w := r.vault.conns["c1"].Writes; w == types.WritesAllow {
		t.Fatalf("writes changed from a foreign origin: %s", w)
	}

	// A read is refused the same way.
	resp, _ = b.do("GET", "/v1/approvals", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin read the approval queue: %d", resp.StatusCode)
	}
}

// A mutating request from the right origin still needs the CSRF token.
func TestLoopbackRequiresCSRFToken(t *testing.T) {
	r := newRig(t)
	b := r.browser()
	b.header.Del("X-Keeper-CSRF")

	register := map[string]any{"name": "x", "host_id": "h1", "database": "db", "credential": map[string]any{"user": "u"}}

	resp, raw := b.do("POST", "/v1/connections", register)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a mutating request without the CSRF token succeeded: %d %s", resp.StatusCode, raw)
	}

	b.header.Set("X-Keeper-CSRF", "not-the-token")
	resp, raw = b.do("POST", "/v1/connections", register)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a wrong CSRF token was accepted: %d %s", resp.StatusCode, raw)
	}

	b.header.Set("X-Keeper-CSRF", testCSRF)
	if resp, raw := b.do("POST", "/v1/connections", register); resp.StatusCode != http.StatusOK {
		t.Fatalf("the right token was refused: %d %s", resp.StatusCode, raw)
	}
}

// §6.3 and R4.1f: register, approve, the reads and writes settings, catalog edits and
// every §4.5 limit are never reachable from an MCP session.
func TestAgentSessionIsRefusedOnHumanRoutes(t *testing.T) {
	r := newRig(t)
	a := r.agent()

	cases := []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/connections", map[string]any{"name": "x", "host_id": "h1", "database": "db", "credential": map[string]any{"user": "u"}}},
		{"GET", "/v1/hosts", nil},
		{"POST", "/v1/hosts", map[string]any{"name": "x", "address": "h"}},
		{"DELETE", "/v1/hosts/h1", nil},
		{"PATCH", "/v1/connections/c1", map[string]any{"writes": "allow"}},
		{"GET", "/v1/approvals", nil},
		{"POST", "/v1/approvals/x/decide", map[string]any{"decision": "approve"}},
		{"PUT", "/v1/catalog/c1/columns", map[string]any{"entries": map[string]any{}}},
		{"POST", "/v1/catalog/c1/init", map[string]any{}},
		{"GET", "/v1/activity", nil},
		{"GET", "/v1/doctor", nil},
		{"POST", "/v1/vault/export", nil},
	}
	for _, tc := range cases {
		resp, raw := a.do(tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s reachable from an MCP session: %d %s", tc.method, tc.path, resp.StatusCode, raw)
			continue
		}
		if code := decodeError(t, raw).Code; code != types.CodePermissionDenied {
			t.Errorf("%s %s: want permission_denied, got %q", tc.method, tc.path, code)
		}
	}

	// Dropping the header does not help: the connection is an agent connection.
	a.header.Del("X-Keeper-Session")
	if resp, _ := a.do("GET", "/v1/approvals", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("omitting the session header reached the human surface: %d", resp.StatusCode)
	}
}

// The agent surface is socket-only: a browser cannot open a session or query.
func TestAgentSurfaceIsNotOnTheLoopbackListener(t *testing.T) {
	r := newRig(t)
	b := r.browser()
	for _, path := range []string{"/v1/session", "/v1/connections/c1/query", "/v1/requests"} {
		resp, raw := b.do("POST", path, map[string]any{"sql": "SELECT 1"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s served to the browser: %d %s", path, resp.StatusCode, raw)
		}
	}
}

// An input request returns a token into the session's reverse map and never
// the value.
func TestInputRequestReturnsATokenIntoTheSessionAndNeverTheValue(t *testing.T) {
	r := newRig(t)
	a := r.agent()
	b := r.browser()

	// --- input -------------------------------------------------------
	var input types.LocalRequest
	a.mustJSON("POST", "/v1/requests", map[string]any{
		"kind": types.RequestInput, "connection_id": "c1",
		"namespace": "email", "purpose": "find all orders for an email",
	}, &input)
	if input.ID == "" || input.State != "pending_input" {
		t.Fatalf("no pending input request: %+v", input)
	}
	if input.URL == "" {
		t.Fatalf("no loopback link for the human to open")
	}

	var page map[string]any
	b.mustJSON("GET", "/v1/requests/"+input.ID, nil, &page)
	if page["kind"] != string(types.RequestInput) {
		t.Fatalf("page does not say which kind it is serving: %+v", page)
	}

	answerRaw := b.mustJSON("POST", "/v1/requests/"+input.ID+"/submit",
		map[string]any{"value": "a@example.com", "namespace": "email"}, nil)
	if containsToken(answerRaw, "a@example.com") {
		t.Fatalf("the browser response echoed the value: %s", answerRaw)
	}

	var got struct {
		State     string `json:"state"`
		Token     string `json:"token"`
		Namespace string `json:"namespace"`
	}
	a.mustJSON("GET", "/v1/requests/"+input.ID+"?wait_ms=1000", nil, &got)
	if got.State != "ready" || got.Token == "" || got.Namespace != "email" {
		t.Fatalf("input request did not return a token: %+v", got)
	}
	if containsToken(answerRaw, got.Token) {
		t.Fatalf("the token reached the browser: %s", answerRaw)
	}

	// The token resolves to a bound parameter, and only in this session.
	r.pipe.escalate = false
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{
		"sql": "SELECT 1 WHERE email = $1", "params": []any{map[string]any{"token": got.Token}},
	}, nil)
}

// R8.7c: a different session cannot retrieve another's request.
func TestLocalRequestIsRefusedToAnotherSession(t *testing.T) {
	r := newRig(t)
	a := r.agent()
	b := r.agent()

	var input types.LocalRequest
	a.mustJSON("POST", "/v1/requests", map[string]any{
		"kind": types.RequestInput, "connection_id": "c1", "namespace": "email",
	}, &input)

	resp, raw := b.do("GET", "/v1/requests/"+input.ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("session B retrieved session A's request: %d %s", resp.StatusCode, raw)
	}
}

// R8.7c: one-use.
func TestLocalRequestIsOneUse(t *testing.T) {
	r := newRig(t)
	a := r.agent()
	b := r.browser()

	var input types.LocalRequest
	a.mustJSON("POST", "/v1/requests", map[string]any{
		"kind": types.RequestInput, "connection_id": "c1", "namespace": "email",
	}, &input)

	b.mustJSON("POST", "/v1/requests/"+input.ID+"/submit", map[string]any{"value": "a@example.com"}, nil)
	resp, raw := b.do("POST", "/v1/requests/"+input.ID+"/submit", map[string]any{"value": "b@example.com"})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a request was answered twice: %s", raw)
	}
}

// §3.2 and R4.2d: an approval starts the statement, the agent collects it by
// polling, and the connection it polls on is the one the ticket was bound to.
func TestApprovalExecutesAndTheAgentCollectsTheResult(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	cli := r.socket()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	cli.mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)

	var got struct {
		State  string             `json:"state"`
		Result *types.QueryResult `json:"result"`
	}
	a.mustJSON("GET", "/v1/tickets/"+tk.ID+"?wait_ms=2000", nil, &got)
	if got.State != string(types.TicketReady) || got.Result == nil || got.Result.RowCount != 1 {
		t.Fatalf("approved statement did not come back: %+v", got)
	}
	if got.Result.Authorization != "ticket:"+tk.ID {
		t.Fatalf("authorization basis not reported: %q", got.Result.Authorization)
	}
}

// A refusal comes back as a keeper error and nothing runs.
func TestRefusalComesBackAsAnError(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	cli := r.socket()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	cli.mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "refuse", "actor": "ming"}, nil)

	var got struct {
		State string       `json:"state"`
		Error *types.Error `json:"error"`
	}
	a.mustJSON("GET", "/v1/tickets/"+tk.ID+"?wait_ms=2000", nil, &got)
	if got.State != string(types.TicketRefused) || got.Error == nil || got.Error.Code != types.CodeApprovalRefused {
		t.Fatalf("refusal not reported: %+v", got)
	}
	for _, auth := range r.pipe.seenAuths() {
		if auth != "" {
			t.Fatalf("a refused statement ran under %q", auth)
		}
	}
}

// A write waits with its preview, and an approval commits it.
func TestAWriteApprovalCarriesItsPreviewAndCommits(t *testing.T) {
	r := escalatingRig(t)
	r.pipe.facts.StatementType = "UPDATE"
	r.pipe.preview = &types.WritePreview{Operation: "UPDATE", RowCount: 4182, PreviewedAt: time.Now()}
	a := r.agent()
	cli := r.socket()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE users SET tier = 'x'"}, &tk)

	queue := r.d.Approvals()
	if len(queue) != 1 || queue[0].Write == nil || queue[0].Write.RowCount != 4182 {
		t.Fatalf("write approval carries no preview: %+v", queue)
	}

	cli.mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)
	var got struct {
		State  string             `json:"state"`
		Result *types.QueryResult `json:"result"`
	}
	a.mustJSON("GET", "/v1/tickets/"+tk.ID+"?wait_ms=2000", nil, &got)
	if got.State != string(types.TicketReady) || got.Result == nil || got.Result.ExecutedRows != 1 {
		t.Fatalf("approved write did not commit: %+v", got)
	}
}

// §4.3: keeper serves no unlock surface, and still names its key source.
//
// keeperd opens the vault from the keychain, KEEPER_MASTER_KEY or key.age
// before it announces a listener and exits if it cannot, so there is no locked
// state for a client to resolve and nothing for an operator to type. The route
// is deleted rather than deprecated: a vault something can ask to open is a
// vault something has to remember to ask to open, and every source it would
// have asked about resolves inside the daemon anyway.
//
// R4.3 survives the removal — silent degradation to a weaker source is still a
// defect, so doctor still names the one in use.
func TestNoUnlockSurfaceAndTheKeySourceIsStillNamed(t *testing.T) {
	r := newRig(t)
	b := r.browser()

	// Compared against a path that was never registered, rather than against a
	// literal status: what matters is that the router treats the two the same.
	gone, _ := b.do("POST", "/v1/vault/unlock", map[string]any{"passphrase": "x"})
	never, _ := b.do("POST", "/v1/no-such-route", map[string]any{})
	if gone.StatusCode != never.StatusCode {
		t.Fatalf("the unlock route still answers: %d, while an unregistered path gives %d",
			gone.StatusCode, never.StatusCode)
	}

	var rep struct {
		KeySource string `json:"key_source"`
	}
	b.mustJSON("GET", "/v1/doctor", nil, &rep)
	if rep.KeySource != "test" {
		t.Fatalf("doctor stopped naming the key source: %q", rep.KeySource)
	}
}

// R3.4d: an unresolvable token is not "retry". Every token the agent holds is
// permanently unresolvable, and the error has to say that.
func TestUnresolvableTokenExplainsWhatHappened(t *testing.T) {
	r := newRig(t)
	a := r.agent()

	resp, raw := a.do("POST", "/v1/connections/c1/query", map[string]any{
		"sql": "SELECT 1 WHERE email = $1", "params": []any{map[string]any{"token": "<email:99>"}},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d %s", resp.StatusCode, raw)
	}
	e := decodeError(t, raw)
	if e.Code != types.CodeStaleToken || e.Action == "" {
		t.Fatalf("stale token not explained: %+v", e)
	}
	if len(r.pipe.seenAuths()) != 0 {
		t.Fatal("a statement with an unresolvable token reached the pipeline")
	}
}

// R8.7d: the local page is no-store and serves the app rather than the id.
func TestLocalPageIsNoStore(t *testing.T) {
	r := newRig(t)
	b := r.browser()

	resp, raw := b.do("GET", "/r/"+"0123456789abcdef0123456789abcdef", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the decision page is not served: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("want no-store, got %q", got)
	}
	if !containsToken(raw, "<!doctype html>") {
		t.Fatalf("the page did not come from the embedded UI: %s", raw[:min(len(raw), 80)])
	}

	// The same path on the socket is not the UI.
	if resp, _ := r.socket().do("GET", "/r/abc", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the UI was served over the agent socket: %d", resp.StatusCode)
	}
}

// An unreadable column is not listed in get_schema and is counted (R5.5); a
// readable one is listed with its policy.
func TestSchemaOmitsAndCountsUnreadableColumns(t *testing.T) {
	r := newRig(t)
	users := types.RelationRef{Schema: "public", Relation: "users"}
	r.exec.rels = []ports.Relation{{
		Ref: users, OID: 1, Kind: 'r',
		Columns: []ports.Column{
			{Name: "id", AttNum: 1, Type: "int4", IsPK: true},
			{Name: "email", AttNum: 2, Type: "text"},
			{Name: "has_hiv_diagnosis", AttNum: 3, Type: "bool"},
			{Name: "secret", AttNum: 4, Type: "text"},
		},
	}}
	r.cat.readable[users.String()] = []string{"id", "email", "has_hiv_diagnosis"}
	r.cat.byName[users.String()+".id"] = types.ColumnPolicy{Policy: types.PolicyAllow}
	r.cat.byName[users.String()+".email"] = types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "email"}
	r.cat.byName[users.String()+".has_hiv_diagnosis"] = types.ColumnPolicy{Policy: types.PolicyRedact}

	a := r.agent()
	var out struct {
		Tables []struct {
			Name       string                  `json:"name"`
			Columns    []struct{ Name string } `json:"columns"`
			Unreadable int                     `json:"unreadable_columns"`
		} `json:"tables"`
	}
	a.mustJSON("GET", "/v1/connections/c1/schema?schema=public&table=users", nil, &out)
	if len(out.Tables) != 1 {
		t.Fatalf("want one table, got %d", len(out.Tables))
	}
	tbl := out.Tables[0]
	if tbl.Unreadable != 1 || len(tbl.Columns) != 3 {
		t.Fatalf("unreadable column not counted, or a readable one hidden: %+v", tbl)
	}
	for _, c := range tbl.Columns {
		if c.Name == "secret" {
			t.Fatalf("an unreadable column name was disclosed: %s", c.Name)
		}
	}
}

// R9.2: the approval screen shows the override rather than obeying it.
func TestApprovalSQLShowsBidiOverrides(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	a.mustJSON("POST", "/v1/connections/c1/query",
		map[string]any{"sql": "SELECT id FROM users -- \u202e DROP"}, nil)

	queue := r.d.Approvals()
	if len(queue) != 1 {
		t.Fatalf("want one item, got %d", len(queue))
	}
	if !containsToken([]byte(queue[0].SQL), "[U+202E]") {
		t.Fatalf("the override is not visible on the approval screen: %q", queue[0].SQL)
	}
}

// CONTRACT §3: the SSE stream carries the five topics, and a request event never
// carries the request id — the id is a capability and the page that needs it
// already has it.
func TestEventStreamCarriesNoRequestID(t *testing.T) {
	r := newRig(t)
	a := r.agent()
	b := r.browser()

	req, err := http.NewRequest("GET", r.ui.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", r.uiOrig)
	resp, err := b.http.Do(req)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream refused: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type %q", got)
	}

	var input types.LocalRequest
	a.mustJSON("POST", "/v1/requests", map[string]any{
		"kind": types.RequestInput, "connection_id": "c1", "namespace": "email",
	}, &input)

	buf := make([]byte, 4096)
	deadline := time.Now().Add(3 * time.Second)
	var seen string
	for time.Now().Before(deadline) && !containsToken([]byte(seen), "event: request") {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			seen += string(buf[:n])
		}
		if err != nil {
			break
		}
	}
	if !containsToken([]byte(seen), "event: request") {
		t.Fatalf("no request event arrived: %q", seen)
	}
	if containsToken([]byte(seen), input.ID) {
		t.Fatalf("the event stream leaked a request id: %q", seen)
	}
}

// A decision on a ticket that is already settled is refused, and the queue does
// not grow a second copy.
func TestATicketIsDecidedOnce(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	cli := r.socket()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	cli.mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "refuse", "actor": "ming"}, nil)

	resp, raw := cli.do("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a settled ticket was decided again: %s", raw)
	}
	if n := len(r.d.Approvals()); n != 0 {
		t.Fatalf("queue still holds the item: %d", n)
	}
}

// The detail response carries types.Limits, and its StatementTimeout is a
// time.Duration — a type encoding/json/v2 refuses to encode without being told
// how. writeJSON used to stream straight at the ResponseWriter and discard the
// error, so 200 was already on the wire when the encode gave up and the body
// stopped mid-value. Every reader saw a success it could not parse, and
// `keeper doctor` called the daemon unreachable while it was serving requests.
// CONTRACT §2 puts nanoseconds on the wire; this pins both halves.
func TestConnectionDetailIsCompleteAndCarriesDuration(t *testing.T) {
	r := newRig(t)
	resp, raw := r.socket().do("GET", "/v1/connections/c1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	// An untyped decode is the truncation check: a body that stops inside a
	// value fails here regardless of which field it stopped in.
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not complete JSON (%d bytes): %v", len(raw), err)
	}
	if _, ok := body["limits"]; !ok {
		t.Fatalf("no limits in detail response: %s", raw)
	}
	var detail struct {
		Limits types.Limits `json:"limits"`
	}
	if err := json.Unmarshal(raw, &detail, jsonv1.FormatDurationAsNano(true)); err != nil {
		t.Fatalf("decode with the wire's duration representation: %v", err)
	}
	if got, want := detail.Limits.StatementTimeout, types.DefaultLimits().StatementTimeout; got != want {
		t.Fatalf("statement_timeout = %v, want %v", got, want)
	}
}

// A human reviewing Activity sees what the agent received, masked as it was
// sent. The rows are held in memory beside the audit record, never in it (R10b).
func TestActivityRecordCarriesTheResultTheAgentSaw(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	cli := r.socket()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	cli.mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)
	var got struct {
		Result *types.QueryResult `json:"result"`
	}
	a.mustJSON("GET", "/v1/tickets/"+tk.ID+"?wait_ms=2000", nil, &got)
	if got.Result == nil || got.Result.AuditID == "" {
		t.Fatalf("no result to review: %+v", got)
	}

	// The fake pipeline reports an audit id without writing the record; the
	// real one writes it before it returns.
	if err := r.alog.Write(t.Context(), &types.AuditRecord{ID: got.Result.AuditID}); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		ID     string             `json:"id"`
		Result *types.QueryResult `json:"result"`
	}
	cli.mustJSON("GET", "/v1/activity/"+got.Result.AuditID, nil, &detail)
	if detail.ID != got.Result.AuditID || detail.Result == nil || detail.Result.RowCount != got.Result.RowCount {
		t.Fatalf("activity detail does not carry the agent's result: %+v", detail)
	}
}

// One host carries any number of databases, each reached as its own role. A
// host with a connection on it cannot be removed out from under that
// connection's credentials.
func TestHostCarriesSeveralConnections(t *testing.T) {
	r := newRig(t)
	b := r.browser()

	var h types.Host
	b.mustJSON("POST", "/v1/hosts", map[string]any{"name": "prod", "address": "db.internal"}, &h)
	if h.ID == "" || h.Port != 5432 || h.SSLMode != "require" {
		t.Fatalf("host defaults not applied: %+v", h)
	}

	for _, p := range []struct{ name, database, user string }{
		{"orders", "orders", "orders_ro"},
		{"billing", "billing", "analyst"},
	} {
		var c struct {
			HostID   string `json:"host_id"`
			Database string `json:"database"`
			Role     string `json:"role"`
		}
		b.mustJSON("POST", "/v1/connections", map[string]any{
			"name": p.name, "host_id": h.ID, "database": p.database,
			"credential": map[string]any{"user": p.user, "password": "pw"},
		}, &c)
		if c.HostID != h.ID || c.Database != p.database || c.Role != p.user {
			t.Fatalf("connection %s registered as %+v", p.name, c)
		}
	}

	var hosts []struct {
		ID          string   `json:"id"`
		Connections []string `json:"connections"`
	}
	b.mustJSON("GET", "/v1/hosts", nil, &hosts)
	var onProd int
	for _, x := range hosts {
		if x.ID == h.ID {
			onProd = len(x.Connections)
		}
	}
	if onProd != 2 {
		t.Fatalf("host lists %d connections, want 2: %+v", onProd, hosts)
	}

	if resp, _ := b.do("DELETE", "/v1/hosts/"+h.ID, nil); resp.StatusCode == http.StatusOK {
		t.Fatal("removed a host with connections still registered on it")
	}
	if resp, raw := b.do("POST", "/v1/connections", map[string]any{
		"name": "x", "host_id": "nope", "database": "db", "credential": map[string]any{"user": "u"},
	}); resp.StatusCode == http.StatusOK {
		t.Fatalf("registered a connection on a host that does not exist: %s", raw)
	}
}

func TestDetectionIsAnOperatorSetting(t *testing.T) {
	r := newRig(t)
	b := r.browser()
	var c struct {
		ID string `json:"id"`
	}
	b.mustJSON("POST", "/v1/connections", map[string]any{
		"name": "new", "host_id": "h1", "database": "db", "credential": map[string]any{"user": "u"},
	}, &c)
	var d struct {
		Detection types.Detection `json:"detection"`
	}
	b.mustJSON("GET", "/v1/connections/"+c.ID, nil, &d)
	if d.Detection != types.DetectionScan {
		t.Errorf("a new connection's detection = %q, want scan", d.Detection)
	}
	b.mustJSON("PATCH", "/v1/connections/c1", map[string]any{"detection": "all"}, nil)
	b.mustJSON("GET", "/v1/connections/c1", nil, &d)
	if d.Detection != types.DetectionAll {
		t.Errorf("detection = %q, want all", d.Detection)
	}
	for _, bad := range []any{"sometimes", []map[string]any{{"kind": "patterns"}}} {
		if resp, raw := b.do("PATCH", "/v1/connections/c1", map[string]any{"detection": bad}); resp.StatusCode == http.StatusOK {
			t.Errorf("detection %v was accepted: %s", bad, raw)
		}
	}
	a := r.agent()
	if resp, _ := a.do("PATCH", "/v1/connections/c1", map[string]any{"detection": "off"}); resp.StatusCode == http.StatusOK {
		t.Error("an MCP session changed detection")
	}
	if got := r.vault.conns["c1"].Detection; got != types.DetectionAll {
		t.Errorf("detection = %q after refused changes, want all", got)
	}
	for _, path := range []string{"/v1/connections/c1/terms", "/v1/catalog/c1/grants"} {
		if resp, _ := b.do("POST", path, nil); resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s still has a route: %d", path, resp.StatusCode)
		}
	}
}

func TestPatchRefusesAReadsOrWritesValueKeeperDoesNotKnow(t *testing.T) {
	r := newRig(t)
	for _, body := range []map[string]any{{"reads": "sometimes"}, {"writes": "approve"}} {
		resp, raw := r.browser().do("PATCH", "/v1/connections/c1", body)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("%v was accepted: %s", body, raw)
		}
	}
	resp, raw := r.browser().do("PATCH", "/v1/connections/c1", map[string]any{"reads": "never", "writes": "allow"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a known setting was refused: %d %s", resp.StatusCode, raw)
	}
	if c := r.vault.conns["c1"]; c.Reads != types.ReadsNever || c.Writes != types.WritesAllow {
		t.Errorf("reads=%s writes=%s", c.Reads, c.Writes)
	}
}

func Test_JDG_C6_DoctorReportsNoJudge(t *testing.T) {
	r := newRig(t)
	_, raw := r.browser().do("GET", "/v1/doctor", nil)
	if strings.Contains(strings.ToLower(string(raw)), "judge") {
		t.Errorf("doctor still reports a judge: %s", raw)
	}
}

// decisionRecord is the part of an activity record a decision test reads.
type decisionRecord struct {
	At         time.Time      `json:"at"`
	Connection string         `json:"connection"`
	Approver   string         `json:"approver"`
	Decision   types.Decision `json:"decision"`
}

func Test_SHELL_C7_ARefusalIsRecordedWithWhoDecided(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT id FROM users"}, &tk)
	r.socket().mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "refuse", "actor": "ming"}, nil)
	var records []decisionRecord
	r.browser().mustJSON("GET", "/v1/activity", nil, &records)
	if !slices.ContainsFunc(records, func(rec decisionRecord) bool {
		return rec.Decision == types.DecisionRefused && rec.Approver == "ming" && rec.Connection == "c1" && !rec.At.IsZero()
	}) {
		t.Errorf("records = %+v, want a refused record decided by ming", records)
	}
}

func Test_SHELL_C11_RetentionIsSavedAndReported(t *testing.T) {
	r := newRig(t)
	b := r.browser()
	b.mustJSON("PUT", "/v1/settings", map[string]any{"log_retention_days": 14}, nil)
	var rep struct {
		LogRetentionDays int `json:"log_retention_days"`
	}
	b.mustJSON("GET", "/v1/doctor", nil, &rep)
	if rep.LogRetentionDays != 14 {
		t.Errorf("doctor reports %d days, want 14", rep.LogRetentionDays)
	}
}

func Test_SHELL_C12_ANonPositiveRetentionIsRefused(t *testing.T) {
	r := newRig(t)
	b := r.browser()
	b.mustJSON("PUT", "/v1/settings", map[string]any{"log_retention_days": 30}, nil)
	for _, days := range []int{0, -3} {
		if resp, raw := b.do("PUT", "/v1/settings", map[string]any{"log_retention_days": days}); resp.StatusCode == http.StatusOK {
			t.Errorf("%d days was accepted: %s", days, raw)
		}
	}
	var s struct {
		LogRetentionDays int `json:"log_retention_days"`
	}
	b.mustJSON("GET", "/v1/settings", nil, &s)
	if s.LogRetentionDays != 30 {
		t.Errorf("stored retention = %d, want 30 unchanged", s.LogRetentionDays)
	}
}

func TestTheRefusalNamesTheProfilesThatAllowWrites(t *testing.T) {
	r := newRig(t)
	r.vault.conns["c1"].HostID, r.vault.conns["c1"].Database = "h1", "app"
	r.vault.conns["c9"] = &types.Connection{ID: "c9", Name: "prod_rw", HostID: "h1", Database: "app", Writes: types.WritesAsk, Reads: types.ReadsUncertain, Limits: types.DefaultLimits()}
	r.vault.conns["c8"] = &types.Connection{ID: "c8", Name: "other_db", HostID: "h1", Database: "other", Writes: types.WritesAsk, Reads: types.ReadsUncertain, Limits: types.DefaultLimits()}
	r.pipe.err = &types.Error{Code: types.CodeWritesOff, Summary: "writes are off for this profile"}
	a := r.agent()
	resp, raw := a.do("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE orders SET status = 'x'"})
	if resp.StatusCode == http.StatusOK && !strings.Contains(string(raw), "writes_off") {
		t.Fatalf("not refused: %s", raw)
	}
	if !strings.Contains(string(raw), "prod_rw") || strings.Contains(string(raw), "other_db") {
		t.Errorf("refusal should name prod_rw (same host and database) and not other_db: %s", raw)
	}
	var queue []any
	r.socket().mustJSON("GET", "/v1/approvals", nil, &queue)
	if len(queue) != 0 {
		t.Errorf("a refused write is waiting in the Inbox: %v", queue)
	}
}

func Test_WRITE_C3_AgentsSeeProfileDetailsButNeverAPassword(t *testing.T) {
	r := newRig(t)
	r.vault.conns["c1"].HostID, r.vault.conns["c1"].Writes = "h1", types.WritesAsk
	a := r.agent()
	var conns []struct {
		Name     string `json:"name"`
		Host     string `json:"host"`
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Database string `json:"database"`
		Username string `json:"username"`
		Writes   string `json:"writes"`
	}
	a.mustJSON("GET", "/v1/connections", nil, &conns)
	if len(conns) != 1 {
		t.Fatalf("connections = %+v", conns)
	}
	c := conns[0]
	if c.Host != "db1" || c.Address != "h" || c.Port != 5432 || c.Database != "app" || c.Username != "app_ro" || c.Writes != "ask" {
		t.Errorf("profile = %+v, want host db1 at h:5432, database app, username app_ro, writes ask", c)
	}
	for _, path := range []string{"/v1/connections", "/v1/connections/c1"} {
		_, raw := a.do("GET", path, nil)
		if strings.Contains(strings.ToLower(string(raw)), "password") {
			t.Errorf("%s mentions a password: %s", path, raw)
		}
	}
}

func Test_WRITE_C6_PreviewedValuesReachOnlyTheApprover(t *testing.T) {
	const secret = "ada@secret.example"
	r := escalatingRig(t)
	r.pipe.facts.StatementType = "UPDATE"
	r.pipe.preview = &types.WritePreview{
		Operation: "UPDATE", RowCount: 1, PreviewedAt: time.Now(),
		Changes: &types.WriteChanges{Columns: []string{"email"}, Rows: []types.ChangedRow{{Old: []string{"old@x"}, New: []string{secret}}}},
	}
	a := r.agent()
	cli := r.socket()

	_, raw := a.do("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE users SET email = 'x'"})
	var tk types.Ticket
	if err := json.Unmarshal(raw, &tk); err != nil || tk.ID == "" {
		t.Fatalf("no ticket: %s", raw)
	}
	_, approvals := cli.do("GET", "/v1/approvals", nil)
	if !strings.Contains(string(approvals), secret) {
		t.Fatalf("the approver does not see the changed value: %s", approvals)
	}
	cli.mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{"decision": "approve", "actor": "ming"}, nil)
	_, result := a.do("GET", "/v1/tickets/"+tk.ID+"?wait_ms=2000", nil)

	logged, _ := json.Marshal(r.alog.records)
	for name, body := range map[string][]byte{"query response": raw, "ticket result": result, "activity log": logged} {
		if strings.Contains(string(body), secret) || strings.Contains(string(body), "old@x") {
			t.Errorf("the %s carries a previewed value: %s", name, body)
		}
	}
}

func Test_WRITE_C9_AnAgentCancelsItsOwnPendingRequest(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent()
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT id FROM orders"}, &tk)

	var got struct {
		State string `json:"state"`
	}
	a.mustJSON("POST", "/v1/tickets/"+tk.ID+"/cancel", nil, &got)
	if got.State != string(types.TicketCancelled) {
		t.Errorf("cancel answered %q, want cancelled", got.State)
	}
	if q := r.d.Approvals(); len(q) != 0 {
		t.Errorf("a cancelled request is still in the Inbox: %+v", q)
	}
	var recorded bool
	for _, rec := range r.alog.records {
		recorded = recorded || rec.Decision == types.DecisionCancelled
	}
	if !recorded {
		t.Errorf("Activity has no cancelled record: %+v", r.alog.records)
	}
}

func Test_WRITE_C10_AnAgentCannotCancelAnotherSessionsRequest(t *testing.T) {
	r := escalatingRig(t)
	owner := r.agent()
	var tk types.Ticket
	owner.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT id FROM orders"}, &tk)

	other := r.agent()
	resp, raw := other.do("POST", "/v1/tickets/"+tk.ID+"/cancel", nil)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("another session cancelled the request: %s", raw)
	}
	if q := r.d.Approvals(); len(q) != 1 {
		t.Errorf("the request left the Inbox: %+v", q)
	}
}

func Test_LAZY_C1_ReadingStatusContactsNoDatabase(t *testing.T) {
	r := newRig(t)
	opened := 0
	r.open["c1"] = func() error {
		opened++
		return nil
	}
	cli := r.socket()
	for _, path := range []string{"/v1/doctor", "/v1/connections", "/v1/hosts", "/v1/settings"} {
		cli.mustJSON("GET", path, nil, nil)
	}
	if opened != 0 || r.exec.contacts() != 0 {
		t.Errorf("reading status opened %d catalog(s) and made %d database call(s)", opened, r.exec.contacts())
	}
}

func Test_LAZY_C2_DoctorCarriesNoUnclassifiedCount(t *testing.T) {
	r := newRig(t)
	if _, err := r.d.Describe(t.Context(), "c1"); err != nil {
		t.Fatalf("describe: %v", err)
	}
	before := r.exec.contacts()
	_, raw := r.socket().do("GET", "/v1/doctor", nil)
	if strings.Contains(string(raw), "unclassified") || strings.Contains(string(raw), `"state"`) {
		t.Errorf("doctor reports a count or a state: %s", raw)
	}
	if r.exec.contacts() != before {
		t.Errorf("doctor contacted a database")
	}
}

func Test_LAZY_C3_TestConnectionSaysWhoItConnectedAs(t *testing.T) {
	r := newRig(t)
	r.exec.ping = &ports.PingResult{User: "app_ro", ServerVersion: "18.0", RoundTrip: 4 * time.Millisecond}
	var got struct {
		User          string `json:"user"`
		ServerVersion string `json:"server_version"`
		RoundTrip     int64  `json:"round_trip"`
	}
	r.socket().mustJSON("POST", "/v1/connections/c1/test", nil, &got)
	if got.User != "app_ro" || got.ServerVersion != "18.0" || got.RoundTrip != int64(4*time.Millisecond) {
		t.Errorf("test = %+v", got)
	}
}

func Test_LAZY_C4_TestConnectionReportsAnUnreachableServer(t *testing.T) {
	r := newRig(t)
	r.exec.pingErr = &types.Error{Code: types.CodeUnreachable, Summary: "keeper could not connect to this database", Action: "check that the server is running and reachable from this machine"}
	resp, raw := r.socket().do("POST", "/v1/connections/c1/test", nil)
	if resp.StatusCode == http.StatusOK || !strings.Contains(string(raw), "unreachable") || !strings.Contains(string(raw), "reachable from this machine") {
		t.Errorf("test of an unreachable server = %d %s", resp.StatusCode, raw)
	}
}

func Test_LAZY_C8_TheIdleSettingIsSavedAndReported(t *testing.T) {
	r := newRig(t)
	cli := r.socket()
	cli.mustJSON("PUT", "/v1/settings", map[string]any{"connection_idle_minutes": 10}, nil)
	var s types.Settings
	cli.mustJSON("GET", "/v1/settings", nil, &s)
	var doc struct {
		Idle int `json:"connection_idle_minutes"`
	}
	cli.mustJSON("GET", "/v1/doctor", nil, &doc)
	if s.ConnectionIdleMinutes != 10 || doc.Idle != 10 {
		t.Errorf("settings %d, doctor %d; want 10 and 10", s.ConnectionIdleMinutes, doc.Idle)
	}
}

func Test_LAZY_C9_AnIdleLimitBelowOneMinuteIsRefused(t *testing.T) {
	r := newRig(t)
	cli := r.socket()
	for _, bad := range []int{0, -3} {
		if resp, raw := cli.do("PUT", "/v1/settings", map[string]any{"connection_idle_minutes": bad}); resp.StatusCode == http.StatusOK {
			t.Errorf("%d accepted: %s", bad, raw)
		}
	}
	var s types.Settings
	cli.mustJSON("GET", "/v1/settings", nil, &s)
	if s.ConnectionIdleMinutes != 5 {
		t.Errorf("idle minutes = %d, want the unchanged 5", s.ConnectionIdleMinutes)
	}
}

func Test_LAZY_C10_ASettingsUpdateChangesOnlyWhatItNames(t *testing.T) {
	r := newRig(t)
	cli := r.socket()
	cli.mustJSON("PUT", "/v1/settings", map[string]any{"connection_idle_minutes": 12}, nil)
	cli.mustJSON("PUT", "/v1/settings", map[string]any{"log_retention_days": 14}, nil)
	var s types.Settings
	cli.mustJSON("GET", "/v1/settings", nil, &s)
	if s.LogRetentionDays != 14 || s.ConnectionIdleMinutes != 12 {
		t.Errorf("settings = %+v, want 14 days and 12 minutes", s)
	}
}

func Test_LAZY_C12_TheDaemonsTimerContactsNoDatabase(t *testing.T) {
	r := newRigWith(t, daemon.Config{Version: "test", Sweep: 5 * time.Millisecond, IdleExit: -1})
	opened := 0
	var mu sync.Mutex
	r.open["c1"] = func() error {
		mu.Lock()
		defer mu.Unlock()
		opened++
		return nil
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if opened != 0 || r.exec.contacts() != 0 {
		t.Errorf("the timer opened %d catalog(s) and made %d database call(s)", opened, r.exec.contacts())
	}
}
