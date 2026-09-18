import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * The facts block of SPEC §9.2, as one component.
 *
 * A human cannot tell from `SELECT * FROM users WHERE created_at > '2024-01-01'`
 * that it returns 50,000 SSNs. So every screen that shows a statement shows what
 * it would do above it, in the same shape — an approval, an activity record, the
 * authorization page — and UI.md §3.3 requires that shape to be one component
 * rather than three that drift.
 *
 * A label column of fixed width rather than a grid, so a long value wraps under
 * itself instead of stretching the column for every other row.
 */
export function Facts({ children, className }: { children: ReactNode; className?: string }) {
  return <dl className={cn('flex flex-col gap-2 text-sm', className)}>{children}</dl>
}

/** One label/value row inside a {@link Facts}. */
export function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex gap-6">
      <dt className="w-32 shrink-0 text-meta text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}
