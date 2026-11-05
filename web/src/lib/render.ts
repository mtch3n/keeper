/**
 * Rendering rules that are requirements rather than presentation.
 *
 * UI.md §4 puts two of them in the renderer on purpose: a trojan-source payload
 * must be visible rather than acted on, and a homoglyph in an identifier must be
 * flagged. Both are properties of how text is drawn, so they belong here and not
 * in the screen that happens to be drawing it.
 */

/** Unicode bidirectional controls. Rendered, they reorder what follows. */
const BIDI = /[‪-‮⁦-⁩‎‏]/g

/**
 * Makes bidirectional overrides visible as their code point, so a statement
 * cannot read one way and run another (SPEC R9.2). Use this anywhere SQL or a
 * server-supplied identifier is shown.
 */
export function renderSQL(sql: string): string {
  return sql.replace(BIDI, (c) => `[U+${c.codePointAt(0)!.toString(16).toUpperCase().padStart(4, '0')}]`)
}

/**
 * True when an identifier mixes scripts — the shape of a homoglyph attack, where
 * a Cyrillic а stands in for a Latin a. Flagged rather than rejected: a schema
 * may legitimately be named in one non-Latin script, and it is the *mixture*
 * inside one identifier that is the tell.
 */
export function suspectHomoglyph(identifier: string): boolean {
  const scripts = new Set<string>()
  for (const ch of identifier) {
    if (/[a-zA-Z]/.test(ch)) scripts.add('latin')
    else if (/[Ѐ-ӿ]/.test(ch)) scripts.add('cyrillic')
    else if (/[Ͱ-Ͽ]/.test(ch)) scripts.add('greek')
  }
  return scripts.size > 1
}

function short(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86400)}d`
}

/** A short time since a past moment: 40s, 12m, 3h, 2d. */
export function age(iso: string | undefined): string {
  if (!iso) return '—'
  const ms = Date.now() - new Date(iso).getTime()
  return Number.isFinite(ms) ? short(ms) : '—'
}

/** A short time until a future moment, in the same units as `age`. */
export function until(iso: string | undefined): string {
  if (!iso) return '—'
  const ms = new Date(iso).getTime() - Date.now()
  return Number.isFinite(ms) ? short(ms) : '—'
}

/** A relation list as text, for one line of facts. */
export function relationList(rels: { schema: string; relation: string }[] | undefined): string {
  if (!rels?.length) return '—'
  return rels.map((r) => `${r.schema}.${r.relation}`).join(', ')
}

/** Nanoseconds, as the Go side sends them, in milliseconds. */
export function durationMs(ns: number | undefined): string {
  if (!ns) return '—'
  return `${(ns / 1e6).toFixed(0)}ms`
}

const REASONS: Record<string, string> = {
  masked_output: 'The result would include masked columns.',
  unresolved_column: 'A column could not be matched to the catalog.',
  stale_catalog: 'The catalog is out of date with the database.',
  write: 'It changes data.',
  ddl: 'It changes the schema.',
  writes_off: 'Writes are off on this profile.',
  reads_ask: 'This profile asks before reads.',
  volatile_call: 'It calls a function that may change data.',
  multi_statement: 'It holds more than one statement.',
  unparseable: 'keeper could not read the statement.',
  plan_timeout: 'Planning the statement took too long.',
  permission_denied: 'The database role lacks permission.',
}

/** A reason code the pipeline gave for waiting, as a sentence; an unknown code shows as itself. */
export function reasonText(code: string): string {
  return REASONS[code] ?? code
}

/** A count with its noun, singular at one: `1 row`, `3 rows`. */
export function plural(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? '' : 's'}`
}

/**
 * The first half of an impact line: the statement kind, and the tables it
 * touches only when it touches any. A statement with no table reads `SELECT`,
 * not `SELECT on —`.
 */
export function onRelations(
  kind: string,
  rels: { schema: string; relation: string }[] | undefined,
): string {
  return rels?.length ? `${kind} on ${relationList(rels)}` : kind
}

/** Where a person decided, for the approvers that are a surface rather than a name. */
const WHERE: Record<string, string> = { dialog: 'in the terminal', ui: 'in the dashboard' }

/** Error codes that are a decision's own record, not a failure of the statement. */
const DECIDED = new Set(['approval_required', 'approval_refused'])

/**
 * What happened to an audit record's statement, as one of a closed set of words:
 * ran, asked, approved, refused, cancelled, expired, or failed with its code. A
 * statement approved and then failed reads failed: the approval is in the record.
 */
export function outcomeText(r: {
  decision?: string
  approver?: string
  error_code?: string
}): string {
  if (r.error_code && !DECIDED.has(r.error_code)) return `failed · ${r.error_code}`
  if (r.decision === 'expired') return 'expired'
  if (r.decision) return r.approver ? `${r.decision} ${WHERE[r.approver] ?? `by ${r.approver}`}` : r.decision
  if (r.error_code === 'approval_required') return 'asked'
  return 'ran'
}

const VERBS: Record<string, string> = {
  SELECT: 'Read',
  INSERT: 'Insert into',
  UPDATE: 'Update',
  DELETE: 'Delete from',
  MERGE: 'Merge into',
}

/**
 * A record's title: what the statement did to which tables, "Read public.orders".
 * A statement that touches no table has nothing better than its own text.
 */
export function recordTitle(r: {
  statement: string
  statement_type: string
  relations?: { schema: string; relation: string }[]
}): string {
  const rels = r.relations ?? []
  if (rels.length === 0) return renderSQL(r.statement)
  const verb = VERBS[r.statement_type] ?? r.statement_type
  const names = rels.slice(0, 2).map((x) => `${x.schema}.${x.relation}`).join(', ')
  return rels.length > 2 ? `${verb} ${names} +${rels.length - 2}` : `${verb} ${names}`
}

/** A local calendar day, for grouping: 2026-11-05. */
export function dayKey(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

/** A day's name: Today, Yesterday, then the date. */
export function dayLabel(iso: string): string {
  const key = dayKey(iso)
  const now = new Date()
  if (key === dayKey(now.toISOString())) return 'Today'
  now.setDate(now.getDate() - 1)
  if (key === dayKey(now.toISOString())) return 'Yesterday'
  return new Date(iso).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' })
}

/** The time of day a moment fell at: 14:02. */
export function clockTime(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

const BASES: Record<string, string> = {
  catalog: 'set in the catalog',
  rules: 'detection rules',
  inherited: 'computed from its inputs',
  parameter: 'follows a parameter',
  unknown: 'not in the catalog',
  unexamined: 'not examined',
}

/** Why a column got its policy, in words; an unknown basis shows as itself. */
export function basisText(basis: string): string {
  return BASES[basis] ?? basis
}

/** A record's row count, or a dash when the statement did not run. */
export function rowsText(r: { decision?: string; error_code?: string; row_count: number }): string {
  if (r.error_code) return '—'
  return String(r.row_count)
}
