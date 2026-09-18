package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

	if c.SessionID() != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", c.SessionID())
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
	mux.HandleFunc("POST /v1/session/intent", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Keeper-Session")
		writeJSON(t, w, http.StatusOK, okResponse{OK: true})
	})
	sock, _ := newTestServer(t, mux)

	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if err := c.SetSessionIntent(t.Context(), "reconciliation OPS-441"); err != nil {
		t.Fatalf("SetSessionIntent: %v", err)
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
			{ID: "c1", Name: "prod", Engine: "postgresql", Database: "app", Role: "app_ro", Degraded: false},
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
			Tier:       types.Tier0Run,
			AuditID:    "a1",
		})
	})
	mux.HandleFunc("POST /v1/connections/c2/query", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, types.Ticket{
			ID:      "tik-1",
			State:   types.TicketPending,
			Tier:    types.Tier2Judge,
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
			Code:    types.CodeConnectionDisabled,
			Summary: "connection missing has unaccepted findings",
			Action:  "run keeper connection accept",
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
	if !IsCode(err, types.CodeConnectionDisabled) {
		t.Errorf("IsCode(CodeConnectionDisabled) = false, err = %v", err)
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

func TestGetResultWaitMsQueryParam(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	var gotQuery string
	mux.HandleFunc("GET /v1/tickets/tik-1", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSON(t, w, http.StatusOK, TicketResult{State: types.TicketPending})
	})
	sock, _ := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if _, err := c.GetResult(t.Context(), "tik-1", 999999); err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if gotQuery != "wait_ms=25000" {
		t.Errorf("query = %q, want wait_ms=25000", gotQuery)
	}
}

func TestGetAuthorizationResultNeverCarriesToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"session_id": "sess-1"})
	})
	mux.HandleFunc("GET /v1/requests/req-1", func(w http.ResponseWriter, r *http.Request) {
		// Even if keeperd misbehaved and sent a token, the client must not
		// hand it back from GetAuthorizationResult (SPEC R8.7g).
		writeJSON(t, w, http.StatusOK, RequestResult{State: "ready", Token: "should-not-leak"})
	})
	sock, _ := newTestServer(t, mux)
	c, err := Dial(t.Context(), sock, types.ClientInfo{Name: "keeper-mcp"})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	res, err := c.GetAuthorizationResult(t.Context(), "req-1", 1000)
	if err != nil {
		t.Fatalf("GetAuthorizationResult: %v", err)
	}
	if res.Token != "" {
		t.Errorf("Token = %q, want empty", res.Token)
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
