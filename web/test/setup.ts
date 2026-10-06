import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  localStorage.clear()
})

// jsdom has neither EventSource nor matchMedia. The stub event source keeps
// every listener so a test can push a daemon event with `emit`.
type Listener = (message: MessageEvent<string>) => void
export const streams: StubEventSource[] = []
class StubEventSource {
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners = new Map<string, Listener[]>()
  constructor() {
    streams.push(this)
  }
  addEventListener(kind: string, fn: Listener) {
    this.listeners.set(kind, [...(this.listeners.get(kind) ?? []), fn])
  }
  close() {
    const i = streams.indexOf(this)
    if (i >= 0) streams.splice(i, 1)
  }
}
globalThis.EventSource = StubEventSource as unknown as typeof EventSource
window.matchMedia ??= ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeListener() {},
  dispatchEvent: () => false,
})) as typeof window.matchMedia
