---
name: keeper-install
description: Install keeper, or work out why it is not working. Use when keeper's MCP server fails to start, when a keeper tool reports the daemon is unreachable, when no connection is registered or a connection is unusable, or when the user asks to install, update, or set up keeper. Do every part the environment allows, then hand over a precise list of what only a person can do.
version: 1.1.0
---

# Installing keeper, and getting it working

Install has state in it: is the binary there, is it on PATH, is the daemon
running, is a connection registered. Any of those can be the reason nothing
works, and they have different fixes. Work down the list. Stop at the first thing
that is wrong, fix it, then carry on.

## 1. Is it installed

The plugin's launcher installs keeper itself. When the MCP server starts and
keeper is missing, the launcher downloads the latest release, checks it against
the release's `SHA256SUMS`, and installs it in `~/.local/bin/keeper`. If the
launcher printed an error on stderr, read it: it names the cause (no network, an
unsupported platform, a checksum mismatch).

```sh
keeper version
```

Two numbers come back, the client's and the daemon's, because they refuse each
other on a mismatch.

`command not found` has two causes. Either keeper is not installed, or it is
installed in `~/.local/bin` and that folder is not on PATH. Check:

```sh
ls -l ~/.local/bin/keeper
echo $PATH
```

If the file exists, the person adds the folder to PATH in their shell profile:
`export PATH="$HOME/.local/bin:$PATH"`. The plugin's hooks and launcher already
look in `~/.local/bin`; only the person's own terminal needs the PATH line. If the
file does not exist, start the MCP server again (reconnect it) so the launcher
installs it, or take a release archive by hand and verify it against
`SHA256SUMS`.

An installed keeper updates itself:

```sh
keeper update --check    # what is published, against what is running
keeper update            # verify, replace, restart onto it
```

It stops when the restart would cost something (sessions connected, a ticket
waiting) and names what it found. Tell the user what that cost is before you use
`--force`.

## 2. Is the daemon running

```sh
keeper daemon status
keeper daemon start
```

`keeper doctor` is the fuller answer and works without the daemon. It reports the
socket, the key source and each connection's state.

If `version` reports two different numbers, an old daemon is still running from
before an upgrade. keeper's MCP server restarts it on its next call when nothing
waits on a person. When the error says approvals, tickets or local requests are
waiting, `keeper daemon restart` replaces it. **Tell the user what that costs
first**: it cancels what is waiting. Never restart it on your own initiative.

## 3. Is there a connection

```sh
keeper connection ls
```

If the list is empty, the person has to register one. **An agent never asks for a
connection string, and never handles one.** Tell the person to run this in their
own terminal:

```sh
keeper connect
```

It reads the connection string at a hidden prompt and makes one profile. The
profile reads and masks, and it cannot write. The person can also use `keeper ui`
to open the dashboard.

If the person pastes a password or connection string into the conversation
anyway, do not repeat it and do not put it in a command.

Prefer a database role that is not a superuser. keeper's transaction stops
database writes on reads, but it cannot stop what a superuser does outside the
transaction.

The vault opens itself from the OS keychain. If the person resets their login
keyring, the registered connections are lost unless they made a copy with
`keeper vault export`. Say so once.

## 4. Check the first query

Run a small `query` on the new profile. An email column should come back as a
token like `⟨email1:…⟩`. The result's `hint` names the one command that changes a
column: `keeper catalog edit <profile> <schema.table.column> --policy <policy>`.
The person runs it. Settings such as `reads`, `writes` and `detection` are
changed with `keeper connection set`, also by the person.

## 5. Connect the harness

The plugin registers the MCP server itself. For another client, point it at
`keeper mcp` over stdio:

```json
{ "mcpServers": { "keeper": { "command": "keeper", "args": ["mcp"] } } }
```

Then reconnect the server and check that keeper's tools are listed.

Codex needs two things from the person. It runs a plugin hook only after the
person trusts it (`/hooks`). To skip a second tool-approval question, the person
sets `default_tools_approval_mode` for the keeper server in the Codex config. The
README has the exact lines.

## What to hand back rather than do

- `sudo` anything.
- `keeper connect`, `keeper catalog edit`, `keeper connection set` and
  `keeper vault export`. They are the person's.
- Supplying a connection string.

Finish by telling the person what is done, what is left, and the exact command for
each remaining step.
