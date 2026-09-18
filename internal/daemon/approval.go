package daemon

import (
	"context"
	"slices"

	"github.com/mtchen/keeper/internal/types"
)

// queueItem is one row of the shared queue.
type queueItem struct{ item types.ApprovalItem }

// Decision is POST /v1/approvals/{ticket}/decide.
type Decision struct {
	// Decide is "approve" or "refuse".
	Decide string
	// Actor is the human the acceptance is recorded against.
	Actor string
	// Grant, when set, turns this one approval into an allow rule over exactly
	// the relations the plan touched. R9.3a: keeper proposes the narrowest scope
	// covering the approved request, and widening is a separate explicit act.
	Grant *GrantRequest
}

// Approvals is the shared queue: one queue across every session, oldest first,
// always (R9.1). The slice is append-ordered and the copy returned here is never
// re-sorted, because a queue that reorders itself while someone is reading it is
// how the wrong row gets approved.
func (d *Daemon) Approvals() []types.ApprovalItem {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]types.ApprovalItem, 0, len(d.queue))
	for _, q := range d.queue {
		out = append(out, q.item)
	}
	return out
}

// dequeueLocked removes a ticket's item and returns the events announcing it.
// A session that disconnects marks its items cancelled rather than removing them
// silently: the state travels on the event, because types.ApprovalItem has no
// field for it.
func (d *Daemon) dequeueLocked(ticketID, action string) []Event {
	i := slices.IndexFunc(d.queue, func(q *queueItem) bool { return q.item.TicketID == ticketID })
	if i < 0 {
		return nil
	}
	item := d.queue[i].item
	d.queue = slices.Delete(d.queue, i, i+1)
	state := types.TicketCancelled
	if tk, ok := d.tickets[ticketID]; ok {
		state = tk.t.State
	}
	return []Event{{Type: EventApproval, Data: ApprovalEvent{
		Action: action, TicketID: ticketID, State: state, Item: &item,
	}}}
}

// Decide answers one queue item. Approving starts the statement in the
// background: §3.2's agent is polling get_result with a bounded wait and must
// not be made to hold a connection open for however long the statement runs.
func (d *Daemon) Decide(ctx context.Context, ticketID string, dec Decision) error {
	if dec.Decide != "approve" && dec.Decide != "refuse" {
		return errValidation("decision", "must be approve or refuse")
	}

	// Read the item first and mutate nothing: a grant that cannot be written
	// must not leave a ticket approved and unexecuted.
	d.mu.Lock()
	tk, ok := d.tickets[ticketID]
	if !ok {
		d.mu.Unlock()
		return errTicketUnknown
	}
	if tk.t.State != types.TicketPending {
		d.mu.Unlock()
		return errNoDecision
	}
	i := slices.IndexFunc(d.queue, func(q *queueItem) bool { return q.item.TicketID == ticketID })
	if i < 0 {
		d.mu.Unlock()
		return errNoDecision
	}
	item := d.queue[i].item
	write := tk.write
	d.mu.Unlock()

	if dec.Grant != nil {
		if write {
			// R4.2f: no allow rule authorizes a write, and none may lower one
			// below tier 3. Revoking a read rule stops future reads; revoking a
			// write rule does not undo the rows a write already changed.
			return errWriteGrant
		}
		if dec.Decide != "approve" {
			return errValidation("grant", "belongs to an approval, not a refusal")
		}
		if dec.Grant.RowCeiling <= 0 {
			// §9.3's default: ten times what was approved, and never unbounded.
			dec.Grant.RowCeiling = max(int(item.Facts.EstimatedRows)*10, 1)
		}
		if _, err := d.GrantPaths(item.Session.ID, item.Connection, item.Facts.Relations, *dec.Grant, dec.Actor); err != nil {
			return err
		}
	}

	d.mu.Lock()
	tk, ok = d.tickets[ticketID]
	if !ok || tk.t.State != types.TicketPending {
		d.mu.Unlock()
		return errNoDecision
	}
	tk.approver = dec.Actor
	if dec.Decide == "refuse" {
		d.setTicketStateLocked(tk, types.TicketRefused, nil, keeperErr(types.CodeApprovalRefused,
			"a human refused this statement", "ask for a narrower statement"))
	} else {
		d.setTicketStateLocked(tk, types.TicketApproved, nil, nil)
	}
	events := d.dequeueLocked(ticketID, "decided")
	d.mu.Unlock()

	for _, e := range events {
		d.hub.Publish(e)
	}
	if dec.Decide == "approve" {
		d.startApproved(tk)
	}
	return nil
}

// startApproved executes a ticket a human approved, off the decider's request.
func (d *Daemon) startApproved(tk *ticket) {
	d.mu.Lock()
	s := d.sessions[tk.t.SessionID]
	d.mu.Unlock()
	if s == nil {
		// The session went while the human was deciding; CloseConn already
		// cancelled the ticket.
		return
	}
	d.wg.Go(func() {
		ctx, cancel := context.WithTimeout(d.ctx, d.cfg.ExecuteTimeout)
		defer cancel()
		auth := "ticket:" + tk.t.ID
		info := s.Info()

		var res *types.QueryResult
		var err error
		if tk.write {
			res, err = d.deps.Pipeline.CommitWrite(ctx, info, tk.t.ConnectionID, tk.sql, tk.params, auth)
		} else {
			var again *types.Ticket
			res, again, err = d.deps.Pipeline.Query(ctx, info, tk.t.ConnectionID, tk.sql, tk.params, tk.maxRows, auth)
			if err == nil && res == nil && again != nil {
				err = errInternal
			}
		}

		d.mu.Lock()
		defer d.mu.Unlock()
		cur, ok := d.tickets[tk.t.ID]
		if !ok || cur.t.State != types.TicketApproved {
			return // cancelled or expired while it ran
		}
		if err != nil {
			d.setTicketStateLocked(cur, types.TicketFailed, nil, err)
			return
		}
		d.setTicketStateLocked(cur, types.TicketReady, res, nil)
	})
}
