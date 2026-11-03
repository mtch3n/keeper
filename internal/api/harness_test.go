package api_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/api"
	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// ---------------------------------------------------------------- pipeline

// fakePipeline stands in for internal/pipeline. Its default behaviour is the one
// the daemon's ticket logic depends on: a statement escalates while nothing
// authorizes it, and runs once something does.
type fakePipeline struct {
	mu       sync.Mutex
	escalate bool
	facts    types.ApprovalFacts
	preview  *types.WritePreview
	err      error
	auths    []string
	params   [][]ports.Param
}

func (p *fakePipeline) Explain(context.Context, types.Session, string, string, []ports.Param) (*types.ExplainResult, error) {
	return &types.ExplainResult{StatementType: "SELECT", Outcome: types.OutcomeRun}, nil
}

func (p *fakePipeline) Query(_ context.Context, _ types.Session, _, _ string, params []ports.Param, maxRows int, auth string) (*types.QueryResult, *types.Ticket, error) {
	p.mu.Lock()
	p.auths = append(p.auths, auth)
	p.params = append(p.params, params)
	escalate, err, preview := p.escalate, p.err, p.preview
	p.mu.Unlock()

	if err != nil {
		return nil, nil, err
	}
	if escalate && auth == "" {
		return nil, &types.Ticket{
			State:  types.TicketPending,
			Reason: "needs a human", AuditID: "audit-1", Preview: preview,
		}, nil
	}
	return &types.QueryResult{
		Rows: [][]any{{1}}, RowCount: 1,
		AuditID: "audit-1", Authorization: auth,
		Columns: []types.ColumnMeta{{Name: "id", Type: "int4", Policy: types.PolicyAllow}},
	}, nil, nil
}

func (p *fakePipeline) Facts(context.Context, types.Session, string, string, []ports.Param) (*types.ApprovalFacts, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.facts
	return &f, nil
}

func (p *fakePipeline) CommitWrite(_ context.Context, _ types.Session, _, _ string, _ []ports.Param, auth string) (*types.QueryResult, error) {
	p.mu.Lock()
	p.auths = append(p.auths, auth)
	p.mu.Unlock()
	return &types.QueryResult{RowCount: 1, ExecutedRows: 1, Authorization: auth, AuditID: "audit-1"}, nil
}

func (p *fakePipeline) seenAuths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.auths...)
}

// ---------------------------------------------------------------- listener

// pipeListener is a net.Pipe-backed listener. It gives each client its own real
// net.Conn, which is what makes connection lifetime — and therefore session
// lifetime (R3.4e) — testable without a socket on disk.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "unix" }
func (pipeAddr) String() string  { return "keeper.sock" }

// ---------------------------------------------------------------- rig

// testCSRF is the loopback token every rig's server is built with.
const testCSRF = "test-csrf-token"

type rig struct {
	t *testing.T

	d   *daemon.Daemon
	srv *api.Server

	vault *fakeVault
	cat   *fakeCatalog
	exec  *fakeExecutor
	red   *fakeRedactor
	alog  *fakeAuditLog
	pipe  *fakePipeline
	notes *notes
	uictl *fakeUI

	sockLn  *pipeListener
	sockSrv *http.Server
	ui      *httptest.Server
	uiOrig  string
	conn    *types.Connection

	// open makes opening one connection's catalog fail or stall, the way an
	// unreachable or a slow server does.
	open map[string]func() error
}

func newRig(t *testing.T) *rig {
	t.Helper()
	return newRigWith(t, daemon.Config{Version: "test"})
}

// newRigWith is newRig with the daemon's tuning given, for tests of its timer.
func newRigWith(t *testing.T, cfg daemon.Config) *rig {
	t.Helper()
	return newRigSettings(t, cfg, nil)
}

// newRigSettings starts the daemon over a vault that already holds settings.
func newRigSettings(t *testing.T, cfg daemon.Config, settings *types.Settings) *rig {
	t.Helper()

	conn := &types.Connection{
		ID: "c1", Name: "prod", Engine: "postgres", Database: "app", Role: "app_ro",
		Reads: types.ReadsUncertain, Limits: types.DefaultLimits(),
	}
	r := &rig{
		t:     t,
		vault: newVault(conn),
		cat:   newCatalog(),
		exec:  &fakeExecutor{},
		red:   newRedactor(),
		alog:  &fakeAuditLog{},
		pipe:  &fakePipeline{},
		notes: &notes{},
		uictl: &fakeUI{},
		conn:  conn,
		open:  map[string]func() error{},
	}

	d, err := daemon.New(cfg, daemon.Deps{
		Vault: r.vault,
		Catalogs: func(id string) (ports.Catalog, error) {
			if f := r.open[id]; f != nil {
				if err := f(); err != nil {
					return nil, err
				}
			}
			return r.cat, nil
		},
		CatalogStore: r.cat,
		Executor:     r.exec,
		Redactor:     r.red,
		Audit:        r.alog,
		Detector:     fakeDetector{},
		Pipeline:     r.pipe,
		Notifier:     r.notes,
		UI:           r.uictl,
	})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	r.d = d
	r.vault.settings = settings
	if err := d.StartUI(t.Context()); err != nil {
		t.Fatalf("StartUI: %v", err)
	}

	// The loopback listener first, so the server knows the port it must accept
	// in Host and Origin.
	ts := httptest.NewUnstartedServer(nil)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	r.srv = api.New(d, api.Options{LoopbackPort: port, CSRFToken: testCSRF})
	ts.Config.Handler = r.srv.Loopback()
	ts.Config.ConnContext = d.ConnContext
	ts.Config.ConnState = d.ConnState
	ts.Start()
	r.ui = ts
	r.uiOrig = ts.URL

	r.sockLn = newPipeListener()
	r.sockSrv = &http.Server{
		Handler:     r.srv.Socket(),
		ConnContext: d.ConnContext,
		ConnState:   d.ConnState,
	}
	go r.sockSrv.Serve(r.sockLn)

	t.Cleanup(func() {
		r.sockSrv.Close()
		r.sockLn.Close()
		ts.Close()
		d.Close()
	})
	return r
}

// client is one connection to one of the two listeners.
type client struct {
	t      *testing.T
	http   *http.Client
	tr     *http.Transport
	base   string
	header http.Header
}

// socket opens a new connection to the socket listener. Each client gets its own
// transport, and therefore its own net.Conn and its own session.
func (r *rig) socket() *client {
	r.t.Helper()
	tr := &http.Transport{DialContext: r.sockLn.dial, MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1}
	return &client{t: r.t, http: &http.Client{Transport: tr}, tr: tr, base: "http://keeper", header: http.Header{}}
}

// agent opens a socket connection and completes the handshake.
func (r *rig) agent() *client {
	r.t.Helper()
	c := r.socket()
	var out struct {
		SessionID string `json:"session_id"`
	}
	c.mustJSON("POST", "/v1/session", map[string]any{
		"client": types.ClientInfo{Name: "keeper-mcp", Version: "1", PID: 1, Workspace: "/w"},
	}, &out)
	c.header.Set("X-Keeper-Session", out.SessionID)
	return c
}

func (c *client) sessionID() string { return c.header.Get("X-Keeper-Session") }

// visitor talks to the loopback listener with a good Origin and the CSRF token
// and no dashboard session: any process on this machine can do as much.
func (r *rig) visitor() *client {
	c := &client{
		t: r.t, http: &http.Client{Timeout: 5 * time.Second}, tr: nil, base: r.ui.URL, header: http.Header{},
	}
	c.header.Set("Origin", r.uiOrig)
	c.header.Set("X-Keeper-CSRF", testCSRF)
	return c
}

// browser is a visitor that holds the dashboard session `keeper ui` opens.
func (r *rig) browser() *client {
	c := r.visitor()
	_, secret := r.d.UISession()
	c.header.Set("Cookie", "keeper_ui="+secret)
	return c
}

// fakeUI stands for the loopback listener's lifecycle.
type fakeUI struct {
	mu            sync.Mutex
	starts, stops int
}

func (u *fakeUI) Start() (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.starts++
	return "http://127.0.0.1:7774", nil
}

func (u *fakeUI) Stop() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stops++
}

func (u *fakeUI) counts() (starts, stops int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.starts, u.stops
}

// notes records what the desktop notifier was told.
type notes struct {
	mu    sync.Mutex
	links []string
}

func (n *notes) Notify(_ context.Context, _, link string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.links = append(n.links, link)
}

func (n *notes) seen() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.links...)
}

func (c *client) do(method, path string, body any) (*http.Response, []byte) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.MarshalWrite(&buf, body); err != nil {
			c.t.Fatalf("marshal: %v", err)
		}
		rdr = &buf
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		c.t.Fatalf("new request: %v", err)
	}
	for k, vs := range c.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (c *client) mustJSON(method, path string, body, out any) []byte {
	c.t.Helper()
	resp, raw := c.do(method, path, body)
	if resp.StatusCode/100 != 2 {
		c.t.Fatalf("%s %s: status %d: %s", method, path, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, raw, err)
		}
	}
	return raw
}

func (c *client) close() {
	if c.tr != nil {
		c.tr.CloseIdleConnections()
	}
}

func decodeError(t *testing.T, raw []byte) *types.Error {
	t.Helper()
	var e types.Error
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("decode error %s: %v", raw, err)
	}
	return &e
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func containsToken(raw []byte, token string) bool {
	return token != "" && strings.Contains(string(raw), token)
}
