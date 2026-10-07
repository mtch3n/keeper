import type { LampState } from '@/components/wrappers/Lamp'

/** A connection's reach as the daemon's health check last saw it. */
type Reach = 'reachable' | 'unreachable' | 'unknown'

/**
 * How each reach reads. Unreachable is the one that needs a person; a
 * connection nobody has an answer for is an outline, because amber is
 * reserved for what waits on you and green for what is known to be up.
 */
const REACH: Record<Reach, { lamp: LampState; label: string }> = {
  reachable: { lamp: 'live', label: 'reachable' },
  unreachable: { lamp: 'blocked', label: 'unreachable' },
  unknown: { lamp: 'idle', label: 'no answer yet' },
}

export function reachOf(state?: string) {
  return REACH[state as Reach] ?? { lamp: 'idle' as const, label: 'not checked yet' }
}
