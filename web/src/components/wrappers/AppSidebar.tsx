import { useCallback, useEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, Database, Folder } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubItem,
  SidebarSeparator,
} from '@/components/ui/sidebar'
import { Brand } from '@/components/wrappers/Brand'
import { Lamp } from '@/components/wrappers/Lamp'
import { AcceptedFindingsMark } from '@/components/wrappers/AcceptedFindingsMark'
import { getDoctor, listConnections } from '@/lib/api'
import { useLiveStatus } from '@/lib/live-status'
import { useSessionScope } from '@/lib/session-scope'
import { useConnectionScope } from '@/lib/connection-scope'
import type { ConnectionSummary, Session } from '@/lib/types'

/** Which of the two scopes the screen in view actually obeys. The other one
 * is still reachable, but it is dimmed: a control that is always present and
 * only sometimes takes effect is the thing that made the old chrome hard to
 * read, and saying so in the interface costs one class. */
export type Scope = 'session' | 'connection' | null

const UNKNOWN_WORKSPACE = 'Unknown workspace'

/** Sessions grouped by the directory their client is working in. The
 * workspace is the folder here because keeper already treats it as the
 * human-recognisable unit — `ClientInfo.Workspace` is documented as what a
 * person recognises when two agents query the same database — and because
 * the alternative unit, the session, is too short-lived to navigate by: one
 * socket connection is one session and a reconnect mints a new one (SPEC
 * R3.4e). Folders therefore persist for as long as this page is open even
 * after their last session drops, so an agent reconnecting does not make the
 * row you were reading disappear. */
function groupByWorkspace(sessions: Session[], remembered: string[]) {
  const folders = new Map<string, Session[]>()
  for (const workspace of remembered) folders.set(workspace, [])
  for (const session of sessions) {
    const workspace = session.client.workspace ?? UNKNOWN_WORKSPACE
    folders.set(workspace, [...(folders.get(workspace) ?? []), session])
  }
  return [...folders.entries()].sort(([a], [b]) => a.localeCompare(b))
}

/** The last path segment, which is what a person calls the project. The full
 * path stays as the title, because two checkouts of the same repository are
 * told apart only by what is above them. */
function folderName(workspace: string) {
  if (workspace === UNKNOWN_WORKSPACE) return workspace
  const parts = workspace.split('/').filter(Boolean)
  return parts[parts.length - 1] ?? workspace
}

/**
 * The one switching surface: which agent, and which database.
 *
 * Both scopes live here rather than in the top bar because they are the same
 * kind of act and belong in the same place, and because a scope control in
 * the chrome that four of six screens ignore reads as broken. Agents come
 * first: keeper's subject is what an agent is doing, and `Approvals`,
 * `Permissions` and `Activity` are all read one agent at a time.
 *
 * Registry search: `pnpm dlx shadcn@latest search @shadcn -q "sidebar"`
 * returned `@shadcn/sidebar` (used here, with `sidebar-01`'s grouped-section
 * shape) and `-q "tree"` returned only `@shadcn/sidebar-11`, a file tree with
 * no notion of either scope. This wrapper owns the binding the primitives do
 * not have: sessions read from `GET /v1/doctor` and refreshed off the
 * `session` event, grouped into workspace folders that outlive a reconnect,
 * and the two scope contexts they write to.
 */
export function AppSidebar({ scope }: { scope: Scope }) {
  const [sessions, setSessions] = useState<Session[]>([])
  const [connections, setConnections] = useState<ConnectionSummary[]>([])
  const seenWorkspaces = useRef<string[]>([])
  const { sessionId, setSessionId } = useSessionScope()
  const { connectionId, setConnectionId } = useConnectionScope()
  const { lastEvent } = useLiveStatus()

  const refresh = useCallback(() => {
    getDoctor()
      .then((report) => {
        const live = report.sessions ?? []
        for (const session of live) {
          const workspace = session.client.workspace ?? UNKNOWN_WORKSPACE
          if (!seenWorkspaces.current.includes(workspace)) seenWorkspaces.current.push(workspace)
        }
        setSessions(live)
      })
      .catch(() => {
        /* AppShell's own Lamp reports the daemon connection; a sidebar that
           emptied itself on one failed poll would report it a second time
           and in a worse place. */
      })
    listConnections()
      .then(setConnections)
      .catch(() => {})
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  // Sessions and connections both announce themselves on the one event
  // stream `LiveStatusProvider` already holds open, so the sidebar follows
  // an agent connecting without a poll of its own.
  useEffect(() => {
    if (lastEvent?.kind === 'session' || lastEvent?.kind === 'connection') refresh()
  }, [lastEvent, refresh])

  const folders = groupByWorkspace(sessions, seenWorkspaces.current)
  const dim = (owns: Scope) => (scope !== null && scope !== owns ? 'opacity-60' : undefined)

  return (
    <Sidebar>
      {/* The rail starts on the shell bar's own baseline, and it does so with a
          header of the bar's own height rather than the `pt-shell` this
          replaces. That padding sat inside `SidebarContent`, the scrolling
          element, where it belonged to the scrolled content: it slid away on
          the first wheel gesture and took the top of the rail under the bar
          with it. A header is outside the scroll box and stays put.

          It is worth being exact about what this did and did not fix, because
          the obvious reading is wrong. Padding inside a scroll container adds
          to `scrollHeight` and is subtracted from nothing; a header takes the
          same 54px off the box's `clientHeight` instead. Both arrive at the
          same threshold — the rail begins to overflow at the same content
          height either way. What changed is the behaviour once it does, and
          the fix for the overflow itself is on `SidebarContent` below.

          The header carries the brand, which used to be in the bar. The two
          columns now begin on one baseline with one kind of thing each — a
          name on the left, the sections on the right — instead of the rail
          opening with a group label floating opposite the bar's text. No
          bottom rule, matching the bar, which carries none either. */}
      <SidebarHeader className="h-shell justify-center px-4 py-0">
        <Brand />
      </SidebarHeader>
      {/* The rail itself never scrolls. Moving the offset out of this box was
          necessary but not sufficient: padding inside a scroll container adds
          to its scroll height and comes off its client height in equal
          measure, so the old `pt-shell` changed what scrolling *did* — it
          dragged the top of the rail up under the bar — without changing when
          it happened. Both shapes start scrolling at the same content height.

          So the scroller moves down a level. `overflow-hidden` here, and each
          group's content scrolls on its own; flexbox's default shrink plus
          `min-h-0` on the groups means nothing shrinks or scrolls while the
          two lists fit, and when they do not, only the list that is too long
          scrolls. That is better than a rail-wide scrollbar and not merely
          smaller: `Agents`, `Databases` and the rule between them are the
          structure of this surface — two questions, not one list — and a rail
          that scrolls them off the top hides the second scope behind a gesture
          you have to discover. Here the headings are always on screen and only
          the rows move. */}
      <SidebarContent className="overflow-hidden">
        {/* `min-h-0` is what lets flexbox shrink this below its content; without
            it a flex child refuses to go under its intrinsic height and the
            overflow escapes to the rail again. */}
        <SidebarGroup className={cn('min-h-0', dim('session'))}>
          <SidebarGroupLabel className="text-label text-muted-foreground">Agents</SidebarGroupLabel>
          <SidebarGroupContent className="min-h-0 overflow-y-auto">
            {folders.length === 0 ? (
              <p className="px-2 text-xs text-muted-foreground">No agent has connected yet.</p>
            ) : (
              <SidebarMenu>
                {folders.map(([workspace, inFolder]) => (
                  <WorkspaceFolder
                    key={workspace}
                    workspace={workspace}
                    sessions={inFolder}
                    selected={sessionId}
                    onSelect={setSessionId}
                  />
                ))}
              </SidebarMenu>
            )}
          </SidebarGroupContent>
        </SidebarGroup>

        {/* Agents and databases are two different questions, not one list
            with two headings. The rule says so without spending colour on
            it. */}
        <SidebarSeparator className="shrink-0" />

        <SidebarGroup className={cn('min-h-0', dim('connection'))}>
          <SidebarGroupLabel className="text-label text-muted-foreground">Databases</SidebarGroupLabel>
          <SidebarGroupContent className="min-h-0 overflow-y-auto">
            {connections.length === 0 ? (
              <p className="px-2 text-xs text-muted-foreground">No connections registered.</p>
            ) : (
              <SidebarMenu>
                {connections.map((connection) => (
                  <SidebarMenuItem key={connection.id}>
                    <SidebarMenuButton
                      isActive={connection.id === connectionId}
                      title={`${connection.role} on ${connection.database}`}
                      onClick={() => setConnectionId(connection.id)}
                    >
                      <Database />
                      <span className="truncate">{connection.name}</span>
                      {connection.degraded && <AcceptedFindingsMark className="ml-auto text-waiting" />}
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            )}
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
    </Sidebar>
  )
}

/** One workspace and the sessions currently in it. The open state is held
 * here rather than driven off a data attribute because the audit rejects
 * arbitrary Tailwind values, and a chevron that is chosen rather than
 * rotated needs no arbitrary selector and no transition. */
function WorkspaceFolder({
  workspace,
  sessions,
  selected,
  onSelect,
}: {
  workspace: string
  sessions: Session[]
  selected: string | null
  onSelect: (id: string | null) => void
}) {
  const [open, setOpen] = useState(true)

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <SidebarMenuItem>
        <CollapsibleTrigger render={<SidebarMenuButton title={workspace} />}>
          <Folder />
          <span className="truncate">{folderName(workspace)}</span>
          {open ? (
            <ChevronDown data-icon="inline-end" className="ml-auto" />
          ) : (
            <ChevronRight data-icon="inline-end" className="ml-auto" />
          )}
        </CollapsibleTrigger>
        <CollapsibleContent>
          <SidebarMenuSub>
            {sessions.length === 0 ? (
              <p className="px-2 py-1 text-xs text-muted-foreground">No live session</p>
            ) : (
              sessions.map((session) => (
                <SidebarMenuSubItem key={session.id}>
                  {/* `SidebarMenuButton` rather than `SidebarMenuSubButton`,
                      which the registry renders as an anchor: choosing a
                      session is a selection, not a navigation, and announced
                      as a link it invites the wrong expectation. The indent
                      comes from `SidebarMenuSub` either way. */}
                  <SidebarMenuButton
                    size="sm"
                    isActive={session.id === selected}
                    title={session.intent ?? 'No intent set'}
                    onClick={() => onSelect(session.id === selected ? null : session.id)}
                  >
                    <Lamp state="live" label="Connected" />
                    <span className="truncate">{session.client.name}</span>
                  </SidebarMenuButton>
                </SidebarMenuSubItem>
              ))
            )}
          </SidebarMenuSub>
        </CollapsibleContent>
      </SidebarMenuItem>
    </Collapsible>
  )
}
