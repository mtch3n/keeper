---
name: keeper
description: Query a database through keeper, which masks what comes back before it reaches this conversation. Use this whenever your task touches a database at all — reading rows, exploring a schema, checking a count, verifying a migration, debugging against real data — and whenever the user mentions keeper, a masked column, a token, an approval, or a connection. It is the default path to the database, not a special case.
version: 1.1.0
---

# keeper

keeper is a database proxy that sits between you and the rows. You send SQL, it
runs it, and it masks the result before you ever see it: an email comes back as a
token you can still join on, a sensitive value comes back redacted, a free-text
note comes back with the phone number inside it blacked out.

Use it whenever the task touches the database. If you are tempted to reach for a
connection string, a `psql` invocation, or your own credentials, use keeper
instead — not because those are forbidden, but because everything you read
through them lands in this conversation, gets sent to a model provider, and ends
up in the transcript, the summary and whatever you write afterwards. That is the
problem keeper exists to solve, and going around it re-creates it exactly.

## The normal path

```
list_connections()                    every profile, with its name and whether it allows writes
query(connection_id, sql, params?, max_rows?)
```

`query` returns rows on the same call. When the profile's settings need a person
to accept first, the client shows them a dialog and the same call returns the
rows when they accept, or a refusal when they decline. You wait inside the call;
you do not poll.

Each profile is one login. When the user names one — "use orders_rw" — find it
in `list_connections` and send the statement on it. `connection_id` takes the
id or the exact profile name, so `shop_ro` works without a lookup first. If two
profiles share a name, the error lists their ids. keeper never switches
profiles for you, and never shows you a password.

`explain(connection_id, sql, params?)` says what a statement would touch, which
columns would come back masked, and whether it would run, ask or be refused. It
costs nothing. `get_schema` and `describe_connection` show the tables and what
the profile's `reads` and `writes` settings do.

## The fallback: a ticket with a link

If the client cannot show a dialog, or the person pressed Esc, `query` returns a
**ticket** and a local link instead of rows. Tell the person to open the link
(or answer the desktop notification), then poll:

```
get_result(ticket, wait_ms: 25000)
```

`wait_ms` is capped at 25000. The call returns when a person decides, or after 25
seconds with the same pending state. **That is a checkpoint, not an answer.**
Call it again until the state is terminal: `ready`, `approved`, `refused`,
`expired`, `cancelled` or `failed`.

**Never end your turn while a ticket you opened is still pending.** Once your
turn is over the decision reaches nobody, and in a headless run the result is
lost. If you no longer need the statement, withdraw it with `cancel(ticket)`.

## Writes

A write runs only on a profile whose `writes` setting allows it. On a profile
with `writes` off it is refused at once with `writes_off`. Resubmit on another
profile only if the user meant that login; do not pick one to get past the
refusal. When `writes` is `ask`, a person sees the rows the write would change
and chooses explicitly.

## Reading what came back

`transforms` tells you what you are allowed to conclude. Read it; do not assume
a column you can see is a column that arrived intact.

```
allow      the value, unchanged
token      ⟨email1:a3f21b4c9d8e7f60⟩ — equal values give equal tokens, so joins,
           GROUP BY and COUNT DISTINCT all still work. You cannot read it, and
           you are not meant to
redact     present, nothing survived
scan       free text with matched spans blacked out inside it
```

A masked value is **not** a missing value: a redacted cell means there is a value
there, and a summary that says "three customers have no email" when the emails
were tokenized is wrong. A count over a masked column is still a real count.

If a result has a `hint`, keeper classified a column by itself. It names the one
command that changes that, which only the person can run. Pass it on; do not run
it.

If `transforms` says `basis: "unexamined"`, no detection ran over that free text
and it was redacted whole. Say so if it matters to the conclusion.

## Querying by a value you cannot see

You can filter on a token without knowing what it stands for:

```
query(conn, "SELECT * FROM orders WHERE email = $1", params: [{"token": "⟨email1:a3f21b…⟩"}])
```

keeper resolves it on its side and binds it as a parameter. **Never** paste a
token into the SQL text — pass it in `params`. A token works only in the session
that minted it. If the daemon restarts, every token you hold stops resolving
(`stale_token`); run the query that produced it again.

## When the task needs a value nobody should type here

A user asking you to "find the orders for this customer's email" has a value you
must not see and must not ask for. Do not ask them to paste it. Do not accept it
if they paste it anyway — do not repeat it, do not put it in SQL, and tell them
to use the form instead.

1. `request_input(connection_id, namespace, purpose)` — `purpose` is the
   non-sensitive task description, never a value.
2. Show the returned `url` as a plain clickable link with one line saying what
   to enter and why.
3. Poll `get_input_result(request_id, wait_ms: 25000)` while it is
   `pending_input`. `cancelled` or `expired` means say so and offer a fresh one.
4. `ready` gives you a **token and a namespace, nothing else**. Use it as a
   parameter, as above.

Invoked as `$keeper <task>` (Codex), the text after it describes the task only.
A value never goes in a `$keeper` argument or a tool argument. `$keeper` with no
task means the user wants the dashboard: tell them to run `keeper ui`.

## What keeper will say when it refuses

| Code | What it means |
|---|---|
| `unclassified` | a column has no policy yet. It came back redacted. The fix is a person editing the catalog, not a retry |
| `permission_denied` | the database refused. The message names the columns you *may* read — use them |
| `ddl_refused` | keeper does not run DDL. Schema changes belong to the project's migration tooling |
| `writes_off` | this profile does not allow writes |
| `syntax`, `multi_statement` | the statement did not parse, or held more than one statement |
| `approval_required`, `approval_refused` | a person has to accept first, or they refused |
| `unknown_connection` | no connection has that id or name, or the name is ambiguous. Call `list_connections` |
| `ticket_unknown`, `timeout` | the ticket is not known, or keeper did not finish in time. Retry once |
| `row_cap` | more rows than the ceiling allows. Narrow the query or lower `max_rows` |
| `stale_token` | the daemon restarted; run the query that made the token again |
| `unreachable` | keeper could not connect to the database. Tell the user |
| `internal` | a keeper fault. Say so |

Errors never carry the database's own message. keeper composes them, because
PostgreSQL puts the offending value into its error text and that is an egress
path like any other. If an error seems vague, that is deliberate; `explain` will
usually tell you more than a retry will.

## Things not to do

- Do not ask for a raw sensitive value in chat, in `/keeper`'s argument, or in
  any tool call. The form is the only place one is typed.
- Do not ask for or handle a connection string. If no profile exists, the person
  runs `keeper connect` in their own terminal.
- Do not try to defeat the masking — no `substr`, `ascii`, `length` or
  narrowing-range probing of a masked column to recover what is under it.
- Do not register connections, approve or edit the catalog. Those are not exposed
  to you, deliberately: they are the person's.
- Do not present a masked result as if it were complete, and do not present
  keeper's own summaries as the database's data.
