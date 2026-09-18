import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Settings } from 'lucide-react'
import { cn } from '@/lib/utils'
import { buttonVariants } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Lamp } from '@/components/wrappers/Lamp'
import { KeeperMark } from '@/components/wrappers/KeeperMark'
import { ThemeToggle } from '@/components/wrappers/ThemeToggle'
import { ConnectionSwitcher } from '@/components/wrappers/ConnectionSwitcher'
import { useLiveStatus } from '@/lib/live-status'

export type Section = 'connections' | 'approvals' | 'permissions' | 'activity' | 'catalog' | 'policy' | 'settings'

const SECTIONS: { section: Section; label: string; to: string }[] = [
  { section: 'connections', label: 'Connections', to: '/connections' },
  { section: 'approvals', label: 'Approvals', to: '/approvals' },
  { section: 'permissions', label: 'Permissions', to: '/permissions' },
  { section: 'activity', label: 'Activity', to: '/activity' },
  { section: 'catalog', label: 'Catalog', to: '/catalog' },
  { section: 'policy', label: 'Policy', to: '/policy' },
]

/**
 * The persistent chrome every screen sits inside: the mark, the connection
 * scope control, the six sections, the daemon's `Lamp`, the theme toggle,
 * and settings as the right-most control (CONTRACT.md §5, UI.md §2.3). The
 * current section is marked by a foreground rule that grows from the centre,
 * never by the amber accent — amber is spent entirely on `waiting` (UI.md
 * §1's governing rule: three colours carry meaning and nothing else does).
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "app shell"
 * -q "navbar"` returned no items composing a scope control, a fixed section
 * set and a connection lamp into one persistent bar. `NavigationMenu` and
 * `Sidebar` are page navigation primitives with no notion of connection
 * scope or a live daemon indicator; AppShell owns that composition.
 */
export function AppShell({ section, children }: { section?: Section; children: ReactNode }) {
  const { status } = useLiveStatus()

  // The daemon Lamp only ever uses three of the four states: it never
  // "waits" for a human decision the way an approval row does, so its amber
  // means "reconnecting" rather than "needs you" — still the same meaning,
  // attention, just aimed at the connection instead of a queue item.
  const daemon =
    status === 'live'
      ? { lamp: 'live' as const, label: 'Live', title: 'Receiving live updates from keeperd' }
      : status === 'waiting'
        ? { lamp: 'waiting' as const, label: 'Reconnecting', title: 'Live updates dropped; reconnecting' }
        : status === 'blocked'
          ? { lamp: 'blocked' as const, label: 'Offline', title: 'keeperd is not responding' }
          : { lamp: 'idle' as const, label: 'Idle', title: 'No live stream open' }

  const tab =
    'relative flex items-center px-2.5 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground sm:px-4 ' +
    'after:absolute after:inset-x-2.5 sm:after:inset-x-4 after:bottom-2.5 after:h-0.5 after:scale-x-0 after:bg-foreground after:transition-transform after:duration-200 after:ease-settle ' +
    'aria-[current=page]:text-foreground aria-[current=page]:after:scale-x-100'

  return (
    <>
      <div className="sticky top-0 z-20 flex h-shell items-stretch gap-3 border-b border-border bg-background px-6 sm:gap-6 lg:px-8">
        <Link to="/connections" className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KeeperMark />
          keeper
        </Link>

        <div className="flex items-center">
          <ConnectionSwitcher />
        </div>

        <nav className="-ml-2 flex min-w-0 overflow-x-auto">
          {SECTIONS.map((item) => (
            <Link
              key={item.section}
              to={item.to}
              aria-current={section === item.section ? 'page' : undefined}
              className={tab}
            >
              {item.label}
            </Link>
          ))}
        </nav>

        <div className="ml-auto flex items-center gap-2 sm:gap-3">
          <span className={cn('flex items-center gap-2 text-xs text-muted-foreground')} title={daemon.title}>
            <Lamp state={daemon.lamp} label={daemon.label} />
            <span aria-hidden="true" className="max-sm:hidden">
              {daemon.label}
            </span>
          </span>
          <ThemeToggle />
          <Tooltip>
            <TooltipTrigger
              render={
                <Link
                  to="/settings"
                  aria-label="Settings"
                  aria-current={section === 'settings' ? 'page' : undefined}
                  className={cn(
                    buttonVariants({ variant: 'ghost', size: 'icon-sm' }),
                    'aria-[current=page]:bg-muted aria-[current=page]:text-foreground',
                  )}
                />
              }
            >
              <Settings />
            </TooltipTrigger>
            <TooltipContent side="bottom">Settings</TooltipContent>
          </Tooltip>
        </div>
      </div>
      {/* Every screen sits in the same container: the shell's own gutter
          (px-6/lg:px-8), a floor of vertical breathing room, and a width
          that takes the space a table wants without stretching to the edge
          of an ultra-wide monitor (UI.md — keeper is tables and facts, not
          a page of prose). */}
      <main className="mx-auto w-full max-w-page px-6 pt-8 pb-16 lg:px-8">{children}</main>
    </>
  )
}
