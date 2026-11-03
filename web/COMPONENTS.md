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
| AlertDialog | `@/components/ui/alert-dialog` | The confirmation behind `ConfirmWiden`: a modal question that must be answered, not dismissed |
| Badge | `@/components/ui/badge` | A count on a navigation entry: what waits in Inbox |
| Collapsible | `@/components/ui/collapsible` | An open/closed panel, e.g. one approval's expanded facts |
| Dialog | `@/components/ui/dialog` | Modal flows; owns focus trap, Escape, scroll lock and `aria-modal` |
| Empty | `@/components/ui/empty` | Empty states and every placeholder page in this foundation. Never boxed: a title, a line of description, and an action when one exists |
| Field | `@/components/ui/field` | Form layout (FieldGroup, Field, FieldLabel) |
| Input | `@/components/ui/input` | Single-line form control |
| Label | `@/components/ui/label` | Control label primitive used by `Field` |
| RadioGroup | `@/components/ui/radio-group` | A choice among a fixed, small set where each option needs a sentence of explanation beside it: reads, writes and detection. A `Select` would hide the consequences behind a click, and the consequences are the point. Square, like everything else: the registry's `rounded-full` indicator is replaced |
| ScrollArea | `@/components/ui/scroll-area` | A fixed-height region that scrolls on its own, e.g. the Activity log. Thumb is square (registry's `rounded-full` replaced with `rounded-sm`, which resolves to 0 through the radius tokens) |
| Pagination | `@/components/ui/pagination` | The frame of `PagedTable`'s previous and next controls. Installed with the registry's `cn` import corrected to `@/lib/utils` |
| Select | `@/components/ui/select` | A pick from a fixed set |
| Separator | `@/components/ui/separator` | Dividers, replacing `border-t` spacer divs |
| Sheet | `@/components/ui/sheet` | The off-canvas panel the agents indicator opens |
| Skeleton | `@/components/ui/skeleton` | Loading placeholders |
| Table | `@/components/ui/table` | Rows of like things with columns — `Catalog`, `Activity`, query results |
| Toast | `@/components/ui/toast` | Transient action failures. Base UI ships `toast`; `sonner` is the Radix/React-Aria equivalent and is not used here |
| Tooltip | `@/components/ui/tooltip` | The label of every icon-only button, through `IconButton`. `TooltipProvider` is mounted once in `main.tsx` |

## Wrapper Components

`web/src/components/wrappers/` — composed from the registry, listed with what each owns.

| Component | Composes | Purpose |
|-----------|----------|---------|
| AppShell | `Brand`, `Lamp`, `Badge`, `AgentsIndicator`, `ThemeToggle` | The persistent chrome: one borderless bar with three places — Inbox first, then Activity and Settings — the agents indicator and the theme toggle. Settings carries its own General and Connections tabs, because connections are configuration; a connection's pages keep their `/connections` addresses and show under Settings. There is no sidebar: its agent list became the indicator, and its database list was a second route to a connection page that disagreed with the first. Inbox carries a `Badge` with the count of what waits. The daemon's `Lamp` appears only while the stream is degraded. The shell loads the Inbox once and shares it with the screen inside, so the badge and the cards come from one load. The page content is the one `main` landmark |
| LiveStatusProvider | React context (`@/lib/live-status`), `shareEvents` | Opens the one `GET /v1/events` stream for the whole app and, through a Web Lock and a `BroadcastChannel`, for every tab of the browser. It carries the stream's state and a `subscribe` for events down through context; screens use `useEvents` and never open an `EventSource`, because a browser allows six connections per host and each stream held one |
| PagedTable | `Table`, `Pagination`, `Select`, `Button` (and `usePaging` in `@/lib/paging`) | The one table every list uses, so every list pages the same way. Under the rows: the count and range (`51–100 of 312 records`), a page size of 25, 50 or 100 remembered per list, and Previous and Next, disabled at the ends. The page is in the URL as `<list>_page`, so a reload and the back button keep it; a filter change goes back to page 1; a page past the end falls back to the last. A list that fits one page keeps its count and loses the controls. Rows are paged on the client from a whole list, except Activity, which the server pages and counts. The table scrolls inside its own region at phone width. Registry search: `pnpm dlx shadcn@latest search @shadcn -q "pagination"` returned `@shadcn/pagination`, which has no page size, range or clamped page |
| ThemeToggle | `IconButton` | Flips the `dark` class on `<html>` and persists the choice. `index.html` sets the class before first paint, so there is no flash. Light and dark are one theme, not a second design (UI.md §5) |
| IconButton | `Button`, `Tooltip` | An icon-only button whose label is both its accessible name and its tooltip |
| Brand | `KeeperMark`, `Link` | The mark and the name, linking home, at the start of the top bar |
| KeeperMark | none (the brand path) | keeper's mark, drawn in `currentColor` so it stays achromatic like every other glyph in the chrome |
| Lamp | none (registry search below) | The four-state vocabulary: idle, waiting, blocked, live |
| Cell | `ScannedText` | The redacted-cell treatments, plus the plain value case. Each shows what the agent received: `⟨redacted⟩` in the muted text colour, a token as the token, scan text with its `⟨redacted:type⟩` spans marked. No block glyph is drawn. The Activity record shows each column's policy beside its header, and a `redact` column's header links to the Catalog filtered to that column |
| ScannedText | none (registry search below) | Renders a `scan` result's inline `⟨redacted:type⟩` spans inside otherwise-readable text |

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
cell"` both returned *No items found*. No primitive has a notion of five
mutually-distinguishable masking states over one value — `null`, `tokenized`,
`redacted`, `scanned`, `unclassified` — each of which must read
as neither a null nor an error and none of which may look like another (UI.md §2.2).
Cell owns exactly that vocabulary, the way `Lamp` owns the four-state one for
connection/approval status.

**ScannedText.** `pnpm dlx shadcn@latest search @shadcn -q "highlight text"` and
`-q "mark"` both returned *No items found*. A `scan` policy redacts a matched span
inside otherwise-readable text, written `⟨redacted:email_address⟩` — this is cell-*internal*, not a
cell-replacement treatment, and no primitive parses a string for embedded redaction
spans. ScannedText owns exactly that parse (`splitRedactedSpans`) and the span's visual
treatment, so a redacted span looks the same everywhere it appears (UI.md §3.3).

## Application Shell

| Component | Purpose |
|-----------|---------|
| App | Router root. Mounts `Toaster` once and owns the catch-all not-found route |

## Page Components

Placeholder pages, each rendering its title and an `Empty` per this foundation's
scope. The screens agent replaces the body of each; the route and the section it
lights in `AppShell` are already wired.

| Component | Route | Owns |
|-----------|-------|-------|
| ConnectionsPage | `/connections`, Settings → Connections | Every connection in one table grouped by host rows, with a filter box |
| InboxPage | `/inbox` | Everything waiting on a human: approvals and input requests |
| ActivityPage | `Table`, `Select`, `Button`, `Empty`, `Facts`, `Cell` | The query log. Filters by session, connection and decision, because a human arriving after a long agent run wants one session's trail. Pages by a `before` cursor, newest first, so no record is out of reach behind a fixed limit. A row expands in place: a detail drawn below the table was off-screen for any row past the first few, and read as a click that did nothing |
| ConnectionPage | `/connections/:id[/settings|catalog]` | Everything about one database as tabs: Overview, Settings (reads, writes, detection, limits) and Catalog. The inner tab keeps the name Settings: the top bar's Settings holds the daemon and this one the connection, and a locked test fixes the label |
| SettingsPage | `/settings` | `GET /v1/doctor`: daemon state, key source, detector and sessions. Holds the daemon-wide preferences and what `/v1/doctor` reports |
| Preferences | `Field`, `Input`, `Button` | Log retention in days, saved explicitly and confirmed |
| LocalRequestPage | `/r/:requestId` | The local page: a ticket's approval card, or an input request's form (UI.md §6). Deliberately rendered outside `AppShell` |

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
| `text-meta` | 12px mono | 400 | Identifiers: refs, tokens, ids, paths — and every redacted/tokenized `Cell` treatment |

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

## Screen components

| Component | Composes | Purpose |
|-----------|----------|---------|
| Facts | none (layout) | The facts block: what a statement would do, above the statement itself. One component because an approval, an activity record and the ticket page all show it and UI.md §3.3 forbids three renderings of one thing |
| Fact | Facts | One label/value row. A fixed label column rather than a grid, so a long value wraps under itself instead of widening the column for every other row |
| InboxPage | `Lamp`, `Facts`, `Separator`, `Button`, `Empty` | Everything waiting on a human as one `PagedTable`, a row for each: risk, agent, profile, why, statement, how long. A read can be approved from its row; a write is opened first (a lone item opens itself), because its changed rows are what is decided on. An open row is the card, which says without a click who asks (agent, workspace), on which profile (name, host, username, database), why it waited and what it would do, with the SQL last as evidence. Oldest first, never reordered, and deciding one item never advances to the next |
| ApprovalCard | `Lamp`, `Badge`, `Facts`, `Separator`, `Button` | One statement waiting for approval: its facts above its SQL, decided in place. A risk badge — read, write or destructive, and destructive whenever keeper could not read the statement — beside the verb and target, the columns with `SELECT *` expanded, any sensitive or uncatalogued column it filters on. A write adds its changed rows and a line that approving cannot be undone from here |
| ChangedRows | `Table` | The rows a waiting write changes, in cleartext, up to 50, each changed value shown struck through beside its new one, with a count of the rows not shown. For the approver only: nothing logs these values and no agent receives them |
| ApprovalRow | `PagedTable`, `Badge`, `Button`, `ApprovalCard` | One waiting statement as a row of the Inbox table, and its expansion |
| RequestRow | `Badge`, `Link` | One input request as a row of the Inbox table, opened on its own page where the value is typed |
| ProfileFact | `Fact` | The profile a statement runs as — connection name, host and address, username, database — so an approver knows which credential they are approving |
| AgentsIndicator | `Sheet`, `Button`, `Lamp` | The agents connected now: a lamp and a count in the top bar, opening to each one's client and workspace |
| ActivityPage | `Table`, `Select`, `Button`, `Empty`, `Facts`, `Cell` | The query log. Filters by agent, connection and decision (on the server, so it reaches records older than the page), because a human arriving after a long agent run wants one session's trail. Pages by a `before` cursor, newest first, so no record is out of reach behind a fixed limit. A row expands in place: a detail drawn below the table was off-screen for any row past the first few, and read as a click that did nothing |
| RecordDetail | `Facts`, `Table`, `Cell`, `Separator` | One activity record in full: what was returned to the agent, drawn through `Cell` exactly as masked, then policies applied with their basis, degradations, and the statement as kept. The rows come from the daemon's memory, never the log, and when they are gone it says why. Never a "reveal literals" affordance — there are none stored |
| Filter | `Select` | One labelled filter on the activity log: a list of known values, never a field to type an id into |
| SettingsPage | `Table`, `Lamp`, `Facts`, `Input`, `Button`, `Separator`, `Skeleton` | Per-daemon state from `GET /v1/doctor`: vault key source, detector identity and network posture, and sessions. Reports rather than configures — it is what you open when something is wrong |
| ConnectionsPage | `Table`, `Lamp`, `Facts`, `Input`, `Button`, `Empty` | Hosts and the databases on them. Grouped by host, because a host is entered once and each database and role on it is its own connection — what an agent queries and what owns a catalog and token keys. Each row links to that connection's page rather than restating it |
| HostRows | `TableRow`, `Button`, `RegisterDialog` | One host inside the single Connections table: a heading row with its address, sslmode and Add a database, then its connections as aligned rows with writes and reads. No reachability: keeper connects only when a database is needed, and whether an account logs in is asked with Test connection. Remove host is offered only while no connection is on it |
| HostDialog | `Dialog`, `Field`, `Input`, `Select`, `Button` | Adding a database server, as a modal: name, address, port, sslmode. It holds no credential and connects to nothing, so an address is typed once however many roles share it. sslmode is a `Select` of libpq's six values rather than a TLS switch, because a switch can say two of six things. It follows the address until chosen: prefer for a loopback address, require elsewhere |
| RegisterDialog | `Dialog`, `Field`, `Input`, `RadioGroup`, `Separator`, `Button` | Registering a database on a host already chosen, as a modal. Database, user and password are separate fields rather than one connection string, because a pasted DSN is where a typo is invisible until it returns as an unattributable permission error; the daemon's vault assembles the string and nothing here builds or renders one. One profile is one credential, with a Writes choice of Off, Ask or Allow, Off by default, and an optional catalog path left empty by default. No engine control (registration records `postgres` and nothing else) and no SSH tunnel (keeper has none). No "Test connection" here: registering does not connect, and the test is on the connection's Overview |
| ReadsSetting | `Choices`, `ConfirmWiden` | When a read asks you: always, uncertain or never, as text with a sentence of consequence under each. Saves at once; Never asks first because it removes the question. Never a pill or a slider |
| DetectionSetting | `Choices` | Whether text is scanned for personal data: off, scan columns or all columns, saved at once. It never sets a column's policy; the catalog does |
| LimitsForm | `Input`, `Button` | Row ceiling, size cap and statement timeout. The ceiling is a privacy control, not a performance one |
| SettingSection | `Collapsible` | One setting on a settings page: its heading, its controls, a polite "Saving…"/"Saved" line, the refusal beside it as an alert, and the reasoning behind a "Why" disclosure that remembers whether it was opened. The sentence of consequence under each option stays with the controls, because it is what a choice is decided on. Registry search: `pnpm dlx shadcn@latest search @shadcn -q "setting section"` returns no composed item |
| ConfirmWiden | `AlertDialog` | The one confirmation a setting asks for: before it widens what an agent may do (reads never, writes ask or allow). Narrowing never asks |
| Choices | `RadioGroup` | A choice as text with its consequence under each option, shared by Reads, Writes and Detection |
| LimitField | `Field`, `Input` | One limit as a positive whole number, validated as it is typed, with the reason beside the field |
| TestConnection | `Button` | On a connection's Overview: logs in once with the profile's credential and says who the server says it is, the server version and the round trip, or keeper's own error as an alert. keeper never checks a connection on its own |
| NumberSetting | `Field`, `Input`, `Button` | One whole-number daemon setting on Settings → General, validated as it is typed and saved alone, so saving one setting can never reset another |
| WritesSetting | `Choices`, `ConfirmWiden` | Whether a profile's sessions may write: off, ask or allow, as text with a sentence of consequence under each. Allow commits with no question, so it asks first |
| CatalogTab | `Table`, `Input`, `Button` | One connection's column classification a tab of its page. A table rather than cards, because it shows hundreds of columns and is for bulk work. The unclassified count is the daemon's, not the length of the sample list beside it |
| CatalogRow | `Input`, `Select` | One column's policy (allow, token, redact or scan) and a token's namespace, which appears only where it applies because its absence is a validation error rather than a default |
| Proposal | `Table`, `Button` | `catalog init`'s output in its two groups: what may be accepted without reading, and the review task where unnamed name and address columns live |
| LocalRequestPage | `Facts`, `Input`, `Button`, `Separator`, `KeeperMark` | The local page (R8.7g), outside the app shell. Somebody arrives from a link a blocked agent printed to make one decision; navigation would invite them to do something else first |
| RequestPage | `Facts`, `Input`, `Button`, `Separator`, `KeeperMark` | What `LocalRequestPage` falls to when the id is not a waiting ticket: an input request's page, or the statement that it was settled or does not exist, with no decision offered |
| TicketPage | `ApprovalCard` | A ticket's own page at `/r/:ticket`, opened from the link in a fallback answer or a desktop notification: the Inbox's card for the one waiting ticket, decided in place. It follows the queue: when another surface decides the ticket or it expires, the card is replaced by a closed notice. An unknown id falls to the request page, which says so and offers no decision |
| InputRequest | `Input`, `Button` | A value the agent must never see. Never echoed back, on success or in an error |
| Page | none (layout) | The local page's frame: one column, no navigation |
| ConnectionPage | `Skeleton`, `Empty`, `Separator`, `Button` | One database's page. Its tabs are routes, so each can be linked to, and the header picker chooses the database. An id that names nothing says so and links back to Connections |
| OverviewTab | `Facts`, `Separator` | The connection in one screen: host, database, role, reads, writes, detection and unclassified columns, each a link to the tab that acts on it, and Remove at the foot |
| RemoveConnection | `Input`, `Button` | Deleting a connection, with its name retyped rather than a click confirmed. Its credential goes with it. The catalog file stays: it lives in the project repo and is reviewed like code |

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
