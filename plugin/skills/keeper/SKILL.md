---
name: keeper
description: Query a database through keeper, which masks what comes back before it reaches this conversation. Use this whenever your task touches a database at all — reading rows, exploring a schema, checking a count, verifying a migration, debugging against real data — and whenever the user mentions keeper, a masked column, a token, an approval, or a connection. It is the default path to the database, not a special case.
version: 1.0.0
---

# keeper

keeper is a database proxy that sits between you and the rows. You send SQL, it
runs it, and it masks the result before you ever see it: an email comes back as a
token you can still join on, an SSN comes back as nothing, a free-text note comes
back with the phone number inside it blacked out.

Use it whenever the task touches the database. If you are tempted to reach for a
connection string, a `psql` invocation, or your own credentials, use keeper
instead — not because those are forbidden, but because everything you read
through them lands in this conversation, gets sent to a model provider, and ends
up in the transcript, the summary and whatever you write afterwards. That is the
problem keeper exists to solve, and going around it re-creates it exactly.

## Say what you are doing first (required)

Call `set_session_intent` once, with a short description of the task — a ticket
id, or the question you are answering. keeper refuses queries until you do.

It is not bookkeeping. When a statement escalates, a human sees your intent
beside the SQL and decides on that; "reconcile duplicate accounts OPS-441" gets
approved and "" does not. Keep credentials and personal data out of it: keeper
screens the text and rejects it if it finds any.

## The shape of a query

```
explain(connection_id, sql, params?)   what would happen, without spending an approval
query(connection_id, sql, params?, max_rows?)
```

`explain` is worth using before anything expensive or anything you are unsure
about. It tells you which columns would come back masked and whether the
statement would stop for a human, and it costs nothing.

`query` returns one of two things:

- **rows**, with a `transforms` map saying what was done to each column, or
- **a ticket**, meaning a human has to decide. Then you wait.

## Waiting for a ticket

```
get_result(ticket, wait_ms: 25000)
```

`wait_ms` is capped at 25000 and keeper rejects anything larger, so never try to
wait longer in one call. The call returns the moment a human decides, or after 25
seconds, whichever comes first — and a call that runs out hands you back the same
pending state. **That is a checkpoint, not an answer.** Call it again, and keep
calling until the state is terminal.

**Never end your turn while a ticket you opened is still pending.** Once your
turn is over the human's decision reaches nobody, and in a headless run the
result is lost for good. If you have other useful work, do it and come back; if
you do not, roll the 25-second waits.

A human may take minutes. That is normal and it is the design: keeper escalates
only where a person can actually change the outcome.

## Reading what came back

`transforms` tells you what you are allowed to conclude. Read it; do not assume
a column you can see is a column that arrived intact.

```
allow      the value, unchanged
token      ⟨email1:a3f21b4c9d8e7f60⟩ — equal values give equal tokens, so joins,
           GROUP BY and COUNT DISTINCT all still work. You cannot read it, and
           you are not meant to
partial    one declared component kept: ████@acme.example, 453211██████3333
redact     present, nothing survived
drop       the column is not in the response at all
scan       free text with matched spans blacked out inside it
```

Two mistakes to avoid. A masked value is **not** a missing value: `████` means
there is an SSN there, not that the row lacks one, and a summary that says
"three customers have no email" when the emails were tokenized is wrong. And a
count over a masked column is still a real count — masking changes what you can
read, not what is there.

If `transforms` says `basis: "sampled"`, the detection layer looked at a sample
rather than every row. Say so if it matters to the conclusion.

## Querying by a value you cannot see

You can filter on a token without ever knowing what it stands for:

```
query(conn, "SELECT * FROM orders WHERE email = $1", params: [{"token": "⟨email1:a3f21b…⟩"}])
```

keeper resolves it on its side and binds it as a parameter. **Never** paste a
token into the SQL text — pass it in `params`. A token only works in the session
that minted it, and if the daemon restarts every token you are holding becomes
permanently unresolvable; re-run the query that produced it.

## When the task needs a value nobody should type here

A user asking you to "find the orders for this customer's email" has a value you
must not see and must not ask for. Do not ask them to paste it. Do not accept it
if they paste it anyway — do not repeat it, do not put it in SQL, and tell them
to use the form instead.

Instead:

1. `request_input(connection_id, namespace, purpose)` — `purpose` is the
   non-sensitive task description, never a value.
2. Show the returned `url` as a plain clickable link with one line saying what
   to enter and why.
3. Poll `get_input_result(request_id, wait_ms: 25000)` while it is
   `pending_input`. `cancelled` or `expired` means say so and offer a fresh one.
4. `ready` gives you a **token and a namespace, nothing else**. Use it as a
   parameter, as above.

The value goes from their keyboard to keeper on their machine. It does not pass
through this conversation, the slash command, a tool argument, or any transcript.

## When keeper says a path needs authorization

A blocked `query` may come back with a loopback link of its own: a human has to
grant that exact relation before it can be read. Surface it the same way — a
plain link and one line — then poll `get_authorization_result`. That result is a
**state only and never a token**; nothing comes back into your session except
permission for the next statement to run.

Each grant covers exactly the path it names. A join reaching one granted relation
and one ungranted one still stops.

## What keeper will refuse, and what to do about it

| It says | What it means |
|---|---|
| `unclassified` | a column has no policy yet. It came back redacted and the statement still ran. The fix is a human editing the catalog, not a retry |
| `denylisted` | that relation is off-limits on this connection. No approval overrides it; ask the user to change the list if they meant to allow it |
| `permission_denied` | the database refused. The message names the columns you *may* read — use them |
| `ddl_refused` | keeper does not run DDL, at any tier. Schema changes belong to the project's migration tooling |
| `vault_locked` | nobody has unlocked keeper yet. Ask the user to run `keeper vault unlock` |
| `connection_disabled` | the credential has privilege findings nobody has accepted. Ask them to open keeper and accept them |

Errors never carry the database's own message. keeper composes them, because
PostgreSQL puts the offending value into its error text and that is an egress
path like any other. If an error seems vague, that is deliberate; `explain` will
usually tell you more than a retry will.

## Things not to do

- Do not ask for a raw sensitive value in chat, in `/keeper`'s argument, or in
  any tool call. The form is the only place one is typed.
- Do not try to defeat the masking — no `substr`, `ascii`, `length` or
  narrowing-range probing of a masked column to recover what is under it. keeper
  cannot reliably detect that and the user is trusting you not to.
- Do not register connections, accept privilege findings, approve, grant or edit
  the catalog. Those are not exposed to you, deliberately: they are the human's.
- Do not present a masked result as if it were complete, and do not present
  keeper's own summaries as the database's data.
