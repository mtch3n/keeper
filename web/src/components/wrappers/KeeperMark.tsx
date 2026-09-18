import { cn } from '@/lib/utils'

/**
 * keeper's mark: a square keyhole. It draws in `currentColor` so it stays
 * achromatic like every other glyph in the chrome — the mark itself is not a
 * place for the amber accent, which is spent entirely on `waiting`.
 */
export function KeeperMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className={cn('size-5 shrink-0', className)} fill="none">
      <rect x="3.5" y="3.5" width="17" height="17" stroke="currentColor" strokeWidth="1.5" />
      <circle cx="12" cy="10" r="2.5" fill="currentColor" />
      <path d="M12 12.5 L12 17" stroke="currentColor" strokeWidth="1.5" strokeLinecap="square" />
    </svg>
  )
}
