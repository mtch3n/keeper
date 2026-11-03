import { subscribeToEvents, type EventStreamStatus } from '@/lib/api'
import type { KeeperEvent } from '@/lib/types'

const LOCK = 'keeper-events'

type Message =
  | { type: 'event'; event: KeeperEvent }
  | { type: 'status'; status: EventStreamStatus }
  | { type: 'hello' }

/** The browser services the sharing needs, so a test can stand in for them. */
export interface TabEnv {
  locks: Pick<LockManager, 'request'>
  channel: (name: string) => Pick<BroadcastChannel, 'postMessage' | 'close' | 'onmessage'>
  connect: typeof subscribeToEvents
}

function browserEnv(): TabEnv | undefined {
  if (typeof navigator === 'undefined' || !navigator.locks || typeof BroadcastChannel === 'undefined') return undefined
  return { locks: navigator.locks, channel: (name) => new BroadcastChannel(name), connect: subscribeToEvents }
}

/**
 * The daemon's event stream, shared by every tab of this browser. A browser
 * allows six HTTP/1.1 connections per host; a stream per tab would leave no
 * room for requests. One tab holds a Web Lock and owns the only stream; it
 * relays events and status to the others over a BroadcastChannel. When it
 * closes, the lock passes to a waiting tab, which opens the stream instead.
 */
export function shareEvents(
  onEvent: (event: KeeperEvent) => void,
  onStatus: (status: EventStreamStatus) => void,
  env: TabEnv | undefined = browserEnv(),
): () => void {
  if (!env) return subscribeToEvents(onEvent, onStatus)

  const channel = env.channel(LOCK)
  const abort = new AbortController()
  let stopLeading: (() => void) | undefined
  let closed = false
  let status: EventStreamStatus = 'waiting'

  const send = (m: Message) => {
    if (!closed) channel.postMessage(m)
  }

  channel.onmessage = (message: MessageEvent<Message>) => {
    const m = message.data
    if (m.type === 'event' && !stopLeading) onEvent(m.event)
    else if (m.type === 'status' && !stopLeading) onStatus(m.status)
    else if (m.type === 'hello' && stopLeading) send({ type: 'status', status })
  }

  onStatus('waiting')
  send({ type: 'hello' })

  env.locks
    .request(LOCK, { signal: abort.signal }, () => {
      if (closed) return
      return new Promise<void>((release) => {
        const stop = env.connect(
          (event) => {
            onEvent(event)
            send({ type: 'event', event })
          },
          (next) => {
            if (closed) return
            status = next
            onStatus(next)
            send({ type: 'status', status: next })
          },
        )
        stopLeading = () => {
          stop()
          release()
        }
      })
    })
    .catch(() => undefined)

  return () => {
    closed = true
    abort.abort()
    stopLeading?.()
    channel.close()
  }
}
