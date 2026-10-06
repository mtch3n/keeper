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
| Button | `@/components/ui/button` | Primary interactive element |
| Badge | `@/components/ui/badge` | A count on a navigation entry: what waits in Inbox |
| Checkbox | `@/components/ui/checkbox` | A single independent boolean in a form. Never a collapsed "I understand" confirmation standing in for several separate facts |
| Collapsible | `@/components/ui/collapsible` | An open/closed panel, e.g. one approval's expanded facts |
| Dialog | `@/components/ui/dialog` | Modal flows; owns focus trap, Escape, scroll lock and `aria-modal` |
| Empty | `@/components/ui/empty` | Empty states and every placeholder page in this foundation. Never boxed: a title, a line of description, and an action when one exists |
| Field | `@/components/ui/field` | Form layout (FieldGroup, Field, FieldLabel) |
| Input | `@/components/ui/input` | Single-line form control |
| Label | `@/components/ui/label` | Control label primitive used by `Field` |
| RadioGroup | `@/components/ui/radio-group` | A choice among a fixed, small set where each option needs a sentence of explanation beside it: the connection mode. A `Select` would hide the consequences behind a click, and the consequences are the point. Square, like everything else: the registry's `rounded-full` indicator is replaced |
| ScrollArea | `@/components/ui/scroll-area` | A fixed-height region that scrolls on its own, e.g. the Activity log. Thumb is square (registry's `rounded-full` replaced with `rounded-sm`, which resolves to 0 through the radius tokens) |
| Select | `@/components/ui/select` | A pick from a fixed set |
| Separator | `@/components/ui/separator` | Dividers, replacing `border-t` spacer divs |
| Sheet | `@/components/ui/sheet` | The off-canvas panel `Sidebar` becomes below the mobile breakpoint. Not used directly by application code |
| Skeleton | `@/components/ui/skeleton` | Loading placeholders |
| Switch | `@/components/ui/switch` | A true-or-false setting. Square, like everything else: registry's `rounded-full` replaced with `rounded-sm` on both the root and the thumb |
| Table | `@/components/ui/table` | Rows of like things with columns — `Catalog`, `Permissions`, `Activity`, query results |
| Textarea | `@/components/ui/textarea` | Multiline text control, e.g. a DSN or SQL preview |
| Toast | `@/components/ui/toast` | Transient action failures. Base UI ships `toast`; `sonner` is the Radix/React-Aria equivalent and is not used here |
| Tooltip | `@/components/ui/tooltip` | The label of every icon-only button, through `IconButton`. `TooltipProvider` is mounted once in `main.tsx` |

## Wrapper Components

`web/src/components/wrappers/` — composed from the registry, listed with what each owns.

| Component | Composes | Purpose |
|-----------|----------|---------|
| AppShell | `Brand`, `Lamp`, `Badge`, `AgentsIndicator`, `ThemeToggle` | The persistent chrome: one bar with four places — Connections, Inbox, Activity, Settings — the agents indicator and the theme toggle. There is no sidebar: its agent list became the indicator, and its database list was a second route to a connection page that disagreed with the first. Inbox carries a `Badge` with the count of what waits, and the bar raises a notification when something new arrives while the page is out of view. The daemon's `Lamp` appears only while the stream is degraded. The page content is the one `main` landmark |
| LiveStatusProvider | React context (`@/lib/live-status`), `subscribeToEvents` | Opens the one `GET /v1/events` subscription for the whole app and carries its state down through context, so `AppShell`'s daemon `Lamp` is reported once rather than every screen opening its own `EventSource` |
| ThemeToggle | `IconButton` | Flips the `dark` class on `<html>` and persists the choice. `index.html` sets the class before first paint, so there is no flash. Light and dark are one theme, not a second design (UI.md §5) |
| IconButton | `Button`, `Tooltip` | An icon-only button whose label is both its accessible name and its tooltip |
| Brand | `KeeperMark`, `Link` | The mark and the name, linking home, at the start of the top bar |
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
| ConnectionsPage | `/connections` | Hosts and the databases registered on them (SPEC §4.1) |
| InboxPage | `/inbox` | Everything waiting on a human: approvals, input and authorization requests (SPEC §9.1) |
| ActivityPage | `/activity` | The query log — what ran, what was masked, what left the machine (SPEC §10) |
| ConnectionPage | `/connections/:id[/detection|catalog|limits|privileges]` | Everything about one database as tabs: Overview, Detection, Catalog (SPEC §5), Limits (mode, row ceiling, statement timeout, scan sample, denylist, write scope — SPEC §4.5) and Privileges (SPEC R4.1) |
| SettingsPage | `/settings` | `GET /v1/doctor`: daemon state, key source, connection health, catalog freshness. Not one of UI.md §2.3's six screens; added because `AppShell`'s settings control needs a destination and `/v1/doctor` needs a home — reconsider its placement with the product owner |
| Preferences | `Field`, `Input`, `Switch`, `Button` | Log retention in days, saved explicitly and confirmed, and whether this browser notifies for new Inbox items — with what the browser's own permission currently allows |
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
| InboxPage | `Lamp`, `Facts`, `Separator`, `Button`, `Empty` | Everything waiting on a human (SPEC R9.1): approval cards, then input and authorization requests. Each card says without a click who asks (agent, workspace, intent), on which profile (name, host, username, database), why it waited and what it would do, with the SQL last as evidence. Oldest first, never reordered, and deciding one item never advances to the next |
| ApprovalCard | `Lamp`, `Facts`, `Separator`, `Button` | One statement waiting for approval: §9.2's facts above its SQL, decided in place, with a write's "revocation restores nothing" line where it applies |
| RequestCard | `Lamp`, `Facts`, `Link` | One input or authorization request, opened on its own page where the value is typed or the access granted |
| ProfileFact | `Fact` | The profile a statement runs as — connection name, host and address, username, database — so an approver knows which credential they are approving |
| AgentsIndicator | `Sheet`, `Button`, `Lamp` | The agents connected now: a lamp and a count in the top bar, opening to each one's client, workspace and stated intent. It replaces the sidebar's agent list, which took a column of the screen to say one number |
| ActivityPage | `Table`, `Select`, `Button`, `Empty`, `Facts`, `Cell` | The query log (SPEC §10). Filters by session, connection and tier, because a human arriving after a long agent run wants one session's trail. Pages by a `before` cursor, newest first, so no record is out of reach behind a fixed limit. A row expands in place: a detail drawn below the table was off-screen for any row past the first few, and read as a click that did nothing |
| RecordDetail | `Facts`, `Table`, `Cell`, `Separator` | One audit record in full: what was returned to the agent, drawn through `Cell` exactly as masked, then policies applied with their basis, degradations, and the statement as kept. The rows come from the daemon's memory, never the log (R10b), and when they are gone it says why. Never a "reveal literals" affordance — there are none stored (R10a) |
| Filter | `Select` | One labelled filter on the activity log: a list of known values, never a field to type an id into |
| SettingsPage | `Table`, `Lamp`, `Facts`, `Input`, `Button`, `Separator`, `Skeleton` | Per-daemon state from `GET /v1/doctor`: vault key source, detector identity and network posture, connections and sessions. Reports rather than configures — it is what you open when something is wrong |
| ConnectionsPage | `Table`, `Lamp`, `Facts`, `Input`, `Button`, `Empty` | Hosts and the databases on them (SPEC R4.1). Grouped by host, because a host is entered once and each database and role on it is its own connection — what an agent queries and what owns a catalog, token keys and an audit. keeper never refuses a credential and no longer holds one shut either; what the role can do beyond reading is read on that connection's page, and each row links there rather than restating it |
| HostSection | `Table`, `Lamp`, `Button` | One host on `ConnectionsPage`: its address and sslmode, its connections, and `RegisterDialog` for adding another. Remove is offered only while no connection is on it, because the daemon refuses otherwise and a button that always fails is a lie |
| FindingRow | none (layout) | One finding: its id and kind, its one-sentence meaning, and the suggested fix — or the sentence explaining why no single statement removes it |
| HostDialog | `Dialog`, `Field`, `Input`, `Select`, `Button` | Adding a database server, as a modal: name, address, port, sslmode. It holds no credential and connects to nothing, so an address is typed once however many roles share it. sslmode is a `Select` of libpq's six values rather than a TLS switch, because a switch can say two of six things |
| RegisterDialog | `Dialog`, `Field`, `Input`, `Switch`, `Separator`, `Button` | Registering a database on a host already chosen, as a modal. Database, user and password are separate fields rather than one connection string, because a pasted DSN is where a typo is invisible until it returns as an unattributable permission error; the daemon's vault assembles the string and nothing here builds or renders one. One profile is one credential, with an "Allow writes" switch: off makes its sessions read-only at the server, on lets an agent send writes that each wait in the Inbox. No engine control (registration records `postgres` and nothing else) and no SSH tunnel (keeper has none). No "Test connection": storing the connection is the test, and what the audit found is read on the connection's Privileges tab |
| PermissionsSection | `Table`, `Button`, `Skeleton` | The standing allow rules (SPEC §9.3), a section of Settings. One row per path, session grants first, and last used / uses as the columns that decide whether an entry should still exist. No tree, no wildcard, no select-all: R9.3c grants exactly the path named |
| Privileges | `Button` | What this connection's role can do beyond reading (SPEC R4.1), read where its other controls are set rather than on a page of its own. It says when the audit ran, says "no report yet" when it never has — which is not the same as clean — and offers Re-audit. Nothing here gates the connection |
| FindingRow | none (layout) | One finding: what it is, what it means, and the statement that would narrow it, shown in full because a statement behind a disclosure is one nobody pastes. keeper never runs it |
| ModeSelector | radio inputs | Strict or assisted, as text with a sentence of consequence under each. Never a pill or a slider: the two differ in who decides an uncertain read — keeper's masking or a human — and a scale would be lying about that |
| DetectionEditor | `Field`, `Switch`, `Input`, `Textarea`, `Button` | The pipeline a connection's free text runs through: one `Switch` per stage kind with what it finds, an optional entity filter per stage, and the list stage's deny terms, expressions and allow terms. Terms and expressions are write-only — the daemon answers a save with counts — so the boxes start empty and a save states the whole set. It never sets a column's policy; the catalog does |
| LimitsForm | `Input`, `Button` | Row ceiling, statement timeout and scan sample. The ceiling is a privacy control, not a performance one |
| DenylistEditor | `Table`, `Input`, `Button` | Relations this connection may never read. Evaluated against the plan, so a denied base table is caught through a view; no approval overrides it |
| WritesSelector | radio inputs | Whether a profile's sessions may write — read-only, or writes with approval — as text with a sentence of consequence under each, like ModeSelector. Allowing writes never approves one |
| CatalogTab | `Table`, `Input`, `Button` | One connection's column classification (SPEC §5), a tab of its page. A table rather than cards, because it shows hundreds of columns and is for bulk work. The unclassified count is the daemon's, not the length of the sample list beside it |
| CatalogRow | `Input`, native select | One column's policy and its arguments. A token's namespace and a partial's form appear only where they apply, because their absence is a validation error rather than a default |
| Proposal | `Table`, `Button` | `catalog init`'s output in its two groups: what may be accepted without reading, and the review task where unnamed name and address columns live |
| LocalRequestPage | `Facts`, `Input`, `Button`, `Separator`, `KeeperMark` | The local page (R8.7g), outside the app shell. Somebody arrives from a link a blocked agent printed to make one decision; navigation would invite them to do something else first |
| InputRequest | `Input`, `Button` | A value the agent must never see. Never echoed back, on success or in an error |
| AuthorizationRequest | `Facts`, `Button` | Granting one path. "Allow for this session" first and as the default, because that and "allow until revoked" are different requests. Says plainly that nothing returns to the agent |
| Page | none (layout) | The local page's frame: one column, no navigation |
| ConnectionPage | `Skeleton`, `Empty`, `Separator`, `Button` | One database's page. Its tabs are routes, so each can be linked to, and the sidebar picks the database — the per-page rows of connection buttons it replaces were a second picker that disagreed with the first. An id that names nothing says so and links back to Connections |
| OverviewTab | `Facts`, `Separator` | The connection in one screen: host, database, role, mode, detection stages, unclassified columns and privilege findings, each count a link to the tab that acts on it, and Remove at the foot |
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
- No component sets `z-index` by hand; overlay components (`Dialog`, `Sheet`,
  `Select`) manage their own stacking.
