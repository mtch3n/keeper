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

/** A short relative age: 40s, 12m, 3h, 2d. */
export function age(iso: string | undefined): string {
  if (!iso) return '—'
  const ms = Date.now() - new Date(iso).getTime()
  if (!Number.isFinite(ms)) return '—'
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86400)}d`
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
