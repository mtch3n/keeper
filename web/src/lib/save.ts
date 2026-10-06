import { useCallback, useState } from 'react'

export type SaveState = { status: 'idle' | 'saving' | 'saved' | 'failed'; error?: string }

/**
 * One save's lifecycle, so every setting reports the same way: saving, then
 * Saved or the reason it was refused, beside the control that changed.
 */
export function useSave() {
  const [state, setState] = useState<SaveState>({ status: 'idle' })
  const run = useCallback(async (fn: () => Promise<unknown>) => {
    setState({ status: 'saving' })
    try {
      await fn()
      setState({ status: 'saved' })
      return true
    } catch (e) {
      setState({ status: 'failed', error: e instanceof Error ? e.message : String(e) })
      return false
    }
  }, [])
  return { ...state, busy: state.status === 'saving', run }
}

/** A positive whole number typed into a field, or null. */
export function positiveInt(s: string): number | null {
  const t = s.trim()
  if (!/^\d+$/.test(t)) return null
  const n = Number(t)
  return n > 0 ? n : null
}
