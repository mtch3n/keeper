package api_test

import (
	json "encoding/json/v2"
	"net/http"
	"testing"
	"time"

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
	a := r.agent("reconcile OPS-441")
	b := r.agent("explore schema")

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
// tickets, its queue items, its session grants and its reverse map.
func TestClosingTheConnectionDropsEverythingScopedToIt(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent("reconcile OPS-441")
	session := a.sessionID()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)

	cli := r.socket() // the CLI: a socket client that never handshakes
	var grant types.Grant
	cli.mustJSON("POST", "/v1/grants", map[string]any{
		"path":        types.PathRef{ConnectionID: "c1", Relation: types.RelationRef{Schema: "public", Relation: "users"}},
		"lifetime":    types.GrantSession,
		"row_ceiling": 100,
		"session_id":  session,
		"actor":       "ming",
	}, &grant)

	if got := len(r.d.Grants()); got != 1 {
		t.Fatalf("grant not stored: %d", got)
	}
	if got := len(r.d.Approvals()); got != 1 {
		t.Fatalf("queue should hold the escalation: %d", got)
	}

	a.close()

	eventually(t, "the session's grants to go", func() bool { return len(r.d.Grants()) == 0 })
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

// R9.3c: a statement matches only if every relation its plan touches has its own
// entry. One unlisted relation sends it back to its normal tier.
func TestGrantsMatchExactPathsOnly(t *testing.T) {
	users := types.RelationRef{Schema: "public", Relation: "users"}
	orders := types.RelationRef{Schema: "public", Relation: "orders"}
	r := escalatingRig(t, users, orders)
	a := r.agent("reconcile OPS-441")
	cli := r.socket()

	grant := func(rel types.RelationRef, ceiling int) {
		t.Helper()
		cli.mustJSON("POST", "/v1/grants", map[string]any{
			"path":        types.PathRef{ConnectionID: "c1", Relation: rel},
			"lifetime":    types.GrantStanding,
			"row_ceiling": ceiling,
			"actor":       "ming",
		}, nil)
	}

	// One of the two relations granted: the join still escalates.
	grant(users, 100)
	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	if tk.ID == "" {
		t.Fatal("a join reaching an ungranted relation was authorized by the grant on its neighbour")
	}

	// A grant on a different connection is a different path.
	cli.mustJSON("POST", "/v1/grants", map[string]any{
		"path":        types.PathRef{ConnectionID: "staging", Relation: orders},
		"lifetime":    types.GrantStanding,
		"row_ceiling": 100,
		"actor":       "ming",
	}, nil)
	tk = types.Ticket{}
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	if tk.ID == "" {
		t.Fatal("a grant on another connection authorized this one")
	}

	// Both relations granted on this connection: it runs, and says what let it.
	grant(orders, 100)
	var res types.QueryResult
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &res)
	if res.RowCount != 1 {
		t.Fatalf("statement did not run with every relation granted: %+v", res)
	}
	if res.Authorization == "" || res.Authorization[:6] != "grant:" {
		t.Fatalf("authorization basis not reported: %q", res.Authorization)
	}
	for _, g := range r.d.Grants() {
		if g.Path.ConnectionID == "c1" && g.Uses == 0 {
			t.Fatalf("grant use not recorded for %s", g.Path)
		}
	}
}

// §9.3: the row ceiling is required, because identical relations with a
// different WHERE can return three orders of magnitude more rows.
func TestGrantRowCeilingBounds(t *testing.T) {
	users := types.RelationRef{Schema: "public", Relation: "users"}
	r := escalatingRig(t, users)
	r.pipe.facts.EstimatedRows = 5000
	a := r.agent("reconcile OPS-441")
	cli := r.socket()

	cli.mustJSON("POST", "/v1/grants", map[string]any{
		"path":        types.PathRef{ConnectionID: "c1", Relation: users},
		"lifetime":    types.GrantStanding,
		"row_ceiling": 100,
		"actor":       "ming",
	}, nil)

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	if tk.ID == "" {
		t.Fatal("a plan expecting 5000 rows was authorized by a grant with a ceiling of 100")
	}

	resp, raw := cli.do("POST", "/v1/grants", map[string]any{
		"path":     types.PathRef{ConnectionID: "c1", Relation: users},
		"lifetime": types.GrantStanding,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a grant without a ceiling was accepted: %d %s", resp.StatusCode, raw)
	}
}

// R9.1: one queue, oldest first, always. Never by severity and never reordered
// while a client is reading it.
func TestApprovalQueueIsOldestFirst(t *testing.T) {
	r := escalatingRig(t)
	agents := []*client{r.agent("first task"), r.agent("second task"), r.agent("third task")}
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
		if item.Session.Client.Workspace == "" || item.Session.Intent == "" {
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

	resp, raw := b.do("POST", "/v1/grants", map[string]any{
		"path":        types.PathRef{ConnectionID: "c1", Relation: types.RelationRef{Schema: "public", Relation: "users"}},
		"lifetime":    types.GrantStanding,
		"row_ceiling": 10,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin drove a policy change: %d %s", resp.StatusCode, raw)
	}
	if n := len(r.d.Grants()); n != 0 {
		t.Fatalf("grant created from a foreign origin: %d", n)
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

	resp, raw := b.do("POST", "/v1/vault/unlock", map[string]any{"passphrase": "x"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a mutating request without the CSRF token succeeded: %d %s", resp.StatusCode, raw)
	}

	b.header.Set("X-Keeper-CSRF", "not-the-token")
	resp, raw = b.do("POST", "/v1/vault/unlock", map[string]any{"passphrase": "x"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a wrong CSRF token was accepted: %d %s", resp.StatusCode, raw)
	}

	b.header.Set("X-Keeper-CSRF", r.srv.CSRFToken())
	if resp, raw := b.do("POST", "/v1/vault/unlock", map[string]any{"passphrase": "x"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("the right token was refused: %d %s", resp.StatusCode, raw)
	}
}

// §6.3 and R4.1f: register, accept, approve, grants, denylist, catalog edits and
// every §4.5 limit are never reachable from an MCP session.
func TestAgentSessionIsRefusedOnHumanRoutes(t *testing.T) {
	r := newRig(t)
	a := r.agent("reconcile OPS-441")

	cases := []struct {
		method, path string
		body         any
	}{
		{"POST", "/v1/connections", map[string]any{"name": "x", "dsn": "postgres://u@h/db"}},
		{"POST", "/v1/connections/c1/accept", map[string]any{"finding_ids": []string{"rolsuper"}, "actor": "a", "via": "cli"}},
		{"PATCH", "/v1/connections/c1", map[string]any{"mode": "permissive"}},
		{"PUT", "/v1/connections/c1/denylist", map[string]any{"relations": []types.RelationRef{}}},
		{"GET", "/v1/approvals", nil},
		{"POST", "/v1/approvals/x/decide", map[string]any{"decision": "approve"}},
		{"GET", "/v1/grants", nil},
		{"POST", "/v1/grants", map[string]any{"row_ceiling": 1}},
		{"PUT", "/v1/catalog/c1/columns", map[string]any{"entries": map[string]any{}}},
		{"POST", "/v1/catalog/c1/init", map[string]any{}},
		{"GET", "/v1/activity", nil},
		{"GET", "/v1/doctor", nil},
		{"POST", "/v1/vault/unlock", map[string]any{"passphrase": "x"}},
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

// R8.7g: one mechanism, two kinds. An input request returns a token into the
// session's reverse map; an authorization request returns nothing at all.
func TestInputAndAuthorizationRequestsDifferInWhatComesBack(t *testing.T) {
	r := newRig(t)
	a := r.agent("find all orders for an email")
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

	// --- authorization ------------------------------------------------
	var authReq types.LocalRequest
	a.mustJSON("POST", "/v1/requests", map[string]any{
		"kind": types.RequestAuthorization, "connection_id": "c1",
		"path": types.PathRef{ConnectionID: "c1", Relation: types.RelationRef{Schema: "public", Relation: "users"}},
	}, &authReq)

	grantRaw := b.mustJSON("POST", "/v1/requests/"+authReq.ID+"/decide",
		map[string]any{"decision": "grant", "lifetime": types.GrantSession, "row_ceiling": 100}, nil)

	var answer struct {
		Kind      string        `json:"kind"`
		Delivered bool          `json:"delivered"`
		Granted   []types.Grant `json:"granted"`
	}
	if err := json.Unmarshal(grantRaw, &answer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if answer.Delivered {
		t.Fatal("an authorization request reported a delivery, which did not happen")
	}
	if len(answer.Granted) != 1 || answer.Granted[0].Path.Relation.Relation != "users" {
		t.Fatalf("the page did not say what was granted: %+v", answer)
	}

	var authGot struct {
		State string `json:"state"`
		Token string `json:"token"`
	}
	a.mustJSON("GET", "/v1/requests/"+authReq.ID+"?wait_ms=1000", nil, &authGot)
	if authGot.State != "ready" {
		t.Fatalf("authorization request never resolved: %+v", authGot)
	}
	if authGot.Token != "" {
		t.Fatal("an authorization request minted a token; the session gained a value it did not have")
	}
}

// R8.7c: a different session cannot retrieve another's request.
func TestLocalRequestIsRefusedToAnotherSession(t *testing.T) {
	r := newRig(t)
	a := r.agent("task a")
	b := r.agent("task b")

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
	a := r.agent("task")
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
	a := r.agent("reconcile OPS-441")
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
	a := r.agent("reconcile OPS-441")
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

// R4.2f: no allow rule authorizes a write, and none may lower one below tier 3.
func TestAWriteApprovalCannotBecomeAGrant(t *testing.T) {
	r := escalatingRig(t)
	r.pipe.facts.StatementType = "UPDATE"
	r.pipe.preview = &types.WritePreview{Operation: "UPDATE", RowCount: 4182, PreviewedAt: time.Now()}
	a := r.agent("close duplicate accounts OPS-512")
	cli := r.socket()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "UPDATE users SET tier = 'x'"}, &tk)

	queue := r.d.Approvals()
	if len(queue) != 1 || queue[0].Write == nil || queue[0].Write.RowCount != 4182 {
		t.Fatalf("write approval carries no preview: %+v", queue)
	}

	resp, raw := cli.do("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{
		"decision": "approve", "actor": "ming",
		"grant": map[string]any{"lifetime": "standing", "row_ceiling": 10000},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a write approval created an allow rule: %d %s", resp.StatusCode, raw)
	}
	if n := len(r.d.Grants()); n != 0 {
		t.Fatalf("grants created for a write: %d", n)
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

// §3.4: the daemon starts locked and says so with a status a client can act on.
func TestLockedVaultIsReportedAs423(t *testing.T) {
	r := newRig(t)
	r.vault.locked = true
	a := r.agent("reconcile OPS-441")

	resp, raw := a.do("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"})
	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("want 423, got %d %s", resp.StatusCode, raw)
	}
	if code := decodeError(t, raw).Code; code != types.CodeVaultLocked {
		t.Fatalf("want vault_locked, got %q", code)
	}
}

// R3.4d: an unresolvable token is not "retry". Every token the agent holds is
// permanently unresolvable, and the error has to say that.
func TestUnresolvableTokenExplainsWhatHappened(t *testing.T) {
	r := newRig(t)
	a := r.agent("reconcile OPS-441")

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

// §6.1: set_session_intent is required before any query, and R10c screens it.
func TestIntentIsRequiredAndScreened(t *testing.T) {
	r := newRig(t)
	r.alog.reject = "patient Jane Doe"
	a := r.agent("")

	resp, raw := a.do("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a query ran with no declared intent: %d %s", resp.StatusCode, raw)
	}

	resp, raw = a.do("POST", "/v1/session/intent", map[string]any{"intent": "patient Jane Doe"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an intent carrying PII was recorded: %d %s", resp.StatusCode, raw)
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

// R6.4b: a column whose name is disclosive is omitted from get_schema and
// counted, and an unreadable column is not listed either (R5.5).
func TestSchemaOmitsAndCountsHiddenColumns(t *testing.T) {
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
	r.cat.byName[users.String()+".has_hiv_diagnosis"] = types.ColumnPolicy{Policy: types.PolicyDrop, HideName: true}

	a := r.agent("explore schema")
	var out struct {
		Tables []struct {
			Name          string                  `json:"name"`
			Columns       []struct{ Name string } `json:"columns"`
			HiddenColumns int                     `json:"hidden_columns"`
			Unreadable    int                     `json:"unreadable_columns"`
		} `json:"tables"`
	}
	a.mustJSON("GET", "/v1/connections/c1/schema?schema=public&table=users", nil, &out)
	if len(out.Tables) != 1 {
		t.Fatalf("want one table, got %d", len(out.Tables))
	}
	tbl := out.Tables[0]
	if tbl.HiddenColumns != 1 || tbl.Unreadable != 1 {
		t.Fatalf("hidden/unreadable not counted: %+v", tbl)
	}
	for _, c := range tbl.Columns {
		if c.Name == "has_hiv_diagnosis" || c.Name == "secret" {
			t.Fatalf("a suppressed column name was disclosed: %s", c.Name)
		}
	}
}

// R4.1f: an acceptance is CLI or UI, never MCP.
func TestAcceptanceRefusesAnMCPVia(t *testing.T) {
	r := newRig(t)
	cli := r.socket()
	resp, raw := cli.do("POST", "/v1/connections/c1/accept", map[string]any{
		"finding_ids": []string{"rolsuper"}, "actor": "ming", "via": "mcp",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("via=mcp accepted: %d %s", resp.StatusCode, raw)
	}
	cli.mustJSON("POST", "/v1/connections/c1/accept", map[string]any{
		"finding_ids": []string{"rolsuper"}, "actor": "ming",
	}, nil)
	if got := r.vault.acceptCalls; len(got) != 1 || got[0] != "cli" {
		t.Fatalf("the socket's default via is not cli: %v", got)
	}
}

// R9.2: the approval screen shows the override rather than obeying it.
func TestApprovalSQLShowsBidiOverrides(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent("reconcile OPS-441")
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
	a := r.agent("task")
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

// R9.3a and R9.3c: approving with a grant writes the narrowest scope covering
// the approved request — one entry per relation the plan touched, with §9.3's
// ten-times-what-was-approved ceiling when the human did not name one.
func TestApprovingWithAGrantWritesOneEntryPerRelation(t *testing.T) {
	users := types.RelationRef{Schema: "public", Relation: "users"}
	orders := types.RelationRef{Schema: "public", Relation: "orders"}
	r := escalatingRig(t, users, orders)
	a := r.agent("reconcile OPS-441")
	cli := r.socket()

	var tk types.Ticket
	a.mustJSON("POST", "/v1/connections/c1/query", map[string]any{"sql": "SELECT 1"}, &tk)
	cli.mustJSON("POST", "/v1/approvals/"+tk.ID+"/decide", map[string]any{
		"decision": "approve", "actor": "ming",
		"grant": map[string]any{"lifetime": types.GrantSession},
	}, nil)

	grants := r.d.Grants()
	if len(grants) != 2 {
		t.Fatalf("want one grant per relation, got %d: %+v", len(grants), grants)
	}
	seen := map[string]bool{}
	for _, g := range grants {
		seen[g.Path.Relation.String()] = true
		if g.Lifetime != types.GrantSession {
			t.Fatalf("want a session grant, got %q", g.Lifetime)
		}
		if g.SessionID == "" {
			t.Fatal("a session grant was written without a session to die with")
		}
		if g.RowCeiling != 100 { // 10 x the 10 estimated rows
			t.Fatalf("ceiling %d, want ten times the approved estimate", g.RowCeiling)
		}
		if g.Path.ConnectionID != "c1" {
			t.Fatalf("grant escaped its connection: %s", g.Path)
		}
	}
	if !seen["public.users"] || !seen["public.orders"] {
		t.Fatalf("the grant set does not cover the plan: %v", seen)
	}
}

// A decision on a ticket that is already settled is refused, and the queue does
// not grow a second copy.
func TestATicketIsDecidedOnce(t *testing.T) {
	r := escalatingRig(t)
	a := r.agent("reconcile OPS-441")
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
