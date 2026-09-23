package types

import "time"

// ClientInfo is who connected. It comes from the MCP handshake and is what makes
// a shared approval queue legible: SPEC R9.1.
type ClientInfo struct {
	Name    string `json:"name"` // "keeper-mcp", "codex", the MCP clientInfo name
	Version string `json:"version,omitzero"`
	PID     int    `json:"pid,omitzero"`
	// Workspace is the client's working directory, which is what a human
	// recognises when two agents query the same database.
	Workspace string `json:"workspace,omitzero"`
}

// Session is one client connection to the daemon. SPEC R3.4e: one socket
// connection, one session. Ticket binding and the reverse map both scope to it,
// and a reconnect is a new session with an empty map.
type Session struct {
	ID          string     `json:"id"`
	Client      ClientInfo `json:"client"`
	Intent      string     `json:"intent,omitzero"` // set_session_intent, required before any query
	ConnectedAt time.Time  `json:"connected_at"`
}

// TicketState is where an escalated statement has got to.
type TicketState string

const (
	TicketPending   TicketState = "pending_approval"
	TicketApproved  TicketState = "approved"
	TicketRefused   TicketState = "refused"
	TicketExpired   TicketState = "expired"
	TicketCancelled TicketState = "cancelled"
	TicketReady     TicketState = "ready"
	TicketFailed    TicketState = "failed"
)

// Terminal reports whether no further transition is possible.
func (s TicketState) Terminal() bool {
	switch s {
	case TicketPending, TicketApproved:
		return false
	default:
		return true
	}
}

// Ticket is an escalation the agent polls. 128 bits of CSPRNG entropy, bound to
// the issuing session and connection, expiring with the session. get_result
// refuses a ticket issued to a different session. SPEC R3.4b.
type Ticket struct {
	ID           string      `json:"ticket"`
	SessionID    string      `json:"-"`
	ConnectionID string      `json:"-"`
	State        TicketState `json:"state"`
	Reason       string      `json:"reason,omitzero"`
	Tier         Tier        `json:"tier"`
	AuditID      string      `json:"audit_id"`
	CreatedAt    time.Time   `json:"created_at"`
}

// RequestKind distinguishes the two things the local page serves. SPEC R8.7g:
// one mechanism, one set of security rules, two outcomes.
type RequestKind string

const (
	// RequestInput takes a value the agent must never see and returns a token.
	RequestInput RequestKind = "input"
	// RequestAuthorization grants a path and returns nothing to the session.
	RequestAuthorization RequestKind = "authorization"
)

// LocalRequest is one pending decision waiting on a human at the local page.
// CSPRNG id of at least 128 bits, one-use, short expiry, bound to the requesting
// session and connection. SPEC R8.7c.
type LocalRequest struct {
	ID           string      `json:"request_id"`
	Kind         RequestKind `json:"kind"`
	SessionID    string      `json:"-"`
	ConnectionID string      `json:"connection_id"`
	// Namespace is the token namespace for an input request.
	Namespace string `json:"namespace,omitzero"`
	// Path is what an authorization request would grant.
	Path *PathRef `json:"path,omitzero"`
	// Purpose is screened user-visible task text. It never carries a value.
	Purpose   string    `json:"purpose,omitzero"`
	URL       string    `json:"url"`
	State     string    `json:"state"` // pending_input, ready, cancelled, expired
	ExpiresAt time.Time `json:"expires_at"`
}

// PathRef is one grantable path. Paths do not inherit: a schema does not cover
// its relations, a view does not cover its base tables, and a different
// connection is a different path. SPEC R9.3c.
type PathRef struct {
	ConnectionID string      `json:"connection_id"`
	Relation     RelationRef `json:"relation"`
}

func (p PathRef) String() string { return "read(" + p.ConnectionID + ":" + p.Relation.String() + ")" }

// GrantLifetime is how long an allow rule lives. The session grant is the
// default, because "stop asking me for the rest of this task" and "stop asking me
// on this machine" are different requests. SPEC R9.3d.
type GrantLifetime string

const (
	GrantSession  GrantLifetime = "session"
	GrantStanding GrantLifetime = "standing"
)

// Grant is a standing approval for exactly one path.
type Grant struct {
	ID       string        `json:"id"`
	Path     PathRef       `json:"path"`
	Lifetime GrantLifetime `json:"lifetime"`
	// SessionID is set for a session-scoped grant and empty for a standing one.
	SessionID string `json:"-"`
	// RowCeiling bounds what the grant covers; identical relations with a
	// different WHERE can return three orders of magnitude more rows. SPEC §9.3.
	RowCeiling int       `json:"row_ceiling"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at,omitzero"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
	Uses       int       `json:"uses"`
	// Suspended is set when a schema change touched the relation. SPEC R9.3b.
	Suspended bool   `json:"suspended,omitzero"`
	Reason    string `json:"reason,omitzero"`
}

// ApprovalItem is one row of the shared queue. Every field above the statement
// exists so a human working across sessions can tell whose work this is.
// SPEC R9.1.
type ApprovalItem struct {
	TicketID   string  `json:"ticket_id"`
	Session    Session `json:"session"`
	Connection string  `json:"connection"`
	Tier       Tier    `json:"tier"`
	// Facts is what a human decides on; SQL is evidence and renders below it.
	Facts     ApprovalFacts `json:"facts"`
	SQL       string        `json:"sql"`
	CreatedAt time.Time     `json:"created_at"`
	// Write is set for a write approval and carries the preview. SPEC R4.2d.
	Write *WritePreview `json:"write,omitzero"`
}

// ApprovalFacts is SPEC §9.2's layout as data: facts first, SQL last.
type ApprovalFacts struct {
	Intent        string        `json:"intent"`
	StatementType string        `json:"statement_type"`
	Relations     []RelationRef `json:"relations"`
	EstimatedRows int64         `json:"estimated_rows"`
	EstimatedCost float64       `json:"estimated_cost"`
	// Egress says what will be masked, in words a human can act on.
	Egress []string `json:"egress,omitzero"`
	// Reasons is why this escalated, from a closed set of reason codes.
	Reasons []string `json:"reasons,omitzero"`
}

// WritePreview is the result of SPEC R4.2d's first execution. The count is exact
// as of PreviewedAt and is not a lock.
type WritePreview struct {
	Operation   string        `json:"operation"` // UPDATE, INSERT, DELETE
	RowCount    int64         `json:"row_count"`
	PreviewedAt time.Time     `json:"previewed_at"`
	Scope       []RelationRef `json:"scope"`
	// Returning is the transform summary for a RETURNING clause, which passes
	// through G7 like any other output. SPEC R4.2e.
	Returning map[string]Transform `json:"returning,omitzero"`
}
