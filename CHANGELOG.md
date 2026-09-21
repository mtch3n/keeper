# Changelog

Generated from the commit history by `make changelog`. Every commit appears
somewhere: one that does not follow the convention lands under *Other* rather
than vanishing, because a changelog that silently drops entries is worse than no
changelog at all.

The 0.0.1 entry below is written by hand, because a first release has no previous
tag to generate a range from. Every entry after it is produced from the commits.




## 0.0.6 — 2026-09-21

The dashboard's chrome stops competing with the one colour that is supposed to
mean something, and `keeperd` stops asking for a passphrase on installs that
never needed one.

### Added

- **keeperd opens the vault at start when a key source needs no passphrase.**
  Three of §4.3's four sources take nothing from the operator — the OS
  keychain, `KEEPER_MASTER_KEY` and `key.age` all resolve inside the daemon,
  and a first-ever start mints a fresh master key straight into the keychain.
  Only an install with none of them needs source 4, and only keeperd can say
  which kind of install it is. It never asked, so every start left the vault
  shut and every install paid an unlock step that on nearly all of them opened
  nothing a passphrase was ever needed for.

  This grants no access that did not already exist. A source that opens with
  no secret from the operator is reachable by anything already running as that
  user, and the manual step it replaces ran the identical resolution chain
  behind a socket any local process could reach. What is removed is a prompt
  that cost a person an action and an attacker nothing.

  §3.4's rule survives intact, and the distinction it rests on is the whole
  design: the daemon may not *block* on a passphrase, because an auto-started
  daemon has no TTY to read one from. That is an argument against prompting,
  not against trying, and a source resolving without input is not the
  interactive path. When none resolves the daemon stays locked exactly as
  before, tools return `CodeVaultLocked`, and Settings and `keeper vault
  unlock` offer source 4. A configured-but-broken source stays locked and logs
  why rather than falling through to a prompt — collapse that branch into the
  `ErrNoKeySource` one and a keychain holding a malformed key silently becomes
  a passphrase install, which is R4.3's silent degradation arrived at by way of
  a convenience.

  `--vault-idle-lock` is untouched and still bounds how long decrypted DSNs sit
  in the process's memory. It is deliberately not re-opened after it fires: an
  idle lock that something in the same process re-opens on a timer protects
  nothing at all.

### Changed

- **Every grey in both themes is chroma 0, and dark is the designed theme.**
  UI.md §2.1 claims three colours carry meaning and nothing else does. The
  palette made that claim false: light was warm (hue 85), dark was blue slate
  (hue 265), and both spent chroma on chrome — precisely the budget amber needs
  in order to read as the one thing asking for attention. The claim now holds
  by construction rather than by discipline, and amber, red, green and an
  unclassified cell's hatched edge are the only chroma a screen can contain.

  The two themes were also unrelated palettes rather than one theme in two
  modes (§5), because they did not share a hue, a ladder or a reason.
  `index.html` has always shipped `class="dark"`, so dark was already the
  default but had never been the designed artefact; it is authored first here
  and light is the same lightness ladder inverted.

- **`Card` loses the header and footer no screen used.** `CardHeader`,
  `CardTitle`, `CardDescription`, `CardAction` and `CardFooter` were imported
  by exactly zero application files, and each carried styling that fought this
  theme: `CardFooter` drew `border-t bg-muted/50`, the same panel-inside-a-panel
  the palette rewrite just removed from the rail, and `CardTitle` was
  `text-base`, off the type scale outright and surviving `ui-audit` only
  because registry files are exempt from the type-scale check.

  They are deleted rather than left unused. An unused slot is a slot the next
  screen reaches for, and it would have brought that treatment back with it —
  which is how the `bg-muted/50` would have returned one screen at a time. What
  replaces them is what the screens already do: a heading is `text-heading`, a
  description is `text-sm text-muted-foreground`, a footer is a flex row of
  buttons. The radius utilities went too, since `rounded-xl` resolves to 0
  through `--radius` and described a corner that is square (UI.md §3.2).

- The brand moves from the shell bar into the rail's header, so the two columns
  open on one baseline with one kind of thing each. Below `md` the rail is an
  off-canvas `Sheet` and takes its header with it, so the bar keeps the brand
  there — hence `Brand`, since one thing shown in two layouts is one component
  (UI.md §3.3) and the second copy is where they drift apart.

### Fixed

- **The rail stops being its own scroller.** `pt-shell` sat inside
  `SidebarContent`, which is the `overflow-auto` element, so the shell bar's
  54px offset belonged to the scrolled content: it slid away on the first wheel
  gesture and dragged the top of the rail under the bar. Moving it into a
  `SidebarHeader` fixes that and nothing else — padding inside a scroll box
  adds to `scrollHeight` where a header takes the same 54px off `clientHeight`,
  and both shapes begin to overflow at identical content height.

  So the scroller moves down a level. `SidebarContent` is `overflow-hidden` and
  each group's content scrolls on its own, with `min-h-0` on the groups so
  flexbox may shrink them below their intrinsic height. Nothing shrinks or
  scrolls while the two lists fit; when they do not, only the list that is too
  long moves. Measured on a 577px viewport with 24 rows injected: document and
  rail both overflow by 0, the two lists by 270 and 200 within themselves.

  That is not merely a smaller scrollbar. `Agents`, `Databases` and the rule
  between them are the structure of this surface — two questions, not one list
  with two headings — and a rail that scrolls them off the top hides the second
  scope behind a gesture you have to find. Restore the rail-wide scroller and
  the `Databases` heading leaves the screen as soon as a few agents connect.

  `no-scrollbar` came off `SidebarContent` with it. The registry writes that
  class in two files and defines it in none, so every region wearing it drew
  the document's 11px thumb anyway; it is defined in `index.css` now for
  `Combobox`, which genuinely wants it, and gone from the rail, which does not.
  A rail that cannot fit its own workspaces has to say so.

## 0.0.5 — 2026-09-21

`keeper daemon restart` killed the daemon it was asked to replace and started
nothing in its place. That is the restart `keeper update` performs, so the
upgrade path 0.0.4 shipped ended with no daemon running.

### Fixed

- **A restart no longer bows out to the daemon it just stopped.** Shutdown
  closes the listeners first and drains open connections second, and the flock
  that elects a single daemon outlives both. The socket stops answering within
  about a millisecond, while the lock stays held for the whole shutdown grace
  whenever a client is holding a connection open — which an attached MCP
  session or the dashboard's event stream always is. Measured against a live
  daemon: socket dead at t=0.01s, lock free at t=10.12s.

  `keeper daemon restart` waits for the socket to stop answering and then
  spawns the replacement, so it landed inside that window every time. The
  replacement asked for the lock, was refused by a process on its way out, and
  exited reporting success into a log nobody reads. Nothing spawned again, and
  the client timed out on a socket that was never going to appear.

  §3.4's rule is that the loser of the start race connects to the winner, and
  it assumed the holder of the lock was a winner at all. A held lock now
  settles the race only while something is serving behind it. When nothing is,
  the holder is on its way out and the replacement waits for it — bounded, so
  that a holder which neither serves nor exits is a message rather than a hang.

- **The register dialog** took the registry's 384px default while pairing host
  with port and user with password on one row, which left each column narrower
  than the values it holds. A port field you cannot read `5432` in is the shape
  of the typo that splitting the DSN into separate fields exists to prevent.

- **The sidebar** began above the top bar's own baseline and its first row
  straddled the bar's bottom edge, so the two columns read as two unrelated
  pages sharing a window. Its content is offset by the bar's height now, and a
  rule divides the two scopes: agents and databases are two questions, not one
  list under two headings.

### Build and CI

- The dev proxy defaulted to port 7799, which is not keeperd's. `pnpm dev`
  proxied the whole API into whatever else was listening there and rendered an
  app with no data rather than failing in a way anyone could read.

## 0.0.4 — 2026-09-18

`keeper update`. The manual upgrade was five commands, and the README's
version of it had been broken since the first release.

### Added

- **`keeper update`** verifies the published archive against its checksum
  before a byte is written, replaces this binary, and restarts the daemon onto
  it. `--check` reports what is published and changes nothing.

  It reads the daemon **before** downloading anything. Completing an update
  means restarting, and a restart permanently invalidates every token every
  session holds and cancels every pending ticket. When there is something to
  lose it names what it found and stops without touching the binary, leaving
  `--force` as a decision rather than a surprise. A daemon at a different
  version — the state a half-finished update leaves behind — counts as
  unknown rather than as nothing, because that is the one dial failure where
  guessing wrong silently cancels somebody's work.

  It refuses a binary under a package manager's tree, where the next upgrade
  of that package would undo it. `/usr/local` is not in that list: it exists
  for installs outside the package manager, and the README names it.

  It is a CLI command and is not on the MCP surface. An agent that could
  replace keeper's executable would be removing its own supervision, which is
  why registering a connection, accepting a finding, approving and granting
  are not there either.

- The `keeper-install` skill leads with `keeper update` and keeps the manual
  download as the fallback it now is. Plugin version 0.0.3.

### Fixed

- The README's release install snippet fetched `SHA256SUMS`, ran the checksum
  check, and then untarred an archive no line had downloaded. It was also
  pinned to 0.0.1, two releases stale.
- `make changelog` wrote its own `# Changelog` heading above the file's
  existing one on every run, and prepended each new section above the preamble
  rather than under it. 0.0.3 shipped with two headings and the duplicate was
  removed by hand; `scripts/changelog.sh` now splices the section into place.

## 0.0.3 — 2026-09-18

The dashboard stops asking which database you are looking at and starts asking
which agent. Underneath it, two fixes: unlocking a vault no longer requires a
passphrase the keychain had already made unnecessary, and `doctor --json` no
longer reports a running daemon as stopped.

### Added

- The chrome is a sidebar rather than a switcher in the top bar. Both scopes
  live in it — agents, grouped into workspace folders, and databases — and the
  group the current screen does not obey is dimmed. The old switcher sat in the
  bar where four of the six sections ignored it, and a control that is always
  present and only sometimes effective cannot be read.
- Selecting an agent scopes `Activity` and `Approvals`. A narrowed approvals
  queue says so above the table and offers the way back, because a filtered
  queue that read as the whole queue would be that screen claiming nothing else
  needs you. `Permissions` is not scoped: a grant's session id is `json:"-"`
  and never reaches the browser.
- Registering a database is a dialog, with host, port, user, password and
  database as separate fields. A pasted connection string is the one input
  where a typo is invisible until it returns as a permission error nobody can
  attribute. The string is assembled from the fields and never rendered back.

### Fixed

- `keeper vault unlock` prompted for a passphrase before asking the daemon
  anything, which put the three sources that need nothing typed — the keychain,
  `KEEPER_MASTER_KEY` and `key.age` — out of the CLI's reach. On a keychain
  install it asked for a secret that install does not have, and because the
  prompt refuses a non-terminal, an unattended unlock was impossible. It now
  asks the daemon first, prompts only when a passphrase is genuinely the
  answer, and names the source that served the key.
- `keeper doctor --json` reported `"daemon_running": false` beside a complete
  report from a daemon that was plainly running. The field was declared and
  never assigned. It is the one a script would branch on to decide whether
  keeper is up, and a boolean that is always false reads as an answer.

### Changed

- The daemon lamp appears only while the event stream is degraded. A permanent
  green "Live" spent the chrome's attention on the reading that never needs
  acting on; `waiting` and `blocked` are unchanged.

## 0.0.2 — 2026-09-18

Plugin fixes. The binaries are unchanged; the version moves because the plugin's
does, and a plugin whose version does not move is never reinstalled.

### Fixed

- `.mcp.json` declared `"type": "stdio"` alongside `command`, and the host took
  the type as the executable — `Executable not found in $PATH: stdio`. Working
  stdio plugins carry no `type` field at all.
- `plugin.json` did not declare `"skills": "./skills/"`, so neither skill loaded.
  The command did, which made the failure look like a naming collision.
- The skill and the command both answered to `keeper:keeper`. The command is now
  `/keeper:ui` and does one thing a skill cannot: print the dashboard's address,
  which is unguessable because the port is chosen at startup. Handing over a
  sensitive value is back where it belongs — in the skill, since the model is
  what has to drive that exchange.
- The MCP server is reached through `keeper mcp` via a launcher that explains
  itself when the binary is missing, instead of failing with a bare `ENOENT`.
- `keeper daemon restart` could not restart a version-mismatched daemon, which
  is the one situation it exists for: it handshook first, and the handshake is
  what refuses on a mismatch. The error told you to run the only command that
  could not work. It now posts the stop request straight to the socket — nothing
  about stopping needs a session.

### Added

- A `keeper-install` skill: install has state in it — binary, version, daemon,
  vault, findings, catalog — and each of those has a different fix.

## 0.0.1 — 2026-09-18

First tagged build. PostgreSQL reads end to end.

### Added

- A daemon that owns all state — vault, catalog, connection pools, approval
  queue, audit log, and the web UI it serves. Every other component is a thin
  client, and a session is one connection to it, so tickets and tokens go when
  the client does.
- A privilege audit that **reports rather than refuses**. A master account works;
  every finding is shown with the narrower grant that would remove it, accepted
  by name, and displayed wherever the connection appears afterwards.
- A name-keyed catalog resolved to column identity, covering tables, views and
  matviews, with JSON key paths and a daemon-owned overlay so automatic changes
  never appear as diff noise in a tracked file.
- The G1–G9 pipeline: admission through PostgreSQL's own parser, plan analysis
  that never uses `EXPLAIN ANALYZE`, output matched by `(tableOID, attnum)`,
  type-family inheritance for computed columns, tier routing, and a read-only
  transaction with `DISCARD ALL` before a connection returns to the pool.
- Keyed, namespaced HMAC tokens that preserve joins, `GROUP BY` and
  `COUNT DISTINCT`, with a per-session reverse map that re-tokenizes any resolved
  value found on the way out.
- Span-level redaction of free text, so one email in one row of `notes` does not
  mask every note in the result.
- A write path: a separate credential with an enumerated scope, a preview that
  rolls back, and an approval that names what revocation cannot undo. DDL is
  refused at every tier.
- One approval queue across every agent session, each item attributed to the
  client, workspace and stated intent that produced it, oldest first and never
  reordered.
- Per-path permissions with no inheritance, in two lifetimes, granted in the UI.
- A local page for values that must not be typed into a conversation, and for
  authorizing a path — one mechanism, two request kinds.
- An append-only audit log with literals stripped and comments discarded.
- A daemon that starts on demand and stops on its own: the vault locks after an
  idle hour, and the process exits after four with nothing attached at all — no
  sessions, no queued approvals, no open requests, no live event streams. An open
  dashboard counts as attached, standing permissions are on disk, and either
  timer can be disabled.
- One binary with three entry points — `keeper` the CLI, `keeper daemon`, and
  `keeper mcp` — so a client and a daemon from different builds cannot happen by
  halves, and there is one artefact to verify.
- A Claude Code plugin carrying the `keeper` skill, a `keeper-install` skill, the
  `/keeper` command and the MCP registration, and a web UI the daemon embeds.

### Measured

Reproductions live in `internal/integration` and run in CI against a real
PostgreSQL, MySQL and SQLite.

- A view column reports the **view's** own identity, while the plan for the same
  statement names only the base table. Views must be catalogued, and the identity
  the protocol reports is one the plan never mentions.
- Aliases, CTEs and subqueries preserve column identity; `UNION` reports `(0,0)`.
- SQLite exposes column origin and resolves through a view to the base column —
  the opposite of PostgreSQL, so view cataloguing is a PostgreSQL requirement
  rather than a general one.
- MySQL's driver reads `org_table` and `org_name` off the wire and discards them,
  so `SELECT ssn AS x` is indistinguishable from a column named `x`. **MySQL does
  not ship**; name matching is not an acceptable substitute.
- A PostgreSQL conversion error carries the value it was converting, which is why
  no server error field reaches an agent.
- An advisory lock survives `COMMIT` and `DISCARD ALL` releases it.
- A concurrent `ALTER TABLE` blocks on `AccessShareLock` rather than racing.
- A network namespace alone does **not** isolate a sidecar: the resolver socket
  survives it, and the resolver has network access. A mount namespace closes it,
  and both are available unprivileged.

### Known limits

- Windows compiles and does not run: no named pipe, no single-daemon election.
- Detection is patterns and a seed name dictionary. Addresses and contextual PII
  in free text are undetected rather than absent, and the list of what is
  validated is published in code.
- keeper pseudonymises rather than anonymises; output remains personal data under
  GDPR Article 4(11).
- It does not detect an agent recovering a masked value through narrowing
  predicates. That is stated rather than mitigated.
