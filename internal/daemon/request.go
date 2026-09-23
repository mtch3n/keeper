package daemon

import (
	"context"
	"net"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// Local request states. types.LocalRequest.State enumerates them.
const (
	statePending   = "pending_input"
	stateReady     = "ready"
	stateCancelled = "cancelled"
	stateExpired   = "expired"
)

// localRequest is one decision waiting on a human at the local page. R8.7g makes
// input and authorization one mechanism with two outcomes, so there is one type
// here and one set of security rules: CSPRNG id of at least 128 bits, one-use,
// short expiry, bound to the requesting session and its connection, and
// invalidated on disconnect, cancellation, expiry or daemon restart.
type localRequest struct {
	r    types.LocalRequest
	conn net.Conn

	// token is set only for an input request. An authorization request returns
	// nothing to the session: no token is minted and the session gains no value
	// it did not have (R8.7g).
	token string

	// facts and sql render §9.2's layout on an authorization page.
	facts *types.ApprovalFacts
	sql   string

	settledAt time.Time
	updated   chan struct{}
}

func (r *localRequest) notify() {
	close(r.updated)
	r.updated = make(chan struct{})
}

// RequestSpec is POST /v1/requests.
type RequestSpec struct {
	Kind         types.RequestKind
	ConnectionID string
	Namespace    string
	Purpose      string
	Path         *types.PathRef
	Facts        *types.ApprovalFacts
	SQL          string
}

// RequestView is GET /v1/requests/{id}: state, and for a ready input request the
// token and its namespace. Never a raw value, and never a token for an
// authorization request.
type RequestView struct {
	State     string            `json:"state"`
	Kind      types.RequestKind `json:"kind"`
	Token     string            `json:"token,omitzero"`
	Namespace string            `json:"namespace,omitzero"`
}

// LocalRequestView is what the local page renders. It carries no value and no
// token: the page's job is to collect a decision, not to display one.
type LocalRequestView struct {
	ID           string               `json:"request_id"`
	Kind         types.RequestKind    `json:"kind"`
	ConnectionID string               `json:"connection_id"`
	Namespace    string               `json:"namespace,omitzero"`
	Purpose      string               `json:"purpose,omitzero"`
	Path         *types.PathRef       `json:"path,omitzero"`
	Facts        *types.ApprovalFacts `json:"facts,omitzero"`
	SQL          string               `json:"sql,omitzero"`
	Session      types.Session        `json:"session"`
	State        string               `json:"state"`
	ExpiresAt    time.Time            `json:"expires_at"`
	Mode         types.Mode           `json:"mode,omitzero"`
	AlreadyHeld  bool                 `json:"already_granted,omitzero"`
	Grants       []types.Grant        `json:"grants,omitzero"`
}

// OpenRequest creates a local request bound to the calling session and its
// connection.
func (d *Daemon) OpenRequest(ctx context.Context, s *Session, spec RequestSpec) (*types.LocalRequest, error) {
	switch spec.Kind {
	case types.RequestInput:
		if spec.Namespace == "" {
			return nil, errValidation("namespace", "is required for an input request")
		}
	case types.RequestAuthorization:
		if spec.Path == nil || spec.Path.Relation.Schema == "" || spec.Path.Relation.Relation == "" {
			return nil, errValidation("path", "is required for an authorization request")
		}
	default:
		return nil, errValidation("kind", "must be input or authorization")
	}
	if spec.ConnectionID == "" {
		return nil, errValidation("connection_id", "is required")
	}
	if spec.Purpose != "" {
		// R8.7c: purpose is screened user-visible task text. It never carries a
		// value, and R10c's rule pass is what makes that true rather than hoped.
		if err := d.deps.Audit.ScreenIntent(ctx, spec.Purpose); err != nil {
			return nil, errIntentScreened
		}
	}
	if spec.Path != nil && spec.Path.ConnectionID == "" {
		spec.Path.ConnectionID = spec.ConnectionID
	}
	if spec.Path != nil && spec.Path.ConnectionID != spec.ConnectionID {
		return nil, errValidation("path.connection_id", "must name the request's own connection")
	}

	now := d.now()
	id := capability()
	r := &localRequest{
		r: types.LocalRequest{
			ID:           id,
			Kind:         spec.Kind,
			SessionID:    s.id,
			ConnectionID: spec.ConnectionID,
			Namespace:    spec.Namespace,
			Path:         spec.Path,
			Purpose:      spec.Purpose,
			URL:          d.cfg.UIBase + "/r/" + id,
			State:        statePending,
			ExpiresAt:    now.Add(d.cfg.RequestTTL),
		},
		conn:    s.conn,
		facts:   spec.Facts,
		sql:     forDisplay(spec.SQL),
		updated: make(chan struct{}),
	}
	d.mu.Lock()
	d.requests[id] = r
	out := r.r
	d.mu.Unlock()

	d.hub.Publish(Event{Type: EventRequest, Data: RequestEvent{Action: "opened", Kind: spec.Kind, State: statePending}})
	return &out, nil
}

// WaitRequest is get_input_result. Cross-session retrieval is refused (R8.7c),
// and reported as unknown so that holding an id is not an oracle.
func (d *Daemon) WaitRequest(ctx context.Context, s *Session, id string, wait time.Duration) (*RequestView, error) {
	wait = min(max(wait, 0), d.cfg.MaxWait)
	deadline := d.now().Add(wait)
	for {
		d.mu.Lock()
		r, ok := d.requests[id]
		if !ok || s == nil || r.r.SessionID != s.id || r.conn != s.conn {
			d.mu.Unlock()
			return nil, errRequestUnknown
		}
		view := &RequestView{State: r.r.State, Kind: r.r.Kind}
		if r.r.Kind == types.RequestInput && r.r.State == stateReady {
			view.Token = r.token
			view.Namespace = r.r.Namespace
		}
		ch := r.updated
		settled := r.r.State != statePending
		d.mu.Unlock()

		if settled || !d.now().Before(deadline) {
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

// LocalRequest renders the page. The id is the capability; possession of it is
// what authorizes the read, which is why it is never logged and never appears in
// an event.
func (d *Daemon) LocalRequest(ctx context.Context, id string) (*LocalRequestView, error) {
	d.mu.Lock()
	r, ok := d.requests[id]
	if !ok {
		d.mu.Unlock()
		return nil, errRequestUnknown
	}
	s := d.sessions[r.r.SessionID]
	v := &LocalRequestView{
		ID:           r.r.ID,
		Kind:         r.r.Kind,
		ConnectionID: r.r.ConnectionID,
		Namespace:    r.r.Namespace,
		Purpose:      r.r.Purpose,
		Path:         r.r.Path,
		Facts:        r.facts,
		SQL:          r.sql,
		State:        r.r.State,
		ExpiresAt:    r.r.ExpiresAt,
	}
	if s != nil {
		v.Session = s.Info()
	}
	if r.r.Kind == types.RequestAuthorization && r.r.Path != nil {
		// UI §6.2's "already granted": a second request for a path someone
		// granted a moment ago in another window resolves without a second
		// prompt, and says so.
		if g := d.findGrantLocked(r.r.SessionID, r.r.Path.ConnectionID, r.r.Path.Relation, d.now()); g != nil {
			v.AlreadyHeld = true
			v.Grants = []types.Grant{*g}
		}
	}
	d.mu.Unlock()

	// UI §6.1: the page states the effective mode, because what happens to an
	// uncertain value depends on it (§9.4).
	if c, err := d.deps.Vault.Connection(ctx, v.ConnectionID); err == nil && c != nil {
		v.Mode = c.Mode
	}
	return v, nil
}

// AnswerInput stores the value in the requesting session's reverse map and
// returns nothing to the caller but success. The value reaches keeperd directly
// from the browser and is never written to the URL, the audit log, an event or a
// response (R8.7a, R8.7d).
func (d *Daemon) AnswerInput(ctx context.Context, id, connectionID, namespace, value string) error {
	if value == "" {
		return errValidation("value", "is required")
	}
	d.mu.Lock()
	r, ok := d.requests[id]
	if !ok {
		d.mu.Unlock()
		return errRequestUnknown
	}
	if r.r.Kind != types.RequestInput {
		d.mu.Unlock()
		return errValidation("kind", "this request is not an input request")
	}
	if err := d.answerableLocked(r); err != nil {
		d.mu.Unlock()
		return err
	}
	if connectionID != "" && connectionID != r.r.ConnectionID {
		// R8.7c: never silently retarget a token.
		d.mu.Unlock()
		return errValidation("connection_id", "does not match the request; selecting another connection needs a new request")
	}
	if namespace != "" && namespace != r.r.Namespace {
		d.mu.Unlock()
		return errValidation("namespace", "does not match the request")
	}
	sessionID, connID, ns := r.r.SessionID, r.r.ConnectionID, r.r.Namespace
	d.mu.Unlock()

	token, err := d.deps.Redactor.Mint(ctx, sessionID, connID, ns, value)
	if err != nil {
		return err
	}

	d.mu.Lock()
	r, ok = d.requests[id]
	if !ok {
		d.mu.Unlock()
		return errRequestUnknown
	}
	if err := d.answerableLocked(r); err != nil {
		d.mu.Unlock()
		return err
	}
	r.token = token
	r.r.State = stateReady
	r.settledAt = d.now()
	r.notify()
	d.mu.Unlock()

	d.hub.Publish(Event{Type: EventRequest, Data: RequestEvent{Action: "answered", Kind: types.RequestInput, State: stateReady}})
	return nil
}

// AnswerAuthorization grants the path the request named. Nothing is returned to
// the session: no token is minted, and the waiting statement simply re-evaluates
// against a grant that now exists (R8.7g).
func (d *Daemon) AnswerAuthorization(ctx context.Context, id string, req GrantRequest, actor string) ([]types.Grant, error) {
	d.mu.Lock()
	r, ok := d.requests[id]
	if !ok {
		d.mu.Unlock()
		return nil, errRequestUnknown
	}
	if r.r.Kind != types.RequestAuthorization {
		d.mu.Unlock()
		return nil, errValidation("kind", "this request is not an authorization request")
	}
	if err := d.answerableLocked(r); err != nil {
		d.mu.Unlock()
		return nil, err
	}
	path, sessionID := *r.r.Path, r.r.SessionID
	d.mu.Unlock()

	g, err := d.CreateGrant(sessionID, path, req, actor)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	r, ok = d.requests[id]
	if ok && r.r.State == statePending {
		r.r.State = stateReady
		r.settledAt = d.now()
		r.notify()
	}
	d.mu.Unlock()

	d.hub.Publish(Event{Type: EventRequest, Data: RequestEvent{Action: "answered", Kind: types.RequestAuthorization, State: stateReady}})
	return []types.Grant{*g}, nil
}

// RebindRequest is R8.7c's explicit retarget: selecting another connection in the
// form cancels this request and opens a new one bound to that connection, rather
// than quietly pointing the old one somewhere else.
func (d *Daemon) RebindRequest(ctx context.Context, id, connectionID string) (*types.LocalRequest, error) {
	if connectionID == "" {
		return nil, errValidation("connection_id", "is required")
	}
	d.mu.Lock()
	r, ok := d.requests[id]
	if !ok {
		d.mu.Unlock()
		return nil, errRequestUnknown
	}
	if err := d.answerableLocked(r); err != nil {
		d.mu.Unlock()
		return nil, err
	}
	s := d.sessions[r.r.SessionID]
	spec := RequestSpec{
		Kind:         r.r.Kind,
		ConnectionID: connectionID,
		Namespace:    r.r.Namespace,
		Purpose:      r.r.Purpose,
		Facts:        r.facts,
		SQL:          r.sql,
	}
	if r.r.Path != nil {
		spec.Path = &types.PathRef{ConnectionID: connectionID, Relation: r.r.Path.Relation}
	}
	r.r.State = stateCancelled
	r.settledAt = d.now()
	r.notify()
	d.mu.Unlock()

	if s == nil {
		return nil, errSessionRequired
	}
	return d.OpenRequest(ctx, s, spec)
}

// CancelRequest invalidates a pending request.
func (d *Daemon) CancelRequest(id string) error {
	d.mu.Lock()
	r, ok := d.requests[id]
	if !ok {
		d.mu.Unlock()
		return errRequestUnknown
	}
	if err := d.answerableLocked(r); err != nil {
		d.mu.Unlock()
		return err
	}
	r.r.State = stateCancelled
	r.settledAt = d.now()
	r.notify()
	d.mu.Unlock()

	d.hub.Publish(Event{Type: EventRequest, Data: RequestEvent{Action: "cancelled", Kind: r.r.Kind, State: stateCancelled}})
	return nil
}

// answerableLocked enforces one-use and expiry.
func (d *Daemon) answerableLocked(r *localRequest) error {
	if r.r.State != statePending {
		return errAlreadyUsed
	}
	if d.now().After(r.r.ExpiresAt) {
		r.r.State = stateExpired
		r.settledAt = d.now()
		r.notify()
		return errAlreadyUsed
	}
	return nil
}
