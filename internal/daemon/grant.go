package daemon

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// GrantRequest is what a human asked for when they turned an approval into an
// allow rule, or what POST /v1/grants carries.
type GrantRequest struct {
	Lifetime   types.GrantLifetime
	RowCeiling int
}

// grantMatch is the authorization a set of grants supplies for one statement.
type grantMatch struct {
	IDs           []string
	RowCeiling    int
	Authorization string
}

// Grants lists every live grant with session grants first (R9.3d): they are the
// temporary ones, they die with the session, and a list that shows them
// identically to standing entries teaches the user that everything is permanent.
func (d *Daemon) Grants() []types.Grant {
	d.mu.Lock()
	out := make([]types.Grant, 0, len(d.grants))
	for _, g := range d.grants {
		out = append(out, *g)
	}
	d.mu.Unlock()
	slices.SortStableFunc(out, func(a, b types.Grant) int {
		if a.Lifetime != b.Lifetime {
			if a.Lifetime == types.GrantSession {
				return -1
			}
			return 1
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out
}

// CreateGrant records one allow rule over exactly one path. R9.3c: a path grants
// exactly itself. There is no prefix, no wildcard and no tree here on purpose —
// inheritance is where a permission list stops describing what was agreed to.
func (d *Daemon) CreateGrant(sessionID string, path types.PathRef, req GrantRequest, actor string) (*types.Grant, error) {
	switch {
	case path.ConnectionID == "":
		return nil, errValidation("path.connection_id", "is required")
	case path.Relation.Schema == "" || path.Relation.Relation == "":
		return nil, errValidation("path.relation", "needs a schema and a relation")
	case req.RowCeiling <= 0:
		// §9.3: the ceiling is required, because identical relations and columns
		// with a different WHERE can return three orders of magnitude more rows.
		return nil, errValidation("row_ceiling", "is required and must be positive")
	}
	if req.Lifetime == "" {
		req.Lifetime = types.GrantSession
	}
	if req.Lifetime != types.GrantSession && req.Lifetime != types.GrantStanding {
		return nil, errValidation("lifetime", "must be session or standing")
	}
	if req.Lifetime == types.GrantSession && sessionID == "" {
		return nil, errValidation("lifetime", "session grants need a session to belong to")
	}

	now := d.now()
	g := &types.Grant{
		ID:         timeID(),
		Path:       path,
		Lifetime:   req.Lifetime,
		RowCeiling: req.RowCeiling,
		CreatedBy:  actor,
		CreatedAt:  now,
	}
	if req.Lifetime == types.GrantSession {
		g.SessionID = sessionID
	} else {
		g.ExpiresAt = now.Add(d.cfg.StandingGrantTTL)
	}

	d.mu.Lock()
	if req.Lifetime == types.GrantSession {
		if _, ok := d.sessions[sessionID]; !ok {
			d.mu.Unlock()
			return nil, errSessionRequired
		}
	}
	d.grants[g.ID] = g
	out := *g
	d.mu.Unlock()

	d.hub.Publish(Event{Type: EventApproval, Data: GrantEvent{Action: "granted", GrantID: g.ID, Path: g.Path, Lifetime: g.Lifetime}})
	if g.Lifetime == types.GrantStanding {
		d.saveGrants()
	}
	return &out, nil
}

// GrantPaths writes one grant per relation. R9.3c again: every relation in the
// plan needs its own entry, so a two-table join produces two rows in the
// permission list rather than one that covers both.
func (d *Daemon) GrantPaths(sessionID, connID string, rels []types.RelationRef, req GrantRequest, actor string) ([]types.Grant, error) {
	if len(rels) == 0 {
		return nil, errValidation("relations", "the plan named no relation to grant")
	}
	out := make([]types.Grant, 0, len(rels))
	for _, rel := range rels {
		g, err := d.CreateGrant(sessionID, types.PathRef{ConnectionID: connID, Relation: rel}, req, actor)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	d.saveGrants()
	return out, nil
}

// RevokeGrant removes one entry. Revocation is one action and takes effect
// immediately (UI §2.4).
func (d *Daemon) RevokeGrant(id string) error {
	d.mu.Lock()
	_, ok := d.grants[id]
	delete(d.grants, id)
	d.mu.Unlock()
	if !ok {
		return errTicketUnknown
	}
	d.hub.Publish(Event{Type: EventApproval, Data: GrantEvent{Action: "revoked", GrantID: id}})
	d.saveGrants()
	return nil
}

// matchGrants is R9.3c's matching rule. A statement matches only if *every*
// relation its plan touches has its own entry; one unlisted relation sends the
// statement back to its normal tier. A view is its own path and so is every base
// table its plan expands to, which is why this reads the plan's relation list
// and never the statement text (R7.4c).
func (d *Daemon) matchGrants(sessionID, connID string, rels []types.RelationRef, estimatedRows int64) (*grantMatch, bool) {
	if len(rels) == 0 {
		return nil, false
	}
	now := d.now()
	d.mu.Lock()
	defer d.mu.Unlock()

	ids := make([]string, 0, len(rels))
	ceiling := 0
	for _, rel := range rels {
		g := d.findGrantLocked(sessionID, connID, rel, now)
		if g == nil {
			return nil, false
		}
		if ceiling == 0 || g.RowCeiling < ceiling {
			ceiling = g.RowCeiling
		}
		if !slices.Contains(ids, g.ID) {
			ids = append(ids, g.ID)
		}
	}
	if estimatedRows > int64(ceiling) {
		// Estimated rows are advisory (§9.4), but a plan that already expects to
		// exceed the ceiling is not covered by the grant that set it.
		return nil, false
	}
	return &grantMatch{IDs: ids, RowCeiling: ceiling, Authorization: "grant:" + strings.Join(ids, ",")}, true
}

// findGrantLocked looks for exactly one path: same connection, same schema, same
// relation. No prefix, no wildcard, no parent — public.users does not cover
// public.users.email, public.* does not exist, and a different connection is a
// different path.
func (d *Daemon) findGrantLocked(sessionID, connID string, rel types.RelationRef, now time.Time) *types.Grant {
	var best *types.Grant
	for _, g := range d.grants {
		if g.Suspended {
			continue
		}
		if g.Path.ConnectionID != connID || g.Path.Relation != rel {
			continue
		}
		if g.SessionID != "" && g.SessionID != sessionID {
			continue
		}
		if !g.ExpiresAt.IsZero() && now.After(g.ExpiresAt) {
			continue
		}
		// A session grant is the narrower of the two, so it wins when both exist.
		if best == nil || (best.Lifetime != types.GrantSession && g.Lifetime == types.GrantSession) {
			best = g
		}
	}
	return best
}

// markGrantsDirty defers a write. Use counts move on every matched query, and a
// file write per query would be a cost paid for a number nobody reads in real
// time; the sweep flushes it.
func (d *Daemon) markGrantsDirty() { d.grantsDirty.Store(true) }

func (d *Daemon) flushGrants() {
	if d.grantsDirty.Swap(false) {
		d.saveGrants()
	}
}

func (d *Daemon) noteGrantUse(ids []string) {
	defer d.markGrantsDirty()
	now := d.now()
	d.mu.Lock()
	for _, id := range ids {
		if g, ok := d.grants[id]; ok {
			g.Uses++
			g.LastUsedAt = now
		}
	}
	d.mu.Unlock()
}

// SuspendGrantsForRelation is R9.3b: a schema change to any relation in a rule's
// scope suspends the rule pending review, because a rule for SELECT ... FROM
// users must not keep firing after someone adds an ssn column.
func (d *Daemon) SuspendGrantsForRelation(connID string, rel types.RelationRef, reason string) int {
	return d.suspend(func(g *types.Grant) bool {
		return g.Path.ConnectionID == connID && g.Path.Relation == rel
	}, reason)
}

// SuspendGrantsForConnection suspends every grant on a connection. It is the
// fallback the ports as frozen permit: ports.Catalog.Fresh answers yes or no for
// a set of OIDs and never says which relation moved, so a daemon that only knows
// "something changed" suspends the whole connection rather than guessing.
func (d *Daemon) SuspendGrantsForConnection(connID, reason string) int {
	return d.suspend(func(g *types.Grant) bool { return g.Path.ConnectionID == connID }, reason)
}

func (d *Daemon) suspend(match func(*types.Grant) bool, reason string) int {
	var ids []string
	d.mu.Lock()
	for id, g := range d.grants {
		if g.Suspended || !match(g) {
			continue
		}
		g.Suspended = true
		g.Reason = reason
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.hub.Publish(Event{Type: EventApproval, Data: GrantEvent{Action: "suspended", GrantID: id, Reason: reason}})
	}
	if len(ids) > 0 {
		d.saveGrants()
	}
	return len(ids)
}

// GrantFor is pipeline.Authority's grant lookup: the live grant covering exactly
// this path, or false.
//
// R9.3c is enforced by the shape of the question rather than by the answer. The
// pipeline asks once per relation in the plan, and this returns a grant only for
// the exact (connection, schema, relation) triple it was given — no prefix
// match, no wildcard, and a view is its own path. One unanswered question sends
// the statement back to its normal tier.
func (d *Daemon) GrantFor(_ context.Context, sessionID string, path types.PathRef) (*types.Grant, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	g := d.findGrantLocked(sessionID, path.ConnectionID, path.Relation, d.now())
	if g == nil {
		return nil, false
	}
	cp := *g
	return &cp, true
}
