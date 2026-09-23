package daemon

import "github.com/mtchen/keeper/internal/types"

// keeperErr composes the only error shape that may reach an agent. Every summary
// here is written by keeper: SPEC R6.4a forbids copying a server's text, because
// PostgreSQL interpolates offending values into its messages.
func keeperErr(code types.Code, summary, action string) *types.Error {
	return &types.Error{Code: code, Summary: summary, Action: action}
}

var (
	errUnknownConnection = keeperErr(types.CodeTicketUnknown,
		"no connection with that id is registered",
		"run `keeper connection list`")

	errTicketUnknown = keeperErr(types.CodeTicketUnknown,
		"no ticket with that id belongs to this session",
		"re-run the query that produced the ticket")

	errRequestUnknown = keeperErr(types.CodeTicketUnknown,
		"no local request with that id belongs to this session",
		"open a new request")

	errIntentRequired = keeperErr(types.CodePermissionDenied,
		"this session has not declared an intent, and every statement is recorded against one",
		"call set_session_intent before querying")

	errIntentScreened = keeperErr(types.CodePermissionDenied,
		"the intent text matched a sensitive-data rule and was not recorded",
		"restate the intent without identifying values")

	errSessionRequired = keeperErr(types.CodePermissionDenied,
		"this route requires an agent session established on this connection",
		"call POST /v1/session first")

	errStaleToken = keeperErr(types.CodeStaleToken,
		"this token cannot be resolved in this session: the reverse map is memory only, so a daemon restart or a reconnect makes every token already held permanently unresolvable",
		"re-run the query that minted the token")

	errNoDecision = keeperErr(types.CodeApprovalRefused,
		"that ticket is no longer waiting for a decision",
		"re-run the query")

	errWriteGrant = keeperErr(types.CodePermissionDenied,
		"no grant authorizes a write: a write needs its own approval every time",
		"approve the ticket")

	// A local request is one-use (R8.7c). A replay is reported as a conflict
	// with the state the daemon already holds rather than as a refusal, because
	// nobody refused anything: the decision was already made.
	errAlreadyUsed = keeperErr(types.CodeApprovalRequired,
		"that local request has already been answered, cancelled or expired",
		"open a new request")

	errInternal = keeperErr(types.CodeInternal,
		"keeper could not complete the request",
		"run `keeper doctor`")
)

func errValidation(field, reason string) *types.Error {
	return keeperErr(types.CodeSyntax, field+" "+reason, "correct the request and retry")
}
