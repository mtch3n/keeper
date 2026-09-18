package redact

import (
	"context"
	"github.com/mtchen/keeper/internal/ports"
)

// ResolvedParam is one bound parameter that came from a token (§6.2).
//
// Its Policy is the policy that caused the value to be tokenized in the first
// place, and it is what R8.4b's output floor is computed from.

// Statement is the per-statement context [Redactor.Apply] needs and the frozen
// ports.Redactor signature does not carry: which connection's token key to use,
// and which parameters were resolved from tokens.
//
// It travels in the context rather than in Redactor state because a session may
// have several statements in flight at once (CONTRACT §3), and per-session
// mutable "current statement" state is a race whose failure mode is applying one
// statement's parameter floor to another statement's output.

// WithStatement attaches per-statement facts to a context. The pipeline sets it
// before calling Apply.

// StatementFrom reads the facts back.
//
// A missing statement is not an error by itself: Apply still masks every column
// the pipeline resolved and still runs R8.4a's emission scan, which needs no
// connection. It becomes an error only when a token policy needs a key, which
// is the one thing that cannot be done without knowing the connection.

// The statement seam lives in ports, because both sides of it — the pipeline
// that attaches these facts and this package that reads them — are separate
// packages that must agree without importing each other. These aliases keep the
// names readable at the point of use.
type (
	// ResolvedParam is one parameter resolved from a token.
	ResolvedParam = ports.ResolvedParam
	// Statement is the per-statement context Apply needs.
	Statement = ports.Statement
)

// WithStatement attaches per-statement facts to a context.
func WithStatement(ctx context.Context, s Statement) context.Context {
	return ports.WithStatement(ctx, s)
}

// StatementFrom reads them back.
func StatementFrom(ctx context.Context) (Statement, bool) {
	return ports.StatementFrom(ctx)
}
