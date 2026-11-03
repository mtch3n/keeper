# keeper

keeper sits between an agent and your PostgreSQL database. It runs the agent's
SQL and masks the result before the result enters the agent's context. The agent
gets rows it can join and count on. It does not get the personal data inside them.

```
database → keeper → MCP response → agent context → cloud provider
             ↑
       masking, tokens
```

keeper pseudonymises. It does not anonymise. Its output is still personal data
under GDPR Article 4(11).

## First run

1. Install the plugin in your agent. The plugin starts keeper through a launcher
   that downloads the release, checks its checksum and installs it in
   `~/.local/bin`.

   Claude Code:

   ```
   /plugin marketplace add mtch3n/keeper
   /plugin install keeper@keeper
   ```

   Codex: add the same repository as a plugin marketplace and install the
   `keeper` plugin. Check the Codex plugin docs for the exact command in your
   Codex version. See "Codex" below for two settings it needs.

   If `~/.local/bin` is not on your `PATH`, the plugin still works. Add the
   folder to `PATH` so that your terminal finds `keeper`:
   `export PATH="$HOME/.local/bin:$PATH"`.

2. Register a database. Run this in your own terminal, not in the agent:

   ```sh
   keeper connect
   ```

   Paste a PostgreSQL connection string at the hidden prompt, for example
   `postgres://app_ro:...@db.internal/app?sslmode=require`. keeper makes one
   profile for that login. The profile reads and masks. It cannot write. With no
   `sslmode` in the string, keeper requires TLS unless the host is loopback. An
   explicit `sslmode`, including `disable`, is used for a new host. A host that
   keeper already holds keeps its own `sslmode`: a string that asks for a weaker
   mode does not lower it, and keeper refuses a string that would send the login
   with less TLS than it asked for or than its default.

3. Ask the agent a question about your data. The agent calls `query`, and the
   rows come back on the same call.

4. In the first result, the email column is a token, for example
   `⟨email1:a3f21b4c9d8e7f60⟩`. Within one session, the same email gives the same
   token, so joins, `GROUP BY` and `COUNT(DISTINCT ...)` still work. Another
   session gets a different token for the same email. The result also names
   the one command that changes how a column is masked:

   ```sh
   keeper catalog edit <profile> <schema.table.column> --policy allow
   ```

keeper classifies columns on its own, by name and type. You do not have to set up
a catalog before the first query.

## Profile settings

Each profile has three settings. Change them with `keeper connection set`, or in
the dashboard (`keeper ui`).

| Setting | Values | Meaning |
|---|---|---|
| `reads` | `always`, `uncertain`, `never` | `always`: every read asks a person first. `uncertain`: a read asks when its plan calls a volatile function. `never`: reads run without a question. |
| `writes` | `off`, `ask`, `allow` | `off`: a write is refused. `ask`: a person approves each write and sees the rows it changes. `allow`: a write runs. |
| `detection` | `off`, `scan`, `all` | `scan` (the default): pattern detection examines the text of `scan` columns. `all`: it also examines the text of `allow` columns. `off`: it examines nothing, so `scan` cells are redacted whole. |

```sh
keeper connection set prod --reads uncertain --writes ask --detection scan
keeper connection show prod
```

## Approvals

When a statement needs a person, the agent's client shows a dialog in the
agent's terminal. The same `query` call then returns the rows after the person
accepts.

- A read: press Enter to run it.
- A write: the person must choose "Run once" or "Refuse". Nothing is
  preselected, so a stray Enter does not commit a write.
- Esc, or a client that cannot show a dialog (a headless run), does not decide
  the statement. The agent gets a ticket and a local link. The ticket waits in
  the Inbox of the dashboard, and keeper sends a desktop notification with the
  link. The agent polls `get_result` until a person decides.

`keeper approve` lists and decides tickets from a terminal.

## Declare personal data columns

keeper classifies a column on its own by its name and its type only. A name that
says personal data gets `token` (or `redact` when the column is not text). Other
text columns are redacted. Numeric, boolean, date and time, and UUID columns are
allowed. `keeper catalog init <profile> --sample N` samples N rows per column and
prints proposals in two groups, safe to accept in bulk and needs review. It only
proposes: it changes nothing until you run `keeper catalog edit`. To set the
policy of one column yourself:

```sh
keeper catalog ls prod --unclassified
keeper catalog edit prod public.customers.email --policy token
```

The policies are `allow`, `token`, `redact` and `scan`. The Catalog tab of the
dashboard does the same. Only a person can change the catalog. The agent cannot.

## Move the vault

`keeper vault export > keeper.vault` seals your registered connections under a
passphrase that you type at export time. `keeper vault import keeper.vault`
restores them on another machine. keeper does not store the passphrase.

## What keeper does not promise

- **The transaction blocks database writes on reads. It does not stop effects
  outside the transaction.** The same role can call `pg_terminate_backend`. A
  superuser can also call `dblink_exec`, `pg_read_file` and `pg_stat_reset`. Use a
  role that is not a superuser.
- **The `uncertain` rule looks for volatile function calls in the plan.** It
  misses a function that is declared STABLE or IMMUTABLE but calls a volatile one.
  It also misses immutable functions that run at plan time. For certainty, set
  `reads=always`.
- **The preview of a write does not undo what a trigger does outside the
  transaction.** keeper runs a write in a transaction and rolls it back to count
  the rows. A trigger can call a function with an effect that stays, such as
  `nextval` or `dblink_exec`. keeper does not run the preview for a statement
  that calls such a function itself, but it cannot see inside a trigger.
- **keeper hides values. It does not hide that a table or column exists.** It
  does not stop inference, such as `length(email)` or a predicate that narrows
  the rows. Use database grants to control which tables a role can reach.
- **Automatic classification lets numeric columns with unremarkable names
  through**, such as `balance`. Review them and declare the ones that matter.
- **A client can answer the dialog for the person.** An Elicitation hook in the
  client can auto-answer it. Check your client's hooks.
- **The launcher's checksum comes from the same release as the binary.** It
  guards against a corrupt download. It does not guard against a compromised
  release.
- **Hooks are guidance, not enforcement.** The plugin's hooks tell the agent to
  use keeper and deny some raw database clients. An agent or a person can work
  around them.

## Codex

The plugin cannot set tool approval in Codex. To avoid a second question
on top of keeper's own dialog, set the approval mode for the keeper server in
your Codex config (`~/.codex/config.toml`). Check this for your Codex version:

```toml
[mcp_servers.keeper]
default_tools_approval_mode = "approve"
```

The key is `mcp_servers.<id>.default_tools_approval_mode`. Its documented values
are `auto`, `prompt`, `writes` and `approve`. The Codex docs do not say what
each value does, so check which one skips the question. A per-tool key,
`mcp_servers.<id>.tools.<tool>.approval_mode`, also exists. Check the
[config reference](https://learn.chatgpt.com/docs/config-file/config-reference)
for your Codex version.

Codex runs a hook only after you trust it. Open `/hooks` in the Codex CLI (or
Settings, Hooks in the desktop app) and trust the plugin's hooks. A plugin update
that changes a hook needs a new review.

## Other MCP clients

Point the client at `keeper mcp` over stdio. The daemon starts on demand.

```json
{ "mcpServers": { "keeper": { "command": "keeper", "args": ["mcp"] } } }
```

## Install without the plugin

```sh
# pick your platform from https://github.com/mtch3n/keeper/releases
base=https://github.com/mtch3n/keeper/releases/latest/download
asset=keeper_<version>_linux_amd64             # _darwin_arm64, _linux_arm64, ...

curl -fsSLO "$base/SHA256SUMS"
curl -fsSLO "$base/$asset.tar.gz"
sha256sum -c SHA256SUMS --ignore-missing       # shasum -a 256 -c on macOS
tar xzf "$asset.tar.gz"
install -m755 "$asset/keeper" ~/.local/bin/
```

From source (Go 1.27 and pnpm):

```sh
git clone https://github.com/mtch3n/keeper && cd keeper
make build
install -m755 bin/keeper ~/.local/bin/
```

After that, `keeper update --check` shows the published version, and
`keeper update` verifies, replaces and restarts onto it. It stops when a session
is connected or a ticket is waiting. `--force` overrides that. Only a person can
run it. It is not an MCP tool.

## The daemon

The daemon starts when an MCP client or the CLI needs it. It is not a system
service. It runs as you and opens the vault from your OS keychain. It stops by
itself after `KEEPER_IDLE_EXIT` (4h by default) with nothing attached. A negative
value disables that. `docs/keeper.service` is a user-level systemd unit if you
want the daemon to stay up.

```sh
keeper doctor                    # daemon, key source, connections
keeper daemon status
keeper ui                        # the dashboard's local URL
keeper activity --since 24h      # what ran, and what was applied to it
```

Windows compiles and does not run: there is no named pipe and no single-daemon
election.

## Changelog and contributing

[CHANGELOG.md](./CHANGELOG.md) is generated from the commit history by
`make changelog`. [CONTRIBUTING.md](./CONTRIBUTING.md) has the commit convention
and the rules that are not negotiable. The first is that output columns match by
`(tableOID, attnum)`, never by name.

`internal/integration` holds the measurements. Those tests run against a real
PostgreSQL, and each one states what it establishes.
