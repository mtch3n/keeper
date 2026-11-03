import { cn } from '@/lib/utils'
import { ScannedText } from '@/components/wrappers/ScannedText'

/**
 * What a table cell shows, decided by the caller from a row's raw value and
 * its `Transform` (`policy`, `basis`) — never guessed here. `Cell` only knows
 * how to draw each of the five treatments, plus the plain value a `PolicyAllow`
 * column carries. A masked cell must read as neither a
 * null nor an error, and no two treatments may look alike:
 *
 * - `value`        PolicyAllow, non-null: the value as the API sent it.
 * - `null`         the underlying value is SQL NULL, independent of policy.
 * - `tokenized`    PolicyToken: `transform.namespace` bound the value; the
 *                  cell holds the literal token string, e.g. "⟨e1:a3f21b⟩".
 * - `redacted`     PolicyRedact: nothing survived; drawn as the `⟨redacted⟩`
 *                  the agent received, in the muted text colour.
 * - `scanned`      PolicyScan: a readable string with inline marked spans
 *                  where a matched span was redacted.
 * - `unclassified` the column has no catalog entry; basis is `unknown`
 *                  and keeper fails closed. The only treatment
 *                  carrying colour (UI.md §2.2).
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "redact mask"`
 * and `-q "table cell"` both returned no items. No primitive has a notion of
 * five mutually-distinguishable masking states over one value; Cell owns
 * that vocabulary the way `Lamp` owns the four-state one.
 */
export type CellValue =
  | { kind: 'value'; text: string }
  | { kind: 'null' }
  | { kind: 'tokenized'; token: string }
  | { kind: 'redacted' }
  | { kind: 'scanned'; text: string }
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

    case 'redacted':
      return (
        <span className={cn('text-meta text-muted-foreground', className)} title="Present; nothing was sent">
          ⟨redacted⟩
        </span>
      )

    case 'scanned':
      return <ScannedText text={value.text} className={className} />

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
