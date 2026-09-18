---
name: keeper-install
description: Install keeper, or work out why it is not working. Use when keeper's MCP server fails to start, when a keeper tool reports the vault is locked or the daemon is unreachable, when a connection has unaccepted privilege findings, or when the user asks to install, update, or set up keeper. Do every part the environment allows, then hand over a precise list of what only a person can do.
version: 1.0.0
---

# Installing keeper, and getting it working

Installing is not the hard part; **install has state in it**, and that is why this
is a skill rather than a paragraph in a README. Is the binary there, is it the
right version, is the daemon running, is the vault unlocked, is a connection
registered, are its privilege findings accepted, is the catalog classified. Any
of those can be the reason nothing works, and they have different fixes.

Work down the list. Stop at the first thing that is wrong, fix it, then carry on
from there rather than starting again.

## 1. Is it installed

```sh
keeper version
```

Two numbers come back — the client's and the daemon's — because they refuse each
other on a mismatch and one number would be the wrong answer.

**If keeper is already installed, it updates itself** — the manual download
below is only for a first install, or for a version too old to have the command:

```sh
keeper update --check    # what is published, against what is running
keeper update            # verify, replace, restart onto it
```

It stops without touching anything when the restart would cost something —
sessions connected, an approval waiting — and names what it found, so `--force`
is a decision rather than a surprise. **You may run `keeper update`**; the cost
it guards is the same one that makes `daemon restart` yours to ask for, and the
command asks on your behalf. It refuses outright when the binary belongs to a
package manager, and says so.

`command not found` means install it. **Prefer a release over building**: it is
faster, and it comes with checksums you can actually verify.

```sh
# Work out the platform first
uname -s   # Linux | Darwin
uname -m   # x86_64 → amd64,  aarch64/arm64 → arm64
```

Then, filling in version and platform:

```sh
cd "$(mktemp -d)"
curl -fsSLO https://github.com/mtch3n/keeper/releases/latest/download/SHA256SUMS
curl -fsSLO "https://github.com/mtch3n/keeper/releases/latest/download/keeper_<version>_<os>_<arch>.tar.gz"
sha256sum -c SHA256SUMS --ignore-missing     # shasum -a 256 -c on macOS
tar xzf keeper_<version>_<os>_<arch>.tar.gz
install -m755 keeper_<version>_<os>_<arch>/keeper ~/.local/bin/
```

**Verify the checksum and say that you did.** This binary is about to hold a
database credential; skipping the one step that checks what you downloaded would
be an odd way to start.

`~/.local/bin` is the right destination when it is on PATH — check with
`echo $PATH`. If it is not, either say so and let the user add it, or use
`/usr/local/bin`, which needs `sudo` and is therefore theirs to run, not yours.

Building from source needs Go 1.27 and pnpm:

```sh
git clone https://github.com/mtch3n/keeper && cd keeper
make build && install -m755 bin/keeper ~/.local/bin/
```

One binary, three entry points: `keeper` is the CLI, `keeper daemon` runs the
daemon, `keeper mcp` is the MCP server a harness talks to.

## 2. Is the daemon running

```sh
keeper daemon status
keeper daemon start      # starts it if not
```

`keeper doctor` is the fuller answer and it **works without the daemon** — that
is the point of it, since the daemon not starting is exactly when you need it.
It reports the socket, the lockfile, the key source and each connection's state.

If `version` reports two different numbers, an old daemon is still running from
before an upgrade. `keeper daemon restart` replaces it. **Tell the user what that
costs before doing it**: it cancels every pending approval and permanently
invalidates every token any agent session is holding, because the reverse map is
memory and the tokens cannot be resolved again. Never restart it on your own
initiative.

## 3. Is the vault unlocked

The daemon starts locked and every tool returns `vault_locked` until it is
opened.

```sh
keeper vault unlock
```

**This is the user's to run, not yours.** It reads a passphrase from a terminal
and refuses a pipe. On a desktop the OS keychain usually resolves without one;
`keeper doctor` names the key source actually in use, which is worth reading — a
silent fall back to a weaker source would be a defect, so it is always reported.

## 4. Is there a connection

```sh
keeper connection ls
```

Registering one needs a database connection string, which means it needs the
user. Offer them both paths and let them pick:

- `keeper ui` prints a local URL; registering there is the flow that was designed
  for it, and it walks the findings one at a time.
- `keeper connection add --name <name> --dsn '<connection string>'` if they would
  rather stay in the terminal.

**Never ask them to paste a connection string into the conversation**, and if
they do it anyway, do not repeat it back and do not put it in a command you echo.

## 5. Are the findings accepted

A new connection is stored **disabled**. keeper audits the credential's
privileges and reports what it found — it never refuses one, and a master account
works — but each finding has to be accepted by name before anything runs.

```sh
keeper connection show <name>          # what is outstanding
keeper connection accept <name> --finding <id>
```

Read the findings out to the user and let them decide. Each one comes with the
narrower grant that would remove it, and the narrower grant is usually the better
answer: a privilege the role does not hold is one keeper cannot get wrong.

Accepting `rolsuper` is a real decision, not a formality. It means keeper's
database-level protection does not apply to that connection, and after it keeper
is doing one job — reducing what reaches your context — with nothing underneath
it. Say that plainly. Do not accept findings on the user's behalf unless they
have said which ones.

## 6. Is the catalog classified

```sh
keeper catalog init <name> --sample 200
```

Every unclassified column comes back redacted, which is safe and not useful, so
this is what turns a working install into a usable one. It proposes a policy for
each column and groups the proposals: typed scalars and name matches are safe to
accept in bulk, and free text that no rule matched is the review task — that is
where unnamed name and address columns live, and the pattern pass cannot find
them.

`keeper ui` is the better place to work through that, because it is a table of
hundreds of rows and the terminal is not.

## 7. Connect the harness

The Claude Code plugin registers the MCP server itself. Elsewhere, point the
client at `keeper mcp` over stdio:

```json
{ "mcpServers": { "keeper": { "command": "keeper", "args": ["mcp"] } } }
```

Then reconnect the server and check that keeper's tools are listed.

## What to hand back rather than do

- `sudo` anything.
- `keeper vault unlock` — it wants a terminal and a human.
- Choosing which privilege findings to accept.
- Supplying a connection string.

Finish by telling them plainly what is done, what is left, and the exact command
for each remaining step. A half-finished install that reports success is worse
than one that says which step it stopped at.
