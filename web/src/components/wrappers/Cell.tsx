import { cn } from '@/lib/utils'
import { ScannedText, splitRedactedRuns } from '@/components/wrappers/ScannedText'

/**
 * What a table cell shows, decided by the caller from a row's raw value and
 * its `Transform` (`policy`, `basis`) — never guessed here. `Cell` only knows
 * how to draw each of the seven treatments UI.md §2.2 defines, plus the plain
 * value a `PolicyAllow` column carries. A masked cell must read as neither a
 * null nor an error, and no two treatments may look alike:
 *
 * - `value`        PolicyAllow, non-null: the value as the API sent it.
 * - `null`         the underlying value is SQL NULL, independent of policy.
 * - `tokenized`    PolicyToken: `transform.namespace` bound the value; the
 *                  cell holds the literal token string, e.g. "⟨e1:a3f21b⟩".
 * - `partial`      PolicyPartial: the pre-formatted string the API sent,
 *                  e.g. "████1234" — the declared component is already kept.
 * - `redacted`     PolicyRedact: nothing survived.
 * - `scanned`      PolicyScan: a readable string with inline solid runs
 *                  where SPEC R8.5a redacted a matched span.
 * - `dropped`      the column is absent from the response under this policy.
 * - `unclassified` the column has no catalog entry; basis is `unknown`
 *                  (SPEC R5.4a) and keeper fails closed. The only treatment
 *                  carrying colour (UI.md §2.2).
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "redact mask"`
 * and `-q "table cell"` both returned no items. No primitive has a notion of
 * seven mutually-distinguishable masking states over one value; Cell owns
 * that vocabulary the way `Lamp` owns the four-state one.
 */
export type CellValue =
  | { kind: 'value'; text: string }
  | { kind: 'null' }
  | { kind: 'tokenized'; token: string }
  | { kind: 'partial'; text: string }
  | { kind: 'redacted' }
  | { kind: 'scanned'; text: string }
  | { kind: 'dropped' }
  | { kind: 'unclassified' }

export function Cell({ value, className }: { value: CellValue; className?: string }) {
  switch (value.kind) {
    case 'value':
      return <span className={cn('text-sm', className)}>{value.text}</span>

    case 'null':
      return (
        <span className={cn('text-sm text-muted-foreground/60', className)} aria-label="null" title="Nothing was ever here">
          ·
        </span>
      )

    case 'tokenized':
      return (
        <span
          className={cn('text-meta text-muted-foreground', className)}
          title="A token: this value can be passed back to keeper as a parameter"
        >
          {value.token}
        </span>
      )

    case 'partial':
      return (
        <span className={cn('text-meta text-foreground', className)} title="Partially released under a declared form">
          {splitRedactedRuns(value.text).map((segment, index) =>
            segment.redacted ? (
              <span key={index} aria-label="redacted span">
                {segment.text}
              </span>
            ) : (
              <span key={index} className="text-muted-foreground">
                {segment.text}
              </span>
            ),
          )}
        </span>
      )

    case 'redacted':
      return (
        <span className={cn('text-meta text-foreground', className)} aria-label="redacted" title="Present; nothing was sent">
          {'█'.repeat(4)}
        </span>
      )

    case 'scanned':
      return <ScannedText text={value.text} className={className} />

    case 'dropped':
      return (
        <span className={cn('inline-flex items-center gap-1.5 text-sm text-muted-foreground', className)}>
          <span aria-hidden="true">—</span>
          <span className="text-xs">dropped</span>
        </span>
      )

    case 'unclassified':
      return (
        <span
          className={cn(
            'cell-unclassified text-meta inline-flex h-4 w-14 items-center justify-center text-transparent',
            className,
          )}
          role="img"
          aria-label="Unclassified — this column needs a catalog entry"
          title="Unclassified — this column needs a catalog entry"
        />
      )
  }
}
