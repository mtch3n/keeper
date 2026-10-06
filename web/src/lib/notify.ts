import { useEffect } from 'react'

import { subscribeToEvents } from '@/lib/api'

const KEY = 'keeper.notifications'

/** Whether this browser raises a notification for a new Inbox item. On unless
 * turned off; the browser's own permission still decides whether one can show. */
export function notificationsOn(): boolean {
  return localStorage.getItem(KEY) !== 'off'
}

export function setNotificationsOn(on: boolean) {
  localStorage.setItem(KEY, on ? 'on' : 'off')
}

/** A short two-note chime. Silent where the browser has no audio context. */
function chime() {
  try {
    const ctx = new AudioContext()
    for (const [i, freq] of [660, 880].entries()) {
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()
      osc.frequency.value = freq
      gain.gain.value = 0.08
      osc.connect(gain).connect(ctx.destination)
      osc.start(ctx.currentTime + i * 0.12)
      osc.stop(ctx.currentTime + i * 0.12 + 0.1)
    }
  } catch {
    // No audio context in this browser; the notification alone stands.
  }
}

/**
 * Raises a notification and a chime when something new lands in the Inbox and
 * the page is not in view. An Inbox nobody is looking at is not a gate, and a
 * notification on a visible page is noise.
 */
export function useInboxNotifications() {
  useEffect(
    () =>
      subscribeToEvents((event) => {
        const data = event.data as { action?: string; item?: { session?: { client?: { name?: string } }; facts?: { intent?: string } } }
        const arrived =
          (event.kind === 'approval' && data.action === 'queued') || (event.kind === 'request' && data.action === 'opened')
        if (!arrived || !notificationsOn() || !document.hidden) return
        if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
        const who = data.item?.session?.client?.name
        new Notification('keeper: something is waiting for you', {
          body: who ? `${who}: ${data.item?.facts?.intent ?? 'needs a decision'}` : 'An agent needs a decision.',
          tag: 'keeper-inbox',
        })
        chime()
      }),
    [],
  )
}
