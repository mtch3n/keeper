# keeper — Claude Code plugin

Installs keeper's skills, the `/keeper:ui` command, and the MCP server
registration in one step.

```
/plugin marketplace add mtch3n/keeper
/plugin install keeper@keeper
```

The plugin declares an MCP server that runs `keeper mcp`, so the `keeper` binary
has to be on your `PATH`. If it is not, ask your agent — the `keeper-install`
skill below does it — or take a release archive and verify it against
`SHA256SUMS`.

It does not ship the binary, and that is deliberate rather than an omission.
keeper holds database credentials and audits privileges; an executable that
arrives as a side effect of installing a plugin is not one anybody chose to
trust, and plugin distribution carries no checksum. Downloading it from a release
and verifying it is a better trust story, not a worse one.

## What it adds

**The `keeper` skill** makes keeper the default path to a database rather than a
special case. It is the one file that decides whether the product works in
practice: it explains what a masked column means, how to wait for a human
without ending the turn, how to query by a token you cannot read, and what not
to do — including not asking for a sensitive value in chat, and not probing a
masked column to recover what is under it.

**The `keeper-install` skill** gets it working. Installing has state in it — is
the binary there, is it the right version, is the daemon running, is a connection
registered, is the catalog classified — and each of those has a different fix.
The skill walks them, does what it can, and hands back the parts only a person
can do: `sudo`, the connection string, and any privilege change the audit
suggests.

**The `/keeper:ui` command** prints the dashboard's address. That is all it
does, and it is a command rather than part of a skill because the port is chosen
at startup and no session can guess it.

The value-handover flow is not a command. Ask for the thing you want — "find the
orders for a customer's email" — and the skill opens a form on your own machine,
you type the email there, and the conversation receives a token instead. It is
the model that has to drive that exchange, so it belongs in the skill; a command
doing the same work would be a second copy of it under a name you have to
remember.

## What it deliberately does not add

No approval command, no connection registration, no catalog editing. Those are
reachable from the CLI and the web UI and from nowhere else — an agent that
could approve its own query, or re-run the privilege audit on the credential
it is being masked by, would be removing its own supervision. The plugin cannot
grant what the MCP surface does not expose.

## Codex

`codex/keeper.md` is the same flow for Codex's `$keeper`. Copy it into your
skills directory; Codex has no plugin format to install into.
