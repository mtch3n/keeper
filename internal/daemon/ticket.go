package daemon

import (
	"context"
	"net"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// ticket is an escalation the agent polls. SPEC R3.4b: 128 bits of CSPRNG
// entropy, bound to the issuing session *and* its connection, expiring with the
// session. The statement is kept so an approval can execute it without the agent
// re-sending anything a human already read.
type ticket struct {
	t       types.Ticket
	conn    net.Conn
	sql     string
	params  []ports.Param
	maxRows int
	write   bool

	approver  string
	result    *types.QueryResult
	failure   error
	settledAt time.Time

	// updated is closed on every state change and replaced, so a waiter can park
	// on it without polling. §3.2's get_result is a wait, not a spin.
	updated chan struct{}
}

func (tk *ticket) notify() {
	close(tk.updated)
	tk.updated = make(chan struct{})
}

// TicketView is what GET /v1/tickets/{id} reports.
type TicketView struct {
	Ticket types.Ticket
	Result *types.QueryResult
	Error  error
}

func (d *Daemon) setTicketStateLocked(tk *ticket, state types.TicketState, res *types.QueryResult, fail error) {
	tk.t.State = state
	tk.result = res
	tk.failure = fail
	if state.Terminal() {
		tk.settledAt = d.now()
	}
	tk.notify()
}

// issueTicket registers the pipeline's escalation and puts it on the shared
// queue. tmpl carries Tier, Reason and AuditID; the id, the session binding and
// the connection binding are the daemon's.
func (d *Daemon) issueTicket(s *Session, connID, sql string, params []ports.Param, maxRows int, write bool, tmpl *types.Ticket, facts *types.ApprovalFacts, preview *types.WritePreview, degraded bool) *types.Ticket {
	now := d.now()
	tk := &ticket{
		t: types.Ticket{
			ID:           capability(),
			SessionID:    s.id,
			ConnectionID: connID,
			State:        types.TicketPending,
			Reason:       tmpl.Reason,
			Tier:         tmpl.Tier,
			AuditID:      tmpl.AuditID,
			CreatedAt:    now,
		},
		conn:    s.conn,
		sql:     sql,
		params:  params,
		maxRows: maxRows,
		write:   write,
		updated: make(chan struct{}),
	}
	item := types.ApprovalItem{
		TicketID:           tk.t.ID,
		Session:            s.Info(),
		Connection:         connID,
		ConnectionDegraded: degraded,
		Tier:               tmpl.Tier,
		SQL:                forDisplay(sql),
		CreatedAt:          now,
		Write:              preview,
	}
	if facts != nil {
		item.Facts = *facts
	}

	d.mu.Lock()
	d.tickets[tk.t.ID] = tk
	d.queue = append(d.queue, &queueItem{item: item})
	d.mu.Unlock()

	d.hub.Publish(Event{Type: EventApproval, Data: ApprovalEvent{
		Action: "queued", TicketID: tk.t.ID, State: types.TicketPending, Item: &item,
	}})
	out := tk.t
	return &out
}

// WaitTicket is get_result. It refuses a ticket issued to a different session —
// R3.4b, without which a guessed or borrowed ticket retrieves another session's
// rows — and reports the refusal as an unknown ticket so that holding one is not
// an oracle for whether it exists.
func (d *Daemon) WaitTicket(ctx context.Context, s *Session, id string, wait time.Duration) (*TicketView, error) {
	wait = min(max(wait, 0), d.cfg.MaxWait)
	deadline := d.now().Add(wait)
	for {
		d.mu.Lock()
		tk, ok := d.tickets[id]
		if !ok || !d.ticketBelongsLocked(tk, s) {
			d.mu.Unlock()
			return nil, errTicketUnknown
		}
		view := &TicketView{Ticket: tk.t, Result: tk.result, Error: tk.failure}
		ch := tk.updated
		terminal := tk.t.State.Terminal()
		d.mu.Unlock()

		if terminal || !d.now().Before(deadline) {
			return view, nil
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-ch:
			timer.Stop()
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return view, nil
		case <-d.ctx.Done():
			timer.Stop()
			return view, nil
		}
	}
}

// ticketBelongsLocked is R3.4b's binding: the same session and the same
// connection. Both are checked, because a session id that surfaced on another
// connection is not this session (R3.4e).
func (d *Daemon) ticketBelongsLocked(tk *ticket, s *Session) bool {
	return s != nil && sameCapability(tk.t.SessionID, s.id) && tk.conn == s.conn
}

// CancelTicketsForConnection is R4.1e: a connection whose re-audit failed is
// disabled, its pool closed and its pending tickets cancelled.
func (d *Daemon) CancelTicketsForConnection(connID string) {
	var events []Event
	d.mu.Lock()
	for id, tk := range d.tickets {
		if tk.t.ConnectionID != connID || tk.t.State.Terminal() {
			continue
		}
		d.setTicketStateLocked(tk, types.TicketCancelled, nil, nil)
		events = append(events, d.dequeueLocked(id, "connection_disabled")...)
	}
	d.mu.Unlock()
	for _, e := range events {
		d.hub.Publish(e)
	}
}
