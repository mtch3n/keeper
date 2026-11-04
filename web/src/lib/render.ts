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

/**
 * What happened to an audit record's statement, in plain words: it ran, it
 * escalated and was only asked about, a person or the clock settled it, or it
 * failed with a code.
 */
export function outcomeText(r: {
  decision?: string
  approver?: string
  error_code?: string
}): string {
  if (r.decision) return r.approver && r.decision !== 'expired' ? `${r.decision} by ${r.approver}` : r.decision
  if (r.error_code === 'approval_required') return 'asked'
  return r.error_code || 'ran'
}

/** A record's row count, or a dash when the statement did not run. */
export function rowsText(r: { decision?: string; error_code?: string; row_count: number }): string {
  if (r.error_code && r.decision !== 'approved') return '—'
  return String(r.row_count)
}
