# keeper

An MCP database server that reduces how much sensitive data reaches an agent's
context, without making you approve every query.

The problem is not an attacker. It is the normal operation of an agentic
workflow: a database row becomes an MCP response, becomes agent context, becomes
a request to a cloud model provider, a transcript on disk, a context summary, a
commit message. A human with a database account may legitimately read every SSN
in it; an agent reading the same rows ships them to a third party and embeds them
in artefacts that outlive the session.

keeper sits between the two and masks results on the way out.

```
database → keeper → MCP response → agent context → cloud provider
             ↑
     policies, tokens, redaction, audit
```

## What it does

- **Masks results by column identity**, using the server's own `(tableOID, attnum)`
  answer rather than parsing the SELECT list. Aliases, CTEs, subqueries, views and
  function wrappers all resolve correctly because none of them are being guessed at.
- **Keyed, namespaced tokens** so masked columns still join, group and count
  distinct. `⟨email1:a3f21b4c9d8e7f60⟩` is unmistakably not real data, which is the
  feature — a plausible fake is worse for an LLM consumer than an obvious token.
- **Audits the credential and tells you what it found.** It never refuses one: a
  master account works, and every finding is reported with the narrower grant that
  would remove it and accepted by name. The cost of accepting is recorded and shown
  on every screen that touches the connection.
- **One approval queue across every agent**, attributed to the session, workspace
  and stated intent, so a human working across three windows can tell whose task
  they are authorizing.
- **Sensitive input never goes through the prompt.** `/keeper` opens a local form;
  only a token returns to the conversation.
- **An append-only audit log** that answers what left this machine, and when.

## What it does not do

It pseudonymises rather than anonymises; output remains personal data under GDPR
Article 4(11). It does not detect an agent inferring a masked value through
narrowing predicates. It does not author SQL. It does not replace database access
control — where you can revoke a grant, that is better than anything here, and
`keeper catalog grants` writes the statement for you.

The limitations are stated rather than implied on purpose, and the ones above are
a summary rather than the list.

## Install

### From a release

```sh
# pick your platform from https://github.com/mtch3n/keeper/releases
curl -fsSLO https://github.com/mtch3n/keeper/releases/latest/download/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
tar xzf keeper_0.0.1_linux_amd64.tar.gz
install -m755 keeper_0.0.1_linux_amd64/keeper ~/.local/bin/
```

One binary, three entry points: `keeper` is the CLI, `keeper daemon` runs the
daemon, `keeper mcp` is the MCP server. Verify the checksum — this is about to
hold a database credential, and that is the one step that checks what you
downloaded.

### From source

```sh
git clone https://github.com/mtch3n/keeper && cd keeper
make build                       # needs Go 1.27 and pnpm
install -m755 bin/keeper ~/.local/bin/
```

## Set up a connection

Either the CLI or the web UI — the same flow, and the UI is the one designed for
it:

```sh
keeper ui                        # prints a loopback URL; register the database there
```

```sh
keeper connection add --name prod --dsn 'postgres://…'
# audits the role, prints every privilege finding with the narrower grant that
# would remove it, and stops. The connection is stored disabled.

keeper connection accept prod --finding rolsuper
keeper catalog init prod --sample 200
```

keeper never refuses a credential. A master account works; what it will not do
is let you hold one without knowing, so every finding is accepted by name and
the consequence is shown wherever that connection appears afterwards.

`catalog init` proposes a policy for every column and groups the proposals: typed
scalars and name matches are safe to accept in bulk, and free text no rule
matched is the review task — that is where unnamed name and address columns live.
Expect fifteen minutes once, then it is a file in your repo that gets reviewed
like code.

## Connect an agent

**Claude Code**, with the plugin — the skill, the `/keeper` command and the MCP
server together:

```
/plugin marketplace add mtch3n/keeper
/plugin install keeper@keeper
```

The plugin also ships a `keeper-install` skill, so an agent can get you the rest
of the way — it checks what is present, installs what is missing, and hands back
the parts only you can do.

**Any other MCP client**: point it at `keeper mcp` over stdio. The daemon starts
on demand.

```json
{ "mcpServers": { "keeper": { "command": "keeper", "args": ["mcp"] } } }
```

**Codex**: copy `plugin/codex/keeper.md` into your skills directory for the
`$keeper` flow, and register `keeper-mcp` the usual way.

## The daemon

It starts on demand — an MCP client or the CLI spawns it when the socket is
absent, the way `gpg-agent` and `ssh-agent` do — and it is not a system service.
It runs as you, uses your OS keychain and your `$XDG_RUNTIME_DIR`; a root service
would be wrong on every one of those.

It also stops on its own. Two timers, because they give up different things:

```
KEEPER_VAULT_IDLE_LOCK   1h   lock the vault, keep running
KEEPER_IDLE_EXIT         4h   stop, once nothing is attached at all
```

Locking forgets the decrypted credential and leaves everything else up. Exiting
gives the whole process back, and only happens when there are no sessions, no
queued approvals, no open local requests and no live event streams — so a
dashboard you left open keeps it alive, and an agent that comes back simply
starts it again. Standing permissions are on disk, so nothing is lost either way.
Set either to a negative duration to disable it.

`docs/keeper.service` is a user-level systemd unit for people who would rather it
stayed up. It is not installed for you.

## Day to day

```sh
keeper approve                   # the queue, across every agent session
keeper activity --since 24h      # what ran, and what was applied to it
keeper doctor                    # daemon, key source, detector, connections
keeper version                   # both versions, because they refuse each other on a mismatch
```

Most of that is also in the UI, which is where the work is meant to happen: the
approval queue attributes each item to the agent, workspace and stated intent
that produced it, and the permission list shows when each grant was last used,
which is the only thing that tells you which ones to revoke.

## Where the reasoning lives

Comments in this repository cite a specification by section — `SPEC R8.5a`,
`§9.2`, `R4.1`. That document is the working record and is not published; the
reasoning a comment needs is in the comment, and the citation is there so a
future change can be traced to the decision it belongs to rather than guessed at.

What is public and load-bearing is `internal/integration`. Those tests are the
measurements: they run against a real PostgreSQL, MySQL and SQLite, and each one
states in its own words what it is establishing and what follows if the answer
comes back the other way.

## Changelog and contributing

[CHANGELOG.md](./CHANGELOG.md) is generated from the commit history by
`make changelog`. [CONTRIBUTING.md](./CONTRIBUTING.md) has the commit convention
that makes that work, and the handful of rules that are not negotiable —
matching output columns by identity rather than by name being the first of them.

## Status

First tagged build. Three measurements are closed with reproductions — view
provenance, engine column origin, and sidecar network isolation on Linux — and
several remain open, including detector quality, local-model evaluation and the
managed-server function inventory. Nothing here was marked done because its
requirement was written down.

Windows compiles and does not run: there is no named pipe and no single-daemon
election. MySQL does not ship, because its driver discards the column origin
`internal/integration/engines_test.go` measures, and name matching is not an
acceptable substitute.
