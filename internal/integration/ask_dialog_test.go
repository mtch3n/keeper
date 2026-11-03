//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/mcpapp"
	"github.com/mtchen/keeper/internal/types"
)

type dialogs struct {
	mu   sync.Mutex
	seen []*mcp.ElicitParams
}

func (d *dialogs) add(p *mcp.ElicitParams) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = append(d.seen, p)
}

func (d *dialogs) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

func (d *dialogs) first(t *testing.T) *mcp.ElicitParams {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.seen) == 0 {
		t.Fatal("no dialog was shown")
	}
	return d.seen[0]
}

type answerFunc func(ctx context.Context, p *mcp.ElicitParams) (*mcp.ElicitResult, error)

func accepting(content map[string]any) answerFunc {
	return func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: "accept", Content: content}, nil
	}
}

func answering(action string) answerFunc {
	return func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{Action: action}, nil
	}
}

// mcpAgentAnswering is an agent whose client can show an elicitation dialog and
// answers it with handler. protocol pins the version the client offers; empty is the latest.
func mcpAgentAnswering(t *testing.T, handler answerFunc, protocol ...string) (*agentSession, *dialogs) {
	t.Helper()
	log := &dialogs{}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := mcpapp.NewServer().Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "dialog-agent", Version: "1"}, &mcp.ClientOptions{
		ElicitationHandler: func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			log.add(req.Params)
			return handler(ctx, req.Params)
		},
	})
	var opts *mcp.ClientSessionOptions
	if len(protocol) > 0 {
		opts = &mcp.ClientSessionOptions{ProtocolVersion: protocol[0]}
	}
	cs, err := c.Connect(t.Context(), ct, opts)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return &agentSession{cs: cs}, log
}

func (e *leanEnv) counter(t *testing.T, name string) func() int64 {
	t.Helper()
	e.super(t, "CREATE SEQUENCE "+name, "GRANT USAGE, SELECT ON SEQUENCE "+name+" TO shop_eval_rw")
	return func() int64 {
		admin, err := pgx.Connect(t.Context(), e.shop.superDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close(t.Context())
		var v int64
		var called bool
		if err := admin.QueryRow(t.Context(), "SELECT last_value, is_called FROM "+name).Scan(&v, &called); err != nil {
			t.Fatal(err)
		}
		if !called {
			return 0
		}
		return v
	}
}

func counted(name string) string {
	return "UPDATE orders SET total = total + 0 * nextval('" + name + "') WHERE id = 1"
}

func (e *leanEnv) inbox(t *testing.T) []types.ApprovalItem {
	t.Helper()
	items, err := e.op.ListApprovals(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return items
}

const readSQL = "SELECT id, status FROM orders ORDER BY id LIMIT 2"

func TestAsk_ASK_C1_enterOnTheDialogReturnsRowsOnTheSameCall(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	a, log := mcpAgentAnswering(t, accepting(nil))
	body := e.query(t, a, e.ro, readSQL)
	q := parseBody(t, body)
	if len(q.Rows) != 2 || q.Ticket != "" {
		t.Fatalf("the same call did not return rows: %s", body)
	}
	if log.count() != 1 {
		t.Errorf("%d dialogs, want one", log.count())
	}
	recs, err := e.activity(t)
	if err != nil {
		t.Fatal(err)
	}
	var approver string
	for _, r := range recs {
		if r.Approver != "" {
			approver = r.Approver
		}
	}
	if approver != "dialog" {
		t.Errorf("the record's approver = %q, want dialog", approver)
	}
}

func TestAsk_ASK_C2_olderProtocolAnswersOnTheSameCall(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	a, log := mcpAgentAnswering(t, accepting(nil), "2025-06-18")
	if v := a.cs.InitializeResult().ProtocolVersion; v != "2025-06-18" {
		t.Fatalf("negotiated %s, not the older protocol", v)
	}
	body := e.query(t, a, e.ro, readSQL)
	if q := parseBody(t, body); len(q.Rows) != 2 || q.Ticket != "" {
		t.Fatalf("the same call did not return rows: %s", body)
	}
	if log.count() != 1 {
		t.Errorf("%d dialogs, want one", log.count())
	}
}

func TestAsk_ASK_C3_interruptedCallLeavesTheTicketInTheInbox(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	a, log := mcpAgentAnswering(t, func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := a.cs.CallTool(ctx, &mcp.CallToolParams{Name: "query", Arguments: map[string]any{"connection_id": e.ro, "sql": readSQL}})
		done <- err
	}()
	for deadline := time.Now().Add(10 * time.Second); log.count() == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the dialog never opened")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the interrupted call did not return")
	}
	items := e.inbox(t)
	if len(items) != 1 {
		t.Fatalf("the Inbox holds %d tickets, want the one left open", len(items))
	}
	got := a.call(t, "cancel", map[string]any{"ticket": items[0].TicketID})
	if !strings.Contains(got, string(types.TicketCancelled)) {
		t.Errorf("the ticket could not be cancelled: %s", got)
	}
	if n := len(e.inbox(t)); n != 0 {
		t.Errorf("the Inbox still holds %d tickets", n)
	}
}

func TestAsk_ASK_C4_declineRefusesAndNothingRuns(t *testing.T) {
	e := newLeanEnv(t)
	runs := e.counter(t, "ask_c4")
	a, _ := mcpAgentAnswering(t, answering("decline"))
	body := e.query(t, a, e.rw, counted("ask_c4"))
	if !strings.Contains(body, string(types.CodeApprovalRefused)) || strings.Contains(body, `"ready"`) {
		t.Errorf("the agent did not get a refusal: %s", body)
	}
	if got := runs(); got != 1 {
		t.Errorf("the statement executed %d times, want the one preview", got)
	}
	if n := len(e.inbox(t)); n != 0 {
		t.Errorf("the refused ticket is still in the Inbox")
	}
}

func TestAsk_ASK_C5_cancelLeavesTheTicketOpenWithALink(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	a, _ := mcpAgentAnswering(t, answering("cancel"))
	body := e.query(t, a, e.ro, readSQL)
	q := parseBody(t, body)
	if q.Ticket == "" || !strings.Contains(body, "/r/"+q.Ticket) || !strings.Contains(body, "get_result") {
		t.Fatalf("the agent did not get the ticket, a link and the poll instruction: %s", body)
	}
	if items := e.inbox(t); len(items) != 1 || items[0].TicketID != q.Ticket {
		t.Errorf("the ticket is not open in the Inbox: %v", items)
	}
	got := e.approveAndWait(t, a, body)
	if !strings.Contains(got, `"ready"`) {
		t.Errorf("get_result after approval: %s", got)
	}
}

func TestAsk_ASK_C6_clientWithoutElicitationGetsTicketAndLink(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	body := e.query(t, e.agent, e.ro, readSQL)
	q := parseBody(t, body)
	if q.Ticket == "" || !strings.Contains(body, "/r/"+q.Ticket) || !strings.Contains(body, "get_result") {
		t.Fatalf("no ticket, link and instruction: %s", body)
	}
	got := e.approveAndWait(t, e.agent, body)
	if !strings.Contains(got, `"ready"`) || !strings.Contains(got, `"rows"`) {
		t.Errorf("get_result returned no rows after approval: %s", got)
	}
}

func schemaJSON(t *testing.T, p *mcp.ElicitParams) string {
	t.Helper()
	b, err := json.Marshal(p.RequestedSchema)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAsk_ASK_C7_aReadDialogHasNoControlsBesideAcceptAndDecline(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	a, log := mcpAgentAnswering(t, accepting(nil))
	e.query(t, a, e.ro, readSQL)
	s := schemaJSON(t, log.first(t))
	if !strings.Contains(s, `"properties":{}`) || strings.Contains(s, "required") {
		t.Errorf("a read dialog has fields to fill: %s", s)
	}
}

func TestAsk_ASK_C8_enterOnAWriteWithNothingSelectedCommitsNothing(t *testing.T) {
	e := newLeanEnv(t)
	runs := e.counter(t, "ask_c8")
	a, log := mcpAgentAnswering(t, accepting(map[string]any{}))
	body := e.query(t, a, e.rw, counted("ask_c8"))
	s := schemaJSON(t, log.first(t))
	for _, want := range []string{`"required":["decision"]`, `"Run once"`, `"Refuse"`, `"enum"`} {
		if !strings.Contains(s, want) {
			t.Errorf("the write dialog lacks %s: %s", want, s)
		}
	}
	if got := runs(); got != 1 {
		t.Errorf("the statement executed %d times, want the one preview", got)
	}
	if n := len(e.inbox(t)); n != 1 {
		t.Errorf("the ticket should stay open, Inbox holds %d: %s", n, body)
	}
}

func TestAsk_ASK_C9_theDialogNamesWhatAHumanDecidesOn(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	e.declare(t, e.ro, map[string]types.ColumnPolicy{"public.customers.email": token("email"), "public.customers.id": {Policy: types.PolicyAllow}})
	a, log := mcpAgentAnswering(t, answering("cancel"))
	sql := "SELECT email FROM customers WHERE id = 4"
	e.query(t, a, e.ro, sql)
	msg := log.first(t).Message
	for _, want := range []string{"shop_ro", "customers", "email", "rows", sql, "profile", "why"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the dialog lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(strings.ToLower(msg), customerFourEmail(t, e)) {
		t.Errorf("the dialog holds a cleartext value:\n%s", msg)
	}
}

func TestAsk_ASK_C10_aWriteDialogHoldsNoRowAndNoResolvedValue(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.rw, map[string]types.ColumnPolicy{"public.customers.email": token("email"), "public.customers.id": {Policy: types.PolicyAllow}})
	e.super(t, "UPDATE orders SET shipping_note = 'old-note-xyz' WHERE id = 1")
	a, log := mcpAgentAnswering(t, answering("cancel"))
	tok := e.mintEmailToken(t, a, e.rw)
	email := customerFourEmail(t, e)
	body := e.query(t, a, e.rw, "UPDATE orders SET shipping_note = $1 WHERE id = 1", map[string]any{"token": tok})
	q := parseBody(t, body)
	msg := log.first(t).Message
	lower := strings.ToLower(msg)
	for _, bad := range []string{email, email[:6], "old-note-xyz"} {
		if strings.Contains(lower, bad) {
			t.Errorf("the dialog holds %q:\n%s", bad, msg)
		}
	}
	if q.Ticket == "" || !strings.Contains(msg, "/r/"+q.Ticket) {
		t.Errorf("the dialog does not link to the rows (ticket %q):\n%s", q.Ticket, msg)
	}
}

func TestAsk_ASK_C11_approvedInTheInboxThenAcceptedRunsOnce(t *testing.T) {
	e := newLeanEnv(t)
	runs := e.counter(t, "ask_c11")
	a, _ := mcpAgentAnswering(t, func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		items, err := e.op.ListApprovals(ctx)
		if err != nil || len(items) != 1 {
			t.Errorf("Inbox while the dialog is open: %d items, %v", len(items), err)
			return &mcp.ElicitResult{Action: "cancel"}, nil
		}
		if err := e.op.DecideApproval(ctx, items[0].TicketID, client.DecisionApprove); err != nil {
			t.Errorf("approve in the Inbox: %v", err)
		}
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"decision": "Run once"}}, nil
	})
	body := e.query(t, a, e.rw, counted("ask_c11"))
	if !strings.Contains(body, `"executed_rows"`) {
		t.Errorf("the write did not return its result: %s", body)
	}
	if got := runs(); got != 2 {
		t.Errorf("executed %d times, want one preview and one commit", got)
	}
}

func TestAsk_ASK_C12_refusedInTheInboxThenAcceptedDoesNotRun(t *testing.T) {
	e := newLeanEnv(t)
	runs := e.counter(t, "ask_c12")
	a, _ := mcpAgentAnswering(t, func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
		items, err := e.op.ListApprovals(ctx)
		if err != nil || len(items) != 1 {
			t.Errorf("Inbox while the dialog is open: %d items, %v", len(items), err)
			return &mcp.ElicitResult{Action: "cancel"}, nil
		}
		if err := e.op.DecideApproval(ctx, items[0].TicketID, client.DecisionRefuse); err != nil {
			t.Errorf("refuse in the Inbox: %v", err)
		}
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"decision": "Run once"}}, nil
	})
	body := e.query(t, a, e.rw, counted("ask_c12"))
	if !strings.Contains(body, string(types.CodeApprovalRefused)) {
		t.Errorf("the agent did not get the refusal: %s", body)
	}
	if got := runs(); got != 1 {
		t.Errorf("executed %d times, want the one preview", got)
	}
}

// A retry names its ticket in RequestState, which the client controls. Nothing
// proves it came from a dialog, so the daemon route is the only check.
func forgedRetry(t *testing.T, a *agentSession, conn, state string) string {
	t.Helper()
	res, err := a.cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:         "query",
		Arguments:    map[string]any{"connection_id": conn, "sql": "SELECT 1"},
		RequestState: state,
		InputResponses: mcp.InputResponseMap{
			"decision": &mcp.ElicitResult{Action: "accept", Content: map[string]any{"decision": "Run once"}},
		},
	})
	if err != nil {
		return err.Error()
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n") + fmt.Sprint(res.StructuredContent)
}

func TestAsk_ASK_C13_forgedOrForeignTicketRunsNothing(t *testing.T) {
	e := newLeanEnv(t)
	runs := e.counter(t, "ask_c13")
	waiting := e.query(t, e.agent, e.rw, counted("ask_c13"))
	ticket := parseBody(t, waiting).Ticket
	other, _ := mcpAgentAnswering(t, answering("cancel"))
	for name, state := range map[string]string{"forged": "0123456789abcdef0123456789abcdef", "another session's": ticket} {
		got := forgedRetry(t, other, e.rw, state)
		if !strings.Contains(got, "resubmit") || strings.Contains(got, `"ready"`) {
			t.Errorf("%s ticket: %s", name, got)
		}
	}
	if got := runs(); got != 1 {
		t.Errorf("executed %d times, want the one preview", got)
	}
	if items := e.inbox(t); len(items) != 1 || items[0].TicketID != ticket {
		t.Errorf("the owner's ticket was disturbed: %v", items)
	}
}

// The daemon cannot be restarted inside this process, so a restart is stood in for
// by what it causes: a retry names a ticket the daemon no longer knows.
func TestAsk_ASK_C14_aTicketTheDaemonNoLongerKnowsRunsNothing(t *testing.T) {
	e := newLeanEnv(t)
	runs := e.counter(t, "ask_c14")
	a, _ := mcpAgentAnswering(t, answering("cancel"))
	got := forgedRetry(t, a, e.rw, "fedcba9876543210fedcba9876543210")
	if !strings.Contains(got, "resubmit") {
		t.Errorf("the error does not say to resubmit: %s", got)
	}
	if runs() != 0 {
		t.Error("a statement ran")
	}
}

func TestAsk_ASK_C15_noToolDecidesATicketAndNoArgumentCarriesAnAnswer(t *testing.T) {
	e := newLeanEnv(t)
	res, err := e.agent.cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		for _, bad := range []string{"approve", "decide", "answer", "accept", "grant", "refuse"} {
			if strings.Contains(tool.Name, bad) {
				t.Errorf("tool %s can decide", tool.Name)
			}
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(raw, &schema)
		for prop := range schema.Properties {
			for _, bad := range []string{"decision", "answer", "approve", "accept", "action", "response", "input_responses"} {
				if strings.Contains(strings.ToLower(prop), bad) {
					t.Errorf("tool %s takes %s", tool.Name, prop)
				}
			}
		}
	}
}

func TestAsk_ASK_C16_aSessionWithoutATicketOfItsOwnIsRefused(t *testing.T) {
	e := newLeanEnv(t)
	runs := e.counter(t, "ask_c16")
	owner, err := client.Dial(t.Context(), e.sock, types.ClientInfo{Name: "owner", Version: "1", PID: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	out, err := owner.Query(t.Context(), e.rw, counted("ask_c16"), nil, 0)
	if err != nil || out.Ticket == nil {
		t.Fatalf("no ticket: %v", err)
	}
	ticket := out.Ticket.ID
	other, err := client.Dial(t.Context(), e.sock, types.ClientInfo{Name: "other", Version: "1", PID: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.AnswerTicket(t.Context(), ticket, true); err == nil {
		t.Error("another session answered the ticket")
	}
	if code := e.status(t, http.MethodPost, "/v1/tickets/"+ticket+"/answer"); code < 400 {
		t.Errorf("a socket request with no session got %d", code)
	}
	if got := runs(); got != 1 {
		t.Errorf("executed %d times, want the one preview", got)
	}
	if items := e.inbox(t); len(items) != 1 {
		t.Errorf("the ticket was settled by a stranger")
	}
	if res, err := owner.AnswerTicket(t.Context(), ticket, false); err != nil || res.State != types.TicketRefused {
		t.Errorf("the owner could not answer its own ticket: %v %v", res, err)
	}
}

func notifyShim(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the desktop notifier runs notify-send on Linux")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "notified")
	script := "#!/bin/sh\n" + strings.ReplaceAll(body, "$OUT", out)
	if err := os.WriteFile(filepath.Join(dir, "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return out
}

func TestAsk_ASK_C17_aTicketRaisesADesktopNotificationWithTheLink(t *testing.T) {
	out := notifyShim(t, "printf '%s\\n' \"$@\" > $OUT\n")
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	ticket := parseBody(t, e.query(t, e.agent, e.ro, readSQL)).Ticket
	var got []byte
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got, _ = os.ReadFile(out); len(got) > 0 {
			break
		}
	}
	if !strings.Contains(string(got), "/r/"+ticket) {
		t.Errorf("the notification did not carry the link: %q", got)
	}
}

func TestAsk_ASK_C18_aFailingNotifierDoesNotStopTheStatement(t *testing.T) {
	tried := notifyShim(t, "echo tried > $OUT\nsleep 1\nexit 1\n")
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	start := time.Now()
	body := e.query(t, e.agent, e.ro, readSQL)
	if time.Since(start) > 800*time.Millisecond {
		t.Errorf("the statement waited for the notifier: %s", time.Since(start))
	}
	if parseBody(t, body).Ticket == "" {
		t.Fatalf("no ticket: %s", body)
	}
	if got := e.approveAndWait(t, e.agent, body); !strings.Contains(got, `"ready"`) {
		t.Errorf("the statement did not run after approval: %s", got)
	}
	if got, _ := os.ReadFile(tried); len(got) == 0 {
		t.Error("the notifier was never run")
	}
}

type loopback struct {
	base, csrf string
	hc         *http.Client
}

func (e *leanEnv) loopback(t *testing.T) *loopback {
	t.Helper()
	rep, err := e.op.GetDoctor(t.Context())
	if err != nil || rep.UIBase == "" {
		t.Fatalf("doctor: %v %q", err, rep.UIBase)
	}
	l := &loopback{base: rep.UIBase, hc: &http.Client{}}
	resp, err := l.hc.Get(l.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "keeper_csrf" {
			l.csrf = c.Value
		}
	}
	return l
}

func (l *loopback) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, l.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", l.base)
	req.Header.Set("X-Keeper-CSRF", l.csrf)
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAsk_ASK_C19_theTicketPageApprovesForTheWaitingAgent(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	l := e.loopback(t)
	body := e.query(t, e.agent, e.ro, readSQL)
	ticket := parseBody(t, body).Ticket
	if !strings.Contains(body, l.base+"/r/"+ticket) {
		t.Errorf("the link is not on the UI base %s: %s", l.base, body)
	}
	if code, page := l.do(t, http.MethodGet, "/r/"+ticket, ""); code != 200 || !strings.Contains(page, "<") {
		t.Fatalf("the ticket link serves %d", code)
	}
	code, facts := l.do(t, http.MethodGet, "/v1/approvals/"+ticket, "")
	if code != 200 || !strings.Contains(facts, ticket) {
		t.Fatalf("GET approval: %d %s", code, facts)
	}
	if code, out := l.do(t, http.MethodPost, "/v1/approvals/"+ticket+"/decide", `{"decision":"approve"}`); code != 200 {
		t.Fatalf("decide: %d %s", code, out)
	}
	got := e.agent.call(t, "get_result", map[string]any{"ticket": ticket, "wait_ms": 10000})
	if !strings.Contains(got, `"ready"`) || !strings.Contains(got, `"rows"`) {
		t.Errorf("the waiting agent got: %s", got)
	}
}

func TestAsk_ASK_C20_aSettledOrUnknownTicketOffersNoDecision(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	l := e.loopback(t)
	ticket := parseBody(t, e.query(t, e.agent, e.ro, readSQL)).Ticket
	if code, _ := l.do(t, http.MethodGet, "/v1/approvals/"+ticket, ""); code != http.StatusOK {
		t.Fatalf("a waiting ticket answered %d", code)
	}
	if code, _ := l.do(t, http.MethodPost, "/v1/approvals/"+ticket+"/decide", `{"decision":"refuse"}`); code != 200 {
		t.Fatal("could not settle the ticket")
	}
	for name, id := range map[string]string{"settled": ticket, "unknown": "00000000000000000000000000000000"} {
		if code, out := l.do(t, http.MethodGet, "/v1/approvals/"+id, ""); code != http.StatusNotFound {
			t.Errorf("%s ticket: GET answered %d %s", name, code, out)
		}
		if code, _ := l.do(t, http.MethodPost, "/v1/approvals/"+id+"/decide", `{"decision":"approve"}`); code < 400 {
			t.Errorf("%s ticket: a decision was accepted", name)
		}
	}
}
