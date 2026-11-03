# keeper — plugin for Claude Code and Codex

Installs keeper's skills, the `/keeper:ui` command, the hooks and the MCP server
registration in one step.

```
/plugin marketplace add mtch3n/keeper
/plugin install keeper@keeper
```

The plugin's MCP server runs `bin/keeper-mcp-launch`. When `keeper` is not
installed, the launcher downloads the latest release, checks the archive against
the release's `SHA256SUMS`, and installs `keeper` in `~/.local/bin`. The checksum
comes from the same release as the binary. It guards against a corrupt download
and not against a compromised release.

`~/.local/bin` may not be on your `PATH`. The launcher and the hooks run
`~/.local/bin/keeper` anyway. The launcher prints on stderr how to add the folder
to `PATH`, which your own terminal needs for `keeper connect`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Then register a database in your terminal. An agent never asks for or handles a
connection string.

```sh
keeper connect
```

## What it adds

**The `keeper` skill** makes keeper the default path to a database. The agent
calls `query` and gets rows on the same call. A ticket with a link is only the
fallback when the client cannot show the approval dialog. The skill also explains
masked columns and tokens, and how to use `request_input` so a sensitive value
never goes through the chat.

**The `keeper-install` skill** gets keeper working and hands back what only a
person can do: `keeper connect`, `sudo`, and the PATH line.

**The `/keeper:ui` command** prints the dashboard's address.

**Hooks** run `keeper hook`. At session start they name the profiles. Before a
tool call they deny raw database clients aimed at a registered host and keeper
commands that only a person may run. Hooks are guidance, not enforcement.

## What it does not add

No approval command, no connection registration and no catalog editing. Those are
reachable from the CLI and the dashboard and from nowhere else. An agent that
could approve its own query would remove its own supervision.

## Codex

Codex runs a plugin hook only after you trust it: open `/hooks` and trust the
keeper hooks. The plugin cannot set tool approval. To avoid a second question
before each keeper tool call, set `default_tools_approval_mode` for the keeper
server in the Codex config. The repository README has the exact lines.
