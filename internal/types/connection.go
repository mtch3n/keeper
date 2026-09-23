package types

import "time"

// Mode is how much keeper decides without asking. Set per connection by a human;
// MCP cannot change it. SPEC §9.4.
type Mode string

const (
	// ModeStrict runs known-safe reads and requires approval for unresolved
	// execution decisions. A model advises and never releases.
	ModeStrict Mode = "strict"
	// ModeAssisted is the default. Ordinary reads run, a local model helps with
	// intent and scope, and unresolved values are masked rather than released.
	ModeAssisted Mode = "assisted"
	// ModePermissive lets a local model authorize release of unpinned uncertain
	// output inside an explicitly delegated scope.
	ModePermissive Mode = "permissive"
)

// Valid reports whether m is a mode keeper knows.
func (m Mode) Valid() bool { return m == ModeStrict || m == ModeAssisted || m == ModePermissive }

// FindingKind groups what a G0 audit can report. SPEC §4.1.
type FindingKind string

const (
	FindingAttribute      FindingKind = "attribute"        // rolsuper and friends
	FindingMembership     FindingKind = "membership"       // pg_read_server_files and friends
	FindingRelationWrite  FindingKind = "relation-write"   // INSERT/UPDATE/DELETE/TRUNCATE/REFERENCES
	FindingSchemaCreate   FindingKind = "schema-create"    // CREATE on a schema
	FindingFunctionExec   FindingKind = "function-exec"    // EXECUTE on a file or program function
	FindingSecurityDefine FindingKind = "security-definer" // SECURITY DEFINER reachable by this role
)

// Finding is one thing the privilege audit found. It is advice, not a gate:
// keeper neither refuses a credential over a finding nor holds the connection
// shut until somebody signs it off. The audit reports what the role can do and
// the statement that would narrow it; acting on that is the operator's call.
// SPEC R4.1.
type Finding struct {
	// ID is the stable key the audit report groups by: "rolsuper",
	// "relation-write:public.orders", "security-definer:pg_catalog.azure_sys_fn".
	ID      string      `json:"id"`
	Kind    FindingKind `json:"kind"`
	Subject string      `json:"subject"` // the role attribute, relation, or function
	// Detail says in one sentence what the finding means for this connection.
	Detail string `json:"detail"`
	// Narrower is the SQL that would remove the finding, copyable as-is. Empty
	// where no single statement would ("rolsuper" on a managed server, say).
	Narrower string `json:"narrower,omitzero"`
}

// RelationRef names a relation. keeper stores names and resolves them to OIDs at
// connect time; SPEC R5.1 explains why the reverse does not work.
type RelationRef struct {
	Schema   string `json:"schema"`
	Relation string `json:"relation"`
}

func (r RelationRef) String() string { return r.Schema + "." + r.Relation }

// WriteOp is one operation a write credential may perform on a relation.
type WriteOp string

const (
	WriteInsert WriteOp = "INSERT"
	WriteUpdate WriteOp = "UPDATE"
	WriteDelete WriteOp = "DELETE"
)

// WriteScopeEntry is one relation a write credential may change, with the
// operations it holds. Enumerated at registration and re-audited. SPEC R4.2b.
type WriteScopeEntry struct {
	Relation   RelationRef `json:"relation"`
	Operations []WriteOp   `json:"operations"`
}

// Limits are the per-connection operator settings of SPEC §4.5. Every one of them
// is set through the CLI or the UI and is unreachable from MCP.
type Limits struct {
	// MaxRowsCeiling bounds query(max_rows); the agent cannot raise it.
	MaxRowsCeiling int `json:"max_rows_ceiling"`
	// StatementTimeout is the SET LOCAL value for every statement.
	StatementTimeout time.Duration `json:"statement_timeout"`
	// ScanSample is how many rows the model layer of a scan column sees.
	ScanSample int `json:"scan_sample"`
}

// DefaultLimits are keeper's starting values. SPEC §15 lists them as tuning
// values rather than design decisions, so they are here rather than in the spec.
func DefaultLimits() Limits {
	return Limits{
		MaxRowsCeiling:   1000,
		StatementTimeout: 30 * time.Second,
		ScanSample:       300, // ~299 rows finds a 1% rate at 95% confidence, SPEC R8.5b
	}
}

// Connection is one registered database. Host, password and connection string
// never appear in an MCP response; SPEC §6.1 returns the fields marked exposed.
type Connection struct {
	ID       string `json:"id"`
	Name     string `json:"name"`   // exposed
	Engine   string `json:"engine"` // exposed
	Database string `json:"database"`
	Role     string `json:"role"` // exposed: the role is the username, deliberately
	Version  string `json:"version,omitzero"`

	// CatalogPath defaults to .keeper/catalog.yaml in the project repo. SPEC R5.2a.
	CatalogPath string `json:"catalog_path"`

	Mode       Mode              `json:"mode"`
	Limits     Limits            `json:"limits"`
	Denylist   []RelationRef     `json:"denylist,omitzero"`
	WriteScope []WriteScopeEntry `json:"write_scope,omitzero"`

	// Findings is what the last audit reported. It does not gate anything: a
	// registered connection is usable, and the audit is a separate report the
	// operator reads on its own schedule. SPEC R4.1.
	Findings  []Finding `json:"findings,omitzero"`
	AuditedAt time.Time `json:"audited_at"`

	// HasWriteCredential reports whether a _rw credential exists. Without one,
	// write mode does not exist; there is no boolean that enables it. SPEC §4.2.
	HasWriteCredential bool `json:"has_write_credential"`
}
