import { cn } from '@/lib/utils'

/** The form keeperd writes for a redacted span inside readable text:
 * `⟨redacted:email_address⟩`, or `⟨redacted⟩` when the type is unknown. */
const REDACTED_SPAN = /⟨redacted(?::[^⟩]*)?⟩/g

/** Splits a string into readable text and redacted-span segments. */
export function splitRedactedSpans(text: string): { text: string; redacted: boolean }[] {
  const segments: { text: string; redacted: boolean }[] = []
  let cursor = 0
  for (const match of text.matchAll(REDACTED_SPAN)) {
    const index = match.index ?? 0
    if (index > cursor) segments.push({ text: text.slice(cursor, index), redacted: false })
    segments.push({ text: match[0], redacted: true })
    cursor = index + match[0].length
  }
  if (cursor < text.length) segments.push({ text: text.slice(cursor), redacted: false })
  return segments
}

/**
 * A `scan` result (UI.md §2.2): a readable sentence with marked spans inside it
 * where a matched span was redacted. This is cell-internal, not a
 * cell-replacement treatment — the other six treatments in `Cell` replace the
 * whole cell; this one marks a range within it, and it is the most common of
 * the seven on any schema with free text.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "highlight
 * text"` and `-q "mark"` both returned no items. Composition cannot produce
 * the behaviour: no primitive parses a string for embedded redaction spans.
 * ScannedText owns exactly that parse and the span's visual treatment.
 */
export function ScannedText({ text, className }: { text: string; className?: string }) {
  const segments = splitRedactedSpans(text)
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
