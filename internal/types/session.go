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
	ID     string     `json:"id"`
	Client ClientInfo `json:"client"`
	Intent string     `json:"intent,omitzero"` // set_session_intent, optional
	// TokenScope is how long this session's tokens live, as its agent declared.
	TokenScope  TokenScope `json:"token_scope,omitzero"`
	ConnectedAt time.Time  `json:"connected_at"`
}

// TokenScope is a session's declared token lifetime. Session tokens resolve only
// inside the session that minted them; persistent tokens mean the same value
// on every machine holding the vault, where the connection allows them.
type TokenScope string

const (
	ScopeSession    TokenScope = "session"
	ScopePersistent TokenScope = "persistent"
)

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
	AuditID      string      `json:"audit_id"`
	CreatedAt    time.Time   `json:"created_at"`
	// Preview is the one rollback run of a write; it stays on the daemon's side.
	Preview *WritePreview `json:"-"`
}

// RequestKind names what the local page serves.
type RequestKind string

const (
	// RequestInput takes a value the agent must never see and returns a token.
	RequestInput RequestKind = "input"
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
	// Purpose is screened user-visible task text. It never carries a value.
	Purpose   string    `json:"purpose,omitzero"`
	URL       string    `json:"url"`
	State     string    `json:"state"` // pending_input, ready, cancelled, expired
	ExpiresAt time.Time `json:"expires_at"`
}

// ApprovalItem is one row of the shared queue. Every field above the statement
// exists so a human working across sessions can tell whose work this is.
// SPEC R9.1.
type ApprovalItem struct {
	TicketID   string  `json:"ticket_id"`
	Session    Session `json:"session"`
	Connection string  `json:"connection"`
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

	// Risk is how much the statement can change, at a glance.
	Risk Risk `json:"risk"`
	// Unreadable is set when keeper could not classify the statement, whose
	// risk is then destructive.
	Unreadable bool `json:"unreadable,omitzero"`
	// Targets are the relations a write changes.
	Targets []RelationRef `json:"targets,omitzero"`
	// Columns are the output columns by name, with SELECT * expanded.
	Columns []string `json:"columns,omitzero"`
	// Filters are the sensitive or uncatalogued columns the statement filters
	// or joins on: a filter on an SSN discloses as much as selecting it.
	Filters []FilterColumn `json:"filters,omitzero"`
}

// Risk classes a statement: a read, a write that adds or changes rows, or one
// that removes them. A statement keeper cannot classify is destructive.
type Risk string

const (
	RiskRead        Risk = "read"
	RiskWrite       Risk = "write"
	RiskDestructive Risk = "destructive"
)

// FilterColumn is one column a statement filters or joins on that an
// approver should notice.
type FilterColumn struct {
	Relation RelationRef `json:"relation"`
	Column   string      `json:"column"`
	// Flag is sensitive (catalogued, not allow) or unclassified (no entry).
	Flag   string `json:"flag"`
	Policy Policy `json:"policy,omitzero"`
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
	// Changes is what the write changes, for the approver. Nil when the
	// server could not report rows; RowCount still holds.
	Changes *WriteChanges `json:"changes,omitzero"`
}

// PreviewRows is how many changed rows an approval shows.
const PreviewRows = 50

// WriteChanges is a write's effect, shown to the approver in cleartext. It is
// held with the ticket in memory: nothing logs it and no response to an agent
// carries it.
type WriteChanges struct {
	Columns []string     `json:"columns"`
	Rows    []ChangedRow `json:"rows"`
	// Omitted counts affected rows beyond the ones shown.
	Omitted int64 `json:"omitted,omitzero"`
}

// ChangedRow is one row a write touches: its values before the change, where
// the server reports them, and after. An INSERT has no old values and a
// DELETE no new ones.
type ChangedRow struct {
	Old []string `json:"old,omitzero"`
	New []string `json:"new,omitzero"`
}
