package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/mtchen/keeper/internal/types"
)

// newTestServer starts an httptest.Server listening on a unix socket and
// returns the socket path plus the server for handler registration.
func newTestServer(t *testing.T, mux *http.ServeMux) (string, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "keeper.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: mux}}
	srv.Start()
	t.Cleanup(srv.Close)
	return sock, srv
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		if err := jsonv2.MarshalWrite(w, v, json.FormatDurationAsNano(true)); err != nil {
			t.Fatalf("write json: %v", err)
		}
	}
}

func TestDialHandshake(t *testing.T) {
	mux := http.NewServeMux()
	var gotSession types.ClientInfo
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Client types.ClientInfo `json:"client"`
		}
		if err := jsonv2.UnmarshalRead(r.Body, &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		gotSession = body.Client
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	sock, _ := newTestServer(t, mux)

	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp", Version: "0.1.0", PID: 42})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if c.sessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", c.sessionID)
	}
	if gotSession.Name != "keeper-mcp" || gotSession.PID != 42 {
		t.Errorf("handshake sent %+v", gotSession)
	}
	if !c.SessionAlive() {
		t.Error("SessionAlive() = false right after Dial")
	}
}

func TestDialVersionMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{
			"session_id":     "sess-1",
			"daemon_version": "9.9.9",
		})
	})
	sock, _ := newTestServer(t, mux)

	_, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err == nil {
		t.Fatal("expected version mismatch error")
	}
	var mismatch *VersionMismatchError
	if !asVersionMismatch(err, &mismatch) {
		t.Fatalf("error = %v, want *VersionMismatchError", err)
	}
	if mismatch.DaemonVersion != "9.9.9" || mismatch.ClientVersion != Version {
		t.Errorf("mismatch = %+v", mismatch)
	}
}

func asVersionMismatch(err error, out **VersionMismatchError) bool {
	if v, ok := err.(*VersionMismatchError); ok {
		*out = v
		return true
	}
	return false
}

func TestSessionHeaderCarried(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	var gotHeader string
	mux.HandleFunc("GET /v1/connections", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Keeper-Session")
		writeJSON(t, w, http.StatusOK, []ConnectionSummary{})
	})
	sock, _ := newTestServer(t, mux)

	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if _, err := c.ListConnections(t.Context()); err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if gotHeader != "sess-1" {
		t.Errorf("X-Keeper-Session = %q, want sess-1", gotHeader)
	}
}

func TestListConnections(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	mux.HandleFunc("GET /v1/connections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, []ConnectionSummary{
			{ID: "c1", Name: "prod", Engine: "postgresql", Profile: Profile{Database: "app", Username: "app_ro", Reads: types.ReadsUncertain}},
		})
	})
	sock, _ := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	conns, err := c.ListConnections(t.Context())
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(conns) != 1 || conns[0].Name != "prod" {
		t.Errorf("conns = %+v", conns)
	}
}

func TestQueryResultVsTicket(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	mux.HandleFunc("POST /v1/connections/c1/query", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, types.QueryResult{
			Rows:       [][]any{{"1"}},
			RowCount:   1,
			Transforms: map[string]types.Transform{},
			AuditID:    "a1",
		})
	})
	mux.HandleFunc("POST /v1/connections/c2/query", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, types.Ticket{
			ID:      "tik-1",
			State:   types.TicketPending,
			AuditID: "a2",
		})
	})
	sock, _ := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	out1, err := c.Query(t.Context(), "c1", "SELECT 1", nil, 0)
	if err != nil {
		t.Fatalf("Query c1: %v", err)
	}
	if out1.Result == nil || out1.Ticket != nil || out1.Result.RowCount != 1 {
		t.Errorf("c1 outcome = %+v", out1)
	}

	out2, err := c.Query(t.Context(), "c2", "SELECT * FROM users", nil, 0)
	if err != nil {
		t.Fatalf("Query c2: %v", err)
	}
	if out2.Ticket == nil || out2.Result != nil || out2.Ticket.ID != "tik-1" {
		t.Errorf("c2 outcome = %+v", out2)
	}
}

func TestErrorDecoding(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	mux.HandleFunc("GET /v1/connections/missing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, types.Error{
			Code:    types.CodeTicketUnknown,
			Summary: "no connection with that id is registered",
			Action:  "run keeper connection list",
		})
	})
	sock, _ := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	_, err = c.DescribeConnection(t.Context(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if kerr, ok := errors.AsType[*types.Error](err); !ok || kerr.Code != types.CodeTicketUnknown {
		t.Errorf("err = %v, want a *types.Error with code %s", err, types.CodeTicketUnknown)
	}
}

func TestWaitMsCapped(t *testing.T) {
	if got := waitMs(999999); got != 25000 {
		t.Errorf("waitMs(999999) = %d, want 25000", got)
	}
	if got := waitMs(100); got != 100 {
		t.Errorf("waitMs(100) = %d, want 100", got)
	}
}

func TestGetResultWaitsInSlicesNoLongerThanTheCap(t *testing.T) {
	defer func(old time.Duration) { waitSlice = old }(waitSlice)
	waitSlice = 20 * time.Millisecond
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	var mu sync.Mutex
	var waits []string
	mux.HandleFunc("GET /v1/tickets/tik-1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		waits = append(waits, r.URL.Query().Get("wait_ms"))
		mu.Unlock()
		writeJSON(t, w, http.StatusOK, TicketResult{State: types.TicketPending})
	})
	sock, _ := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	start := time.Now()
	if _, err := c.GetResult(t.Context(), "tik-1", 100); err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if took := time.Since(start); took < 90*time.Millisecond || took > time.Second {
		t.Errorf("a 100 ms wait took %v", took)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(waits) < 5 {
		t.Fatalf("the wait was not sliced: %v", waits)
	}
	for _, w := range waits {
		if w != "20" && w != "19" && w != "18" && w != "17" && w != "16" && w != "15" && w != "14" && w != "13" && w != "12" && w != "11" && w != "10" {
			if n, _ := strconv.Atoi(w); n > 20 || n < 0 {
				t.Errorf("a slice waited %q ms", w)
			}
		}
	}
}

// Cancelling a call ends the caller's wait and nothing else: net/http would
// close the only connection, and the connection is the session.
func TestCancellingACallKeepsTheSession(t *testing.T) {
	defer func(old time.Duration) { waitSlice = old }(waitSlice)
	waitSlice = 50 * time.Millisecond
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	mux.HandleFunc("GET /v1/tickets/tik-1", func(w http.ResponseWriter, r *http.Request) {
		ms, _ := strconv.Atoi(r.URL.Query().Get("wait_ms"))
		time.Sleep(time.Duration(ms) * time.Millisecond)
		writeJSON(t, w, http.StatusOK, TicketResult{State: types.TicketPending})
	})
	mux.HandleFunc("GET /v1/connections", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Keeper-Session") != "sess-1" {
			writeJSON(t, w, http.StatusForbidden, types.Error{Code: types.CodePermissionDenied, Summary: "this route needs an agent session"})
			return
		}
		writeJSON(t, w, http.StatusOK, []ConnectionSummary{})
	})
	sock := filepath.Join(t.TempDir(), "keeper.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingListener{Listener: l}
	srv := &httptest.Server{Listener: counting, Config: &http.Server{Handler: mux}}
	srv.Start()
	t.Cleanup(srv.Close)
	conns := &counting.accepted
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(t.Context())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := c.GetResult(ctx, "tik-1", 25000); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetResult returned %v, want the cancellation", err)
	}
	for range 3 {
		if _, err := c.ListConnections(t.Context()); err != nil {
			t.Fatalf("a call after the cancel failed: %v", err)
		}
	}
	if !c.SessionAlive() {
		t.Error("the session was reported lost")
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("the daemon saw %d connections, want the one session connection", n)
	}
}

type countingListener struct {
	net.Listener
	accepted atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return c, err
}

func TestALostConnectionIsReportedAsALostSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	mux.HandleFunc("GET /v1/connections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, []ConnectionSummary{})
	})
	sock, srv := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	srv.CloseClientConnections()
	time.Sleep(20 * time.Millisecond)

	_, err = c.ListConnections(t.Context())
	if _, ok := errors.AsType[*StaleSessionError](err); !ok {
		t.Fatalf("err = %v, want a stale session", err)
	}
	if c.SessionAlive() {
		t.Error("a client whose connection went still reports a live session")
	}
}

func TestOneConnectionForWholeSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	mux.HandleFunc("GET /v1/connections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, []ConnectionSummary{})
	})
	sock, _ := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	for range 10 {
		if _, err := c.ListConnections(t.Context()); err != nil {
			t.Fatalf("ListConnections: %v", err)
		}
	}
	if !c.SessionAlive() {
		t.Error("SessionAlive() = false after repeated calls; connection was not reused")
	}
}

func TestStartDaemonNoBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	sock := filepath.Join(t.TempDir(), "keeper.sock")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := StartDaemon(ctx, sock); err == nil {
		t.Fatal("expected error when keeperd cannot be found")
	}
}
