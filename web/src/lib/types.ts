/**
 * TypeScript mirrors of `internal/types`. Kept field-for-field with the Go
 * source so a change there is a diff here, not a guess. `internal/types` is
 * FROZEN (CONTRACT.md §1); treat this file the same way — propose a change
 * rather than drifting it out of sync.
 *
 * A Go `time.Time` serializes as an RFC 3339 string; a zero value is omitted
 * under `omitzero` and otherwise arrives as `"0001-01-01T00:00:00Z"`. A Go
 * `time.Duration` serializes as an integer count of nanoseconds — not a
 * string — because it carries no custom marshaller.
 */

// ── connection.go ──────────────────────────────────────────────────────────

export type Mode = 'strict' | 'assisted'

type FindingKind =
  | 'attribute'
  | 'membership'
  | 'relation-write'
  | 'schema-create'
  | 'function-exec'
  | 'security-definer'

/** One thing the privilege audit found. Advice, not a gate: no finding stops
 * a connection from running (SPEC R4.1). `narrower` is the statement that
 * would remove it, copyable as-is, and is empty where no single one would. */
export interface Finding {
  id: string
  kind: FindingKind
  subject: string
  detail: string
  narrower?: string
}

export interface RelationRef {
  schema: string
  relation: string
}

export interface Limits {
  max_rows_ceiling: number
  /** nanoseconds */
  statement_timeout: number
  scan_sample: number
}

/** Mirrors types.Host: one server, entered once, shared by every connection
 * registered on it. Human surface only — no address reaches MCP. */
export interface Host {
  id: string
  name: string
  address: string
  port: number
  sslmode: SSLMode
}

/** libpq's sslmode values, as types.SSLModes. */
export const SSL_MODES = ['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full'] as const
export type SSLMode = (typeof SSL_MODES)[number]

/** Mirrors daemon.HostView: a host and the ids of its connections. */
export interface HostView extends Host {
  connections: string[]
}

/** Mirrors types.StageKind: a detection adapter a stage runs. */
export type StageKind = 'patterns' | 'list'

/** Mirrors types.Stage: one step of a connection's detection pipeline. */
export interface Stage {
  kind: StageKind
  /** When set, only this stage's hits of these entity types are kept. */
  entities?: string[]
  /** Sends the stage unmasked text, earlier stages' hits left in place. */
  raw?: boolean
}

export interface Connection {
  id: string
  host_id: string
  name: string
  engine: string
  database: string
  role: string
  version?: string
  catalog_path: string
  mode: Mode
  detection?: Stage[]
  limits: Limits
  denylist?: RelationRef[]
  findings?: Finding[]
  audited_at: string
  writes: Writes
}

/** A profile's write setting: off makes its sessions read-only; approve lets
 * an agent send writes, each of which waits for a human. */
export type Writes = 'off' | 'approve'

/** Where a profile logs in and as whom. Never a password. */
export interface Profile {
  host: string
  address: string
  port: number
  database: string
  username: string
  writes: Writes
}

/** The list-view shape `GET /v1/connections` returns — a narrower projection
 * than `Connection`, per CONTRACT.md §3. */
export interface ConnectionSummary extends Profile {
  id: string
  name: string
  engine: string
  mode: Mode
}

// ── policy.go ───────────────────────────────────────────────────────────────

export type Policy = 'allow' | 'scan' | 'partial' | 'token' | 'redact' | 'drop'

export type PartialForm = 'email_domain' | 'card_bin_last4' | 'phone_country_area' | 'ip_network'

export interface ColumnPolicy {
  policy: Policy
  namespace?: string
  form?: PartialForm
  hide_name?: boolean
  paths?: Record<string, ColumnPolicy>
}

// ── result.go ───────────────────────────────────────────────────────────────

type Tier = 0 | 1 | 2 | 3 | 4

type Basis = 'catalog' | 'rules' | 'sampled' | 'inherited' | 'parameter' | 'unknown'

export interface Transform {
  policy: Policy
  namespace?: string
  form?: PartialForm
  basis: Basis
  spans_redacted?: number
  sample_size?: number
  collisions?: number
}

export interface ColumnMeta {
  name: string
  type: string
  policy: Policy
}

export interface Degradation {
  /** "detector" | "catalog" */
  layer: string
  reason: string
}

export interface QueryResult {
  rows: unknown[][]
  columns: ColumnMeta[]
  row_count: number
  truncated?: boolean
  transforms: Record<string, Transform>
  tier: Tier
  audit_id: string
  mode: Mode
  /** "tier0" | "grant:<id>" | "ticket:<id>" | "delegation:<id>" */
  authorization?: string
  degradations?: Degradation[]
  executed_rows?: number
  previewed_rows?: number
}

type Code =
  | 'syntax'
  | 'multi_statement'
  | 'permission_denied'
  | 'unclassified'
  | 'denylisted'
  | 'ddl_refused'
  | 'writes_off'
  | 'approval_required'
  | 'approval_refused'
  | 'ticket_unknown'
  | 'timeout'
  | 'row_cap'
  | 'stale_token'
  | 'unreachable'
  | 'internal'

/** The only error shape that reaches the UI (SPEC R6.4a). No field here ever
 * carries PostgreSQL's own text. */
export interface KeeperError {
  code: Code
  summary: string
  action?: string
  audit_id?: string
}

interface ClientInfo {
  name: string
  version?: string
  pid?: number
  workspace?: string
}

export interface AuditRecord {
  id: string
  at: string
  session_id: string
  client: ClientInfo
  intent?: string
  connection: string
  statement: string
  statement_type: string
  relations?: RelationRef[]
  output_columns?: ColumnMeta[]
  tier: Tier
  transforms?: Record<string, Transform>
  row_count: number
  authorization?: string
  approver?: string
  degradations?: Degradation[]
  collisions?: number
  /** nanoseconds */
  duration: number
  error_code?: Code
  /** How an escalated statement was settled; absent when it never waited. */
  decision?: 'approved' | 'refused' | 'expired' | 'cancelled'
}

/**
 * Mirrors daemon.ActivityDetail: one record and, while the daemon still holds
 * it, the masked result the agent received. Memory only (R10b), so it is absent
 * for anything older than the daemon's most recent results or its last restart.
 */
export interface ActivityDetail extends AuditRecord {
  result?: QueryResult
}

// ── session.go ──────────────────────────────────────────────────────────────

export interface Session {
  id: string
  client: ClientInfo
  intent?: string
  connected_at: string
}

export type RequestKind = 'input' | 'authorization'

export interface PathRef {
  connection_id: string
  relation: RelationRef
}

export type GrantLifetime = 'session' | 'standing'

export interface Grant {
  id: string
  path: PathRef
  lifetime: GrantLifetime
  row_ceiling: number
  created_by: string
  created_at: string
  expires_at?: string
  last_used_at?: string
  uses: number
  suspended?: boolean
  reason?: string
}

export interface ApprovalFacts {
  intent: string
  statement_type: string
  relations: RelationRef[]
  estimated_rows: number
  estimated_cost: number
  egress?: string[]
  reasons?: string[]
}

interface WritePreview {
  operation: string
  row_count: number
  previewed_at: string
  scope: RelationRef[]
  returning?: Record<string, Transform>
}

export interface ApprovalItem {
  ticket_id: string
  session: Session
  connection: string
  tier: Tier
  facts: ApprovalFacts
  sql: string
  created_at: string
  write?: WritePreview
}

// ── SSE (GET /v1/events) ─────────────────────────────────────────────────────

export type KeeperEventKind = 'approval' | 'request' | 'connection' | 'catalog' | 'session'

export interface KeeperEvent<T = unknown> {
  kind: KeeperEventKind
  data: T
}
