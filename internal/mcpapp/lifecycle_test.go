package mcpapp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/types"
)

// fakeDaemon is a keeperd that binds one session to each socket connection, as
// the real one does, and refuses a request whose session is not the one bound
// to its connection.
type fakeDaemon struct {
	mu       sync.Mutex
	sessions map[net.Conn]string
	n        int
	srv      *httptest.Server
	sock     string

	// answer is what POST /v1/tickets/{id}/answer does.
	answer func(w http.ResponseWriter)
	// result is the ticket GET /v1/tickets/{id} returns when it does not wait.
	result map[string]any
}

type connKey struct{}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{sessions: map[net.Conn]string{}}
	d.sock = filepath.Join(t.TempDir(), "keeper.sock")
	l, err := net.Listen("unix", d.sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.n++
		id := "sess-" + strconv.Itoa(d.n)
		d.sessions[r.Context().Value(connKey{}).(net.Conn)] = id
		d.mu.Unlock()
		reply(w, 200, map[string]string{"session_id": id})
	})
	agent := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			d.mu.Lock()
			bound := d.sessions[r.Context().Value(connKey{}).(net.Conn)]
			d.mu.Unlock()
			if bound == "" || bound != r.Header.Get("X-Keeper-Session") {
				reply(w, 403, types.Error{Code: types.CodePermissionDenied, Summary: "this route needs an agent session"})
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v1/connections", agent(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, []map[string]string{{"id": "c1", "name": "shop", "engine": "postgres"}})
	}))
	mux.HandleFunc("GET /v1/tickets/{id}", agent(func(w http.ResponseWriter, r *http.Request) {
		if ms, _ := strconv.Atoi(r.URL.Query().Get("wait_ms")); ms > 0 && d.result == nil {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		if d.result != nil {
			reply(w, 200, d.result)
			return
		}
		reply(w, 200, map[string]any{"state": types.TicketPending, "write": true})
	}))
	mux.HandleFunc("POST /v1/tickets/{id}/answer", agent(func(w http.ResponseWriter, r *http.Request) {
		d.answer(w)
	}))
	d.srv = &httptest.Server{Listener: l, Config: &http.Server{
		Handler: mux,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
	}}
	d.srv.Start()
	t.Cleanup(d.srv.Close)
	return d
}

func connect(t *testing.T, d *fakeDaemon) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "keeper-mcp-test"}, nil)
	registerTools(server, &daemonHolder{sock: d.sock})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(ctx context.Context, cs *mcp.ClientSession, tool string, args map[string]any) (string, error) {
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return "", err
	}
	if res.IsError {
		var parts []string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return "", &toolError{strings.Join(parts, "\n")}
	}
	b, _ := json.Marshal(res.StructuredContent)
	return string(b), nil
}

type toolError struct{ text string }

func (e *toolError) Error() string { return e.text }

func TestCancellingAGetResultMidWaitLeavesTheSessionWorking(t *testing.T) {
	d := newFakeDaemon(t)
	cs := connect(t, d)
	if _, err := call(t.Context(), cs, "list_connections", nil); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	if _, err := call(ctx, cs, "get_result", map[string]any{"ticket": "t1", "wait_ms": 25000}); err == nil {
		t.Fatal("the cancelled call returned")
	}

	for _, tool := range []string{"list_connections", "get_result"} {
		args := map[string]any{}
		if tool == "get_result" {
			args = map[string]any{"ticket": "t1"}
		}
		if out, err := call(t.Context(), cs, tool, args); err != nil {
			t.Fatalf("%s after the cancel: %v (%s)", tool, err, out)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sessions) != 1 {
		t.Errorf("the daemon saw %d sessions, want the one", len(d.sessions))
	}
}

func TestALostConnectionGivesTheAgentANewSessionAndAClearMessageOnce(t *testing.T) {
	d := newFakeDaemon(t)
	cs := connect(t, d)
	if _, err := call(t.Context(), cs, "list_connections", nil); err != nil {
		t.Fatal(err)
	}
	d.srv.CloseClientConnections()
	time.Sleep(50 * time.Millisecond)

	_, err := call(t.Context(), cs, "list_connections", nil)
	if err == nil || !strings.Contains(err.Error(), "new session") {
		t.Fatalf("the call that met the loss returned %v, want a message that the session is new", err)
	}
	for range 2 {
		if _, err := call(t.Context(), cs, "list_connections", nil); err != nil {
			t.Fatalf("a later call failed: %v", err)
		}
	}
}

func TestGetResultReturnsTheFieldsQueryDoes(t *testing.T) {
	d := newFakeDaemon(t)
	d.result = map[string]any{
		"state": types.TicketReady,
		"result": types.QueryResult{
			Rows: [][]any{{1}}, Columns: []types.ColumnMeta{{Name: "id", Type: "int4"}}, RowCount: 1,
			Truncated: true, TruncatedBy: types.CutByRows, ExecutedRows: 3, PreviewedRows: 4,
			Degradations: []types.Degradation{{Layer: "catalog", Reason: "stale"}}, AuditID: "a1", Hint: "keeper catalog edit",
		},
	}
	cs := connect(t, d)
	out, err := call(t.Context(), cs, "get_result", map[string]any{"ticket": "t1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"executed_rows":3`, `"previewed_rows":4`, `"truncated":true`, `"truncated_by":"rows"`, `"columns"`, `"degradations"`, `"hint":"keeper catalog edit"`, `"audit_id":"a1"`, `"row_count":1`} {
		if !strings.Contains(out, want) {
			t.Errorf("get_result lacks %s: %s", want, out)
		}
	}
}

func TestAFailureAfterTheAnswerWasSentIsNotReportedAsNothingRan(t *testing.T) {
	d := newFakeDaemon(t)
	d.answer = func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	cs := connect(t, d)
	_, err := call(t.Context(), cs, "query", map[string]any{"connection_id": "c1", "sql": "UPDATE t SET a = 1"})
	_ = err

	h := &daemonHolder{sock: d.sock}
	cli, derr := h.get(t.Context(), &mcp.CallToolRequest{})
	if derr != nil {
		t.Fatal(derr)
	}
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		RequestState:   "t1",
		InputResponses: mcp.InputResponseMap{dialogKey: &mcp.ElicitResult{Action: "accept", Content: map[string]any{dialogKey: choiceRun}}},
	}}
	_, aerr := h.answered(t.Context(), cli, req)
	kerr, ok := errors.AsType[*types.Error](aerr)
	if !ok || !strings.Contains(kerr.Action, "do not resubmit") || !strings.Contains(kerr.Action, "get_result") {
		t.Fatalf("error = %v, want do not resubmit and check get_result", aerr)
	}
	if strings.Contains(kerr.Summary+kerr.Action, "nothing ran") {
		t.Errorf("the failure claims nothing ran: %v", aerr)
	}
}
