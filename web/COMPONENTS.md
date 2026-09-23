# Component Registry

This file documents every UI component in keeper's frontend and carries the proof
UI.md §1 requires for each custom one. `pnpm ui-audit` fails a build with an
undocumented component.

## Policy

Inherited from trellis (CONTRACT.md §5), keeper's own semantics layered on top:

1. **shadcn/ui components are the default.** Search the registry with `pnpm dlx
   shadcn@latest search @shadcn -q "<term>"` before writing a custom component.
2. **This project is the shadcn Base UI variant**, not Radix (`components.json` →
   `"style": "base-nova"`, `"base": "base"`). Custom triggers use the `render`
   prop; `asChild` is a Radix API and does not exist here.
3. **Custom components require proof, recorded here before the component is
   written:** the registry search actually run and its result, why composition of
   existing primitives cannot produce the behaviour, and what the custom
   component owns that no primitive does.
4. **Third-party libraries are wrapped, not scattered.** `components/wrappers/`
   is the only place a non-registry import may appear in application code.
5. **Composition over custom markup.** Restyle through Tailwind theme tokens
   rather than reimplementing a primitive's markup.
6. **Two screens showing the same thing must show it with the same component** —
   in particular, a redacted cell looks identical in `Activity`, query results
   and the `Catalog` preview (UI.md §3.3). Enforced by `pnpm ui-audit`, not by
   instruction.

## Installed shadcn Components

`"style": "base-nova"`, `"base": "base"`, `"iconLibrary": "lucide"`, `baseColor: neutral`.

| Component | Location | Purpose |
|-----------|----------|---------|
| Alert | `@/components/ui/alert` | Persistent inline failure state that must stay on the page |
| AlertDialog | `@/components/ui/alert-dialog` | Confirming a destructive action needing no typed input, e.g. revoking a grant |
| Badge | `@/components/ui/badge` | Status chips that are not one of the four `Lamp` states |
| Breadcrumb | `@/components/ui/breadcrumb` | Where a full page sits, e.g. Catalog → a table's columns |
| Button | `@/components/ui/button` | Primary interactive element |
| Card | `@/components/ui/card` | A surface and its padding (`Card`, `CardContent`) — never for row data (UI.md §3.3). The registry's `CardHeader`, `CardTitle`, `CardDescription`, `CardAction` and `CardFooter` were deleted: no screen used one, `CardTitle`'s `text-base` is off the type scale, and `CardFooter` drew `border-t bg-muted/50` — a tinted panel inside a panel, the exact figure the palette was rewritten to remove. A heading is `text-heading`, a footer is a flex row of buttons; neither needed a slot, and the slots made the card's own box carry `has-data-[slot=card-footer]:pb-0` to undo padding on their behalf |
| Checkbox | `@/components/ui/checkbox` | A single independent boolean in a form. Never a collapsed "I understand" confirmation standing in for several separate facts |
| Collapsible | `@/components/ui/collapsible` | An open/closed panel, e.g. one approval's expanded facts |
| Combobox | `@/components/ui/combobox` | Choosing one item from many by typing |
| Dialog | `@/components/ui/dialog` | Modal flows; owns focus trap, Escape, scroll lock and `aria-modal` |
| DropdownMenu | `@/components/ui/dropdown-menu` | The connection switcher; a row's secondary actions |
| Empty | `@/components/ui/empty` | Empty states and every placeholder page in this foundation. Never boxed: a title, a line of description, and an action when one exists |
| Field | `@/components/ui/field` | Form layout (FieldGroup, Field, FieldLabel) |
| Input | `@/components/ui/input` | Single-line form control |
| InputGroup | `@/components/ui/input-group` | An input with a control inside its border, e.g. the Activity filter |
| Kbd | `@/components/ui/kbd` | A keyboard shortcut shown beside the action it triggers |
| Label | `@/components/ui/label` | Control label primitive used by `Field` |
| Popover | `@/components/ui/popover` | Contextual detail that stays open while the pointer moves into it |
| RadioGroup | `@/components/ui/radio-group` | A choice among a fixed, small set where each option needs a sentence of explanation beside it: the connection mode. A `Select` would hide the consequences behind a click, and the consequences are the point. Square, like everything else: the registry's `rounded-full` indicator is replaced |
| ScrollArea | `@/components/ui/scroll-area` | A fixed-height region that scrolls on its own, e.g. the Activity log. Thumb is square (registry's `rounded-full` replaced with `rounded-sm`, which resolves to 0 through the radius tokens) |
| Select | `@/components/ui/select` | A pick from a fixed set |
| Separator | `@/components/ui/separator` | Dividers, replacing `border-t` spacer divs |
| Sheet | `@/components/ui/sheet` | The off-canvas panel `Sidebar` becomes below the mobile breakpoint. Not used directly by application code |
| Sidebar | `@/components/ui/sidebar` | The left rail holding both scopes, through `AppSidebar`. Added with `--overwrite`, after which the five registry files it would have reset (`button`, `input`, `separator`, `skeleton`, `tooltip`) were restored from git: they carry this project's square-radius and type-scale edits and the registry copies do not. Its `cn` import was repointed at `@/lib/utils` per the note below. `SidebarContent` also lost the registry's `no-scrollbar`: the rail is navigation, and one that cannot fit its own workspaces has to say so. The utility itself is defined in `index.css` for the regions that do want it — the registry referenced it in two files and defined it nowhere, so every one of them drew the document's 11px thumb |
| Skeleton | `@/components/ui/skeleton` | Loading placeholders |
| Spinner | `@/components/ui/spinner` | Inline pending indicator |
| Switch | `@/components/ui/switch` | A true-or-false setting. Square, like everything else: registry's `rounded-full` replaced with `rounded-sm` on both the root and the thumb |
| Table | `@/components/ui/table` | Rows of like things with columns — `Catalog`, `Permissions`, `Activity`, query results |
| Textarea | `@/components/ui/textarea` | Multiline text control, e.g. a DSN or SQL preview |
| Toast | `@/components/ui/toast` | Transient action failures. Base UI ships `toast`; `sonner` is the Radix/React-Aria equivalent and is not used here |
| Tooltip | `@/components/ui/tooltip` | The label of every icon-only button, through `IconButton`. `TooltipProvider` is mounted once in `main.tsx` |

## Wrapper Components

`web/src/components/wrappers/` — composed from the registry, listed with what each owns.

| Component | Composes | Purpose |
|-----------|----------|---------|
| AppShell | `AppSidebar`, `Brand`, `Lamp`, `ThemeToggle`, `Tooltip`, `Button` | The persistent chrome: the connection scope control, the six sections (Connections, Approvals, Permissions, Activity, Catalog, Policy), the theme toggle, and settings as the right-most control. The brand moved to `AppSidebar`'s header, so the two columns open on one baseline with one kind of thing each; the bar keeps it only below `md`, where the rail is a `Sheet` and its header is off-canvas. The daemon's `Lamp` appears only while the stream is degraded (`waiting`, `blocked`); a healthy daemon and an unopened stream both report nothing, because a permanent "Live" lamp is the one reading that never needs acting on. The bar carries no bottom rule. The current section is a foreground rule that grows from centre, never the amber accent (UI.md §1's governing rule) |
| ConnectionSwitcher | `DropdownMenu`, `Button` | The connection scope control. Lists every registered connection via `listConnections()` (UI.md §2.5) |
| ConnectionScopeProvider | React context (`@/lib/connection-scope`) | Remembers the working connection per browser and exposes `useConnectionScope()`, so `Catalog` and `Policy` (both per-connection, UI.md §2.3) read one source of truth without prop-drilling through the router |
| LiveStatusProvider | React context (`@/lib/live-status`), `subscribeToEvents` | Opens the one `GET /v1/events` subscription for the whole app and carries its state down through context, so `AppShell`'s daemon `Lamp` is reported once rather than every screen opening its own `EventSource` |
| ThemeToggle | `IconButton` | Flips the `dark` class on `<html>` and persists the choice. `index.html` sets the class before first paint, so there is no flash. Light and dark are one theme, not a second design (UI.md §5) |
| IconButton | `Button`, `Tooltip` | An icon-only button whose label is both its accessible name and its tooltip |
| AppSidebar | `Sidebar`, `Collapsible`, `Separator`, `Lamp` | The one switching surface: `Agents`, grouped into workspace folders, and `Databases`. Both scopes live here because they are the same act; the group the current screen does not obey is dimmed, since a control that is always present and only sometimes effective is what made the old top-bar switcher hard to read. Sessions come from `GET /v1/doctor` and refresh off the `session` event. It replaces `ConnectionSwitcher`, now deleted. A `SidebarHeader` of `h-shell` carrying `Brand` begins the rail on the top bar's own baseline — unaligned, the two columns read as two unrelated pages. The rail itself never scrolls: `SidebarContent` is `overflow-hidden` and each group scrolls its own content, so the two group headings and the rule between them stay on screen and only an over-long list moves. It is not the `pt-shell` it replaces, which sat inside the scroll box and slid away on the first wheel gesture. A `SidebarSeparator` divides the two scopes, which are two questions rather than one list with two headings |
| Brand | `KeeperMark`, `Link` | The mark and the name, linking home. A component rather than markup because it appears in two layouts that are never both on screen — `AppSidebar`'s header at `md` and above, `AppShell`'s bar below it, where the rail has become a `Sheet` and taken its header along. One thing in two places is one component (UI.md §3.3); written twice it is two, and the copy is where they drift |
| WorkspaceFolder | `Collapsible`, `SidebarMenuSub`, `Lamp` | One workspace and the sessions in it. A folder persists for as long as the page is open even after its last session drops, because one socket connection is one session and a reconnect mints a new one (SPEC R3.4e) — rows that vanish on reconnect would be worse than no grouping at all |
| SessionScopeProvider | React context (`@/lib/session-scope`) | Holds the selected agent session for `Approvals`, `Permissions` and `Activity`. In memory only, unlike the connection scope: a persisted session id names something that stopped existing the moment its agent reconnected |
| KeeperMark | none (the brand path) | keeper's mark, drawn in `currentColor` so it stays achromatic like every other glyph in the chrome |
| Lamp | none (registry search below) | The four-state vocabulary: idle, waiting, blocked, live |
| Cell | `ScannedText` | The seven redacted-cell treatments of UI.md §2.2, plus the plain value case |
| ScannedText | none (registry search below) | Renders a `scan` result's inline solid runs inside otherwise-readable text |

## Custom component proof

Policy 3 requires the registry search and the reason, recorded before the component exists.

**Lamp.** `pnpm dlx shadcn@latest search @shadcn -q "indicator status dot"` returned
*No items found*; `-q "badge"` returned only `@shadcn/badge`, a text chip with padding
and a label. Composition cannot produce the behaviour: a lamp carries no text, is a
fixed 8px, and switches between four states where two carry their own keyframes.
Lamp owns the state vocabulary itself — the mapping from idle/waiting/blocked/live
onto one visual language — which no primitive holds. Its `live` state is round
(`rounded-full`), the one documented exception to `--radius: 0`: a pilot lamp is a
connection light, not a work state, exactly as trellis's `Lamp` carries the same single
exception. `pnpm ui-audit` excludes this one occurrence by filename.

**Cell.** `pnpm dlx shadcn@latest search @shadcn -q "redact mask"` and `-q "table
cell"` both returned *No items found*. No primitive has a notion of seven
mutually-distinguishable masking states over one value — `null`, `tokenized`,
`partial`, `redacted`, `scanned`, `dropped`, `unclassified` — each of which must read
as neither a null nor an error and none of which may look like another (UI.md §2.2).
Cell owns exactly that vocabulary, the way `Lamp` owns the four-state one for
connection/approval status.

**ScannedText.** `pnpm dlx shadcn@latest search @shadcn -q "highlight text"` and
`-q "mark"` both returned *No items found*. A `scan` policy redacts a matched span
inside otherwise-readable text (SPEC R8.5a) — this is cell-*internal*, not a
cell-replacement treatment, and no primitive parses a string for embedded solid
runs. ScannedText owns exactly that parse (`splitRedactedRuns`) and the run's visual
treatment, shared with `Cell`'s `redacted` and `partial` cases so a redacted span
looks pixel-identical everywhere it appears (UI.md §3.3).

**ConnectionSwitcher.** `pnpm dlx shadcn@latest search @shadcn -q "combobox"` returns
`@shadcn/combobox`, a generic picker with no notion of a degraded-connection marker or
of scoping the rest of the app. Composition of `DropdownMenu` alone cannot fetch and
render the connection list with keeper's own marker semantics; ConnectionSwitcher owns
that binding (`listConnections()` plus `useConnectionScope()`), the way trellis's
`ProjectSwitcher` owns the equivalent scope control.

## Application Shell

| Component | Purpose |
|-----------|---------|
| App | Router root. Mounts `Toaster` once and owns the catch-all not-found route |

## Page Components

Placeholder pages, each rendering its title and an `Empty` per this foundation's
scope. The screens agent replaces the body of each; the route and the section it
lights in `AppShell` are already wired.

| Component | Route | Owns (SPEC) |
|-----------|-------|-------|
| ConnectionsPage | `/connections` | Registering a database (SPEC §4.1) |
| AuditPage | `/audit` | The G0 privilege audit for every connection, with the statement that would narrow each finding (SPEC §4.1) |
| ApprovalsPage | `/approvals` | The shared queue — every agent's escalations, attributed and approved here (SPEC §9.1, §2.5) |
| PermissionsPage | `/permissions` | Per-path grants: what a session or standing rule may read (SPEC R9.3e) |
| ActivityPage | `/activity` | The query log — what ran, what was masked, what left the machine (SPEC §10) |
| CatalogPage | `/catalog` | Per-column classification: policy, namespace, partial form, `hide_name`, the unclassified backlog (SPEC §5) |
| PolicyPage | `/policy` | Per-connection limits: mode, row ceiling, statement timeout, scan sample, denylist, write scope (SPEC §4.5) |
| SettingsPage | `/settings` | `GET /v1/doctor`: daemon state, key source, connection health, catalog freshness. Not one of UI.md §2.3's six screens; added because `AppShell`'s settings control needs a destination and `/v1/doctor` needs a home — reconsider its placement with the product owner |
| LocalRequestPage | `/r/:requestId` | The one-use local decision page for an input or authorization request (SPEC R8.7g, UI.md §6). Deliberately rendered outside `AppShell` |

## Type scale

One scale, defined in `index.css`, identical in mechanics to trellis's (CONTRACT.md §5):

| Role | Size | Weight | Used for |
|---|---|---|---|
| `text-title` | 24px | 600 | The one h1 on a screen |
| `text-heading` | 16px | 600 | An h2 inside a page |
| `text-body` | 15px | 400 | Reading text |
| `text-sm` | 14px | 400 | The interface: rows, controls, table cells |
| `text-xs` | 12px | 400 | Secondary words: captions, counts |
| `text-label` | 12px | 500 | Names of structure: a column, a group head |
| `text-meta` | 12px mono | 400 | Identifiers: refs, tokens, ids, paths — and every redacted/tokenized/partial `Cell` treatment |

Sentence case throughout; nothing is tracked out; `pnpm ui-audit` rejects any other
size, tracking or uppercase utility, and `font-mono` set by hand instead of `text-meta`.

## Colour

Four states carry meaning and nothing else does (UI.md §2.1), and since the
palette rewrite that is true by construction rather than by discipline: **every
grey in both themes is chroma 0**. A hue in the greys makes §2.1's claim false —
a blue-tinted panel is colour spent on chrome, and chrome is not what the budget
is for. Dark is the designed theme (`index.html` ships `class="dark"`); light is
the same lightness ladder inverted, which is what UI.md §5 means by one theme in
two modes rather than a second design.

The surfaces are also far enough apart to be read as decisions. The palette this
replaced put the rail at `0.185` and its own selected row at `0.225` against a
`0.155` canvas — three surfaces inside four hundredths of a lightness, so a
selected connection was indistinguishable from an unselected one. The rail is now
flush with the page (`--sidebar: var(--background)`), and the one filled thing in
it is the row you have selected.


| Token | Means |
|---|---|
| `idle` (achromatic outline) | Nothing happening |
| `waiting` (amber fill) | Needs you — the only state that asks for attention |
| `blocked` (red fill) | Refused |
| `live` (green fill, round) | Connected |
| Facts | none (layout) | SPEC §9.2's facts block: what a statement would do, above the statement itself. One component because an approval, an activity record and the authorization page all show it and UI.md §3.3 forbids three renderings of one thing |
| Fact | Facts | One label/value row. A fixed label column rather than a grid, so a long value wraps under itself instead of widening the column for every other row |
| ApprovalsPage | `Table`, `Lamp`, `Facts`, `Button`, `Empty` | The shared queue across every agent session (SPEC R9.1). Oldest first, never reordered; each row names its client, workspace and stated intent; deciding one item does not advance to the next. Selecting an agent in `AppSidebar` narrows it, and a narrowed queue says so above the table and offers the way back: a filtered queue that read as the whole queue would be this screen telling you nothing else needs you |
| ApprovalDetail | `Facts`, `Separator`, `Button` | One queued item: §9.2's facts above its SQL, and a write's "revocation restores nothing" line where it applies and nowhere else |
| ActivityPage | `Table`, `Input`, `Button`, `Empty`, `Facts` | The query log (SPEC §10). Filters by session, connection and tier, because a human arriving after a long agent run wants one session's trail |
| RecordDetail | `Facts`, `Table`, `Separator`, `Button` | One audit record in full: policies applied with their basis, degradations, and the statement as kept. Never a "reveal literals" affordance — there are none stored (R10a) |
| Filter | `Input` | One labelled filter field on the activity log |
| SettingsPage | `Table`, `Lamp`, `Facts`, `Input`, `Button`, `Separator` | Per-daemon state from `GET /v1/doctor`: vault key source, detector identity and network posture, judge reachability, connections and sessions. Reports rather than configures — it is what you open when something is wrong |
| ConnectionsPage | `Table`, `Lamp`, `Facts`, `Input`, `Button` | Register a database (SPEC R4.1). keeper never refuses a credential and no longer holds one shut either; what the role can do beyond reading is `Audit`'s subject, and this screen links there rather than restating it |
| ConnectionDetailPanel | `Facts`, `Separator`, `Button` | One connection: what it is, when it was last audited, and a line pointing at `Audit` when that audit found something |
| AuditPage | `Table`, `Facts`, `Separator`, `Button`, `Empty` | The privilege audit on its own (SPEC R4.1). It left registration because a finding shown mid-setup reads as paperwork to clear rather than as a report; here each one carries the statement that would narrow it, in full rather than behind a disclosure, since a statement you have to click to see is one you will not paste. keeper never runs them — it holds the credential the report is about |
| ConnectionAudit | `Facts`, `Separator`, `Button` | One connection's findings, with `Re-audit`. Re-auditing replaces the report and changes nothing about whether the connection runs |
| FindingRow | none (layout) | One finding: its id and kind, its one-sentence meaning, and the suggested fix — or the sentence explaining why no single statement removes it |
| RegisterDialog | `Dialog`, `Field`, `Input`, `Switch`, `Collapsible`, `Separator`, `Button` | Registering a database, as a modal. Host, port, user, password and database are separate fields rather than one connection string, because a pasted DSN is where a typo is invisible until it returns as an unattributable permission error; the string is assembled in the wrapper and never rendered back. A paste mode stays for people who already hold a DSN. No engine control (registration records `postgres` and nothing else), no SSH tunnel (keeper has none) and no read-only switch (mode is `Policy`'s `RadioGroup`, with a sentence of consequence under each option). No "Test connection": storing the connection is the test, and what the audit found is read on `Audit` |
| PermissionsPage | `Table`, `Button`, `Empty` | The permission list (SPEC §9.3). One row per path, session grants first, and `last used` / `uses` as the columns that decide whether an entry should still exist. No tree, no wildcard, no select-all: R9.3c grants exactly the path named |
| PolicyPage | `Button`, `Separator` | Per-connection limits and mode. Answers *what may this connection do*, where `Settings` answers *what is this daemon doing* |
| ModeSelector | radio inputs | Mode as text with a sentence of consequence under each option. Never a pill or a slider: the modes differ in what they let a local model release, and a scale would be lying about that |
| LimitsForm | `Input`, `Button` | Row ceiling, statement timeout and scan sample. The ceiling is a privacy control, not a performance one |
| DenylistEditor | `Table`, `Input`, `Button` | Relations this connection may never read. Evaluated against the plan, so a denied base table is caught through a view; no approval overrides it |
| WriteScope | `Table`, `Facts` | The write credential's recorded relations, read-only — it is what the credential holds, discovered by the audit, not a setting |
| CatalogPage | `Table`, `Input`, `Button`, `Empty` | Per-column classification (SPEC §5). A table rather than cards, because it shows hundreds of columns and is for bulk work |
| CatalogRow | `Input`, native select | One column's policy and its arguments. A token's namespace and a partial's form appear only where they apply, because their absence is a validation error rather than a default |
| Proposal | `Table`, `Button` | `catalog init`'s output in its two groups: what may be accepted without reading, and the review task where unnamed name and address columns live |
| LocalRequestPage | `Facts`, `Input`, `Button`, `Separator`, `KeeperMark` | The local page (R8.7g), outside the app shell. Somebody arrives from a link a blocked agent printed to make one decision; navigation would invite them to do something else first |
| InputRequest | `Input`, `Button` | A value the agent must never see. Never echoed back, on success or in an error |
| AuthorizationRequest | `Facts`, `Button` | Granting one path. "Allow for this session" first and as the default, because that and "allow until revoked" are different requests. Says plainly that nothing returns to the agent |
| Page | none (layout) | The local page's frame: one column, no navigation |
| RemoveConnection | `Input`, `Button` | Deleting a connection, with its name retyped rather than a click confirmed. Its credential and every allow rule naming it go with it; a grant naming a connection that no longer exists is a rule nobody can read or revoke. The catalog file stays — it lives in the project repo and is reviewed like code |

`unclassified`'s hatched amber edge (UI.md §2.2) is the one further exception: the
visible exit from fail-closed, carrying the same "needs you" meaning as `waiting`.
No blue, no purple, no gradients, no info/success/warning colours anywhere else.

## Shape

`--radius` is `0`. Every radius token resolves to it, so the whole interface is
square. The one exception is `Lamp`'s `live` state, round because it is a pilot lamp
rather than a work state — identical to trellis's own single exception.

## Notes

- All imports come from `@/components/ui` (registry), `@/components/wrappers`
  (composed), or `@/lib` (no-render logic and the API client). Raw HTML primitives
  are forbidden where a registry component exists.
- `space-x-*` / `space-y-*` are banned; use `flex` with `gap-*`.
- `cn` is always imported from `@/lib/utils`, never the bare `cn` package — the
  bare package does not know this project's type-scale font-size roles and drops
  them when merging classes.
- No component sets `z-index` by hand; overlay components (`Dialog`, `Popover`,
  `DropdownMenu`) manage their own stacking.
