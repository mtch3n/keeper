package types

import "time"

// Tier is the routing decision of SPEC §7.7. It describes routing, not disclosure
// permission: the mode and the explicit policies govern the final action.
type Tier int

const (
	Tier0Run     Tier = 0 // read, under caps, all output allow
	Tier1Record  Tier = 1 // runs, recorded prominently: anything was masked
	Tier2Judge   Tier = 2 // near a cap, or a changed view in assisted/permissive
	Tier3Approve Tier = 3 // any write, or mode-dependent uncertainty
	Tier4Refuse  Tier = 4 // denylisted, DDL, out of write scope, multi-statement
)

// Basis says which layer decided a transform, so the agent can tell what it may
// conclude from what it received. SPEC R8.5b.
type Basis string

const (
	BasisCatalog   Basis = "catalog"   // a human or catalog init classified it
	BasisRules     Basis = "rules"     // the 100% pattern and dictionary pass
	BasisSampled   Basis = "sampled"   // the model layer, over a sample
	BasisInherited Basis = "inherited" // a computed column, from R7.6
	BasisParameter Basis = "parameter" // from a token-resolved parameter, R8.4b
	BasisUnknown   Basis = "unknown"   // an unresolved column redacted under R5.4a
)

// Transform is what happened to one output column, declared in every response.
type Transform struct {
	Policy    Policy      `json:"policy"`
	Namespace string      `json:"namespace,omitzero"`
	Form      PartialForm `json:"form,omitzero"`
	Basis     Basis       `json:"basis"`
	// SpansRedacted counts rule hits replaced inside text values. SPEC R8.5a.
	SpansRedacted int `json:"spans_redacted,omitzero"`
	// SampleSize is set when Basis is sampled, because a probabilistic guarantee
	// must not be presented as a deterministic one.
	SampleSize int `json:"sample_size,omitzero"`
	// Collisions counts cells redacted because a token was already bound to a
	// different value. SPEC R8.3c.
	Collisions int `json:"collisions,omitzero"`
}

// ColumnMeta describes one output column of a result.
type ColumnMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// TableOID and AttNum are the server's own answer from RowDescription.
	// (0,0) means computed. SPEC §7.5.
	TableOID uint32 `json:"-"`
	AttNum   uint16 `json:"-"`
	Policy   Policy `json:"policy"`
}

// Degradation records a layer that was configured but did not run, so a narrower
// result is never mistaken for a clean one. SPEC §7.10.
type Degradation struct {
	Layer  string `json:"layer"` // "detector", "judge", "catalog"
	Reason string `json:"reason"`
}

// QueryResult is a completed statement. SPEC §8.6.
type QueryResult struct {
	Rows     [][]any      `json:"rows"`
	Columns  []ColumnMeta `json:"columns"`
	RowCount int          `json:"row_count"`
	// Truncated reports that the operator ceiling cut the result. SPEC §4.5.
	Truncated  bool                 `json:"truncated,omitzero"`
	Transforms map[string]Transform `json:"transforms"`
	Tier       Tier                 `json:"tier"`
	AuditID    string               `json:"audit_id"`
	Mode       Mode                 `json:"mode"`
	// Authorization names what let this run: "tier0", "grant:<id>", "ticket:<id>",
	// "delegation:<id>". SPEC §9.4 requires the basis to be reported.
	Authorization string        `json:"authorization,omitzero"`
	Degradations  []Degradation `json:"degradations,omitzero"`
	// ExecutedRows is set for a write: the count the commit actually reported,
	// beside the previewed one. SPEC R4.2d.
	ExecutedRows  int64 `json:"executed_rows,omitzero"`
	PreviewedRows int64 `json:"previewed_rows,omitzero"`
}

// ExplainResult is the dry run of SPEC §6.1: what would be masked and whether the
// statement would escalate, without spending an approval.
type ExplainResult struct {
	StatementType string        `json:"statement_type"`
	Relations     []RelationRef `json:"relations"`
	EstimatedRows int64         `json:"estimated_rows"`
	EstimatedCost float64       `json:"estimated_cost"`
	OutputColumns []ColumnMeta  `json:"output_columns"`
	PredictedTier Tier          `json:"predicted_tier"`
	Reasons       []string      `json:"reasons,omitzero"`
}

// Code is a keeper error code from a closed set. SPEC R6.4a: the error response
// is an allowlist, and PostgreSQL's own fields never appear in it. Codes are
// deliberately coarser than SQLSTATE so a mapped code discloses less.
type Code string

const (
	CodeSyntax            Code = "syntax"             // the agent's statement did not parse
	CodeMultiStatement    Code = "multi_statement"    // more than one statement
	CodePermissionDenied  Code = "permission_denied"  // G3 refused; Invariant A working
	CodeUnclassified      Code = "unclassified"       // a column needs a catalog entry
	CodeDenylisted        Code = "denylisted"         // SPEC R4.5
	CodeDDLRefused        Code = "ddl_refused"        // SPEC R4.2c
	CodeOutOfWriteScope   Code = "out_of_write_scope" // SPEC R4.2b
	CodeNoWriteCredential Code = "no_write_credential"
	CodeApprovalRequired  Code = "approval_required"
	CodeApprovalRefused   Code = "approval_refused"
	CodeTicketUnknown     Code = "ticket_unknown"
	CodeTimeout           Code = "timeout"
	CodeRowCap            Code = "row_cap"
	CodeStaleToken        Code = "stale_token" // the daemon restarted; SPEC R3.4d
	CodeInternal          Code = "internal"
)

// Error is the only error shape that reaches an agent. Every field PostgreSQL
// supplies — message, detail, hint, internal query, context, constraint_name,
// schema_name, table_name, column_name, datatype_name — is discarded, along with
// notices and SQLSTATE. SPEC R6.4a.
type Error struct {
	Code Code `json:"code"`
	// Summary is composed by keeper from the code and the catalog. Never copied
	// from the server: PostgreSQL interpolates offending values into its messages.
	Summary string `json:"summary"`
	// Action is an operator instruction from a closed set.
	Action  string `json:"action,omitzero"`
	AuditID string `json:"audit_id,omitzero"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Summary }

// AuditRecord is one line of the append-only log. It records what keeper intended
// to emit — policies applied, transform counts, spans redacted — and does not
// verify the emitted bytes. SPEC R10d.
type AuditRecord struct {
	ID         string     `json:"id"` // uuid.NewV7, so the log sorts by time
	At         time.Time  `json:"at"`
	SessionID  string     `json:"session_id"`
	Client     ClientInfo `json:"client"`
	Intent     string     `json:"intent,omitzero"`
	Connection string     `json:"connection"`
	// Statement is normalized with literals stripped and comments discarded.
	// Quoted identifiers survive only when they match a catalogued relation or
	// column. SPEC R10a and R10c.
	Statement     string               `json:"statement"`
	StatementType string               `json:"statement_type"`
	Relations     []RelationRef        `json:"relations,omitzero"`
	OutputColumns []ColumnMeta         `json:"output_columns,omitzero"`
	Tier          Tier                 `json:"tier"`
	Transforms    map[string]Transform `json:"transforms,omitzero"`
	RowCount      int                  `json:"row_count"`
	Authorization string               `json:"authorization,omitzero"`
	Approver      string               `json:"approver,omitzero"`
	Degradations  []Degradation        `json:"degradations,omitzero"`
	Collisions    int                  `json:"collisions,omitzero"`
	Duration      time.Duration        `json:"duration"`
	ErrorCode     Code                 `json:"error_code,omitzero"`
}
