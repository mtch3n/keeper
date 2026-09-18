package daemon

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// connKey carries the net.Conn a request arrived on. http.Server.ConnContext
// puts it there once per connection and every request on that connection
// inherits it, which is what makes SPEC R3.4e — one socket connection, one
// session — enforceable rather than advisory.
type connKey struct{}

// ConnContext is http.Server.ConnContext. Both listeners use it.
func (d *Daemon) ConnContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, c)
}

// ConnState is http.Server.ConnState. A closed or hijacked connection ends its
// session and everything scoped to it.
func (d *Daemon) ConnState(c net.Conn, s http.ConnState) {
	if s == http.StateClosed || s == http.StateHijacked {
		d.CloseConn(c)
	}
}

// ConnOf returns the connection a request arrived on.
func ConnOf(ctx context.Context) net.Conn {
	c, _ := ctx.Value(connKey{}).(net.Conn)
	return c
}

// Session is one client connection to the daemon (SPEC R3.4e). Ticket binding
// (R3.4b), the reverse map (§8.4) and session-scoped grants (R9.3d) all scope to
// it, and a reconnect is a new session with an empty map.
type Session struct {
	id          string
	client      types.ClientInfo
	connectedAt time.Time
	conn        net.Conn

	mu     sync.Mutex
	intent string
}

// ID is the session identifier clients send in X-Keeper-Session.
func (s *Session) ID() string { return s.id }

// Conn is the connection the session is bound to.
func (s *Session) Conn() net.Conn { return s.conn }

// Info is the snapshot the approval queue, the audit log and the pipeline read.
func (s *Session) Info() types.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return types.Session{ID: s.id, Client: s.client, Intent: s.intent, ConnectedAt: s.connectedAt}
}

func (s *Session) setIntent(v string) {
	s.mu.Lock()
	s.intent = v
	s.mu.Unlock()
}

// OpenSession is the handshake. The session binds to the connection the request
// arrived on; a second handshake on the same connection returns the same
// session, because the connection is the session.
func (d *Daemon) OpenSession(ctx context.Context, client types.ClientInfo) (*Session, error) {
	c := ConnOf(ctx)
	if c == nil {
		return nil, keeperErr(types.CodePermissionDenied,
			"a session must be established on a keeper socket connection",
			"connect to the keeper socket")
	}
	d.mu.Lock()
	if s, ok := d.byConn[c]; ok {
		d.mu.Unlock()
		return s, nil
	}
	s := &Session{id: timeID(), client: client, connectedAt: d.now(), conn: c}
	d.sessions[s.id] = s
	d.byConn[c] = s
	d.mu.Unlock()

	d.hub.Publish(Event{Type: EventSession, Data: SessionEvent{Action: "opened", Session: s.Info()}})
	return s, nil
}

// SessionFor resolves the id a request carried, and refuses it when the request
// did not arrive on that session's own connection. A session id borrowed onto a
// second connection is not that session: R3.4e says the connection is the
// session, and every scope that hangs off it — tickets, the reverse map, session
// grants — would otherwise be reachable from a connection the daemon never
// handed it to.
func (d *Daemon) SessionFor(ctx context.Context, id string) (*Session, error) {
	if id == "" {
		return nil, errSessionRequired
	}
	c := ConnOf(ctx)
	d.mu.Lock()
	s, ok := d.sessions[id]
	d.mu.Unlock()
	if !ok || c == nil || s.conn != c {
		return nil, errSessionRequired
	}
	return s, nil
}

// SessionOnConn returns the session bound to the request's connection, if any.
func (d *Daemon) SessionOnConn(ctx context.Context) *Session {
	c := ConnOf(ctx)
	if c == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.byConn[c]
}

// Sessions lists the open sessions, for doctor and the UI.
func (d *Daemon) Sessions() []types.Session {
	d.mu.Lock()
	out := make([]types.Session, 0, len(d.sessions))
	for _, s := range d.sessions {
		out = append(out, s.Info())
	}
	d.mu.Unlock()
	sortByTime(out, func(s types.Session) time.Time { return s.ConnectedAt })
	return out
}

// SetIntent records set_session_intent. R10c screens it with the same rule pass a
// scan column gets, because the intent is user-supplied text that reaches the
// approval queue and the audit log.
func (d *Daemon) SetIntent(ctx context.Context, s *Session, intent string) error {
	if intent == "" {
		return errValidation("intent", "is required")
	}
	if err := d.deps.Audit.ScreenIntent(ctx, intent); err != nil {
		return errIntentScreened
	}
	s.setIntent(intent)
	d.hub.Publish(Event{Type: EventSession, Data: SessionEvent{Action: "intent", Session: s.Info()}})
	return nil
}

// CloseConn ends the session bound to a connection and drops everything scoped
// to it: tickets are cancelled, queue items are cancelled rather than silently
// removed, session grants are revoked, pending local requests are invalidated
// and the reverse map is cleared. R3.4d and R3.4e, and §2.5's rule that a human
// reading a row deserves to be told why it went.
func (d *Daemon) CloseConn(c net.Conn) {
	if c == nil {
		return
	}
	d.mu.Lock()
	s, ok := d.byConn[c]
	if !ok {
		d.mu.Unlock()
		return
	}
	delete(d.byConn, c)
	delete(d.sessions, s.id)

	var events []Event
	for id, tk := range d.tickets {
		if tk.t.SessionID != s.id {
			continue
		}
		if !tk.t.State.Terminal() {
			d.setTicketStateLocked(tk, types.TicketCancelled, nil, nil)
			events = append(events, d.dequeueLocked(id, "session_closed")...)
		}
		delete(d.tickets, id)
	}
	for id, g := range d.grants {
		if g.SessionID == s.id {
			delete(d.grants, id)
			events = append(events, Event{Type: EventApproval, Data: GrantEvent{Action: "revoked", GrantID: id}})
		}
	}
	for id, r := range d.requests {
		if r.r.SessionID != s.id {
			continue
		}
		if r.r.State == statePending {
			r.r.State = stateCancelled
			r.settledAt = d.now()
			r.notify()
			events = append(events, Event{Type: EventRequest, Data: RequestEvent{Action: "cancelled", Kind: r.r.Kind, State: r.r.State}})
		}
		delete(d.requests, id)
	}
	d.mu.Unlock()

	// The reverse map is memory only and dies with the session (§8.4, R3.4d).
	d.deps.Redactor.DropSession(s.id)

	events = append(events, Event{Type: EventSession, Data: SessionEvent{Action: "closed", Session: s.Info()}})
	for _, e := range events {
		d.hub.Publish(e)
	}
}
