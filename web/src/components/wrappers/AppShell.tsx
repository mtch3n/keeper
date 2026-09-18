import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Settings } from 'lucide-react'
import { cn } from '@/lib/utils'
import { buttonVariants } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Lamp } from '@/components/wrappers/Lamp'
import { KeeperMark } from '@/components/wrappers/KeeperMark'
import { ThemeToggle } from '@/components/wrappers/ThemeToggle'
import { AppSidebar, type Scope } from '@/components/wrappers/AppSidebar'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { useLiveStatus } from '@/lib/live-status'

export type Section = 'connections' | 'approvals' | 'permissions' | 'activity' | 'catalog' | 'policy' | 'settings'

/** Which scope a screen reads. `Approvals`, `Permissions` and `Activity` are
 * read one agent at a time (SPEC R9.1, R9.3e, §10); `Catalog` and `Policy`
 * are per-connection (UI.md §2.3). `Connections` and `Settings` are about the
 * daemon rather than either scope, so neither group is dimmed for them. */
const SCOPE: Record<Section, Scope> = {
  connections: null,
  approvals: 'session',
  permissions: 'session',
  activity: 'session',
  catalog: 'connection',
  policy: 'connection',
  settings: null,
}

const SECTIONS: { section: Section; label: string; to: string }[] = [
  { section: 'connections', label: 'Connections', to: '/connections' },
  { section: 'approvals', label: 'Approvals', to: '/approvals' },
  { section: 'permissions', label: 'Permissions', to: '/permissions' },
  { section: 'activity', label: 'Activity', to: '/activity' },
  { section: 'catalog', label: 'Catalog', to: '/catalog' },
  { section: 'policy', label: 'Policy', to: '/policy' },
]

/**
 * The persistent chrome every screen sits inside: `AppSidebar` down the left
 * holding both scopes, and across the top the mark, the six sections, the
 * theme toggle and settings as the right-most control (CONTRACT.md §5,
 * UI.md §2.3). The scope controls left the bar because four of the six
 * sections ignored the one that used to sit there, and a control that is
 * always present and only sometimes effective is unreadable. The daemon's
 * `Lamp` joins the bar only when the stream is degraded, never when it is
 * healthy. The
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

  // The daemon reports itself only when it has something to report. A live
  // daemon is the expected state, and a permanent "Live" lamp spends the
  // chrome's attention budget on the one reading that never needs acting on
  // — the same reason `idle` stays silent, since it is what the first paint
  // shows before the stream has opened. Amber here means "reconnecting" and
  // red means "not responding": both are the connection asking for attention,
  // which is what the vocabulary is for (UI.md §2.1).
  const daemon =
    status === 'waiting'
      ? { lamp: 'waiting' as const, label: 'Reconnecting', title: 'Live updates dropped; reconnecting' }
      : status === 'blocked'
        ? { lamp: 'blocked' as const, label: 'Offline', title: 'keeperd is not responding' }
        : null

  const tab =
    'relative flex items-center px-2.5 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground sm:px-4 ' +
    'after:absolute after:inset-x-2.5 sm:after:inset-x-4 after:bottom-2.5 after:h-0.5 after:scale-x-0 after:bg-foreground after:transition-transform after:duration-200 after:ease-settle ' +
    'aria-[current=page]:text-foreground aria-[current=page]:after:scale-x-100'

  return (
    <SidebarProvider>
      <AppSidebar scope={section ? SCOPE[section] : null} />
      <SidebarInset>
      <div className="sticky top-0 z-20 flex h-shell items-stretch gap-3 bg-background px-6 sm:gap-6 lg:px-8">
        <div className="flex items-center">
          <SidebarTrigger />
        </div>
        <Link to="/connections" className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <KeeperMark />
          keeper
        </Link>

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
          {daemon && (
            <span className="flex items-center gap-2 text-xs text-muted-foreground" title={daemon.title}>
              <Lamp state={daemon.lamp} label={daemon.label} />
              <span aria-hidden="true" className="max-sm:hidden">
                {daemon.label}
              </span>
            </span>
          )}
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
      </SidebarInset>
    </SidebarProvider>
  )
}
