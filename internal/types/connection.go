package types

import "time"

// Mode is how much keeper decides without asking. Set per connection by a human;
// MCP cannot change it. SPEC §9.4.
type Mode string

const (
	// ModeStrict runs known-safe reads and puts every uncertain one in front of
	// a human.
	ModeStrict Mode = "strict"
	// ModeAssisted is the default. Ordinary reads run, and an uncertain one runs
	// too, under the same deterministic masking.
	ModeAssisted Mode = "assisted"
)

// StageKind names a detection adapter a stage runs.
type StageKind string

const (
	// KindPatterns is in-process pattern and checksum detection.
	KindPatterns StageKind = "patterns"
	// KindList matches the connection's own deny terms and expressions.
	KindList StageKind = "list"
)

// StageKinds are the kinds keeper can run, in the order a description lists them.
var StageKinds = []StageKind{KindPatterns, KindList}

// Stage is one step of a connection's detection pipeline: an adapter and the
// options that shape what it keeps and what it is sent.
type Stage struct {
	Kind StageKind `json:"kind"`
	// Entities, when set, keeps only this stage's hits of these entity types.
	Entities []string `json:"entities,omitzero"`
	// Raw sends the stage unmasked text: earlier stages' hits left in place.
	Raw bool `json:"raw,omitzero"`
}

// Valid reports whether m is a mode keeper knows.
func (m Mode) Valid() bool { return m == ModeStrict || m == ModeAssisted }

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

// Host is one database server, entered once and shared by every connection
// registered on it. It carries where the server is and nothing about who logs
// in: credentials belong to a connection. No field here reaches MCP.
type Host struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	// SSLMode is libpq's sslmode, passed to the server as-is.
	SSLMode string `json:"sslmode"`
}

// SSLModes are the sslmode values libpq accepts.
var SSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// Connection is one database and role on a Host: what an agent queries. Host,
// password and connection string never appear in an MCP response; SPEC §6.1
// returns the fields marked exposed.
//
// It is a connection, not the host, that owns a catalog, token keys and a
// privilege audit: a (tableOID, attnum) pair means nothing outside its
// database, and a finding describes one role.
type Connection struct {
	ID       string `json:"id"`
	HostID   string `json:"host_id"`
	Name     string `json:"name"`   // exposed
	Engine   string `json:"engine"` // exposed
	Database string `json:"database"`
	Role     string `json:"role"` // exposed: the role is the username, deliberately
	Version  string `json:"version,omitzero"`

	// CatalogPath defaults to .keeper/catalog.yaml in the project repo. SPEC R5.2a.
	CatalogPath string `json:"catalog_path"`

	Mode   Mode   `json:"mode"`
	Limits Limits `json:"limits"`
	// Detection is the ordered pipeline a scan column's free text runs through.
	// Empty is off: free text is redacted whole.
	Detection []Stage       `json:"detection,omitzero"`
	Denylist  []RelationRef `json:"denylist,omitzero"`

	// Findings is what the last audit reported. It does not gate anything: a
	// registered connection is usable, and the audit is a separate report the
	// operator reads on its own schedule. SPEC R4.1.
	Findings  []Finding `json:"findings,omitzero"`
	AuditedAt time.Time `json:"audited_at"`

	// Writes is whether this profile's sessions may write at all. Every write
	// still waits for approval; there is no standing approval of a write.
	Writes Writes `json:"writes"`
}

// Writes is a profile's write setting. Off is the default: its sessions are
// read-only, and a write is refused rather than run as some other profile.
type Writes string

const (
	WritesOff     Writes = "off"
	WritesApprove Writes = "approve"
)

// Settings are the daemon-wide choices an operator makes in Settings. They live
// in the vault beside the connections and are unreachable from MCP.
type Settings struct {
	// LogRetentionDays is how long the activity log keeps a record.
	LogRetentionDays int `json:"log_retention_days"`
}

// DefaultSettings are a fresh vault's settings.
func DefaultSettings() Settings { return Settings{LogRetentionDays: 30} }
