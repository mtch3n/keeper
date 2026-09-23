// Package api is keeper's HTTP surface: one handler set behind two listeners.
//
//	unix   $XDG_RUNTIME_DIR/keeper.sock   mode 0600 in a 0700 parent   agents and the CLI
//	tcp    127.0.0.1:<port>               Origin and Host validated    the browser UI
//
// The two listeners exist because they carry different authority, and the
// difference is enforced per route rather than assumed from the address. An MCP
// session cannot register a connection, accept a G0 finding, approve a ticket,
// grant a path, edit the catalog or change a limit — §6.3 and R4.1f — and the
// agent surface is not reachable from a browser at all.
//
// This package is also the only place an error becomes a types.Error. A
// PostgreSQL error never travels this far; anything unrecognised here becomes a
// generic internal error rather than a string (CONTRACT §4).
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	jsonv1 "encoding/json"
	json "encoding/json/v2"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/daemon"
	"github.com/mtchen/keeper/internal/types"
)

// Options configure the server.
type Options struct {
	// LoopbackPort is the port the UI listener binds. It is part of the Host
	// allowlist, so a DNS rebinding attempt that resolves an attacker's name to
	// 127.0.0.1 arrives with the wrong Host and is refused.
	LoopbackPort int
	// CSRFToken is the token the UI must echo in X-Keeper-CSRF on every mutating
	// request. Generated when empty.
	CSRFToken string
	// MaxBody bounds a request body. Defaults to 1 MiB.
	MaxBody int64
	Logger  *slog.Logger
}

// Server holds the routes. Use Socket and Loopback to get the two handlers.
type Server struct {
	d    *daemon.Daemon
	log  *slog.Logger
	csrf string
	mux  *http.ServeMux
	ui   http.Handler

	maxBody int64
	hosts   []string
	origins []string
}

// surface is which listener a request arrived on.
type surface int

const (
	surfaceSocket surface = iota
	surfaceLoopback
)

// access is who may reach a route.
type access int

const (
	// accessHandshake opens a session. Socket only, and the one agent route that
	// does not already have one.
	accessHandshake access = iota
	// accessAgent is the MCP surface: socket, with a session bound to this
	// connection.
	accessAgent
	// accessHuman is the CLI over the socket and the UI over loopback. It is
	// never reachable from an MCP session (§6.3, R4.1f).
	accessHuman
	// accessShared is read-only description that both surfaces may see.
	accessShared
	// accessLoopback is the browser's own surface: the local page resolving a
	// request. Origin, Host and CSRF are already checked by guard.
	accessLoopback
	// accessPublic is the version handshake and the embedded UI.
	accessPublic
)

type ctxKey struct{}

// reqInfo is what the route wrapper resolved about a request.
type reqInfo struct {
	surface surface
	session *daemon.Session
}

func infoOf(ctx context.Context) *reqInfo {
	i, _ := ctx.Value(ctxKey{}).(*reqInfo)
	if i == nil {
		return &reqInfo{}
	}
	return i
}

// handlerFunc returns the value to encode, or an error. Returning (nil, nil)
// writes 204.
type handlerFunc func(ctx context.Context, rq *reqInfo, w http.ResponseWriter, r *http.Request) (any, error)

// New builds the handler set.
func New(d *daemon.Daemon, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = 1 << 20
	}
	if opts.CSRFToken == "" {
		opts.CSRFToken = rand.Text()
	}
	port := strconv.Itoa(opts.LoopbackPort)
	s := &Server{
		d:       d,
		log:     opts.Logger,
		csrf:    opts.CSRFToken,
		mux:     http.NewServeMux(),
		maxBody: opts.MaxBody,
		hosts:   []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port},
	}
	for _, h := range s.hosts {
		s.origins = append(s.origins, "http://"+h)
	}
	s.ui = s.uiHandler()
	s.routes()
	return s
}

// CSRFToken is the token the UI echoes. cmd/keeperd prints nothing about it and
// the UI reads it from the cookie the embedded app is served with.
func (s *Server) CSRFToken() string { return s.csrf }

// Socket is the handler for the unix socket: agents and the CLI.
func (s *Server) Socket() http.Handler {
	return s.withSurface(surfaceSocket, s.mux)
}

// Loopback is the handler for 127.0.0.1: the browser UI. Every request is
// checked for Origin and Host, and every mutating one for the CSRF token,
// because loopback binding alone is not a boundary (R3.4a, R8.7d).
func (s *Server) Loopback() http.Handler {
	return s.withSurface(surfaceLoopback, s.guard(s.mux))
}

func (s *Server) withSurface(sf surface, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// R8.7d: no-store everywhere. Nothing keeper serves is cacheable, and the
		// local page in particular must not be replayable from a browser cache.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		ctx := context.WithValue(r.Context(), ctxKey{}, &reqInfo{surface: sf})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// guard is the browser-facing check. It runs before routing so that an
// unroutable cross-origin request is refused too.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			s.fail(w, r, errBadHost)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !s.allowedOrigin(o) {
			// A browser on an unrelated site sends its own Origin. Refusing here
			// is what stops that page driving approvals or policy changes.
			s.fail(w, r, errBadOrigin)
			return
		}
		if mutating(r.Method) {
			if o := r.Header.Get("Origin"); !s.allowedOrigin(o) {
				// A mutating request with no Origin at all is a form post or a
				// non-browser client pointed at the browser listener.
				s.fail(w, r, errBadOrigin)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Keeper-CSRF")), []byte(s.csrf)) != 1 {
				s.fail(w, r, errNoCSRF)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func mutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func (s *Server) allowedHost(host string) bool {
	if host == "" {
		return false
	}
	for _, h := range s.hosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

func (s *Server) allowedOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	for _, o := range s.origins {
		if strings.EqualFold(origin, o) {
			return true
		}
	}
	return false
}

// handle registers one route with its access rule.
func (s *Server) handle(pattern string, acc access, h handlerFunc) {
	s.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.d.Touch()
		rq := infoOf(r.Context())
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		if err := s.authorize(rq, acc, r); err != nil {
			s.failTo(sw, r, err)
		} else {
			v, err := h(r.Context(), rq, sw, r)
			switch {
			case err != nil:
				s.failTo(sw, r, err)
			case v == nil:
				sw.WriteHeader(http.StatusNoContent)
			default:
				writeJSON(sw, http.StatusOK, v)
			}
		}
		// The matched pattern, never the path: /r/{request_id} carries a
		// capability and a request id is never logged (CONTRACT §3).
		s.log.Info("request",
			"method", r.Method, "route", r.Pattern, "status", sw.status,
			"surface", surfaceName(rq.surface), "ms", time.Since(start).Milliseconds())
	}))
}

// raw registers a route that writes its own response: SSE and the UI.
func (s *Server) raw(pattern string, acc access, h http.HandlerFunc) {
	s.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rq := infoOf(r.Context())
		if err := s.authorize(rq, acc, r); err != nil {
			s.fail(w, r, err)
			return
		}
		h(w, r)
	}))
}

// authorize is the route access rule. It is enforced by listener and by session,
// not by hope: the agent surface is socket-only, and the human surface is refused
// to anything holding an MCP session.
func (s *Server) authorize(rq *reqInfo, acc access, r *http.Request) error {
	agentConn := rq.surface == surfaceSocket && s.d.SessionOnConn(r.Context()) != nil
	claimsSession := r.Header.Get("X-Keeper-Session") != ""

	switch acc {
	case accessPublic:
		return nil

	case accessHandshake:
		if rq.surface != surfaceSocket {
			return errAgentOnly
		}
		return nil

	case accessAgent:
		if rq.surface != surfaceSocket {
			return errAgentOnly
		}
		sess, err := s.d.SessionFor(r.Context(), r.Header.Get("X-Keeper-Session"))
		if err != nil {
			return errSessionNeeded
		}
		rq.session = sess
		return nil

	case accessHuman:
		// §6.3: register_connection, set_policy, catalog init, approve, grants,
		// allow, denylist and every §4.5 limit are CLI and UI only. A connection
		// that ever handshook is an agent connection, so omitting the header is
		// not a way around this.
		if agentConn || claimsSession {
			return errHumanOnly
		}
		return nil

	case accessLoopback:
		if rq.surface != surfaceLoopback {
			return errUIOnly
		}
		return nil

	case accessShared:
		if rq.surface == surfaceSocket && claimsSession {
			sess, err := s.d.SessionFor(r.Context(), r.Header.Get("X-Keeper-Session"))
			if err != nil {
				return errSessionNeeded
			}
			rq.session = sess
		}
		return nil

	default:
		return errHumanOnly
	}
}

func surfaceName(sf surface) string {
	if sf == surfaceLoopback {
		return "loopback"
	}
	return "socket"
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.failTo(&statusWriter{ResponseWriter: w, status: http.StatusOK}, r, err)
}

func (s *Server) failTo(w http.ResponseWriter, r *http.Request, err error) {
	e, code := asError(err)
	writeJSON(w, code, e)
}

// statusWriter records the status for the access log.
type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.written = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// durationAsNano is threaded through every Marshal/Unmarshal call on this
// surface. encoding/json/v2 refuses to encode a time.Duration by default and
// types.Limits.StatementTimeout is one; v1's nanosecond representation is what
// internal/client decodes and what the vault stores (CONTRACT §2).
var durationAsNano = jsonv1.FormatDurationAsNano(true)

// writeJSON marshals into memory before it writes anything. Streaming straight
// at the ResponseWriter cannot fail safely: the status is already on the wire,
// so a mid-encode error ships a body that stops in the middle of a value under
// a 200, and every reader downstream — the CLI, the UI, `keeper doctor` — sees
// a successful response it cannot parse. Buffering keeps a failed encode a 500.
func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v, durationAsNano)
	if err != nil {
		// Not marshalled: encoding the error value could fail the same way and
		// recurse through failTo. The body is a constant for that reason, and
		// carries no detail of what failed (CONTRACT §4).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"` + string(types.CodeInternal) +
			`","summary":"keeper could not encode the response"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

// readJSON decodes a body. The body is never logged and never echoed: R8.7d
// requires request bodies to be redacted from server diagnostics, and the local
// form's body is the one value in the system the agent must never see.
func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, s.maxBody), v, durationAsNano); err != nil {
		return &types.ValidationError{Field: "body", Reason: "is not the JSON this route expects"}
	}
	return nil
}

// waitFor reads a bounded wait_ms. §3.2 caps it at 25000 so a harness never
// times out a tool call waiting on a human.
func (s *Server) waitFor(r *http.Request) time.Duration {
	v := r.URL.Query().Get("wait_ms")
	if v == "" {
		return 0
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms < 0 {
		return 0
	}
	return min(time.Duration(ms)*time.Millisecond, s.d.MaxWait())
}
