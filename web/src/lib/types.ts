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

export interface RelationRef {
  schema: string
  relation: string
}

export interface Limits {
  max_rows_ceiling: number
  /** bytes of row data per result */
  max_bytes: number
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

export interface Connection {
  id: string
  host_id: string
  name: string
  engine: string
  database: string
  role: string
  version?: string
  catalog_path: string
  reads: Reads
  writes: Writes
  detection: Detection
  limits: Limits
}

/** When a read asks a human: every read, only an uncertain one, or none. The
 * transaction is read-only under all three. */
export type Reads = 'always' | 'uncertain' | 'never'

/** Whether a profile's writes are refused, wait for a human, or commit at once. */
export type Writes = 'off' | 'ask' | 'allow'

/** What scans text for personal data: nothing, scan columns, or every column. */
export type Detection = 'off' | 'scan' | 'all'

/** Where a profile logs in and as whom. Never a password. */
export interface Profile {
  host: string
  address: string
  port: number
  database: string
  username: string
  reads: Reads
  writes: Writes
  detection: Detection
}

/** The list-view shape `GET /v1/connections` returns — a narrower projection
 * than `Connection`, per CONTRACT.md §3. */
export interface ConnectionSummary extends Profile {
  id: string
  name: string
  engine: string
}

// ── policy.go ───────────────────────────────────────────────────────────────

export type Policy = 'allow' | 'token' | 'redact' | 'scan'

export interface ColumnPolicy {
  policy: Policy
  namespace?: string
  paths?: Record<string, ColumnPolicy>
}

// ── result.go ───────────────────────────────────────────────────────────────

type Basis = 'catalog' | 'rules' | 'inherited' | 'parameter' | 'unknown' | 'unexamined'

export interface Transform {
  policy: Policy
  namespace?: string
  basis: Basis
  spans_redacted?: number
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
  audit_id: string
  degradations?: Degradation[]
  executed_rows?: number
  previewed_rows?: number
}

type Code =
  | 'syntax'
  | 'multi_statement'
  | 'permission_denied'
  | 'unclassified'
  | 'ddl_refused'
  | 'writes_off'
  | 'approval_required'
  | 'approval_refused'
  | 'ticket_unknown'
  | 'unknown_connection'
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
  connection: string
  statement: string
  statement_type: string
  relations?: RelationRef[]
  output_columns?: ColumnMeta[]
  transforms?: Record<string, Transform>
  row_count: number
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
  connected_at: string
}

export interface ApprovalFacts {
  statement_type: string
  relations: RelationRef[]
  estimated_rows: number
  estimated_cost: number
  egress?: string[]
  reasons?: string[]
  /** How much the statement can change; one keeper could not read is destructive. */
  risk?: Risk
  unreadable?: boolean
  /** The relations a write changes. */
  targets?: RelationRef[]
  /** Output columns, SELECT * expanded. */
  columns?: string[]
  /** Sensitive or uncatalogued columns the statement filters or joins on. */
  filters?: FilterColumn[]
}

export type Risk = 'read' | 'write' | 'destructive'

export interface FilterColumn {
  relation: RelationRef
  column: string
  flag: 'sensitive' | 'unclassified'
  policy?: Policy
}

interface WritePreview {
  operation: string
  row_count: number
  previewed_at: string
  scope: RelationRef[]
  returning?: Record<string, Transform>
  /** What the write changes, for the approver only. Absent when the server
   * could not report rows; row_count still holds. */
  changes?: WriteChanges
}

/** A write's changed rows, in cleartext. Never logged, never sent to an agent. */
export interface WriteChanges {
  columns: string[]
  rows: { old?: string[]; new?: string[] }[]
  /** Affected rows beyond the ones shown. */
  omitted?: number
}

export interface ApprovalItem {
  ticket_id: string
  session: Session
  connection: string
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
