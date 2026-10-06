import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { AgentsIndicator } from '@/components/wrappers/AgentsIndicator'
import { Lamp } from '@/components/wrappers/Lamp'
import { Brand } from '@/components/wrappers/Brand'
import { ThemeToggle } from '@/components/wrappers/ThemeToggle'
import { useInbox } from '@/lib/inbox'
import { useLiveStatus } from '@/lib/live-status'
import { useInboxNotifications } from '@/lib/notify'

export type Section = 'connections' | 'inbox' | 'activity' | 'settings'

const SECTIONS: { section: Section; label: string; to: string }[] = [
  { section: 'connections', label: 'Connections', to: '/connections' },
  { section: 'inbox', label: 'Inbox', to: '/inbox' },
  { section: 'activity', label: 'Activity', to: '/activity' },
  { section: 'settings', label: 'Settings', to: '/settings' },
]

/**
 * The chrome every screen sits inside: one bar, four places, each with one job —
 * Connections (what you manage), Inbox (what is waiting on you), Activity (what
 * happened), Settings (how keeper behaves). There is no sidebar: its agent list
 * is the agents indicator here, and its database list was a second way to reach
 * a connection's page that disagreed with the first.
 *
 * Inbox carries the count of what waits, and the bar raises a notification when
 * something new arrives while the page is out of view. The daemon's lamp joins
 * the bar only when the stream is degraded: a permanent "live" lamp spends the
 * chrome's attention on the reading that never needs acting on.
 */
export function AppShell({ section, children }: { section?: Section; children: ReactNode }) {
  const { status } = useLiveStatus()
  const { count } = useInbox()
  useInboxNotifications()

  const daemon =
    status === 'waiting'
      ? { lamp: 'waiting' as const, label: 'Reconnecting', title: 'Live updates dropped; reconnecting' }
      : status === 'blocked'
        ? { lamp: 'blocked' as const, label: 'Offline', title: 'keeperd is not responding' }
        : null

  const tab =
    'relative flex items-center gap-2 px-2.5 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground sm:px-4 ' +
    'after:absolute after:inset-x-2.5 sm:after:inset-x-4 after:bottom-2.5 after:h-0.5 after:scale-x-0 after:bg-foreground after:transition-transform after:duration-200 after:ease-settle ' +
    'aria-[current=page]:text-foreground aria-[current=page]:after:scale-x-100'

  return (
    <div className="min-h-svh bg-background">
      <div className="sticky top-0 z-20 flex h-shell items-stretch gap-3 border-b border-border bg-background px-6 sm:gap-6 lg:px-8">
        <Brand className="flex items-center" />

        <nav aria-label="Sections" className="flex min-w-0 overflow-x-auto">
          {SECTIONS.map((item) => (
            <Link
              key={item.section}
              to={item.to}
              aria-current={section === item.section ? 'page' : undefined}
              className={tab}
            >
              {item.label}
              {item.section === 'inbox' && count > 0 ? <Badge variant="secondary">{count}</Badge> : null}
            </Link>
          ))}
        </nav>

        <div className="ml-auto flex items-center gap-2 sm:gap-3">
          {daemon && (
            <span className="flex items-center gap-2 text-xs text-muted-foreground" title={daemon.title}>
              <Lamp state={daemon.lamp} label={daemon.label} />
              <span aria-hidden="true" className="max-sm:hidden">
                {daemon.label}
              </span>
            </span>
          )}
          <AgentsIndicator />
          <ThemeToggle />
        </div>
      </div>
      <main className="mx-auto w-full max-w-page px-6 pt-8 pb-16 lg:px-8">{children}</main>
    </div>
  )
}
