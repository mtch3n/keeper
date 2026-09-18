# Changelog

Generated from the commit history by `make changelog`. Every commit appears
somewhere: one that does not follow the convention lands under *Other* rather
than vanishing, because a changelog that silently drops entries is worse than no
changelog at all.

The 0.0.1 entry below is written by hand, because a first release has no previous
tag to generate a range from. Every entry after it is produced from the commits.

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
