package daemon

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Pipeline is internal/pipeline: G1–G9 and computed-column inheritance.
//
// The interface is declared here because the daemon is its consumer. Its
// signatures name only internal/types and internal/ports, because CONTRACT §1
// forbids internal/pipeline from importing this package — a request struct
// declared here could not be named on the other side of the seam.
//
// Two ownership rules the signatures encode:
//
//   - The pipeline never mints a ticket id. Query returns a ticket *template*
//     with an empty ID; the daemon generates the 128 bits of entropy, binds the
//     ticket to the issuing session and connection and owns its lifetime
//     (SPEC R3.4b).
//   - The pipeline never sees a token parameter. The daemon resolves tokens
//     against the requesting session's reverse map and passes bound parameters
//     (SPEC R6.2, and ports.Param's own comment).
//
// types.Session rather than a session id, because the pipeline needs the client
// and the intent for the audit record (§10), and only the
// daemon can resolve an id to either.
type Pipeline interface {
	// Explain is §6.1's dry run: the statement is planned and classified but
	// never executed, so an agent can discover that it would escalate without
	// spending an approval.
	Explain(ctx context.Context, sess types.Session, connectionID, sql string, params []ports.Param) (*types.ExplainResult, error)

	// Query runs G1–G9. Exactly one of the first two results is non-nil on
	// success: a *types.QueryResult when the statement ran, or a ticket
	// template when it must wait for a person, carrying State, Reason and
	// AuditID. A refusal is a *types.Error.
	//
	// authorization is the basis the daemon already established, "" or
	// "ticket:id", and is what the pipeline reports in QueryResult and records
	// in the audit log. maxRows is the agent's request; the per-connection
	// ceiling of §4.5 is still the pipeline's to enforce.
	Query(ctx context.Context, sess types.Session, connectionID, sql string, params []ports.Param, maxRows int, authorization string) (*types.QueryResult, *types.Ticket, error)

	// Facts is §9.2's approval payload — intent, statement type, relations,
	// estimates, what will be masked and why this escalated — for a statement
	// Query escalated. The daemon calls it once, when it enqueues.
	Facts(ctx context.Context, sess types.Session, connectionID, sql string, params []ports.Param) (*types.ApprovalFacts, error)

	// CommitWrite is R4.2d's third step: a fresh transaction on a fresh
	// connection, after a human approved the ticket the daemon passes as
	// authorization.
	CommitWrite(ctx context.Context, sess types.Session, connectionID, sql string, params []ports.Param, authorization string) (*types.QueryResult, error)
}
