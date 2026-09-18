# Contributing

## Commit messages

The changelog is generated from them, so the subject line follows
[Conventional Commits](https://www.conventionalcommits.org):

```
feat(catalog): sample numeric columns for SSN-shaped values
fix(redact): re-tokenize resolved values found inside json output
measure(pgdb): view columns report the view's own identity, not the base column's
spec(R8.5g): a network namespace alone does not isolate a sidecar
ci: pin staticcheck to a version that can read the toolchain's export data
```

`measure` is keeper's own type and it is not decoration. A measurement changes
what the project claims about itself — that MySQL cannot ship, that a namespace
is not enough — and burying that under `chore` would hide the thing a reader of
the changelog most wants to find.

A commit that does not follow the convention is not rejected. It lands under
*Other* in the changelog, because dropping it would make the record untrue.

**The body carries the reasoning.** The subject says what changed; the body says
why, and what breaks if it is changed back. A diff shows the first and a reader
six months out needs the second.

## Before opening a pull request

```sh
make check
```

That is `gofmt`, `go vet`, `staticcheck`, the `EXPLAIN ANALYZE` lint, `go test
-race`, `oxlint` and the UI audit. CI runs the same gates plus the integration
suite, which needs Docker:

```sh
go test -tags integration ./internal/integration/...
```

## The parts with rules of their own

**Output columns are matched by `(tableOID, attnum)`, never by name.** Name
matching has to enumerate aliases, CTEs, views and function wrappers, and that
enumeration is the failure shape behind the CVEs this project is built around. A
change that reintroduces name matching will not be merged whatever it fixes.

**No PostgreSQL error field reaches a response.** `internal/pgdb` converts and
nothing downstream sees the original. PostgreSQL puts the offending value into
its error text, and an error is an egress channel like a row.

**The audit log never receives a literal, a comment, or a result row.**

**Nothing on the MCP surface can register a connection, accept a privilege
finding, approve, grant, or edit the catalog.** An agent that could approve its
own query would be removing its own supervision.

**The frontend follows `web/COMPONENTS.md` and the UI audit.** A component that
is not documented there fails the build, and the documentation is the proof that
composition of existing primitives could not do the job.

## Measurements

If you are closing one of the open questions, the result belongs in
`internal/integration` as a test that states what it establishes and what follows
if the answer comes back the other way — not as a line in a document saying it
was checked once. The existing tests are the pattern.
