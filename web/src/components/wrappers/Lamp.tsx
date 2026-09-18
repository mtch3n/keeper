import { cn } from '@/lib/utils'

export type LampState = 'idle' | 'waiting' | 'blocked' | 'live'

const LABEL: Record<LampState, string> = {
  idle: 'Idle',
  waiting: 'Waiting',
  blocked: 'Blocked',
  live: 'Connected',
}

/**
 * The one state vocabulary in the interface (UI.md §2.1). Every row, queue
 * item and the daemon's SSE connection report themselves through the same
 * lamp, so a quiet board reads as quiet without a second legend.
 *
 * `idle` is an outline, a lit lamp a flat fill, and none of them glow.
 * `waiting` (amber) is the only state that asks for attention; `blocked`
 * (red) is a refusal; `live` (green, round — a pilot lamp rather than a work
 * state) is the daemon connection. Nothing else in the application may use
 * these colours (UI.md §2.1).
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "indicator status
 * dot"` returned no items; `-q "badge"` returns only `@shadcn/badge`, a text
 * chip with padding and a label. Composition cannot produce the behaviour: a
 * lamp carries no text, is a fixed 8px, and switches between four states
 * where two carry their own keyframes. Lamp owns the state vocabulary itself
 * — the mapping from waiting/blocked/idle/live onto one visual language —
 * which no primitive holds.
 */
export function Lamp({
  state = 'idle',
  label,
  className,
}: {
  state?: LampState
  label?: string
  className?: string
}) {
  return (
    <span
      role="img"
      aria-label={label ?? LABEL[state]}
      data-state={state}
      className={cn(
        'inline-block size-2 shrink-0',
        state === 'idle' && 'shadow-lamp-idle',
        state === 'waiting' && 'bg-waiting',
        state === 'blocked' && 'lamp-blocked bg-blocked',
        state === 'live' && 'lamp-live rounded-full bg-live',
        className,
      )}
    />
  )
}
