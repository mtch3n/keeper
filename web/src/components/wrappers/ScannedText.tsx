import { cn } from '@/lib/utils'

/** The character keeperd uses to mark a redacted span inside otherwise-readable
 * text (SPEC R8.5a). A run of one or more of these is a redaction, however
 * long; it is deliberately not length-preserving, so the run's width must
 * never be read as the width of what it replaced. */
const REDACTION_CHAR = '█' // █ FULL BLOCK

/**
 * Splits a string into readable text and solid-run segments. Exported so
 * `Cell` can render `redacted` and `partial` with the exact same solid glyph
 * `scanned` uses inline — UI.md §3.3: a redacted cell must look identical
 * everywhere.
 */
export function splitRedactedRuns(text: string): { text: string; redacted: boolean }[] {
  const runRegex = new RegExp(`${REDACTION_CHAR}+`, 'g')
  const segments: { text: string; redacted: boolean }[] = []
  let cursor = 0
  for (const match of text.matchAll(runRegex)) {
    const index = match.index ?? 0
    if (index > cursor) segments.push({ text: text.slice(cursor, index), redacted: false })
    segments.push({ text: match[0], redacted: true })
    cursor = index + match[0].length
  }
  if (cursor < text.length) segments.push({ text: text.slice(cursor), redacted: false })
  return segments
}

/**
 * A `scan` result (UI.md §2.2): a readable sentence with solid runs inside it
 * where SPEC R8.5a redacted a matched span. This is cell-internal, not a
 * cell-replacement treatment — the other six treatments in `Cell` replace the
 * whole cell; this one marks a range within it, and it is the most common of
 * the seven on any schema with free text.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "highlight
 * text"` and `-q "mark"` both returned no items. Composition cannot produce
 * the behaviour: no primitive parses a string for embedded redaction runs.
 * ScannedText owns exactly that parse and the run's visual treatment.
 */
export function ScannedText({ text, className }: { text: string; className?: string }) {
  const segments = splitRedactedRuns(text)
  return (
    <span className={cn('text-sm', className)}>
      {segments.map((segment, index) =>
        segment.redacted ? (
          <span key={index} className="text-meta text-foreground" aria-label="redacted span">
            {segment.text}
          </span>
        ) : (
          <span key={index}>{segment.text}</span>
        ),
      )}
    </span>
  )
}
