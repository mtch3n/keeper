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

export type Mode = 'strict' | 'assisted' | 'permissive'

export type FindingKind =
  | 'attribute'
  | 'membership'
  | 'relation-write'
  | 'schema-create'
  | 'function-exec'
  | 'security-definer'

export interface Finding {
  id: string
  kind: FindingKind
  subject: string
  detail: string
  narrower?: string
  hash?: string
}

export interface Acceptance {
  finding_id: string
  hash?: string
  actor: string
  at: string
  /** "cli" or "ui" — never "mcp" (SPEC R4.1f, §6.3). */
  via: string
}

export interface RelationRef {
  schema: string
  relation: string
}

export type WriteOp = 'INSERT' | 'UPDATE' | 'DELETE'

export interface WriteScopeEntry {
  relation: RelationRef
  operations: WriteOp[]
}

export interface Limits {
  max_rows_ceiling: number
  /** nanoseconds */
  statement_timeout: number
  scan_sample: number
}

export interface Connection {
  id: string
  name: string
  engine: string
  database: string
  role: string
  version?: string
  catalog_path: string
  mode: Mode
  limits: Limits
  denylist?: RelationRef[]
  write_scope?: WriteScopeEntry[]
  findings?: Finding[]
  acceptances?: Acceptance[]
  enabled: boolean
  audited_at: string
  has_write_credential: boolean
}

/** The list-view shape `GET /v1/connections` returns — a narrower projection
 * than `Connection`, per CONTRACT.md §3. */
export interface ConnectionSummary {
  id: string
  name: string
  engine: string
  database: string
  role: string
  degraded: boolean
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

export type Tier = 0 | 1 | 2 | 3 | 4

export type Basis = 'catalog' | 'rules' | 'sampled' | 'inherited' | 'parameter' | 'unknown'

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
  /** "detector" | "judge" | "catalog" */
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

export interface ExplainResult {
  statement_type: string
  relations: RelationRef[]
  estimated_rows: number
  estimated_cost: number
  output_columns: ColumnMeta[]
  predicted_tier: Tier
  reasons?: string[]
}

export type Code =
  | 'syntax'
  | 'multi_statement'
  | 'permission_denied'
  | 'unclassified'
  | 'denylisted'
  | 'ddl_refused'
  | 'out_of_write_scope'
  | 'no_write_credential'
  | 'vault_locked'
  | 'connection_disabled'
  | 'approval_required'
  | 'approval_refused'
  | 'ticket_unknown'
  | 'timeout'
  | 'row_cap'
  | 'stale_token'
  | 'internal'

/** The only error shape that reaches the UI (SPEC R6.4a). No field here ever
 * carries PostgreSQL's own text. */
export interface KeeperError {
  code: Code
  summary: string
  action?: string
  audit_id?: string
}

export interface ClientInfo {
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
  connection_degraded?: boolean
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
}

// ── session.go ──────────────────────────────────────────────────────────────

export interface Session {
  id: string
  client: ClientInfo
  intent?: string
  connected_at: string
}

export type TicketState =
  | 'pending_approval'
  | 'approved'
  | 'refused'
  | 'expired'
  | 'cancelled'
  | 'ready'
  | 'failed'

export interface Ticket {
  ticket: string
  state: TicketState
  reason?: string
  tier: Tier
  audit_id: string
  created_at: string
}

export type RequestKind = 'input' | 'authorization'

export interface PathRef {
  connection_id: string
  relation: RelationRef
}

export interface LocalRequest {
  request_id: string
  kind: RequestKind
  connection_id: string
  namespace?: string
  path?: PathRef
  purpose?: string
  url: string
  /** "pending_input" | "ready" | "cancelled" | "expired" */
  state: string
  expires_at: string
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

export interface WritePreview {
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
  connection_degraded: boolean
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
