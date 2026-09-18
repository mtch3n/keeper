---
name: keeper
description: Open keeper's local input or authorization form so a human can hand over a sensitive value or unlock a relation without it ever entering this session.
---

# $keeper

keeper masks what a database query returns. Some queries need a sensitive
value going in first — an email, a name, an account number — and that value
must never appear in this session: not in `$keeper`'s own argument, not in a
tool call, not in anything that gets logged or sent anywhere. A human types
it once, in a small local form on their own machine.

`$keeper` is a skill invocation, not a private channel for parameters:
whatever text follows it should describe the task only, never the value.

## Flow

Given `$keeper <non-sensitive task>`:

1. Pick the right connection and token namespace (`list_connections`,
   `describe_connection` if you need to check).
2. Call `request_input(connection_id, namespace, purpose)`, with `purpose`
   holding only the task description — no value, ever.
3. Show the user the returned `url` as a plain link and explain in one line
   what to enter there and why this session can't take it directly.
4. Call `get_input_result(request_id, wait_ms: 25000)` and repeat while
   `state` is `pending_input`. On `cancelled` or `expired`, say so plainly
   and offer to start over.
5. On `ready`, you get a `token` and `namespace` and nothing else. Bind it as
   a query parameter — `query(connection_id, sql, params: [{"token": "<token>"}])`
   — never interpolated into the SQL string.

`$keeper` with no task means the user wants the dashboard: tell them to run
`keeper ui` from a terminal. Do not guess at the loopback port; it is not
part of this session.

A blocked `query`/`explain` call may itself return a loopback link for a path
authorization instead of a value — handle it the same way (link, one-line
explanation, poll `get_authorization_result`), but expect only a state back,
never a token.

## Do not

- Do not accept a sensitive value as a `$keeper` argument, a tool argument,
  or free text in this session, and do not echo one back if a user pastes it
  anyway — point them at the form instead.
- Do not write a command example anywhere that shows a raw value as an
  argument to `keeper`, `$keeper`, or any other command.
