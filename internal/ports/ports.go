// Package ports declares the seams between keeper's packages.
//
// Go interfaces normally belong to the consumer, and these would ordinarily live
// in pipeline, daemon and api. They are collected here for one reason: the
// packages on both sides of each seam are built independently, and a shared
// declaration is what lets an implementation and its caller compile without
// waiting for each other. Each interface names the package expected to satisfy it.
//
// Nothing in this package has an implementation, and nothing imports it except
// the two sides of the seam it describes.
package ports

import (
	"context"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// Role distinguishes the two credentials a connection may hold. A single
// credential may serve both: SPEC §4.2 permits a master account.
type Role string

const (
	RoleRead  Role = "read"
	RoleWrite Role = "write"
)

// Vault is internal/vault. It is the only thing that touches the master key, the
// age file or the OS keychain, and the only place a DSN exists at rest.
type Vault interface {
	// Unlock resolves the master key through the source chain: keychain,
	// KEEPER_MASTER_KEY, key.age, passphrase. SPEC §4.3.
	Unlock(ctx context.Context, passphrase string) error
	// Lock forgets the key again. The daemon calls it after a period with
	// nothing happening, so an unlocked vault does not sit in memory for as
	// long as the machine is on.
	Lock()
	Locked() bool
	// KeySource names the source in use, so doctor can report it. Silent
	// degradation to a weaker source is a defect: SPEC R4.3.
	KeySource() string

	Connections(ctx context.Context) ([]*types.Connection, error)
	Connection(ctx context.Context, id string) (*types.Connection, error)
	// Register stores a connection and its credentials. The connection arrives
	// disabled, with findings and no acceptances.
	Register(ctx context.Context, c *types.Connection, readDSN, writeDSN string) error
	Update(ctx context.Context, c *types.Connection) error
	Remove(ctx context.Context, id string) error

	// DSN returns the credential for a role, or an error when none exists. There
	// is no boolean that enables writes: SPEC §4.2.
	DSN(ctx context.Context, id string, role Role) (string, error)

	// Accept records a human agreeing to named findings. It fails when a finding
	// id is unknown or its hash has changed since the audit. SPEC R4.1.
	Accept(ctx context.Context, id string, findingIDs []string, actor, via string) (*types.Connection, error)

	// TokenKey is the per-connection HMAC key and its version. It never leaves
	// the vault and is never sent to a model: SPEC R8.3a.
	TokenKey(ctx context.Context, id string, version int) (key []byte, current int, err error)

	Export(ctx context.Context) ([]byte, error)
	RotateMaster(ctx context.Context) error
}

// Auditor is internal/pgaudit. It runs G0 against a live connection and reports
// what it found. It never refuses: SPEC R4.1.
type Auditor interface {
	Audit(ctx context.Context, dsn string, role Role) ([]types.Finding, error)
}

// CatalogFor resolves the catalog for one connection.
//
// ports.Catalog is per-connection by construction and nothing holds a single
// one: a (tableOID, attnum) pair means nothing without knowing which database it
// came from, and two databases reuse OIDs freely. A daemon serving three
// connections that shared one Catalog would mask the wrong column as soon as two
// of them assigned the same OID, which they will.
type CatalogFor func(connID string) (Catalog, error)

// Catalog is the read side of internal/catalog, used by the pipeline on every
// statement. Implementations are expected to be safe for concurrent use.
type Catalog interface {
	// Lookup resolves a policy by the server's own column identity. SPEC R7.5a:
	// never by output name.
	Lookup(tableOID uint32, attNum uint16) (types.ColumnPolicy, bool)
	// LookupName resolves by name, for catalog editing and error composition.
	LookupName(rel types.RelationRef, column string) (types.ColumnPolicy, bool)
	// Relation names a table OID, for error messages and audit records.
	Relation(tableOID uint32) (types.RelationRef, bool)
	// RelationPolicies returns every non-allow policy of a relation, with the
	// type family of the column that carries it. This is what R7.6's type-family
	// inheritance consumes; the caller does not read the catalog directly.
	RelationPolicies(tableOID uint32) []FamilyPolicy
	// Readable reports the columns has_column_privilege allows, so get_schema can
	// omit the rest and an error can name what may be read. SPEC R5.5.
	Readable(ctx context.Context, rel types.RelationRef) ([]string, error)
	// Fresh reports whether the catalog's fingerprints still match the database.
	// A false answer is uncertainty, not permission: SPEC R5.6b.
	Fresh(ctx context.Context, relations []uint32) (bool, error)
}

// TypeFamily groups PostgreSQL types for R7.6's inheritance rule. The families
// are enumerated by type OID, never by typcategory: SPEC R7.6c puts uuid, json,
// jsonb, bytea and xml all in category U.
type TypeFamily string

const (
	FamilyNumeric  TypeFamily = "numeric"
	FamilyBoolean  TypeFamily = "boolean"
	FamilyDateTime TypeFamily = "datetime"
	FamilyUUID     TypeFamily = "uuid"
	FamilyText     TypeFamily = "text"
	FamilyUnknown  TypeFamily = "unknown"
)

// FamilyPolicy is one column's policy with the family it belongs to.
type FamilyPolicy struct {
	Column string
	Family TypeFamily
	Policy types.ColumnPolicy
}

// CatalogStore is the write side of internal/catalog, used by the CLI and the UI.
type CatalogStore interface {
	// Entries returns the merged committed file and daemon overlay.
	Entries(ctx context.Context, connID string) (map[string]types.ColumnPolicy, error)
	// Put writes to the committed file. Automatic raises go to the overlay
	// instead, through Raise. SPEC R5.2c.
	Put(ctx context.Context, connID string, entries map[string]types.ColumnPolicy) error
	// Raise writes an automatic classification change to the overlay. It never
	// lowers a policy and never overwrites a human-authored entry. SPEC R5.3.
	Raise(ctx context.Context, connID, key string, p types.ColumnPolicy) error
	// Init proposes a policy for every column, grouped by how safe the proposal
	// is to accept without reading it. sample is 0 for no row sampling.
	Init(ctx context.Context, connID string, sample int) (*InitProposal, error)
	// SuggestGrants emits GRANT SELECT (...) per table, omitting drop columns.
	SuggestGrants(ctx context.Context, connID string) ([]string, error)
	// Unclassified is the Catalog screen's backlog.
	Unclassified(ctx context.Context, connID string) ([]string, error)
}

// InitProposal is catalog init's output, grouped by risk so review effort lands
// where it matters. SPEC R5.3.
type InitProposal struct {
	// SafeToBulkAccept holds typed scalars proposed allow and name-heuristic
	// matches proposed token.
	SafeToBulkAccept map[string]types.ColumnPolicy `json:"safe_to_bulk_accept"`
	// NeedsReview holds text with no heuristic match, proposed scan. This is the
	// review task: it is where unnamed name and address columns live.
	NeedsReview map[string]types.ColumnPolicy `json:"needs_review"`
	// SampleRates reports the measured hit rate per sampled column, so a reviewer
	// sees why a proposal was made. SPEC R5.3a.
	SampleRates map[string]float64 `json:"sample_rates,omitzero"`
}

// Detector is internal/rules and, optionally, a sidecar. A detector that is
// unconfigured, unreachable or slow narrows coverage; it never blocks and never
// widens what is emitted. SPEC R8.5h.
type Detector interface {
	// Scan returns the spans of text that matched, by byte offset.
	Scan(ctx context.Context, text string) ([]Span, error)
	// Identity reports what is examining the data and whether it can reach the
	// network, for doctor and the audit log. SPEC R8.5g.
	Identity() DetectorIdentity
}

// Span is one detected range, half-open.
type Span struct {
	Start int     `json:"start"`
	End   int     `json:"end"`
	Type  string  `json:"type"`
	Score float64 `json:"score,omitzero"`
}

// DetectorIdentity answers "what was examining my data, and could it talk to
// anyone" after the fact.
type DetectorIdentity struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitzero"`
	NetworkPosture string `json:"network_posture"` // "none", "namespaced", "unverified"
}

// ResolvedParam records one parameter the Redactor resolved from a token, so
// the statement's output can inherit its source policy (R8.4b).
type ResolvedParam struct {
	// Ordinal is the parameter's 1-based position, for the audit record.
	Ordinal int
	// Namespace is the token's namespace.
	Namespace string
	// Policy is the source policy the token carries.
	Policy types.Policy
}

// Statement carries the per-statement facts the Redactor needs and the Redactor
// interface does not: which connection's token key to hash under, and which
// parameters were resolved from tokens.
//
// It travels in the context because Apply is called once per statement with the
// rows already in hand, and threading two more arguments through every caller
// would put connection plumbing in signatures that are otherwise about values.
// The pipeline attaches it; the Redactor reads it.
type Statement struct {
	ConnectionID string
	Params       []ResolvedParam
}

type statementKey struct{}

// WithStatement attaches per-statement facts to a context.
func WithStatement(ctx context.Context, s Statement) context.Context {
	return context.WithValue(ctx, statementKey{}, s)
}

// StatementFrom reads them back.
//
// A missing statement is not an error by itself: masking every column the
// pipeline resolved, and the emission scan of R8.4a, both work without one. It
// becomes an error only when a token policy needs a key, which is the one thing
// that cannot be done without knowing the connection.
func StatementFrom(ctx context.Context) (Statement, bool) {
	s, ok := ctx.Value(statementKey{}).(Statement)
	return s, ok
}

// Redactor is internal/redact. It owns G7 and every value that leaves keeper
// passes through it.
type Redactor interface {
	// Apply rewrites rows in place according to the policies the pipeline
	// resolved, and reports what it did. It also re-tokenizes any resolved value
	// found in a text-like output cell: SPEC R8.4a and R8.4c.
	Apply(ctx context.Context, sessionID string, cols []types.ColumnMeta, rows [][]any) (map[string]types.Transform, error)
	// Mint stores a value in the session's reverse map and returns its token,
	// for the local input flow. SPEC R8.7c.
	Mint(ctx context.Context, sessionID, connID, namespace, value string) (string, error)
	// Resolve turns a token back into a bound parameter value. SPEC R6.2.
	Resolve(ctx context.Context, sessionID, token string) (string, types.Policy, error)
	// DropSession clears a session's reverse map. A daemon restart does the same
	// for every session, which is why R3.4d's error says what it says.
	DropSession(sessionID string)
}

// Executor is internal/pgdb: the only package that sees a pgconn error, and the
// only one that opens a connection. Everything it returns is already converted.
type Executor interface {
	// Describe runs Parse and Describe and returns the output columns with the
	// server's own identity for each. No rows are read. SPEC §7.5.
	Describe(ctx context.Context, connID, sql string, params []Param) ([]types.ColumnMeta, error)
	// Plan runs EXPLAIN (FORMAT JSON), never ANALYZE, inside the same read-only
	// transaction as execution would use. SPEC R7.4a.
	Plan(ctx context.Context, connID, sql string, params []Param) (*PlanFacts, error)
	// Run executes a read: Parse, Describe, EXPLAIN and Execute on one pooled
	// connection inside one BEGIN READ ONLY, with DISCARD ALL after. R7.5b, R7.9.
	Run(ctx context.Context, connID, sql string, params []Param, maxRows int) (*RawResult, error)
	// PreviewWrite runs the statement and rolls back, returning the command tag's
	// row count. SPEC R4.2d's first execution.
	PreviewWrite(ctx context.Context, connID, sql string, params []Param) (*RawResult, error)
	// CommitWrite runs the statement for real, in a fresh transaction.
	CommitWrite(ctx context.Context, connID, sql string, params []Param) (*RawResult, error)
	// Introspect lists relations and columns for catalog init and get_schema.
	Introspect(ctx context.Context, connID string) ([]Relation, error)
	// SampleColumn reads n values of a column for catalog-time sampling. Local
	// reading is not egress: SPEC R5.3.
	SampleColumn(ctx context.Context, connID string, rel types.RelationRef, column string, n int) ([]string, error)
	// Close releases a connection's pool, for R4.1e's disable path.
	Close(connID string)
}

// Param is one bound parameter. Exactly one field is set. A token is resolved by
// the Redactor before it reaches the Executor, so Executor never sees one.
type Param struct {
	Value any
}

// PlanFacts is what EXPLAIN told us. Denylists and allow rules are evaluated
// against Relations, never against statement text: SPEC R7.4c.
type PlanFacts struct {
	StatementType string
	Relations     []uint32
	RelationNames []types.RelationRef
	EstimatedRows int64
	EstimatedCost float64
	Writes        bool
	HasFilter     bool
}

// RawResult is what came back from the server, before G7.
type RawResult struct {
	Columns   []types.ColumnMeta
	Rows      [][]any
	RowCount  int
	Truncated bool
	// CommandTag carries the affected-row count for a write.
	CommandTag int64
	Duration   time.Duration
}

// Relation is one table, view or matview with its columns, from introspection.
type Relation struct {
	Ref     types.RelationRef
	OID     uint32
	Kind    byte // r, v, m, p
	Columns []Column
}

// Column is one column of a relation.
type Column struct {
	Name     string
	AttNum   uint16
	Type     string
	TypeOID  uint32
	Family   TypeFamily
	Nullable bool
	IsPK     bool
	IsFK     bool
	// HasDefault and IsIdentity separate an id column from an SSN column that
	// happens to be nine digits. SPEC R5.3b.
	HasDefault bool
	IsIdentity bool
}

// AuditLog is internal/audit.
type AuditLog interface {
	// Write appends one record. It is never skipped: G9.
	Write(ctx context.Context, r *types.AuditRecord) error
	// Query reads the log back for the Activity screen.
	Query(ctx context.Context, f AuditFilter) ([]types.AuditRecord, error)
	Get(ctx context.Context, id string) (*types.AuditRecord, error)
	// Normalize strips literals and comments from a statement and keeps quoted
	// identifiers only where they match the catalog. SPEC R10a and R10c.
	Normalize(sql string, known func(string) bool) string
	// ScreenIntent rejects a session intent that carries PII, using the same rule
	// pass as a scan column. SPEC R10c.
	ScreenIntent(ctx context.Context, intent string) error
}

// AuditFilter narrows the Activity screen.
type AuditFilter struct {
	SessionID    string
	ConnectionID string
	Tier         *types.Tier
	Since        time.Time
	Limit        int
}

// Judge is the local model of SPEC §7.8. keeperd calls it directly, against a
// local endpoint or a sidecar, never through the harness: R7.8c.
type Judge interface {
	// Assess returns a structured verdict. Its explanations are model suggestions
	// and are labelled as such wherever they are shown: R7.8d.
	Assess(ctx context.Context, req JudgeRequest) (*JudgeVerdict, error)
	Available(ctx context.Context) bool
	Identity() string
}

// JudgeRequest is the judge's whole context: the statement, plan facts, output
// columns and session intent. No conversation history: R7.8b.
type JudgeRequest struct {
	SQL           string
	Intent        string
	Plan          *PlanFacts
	OutputColumns []types.ColumnMeta
	Mode          types.Mode
}

// JudgeVerdict is a validated structured answer. A configured-but-failing judge
// never counts as a favourable verdict: SPEC R7.7b.
type JudgeVerdict struct {
	Tier        types.Tier `json:"tier"`
	ReasonCodes []string   `json:"reason_codes"`
	Uncertainty float64    `json:"uncertainty"`
	// Release is honoured only inside an explicit permissive delegation. §9.4.
	Release     bool   `json:"release,omitzero"`
	Explanation string `json:"explanation,omitzero"`
}
