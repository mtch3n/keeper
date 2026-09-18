package pgdb

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mtchen/keeper/internal/types"
)

// Error conversion is this package's exclusive job. internal/pgdb is the only
// package that ever holds a *pgconn.PgError and it converts before returning
// (CONTRACT §4 rule 1, SPEC R6.4a).
//
// The conversion is an allowlist, not a denylist. Exactly two fields of the
// PgError are read:
//
//	Code     the five-character SQLSTATE, mapped to a coarser keeper code and
//	         then discarded — keeper codes disclose less than SQLSTATE does
//	Routine  the name of the C function in the server that raised the error. It
//	         is a compile-time constant of the server's source and can never
//	         carry a value; it is compared, never emitted, and it is the only
//	         thing that separates "you sent two statements" from "you sent one
//	         that does not parse", which share SQLSTATE 42601.
//
// Message, Detail, Hint, Position, InternalPosition, InternalQuery, Where,
// SchemaName, TableName, ColumnName, DataTypeName, ConstraintName, File, Line,
// Severity and SeverityUnlocalized are never read at all. Notices are dropped at
// the connection (see newPool). A denylist already missed ConstraintName once,
// which is why this is written the other way round.
//
// The original error is not wrapped: convert returns a fresh *types.Error and
// drops what it was given, so no caller anywhere can unwrap its way back to a
// PostgreSQL string.

// summaries are composed by keeper from the code alone. SPEC R6.4a: never copied
// from the server, because PostgreSQL interpolates offending values into its
// messages — SELECT ssn::integer FROM users puts the SSN in the error text, and
// there is no result row, so redaction never runs.
var summaries = map[types.Code]string{
	types.CodeSyntax:            "the statement did not parse, or it names a relation, column or function this connection cannot use",
	types.CodeMultiStatement:    "the input contained more than one statement; keeper runs exactly one",
	types.CodePermissionDenied:  "the database refused this statement for this connection's role",
	types.CodeTimeout:           "the statement exceeded this connection's statement timeout",
	types.CodeApprovalRequired:  "the statement modifies data; writes are approved before they run",
	types.CodeVaultLocked:       "the vault holding this connection's credential is locked",
	types.CodeNoWriteCredential: "this connection has no write credential, so it cannot modify data",
	types.CodeInternal:          "the database did not complete this statement",
}

// actions are operator instructions from a closed set. SPEC §6.4.
var actions = map[types.Code]string{
	types.CodeSyntax:            "correct the statement, or run get_schema to see what this connection may name",
	types.CodeMultiStatement:    "send one statement per call",
	types.CodePermissionDenied:  "ask an operator to widen this connection's grants",
	types.CodeTimeout:           "narrow the statement, or ask an operator to raise statement_timeout",
	types.CodeApprovalRequired:  "resubmit the statement so it can be previewed and approved",
	types.CodeVaultLocked:       "run keeper vault unlock",
	types.CodeNoWriteCredential: "ask an operator to register a write credential for this connection",
	types.CodeInternal:          "retry; tell an operator if it persists",
}

// keeperError builds the only error shape that leaves this package.
func keeperError(code types.Code) *types.Error {
	return &types.Error{Code: code, Summary: summaries[code], Action: actions[code]}
}

// convert maps any error produced by the server, the driver or the context into
// a *types.Error. It is total: a non-nil argument always yields a non-nil
// *types.Error, and the argument itself is discarded.
func convert(err error) error {
	if err == nil {
		return nil
	}
	// An error this package already composed passes through unchanged.
	if ke, ok := errors.AsType[*types.Error](err); ok {
		return ke
	}
	if pg, ok := errors.AsType[*pgconn.PgError](err); ok {
		return keeperError(codeForSQLState(pg.Code, pg.Routine))
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return keeperError(types.CodeTimeout)
	case errors.Is(err, pgx.ErrNoRows):
		// Not an error condition for keeper: a caller asking for one row that is
		// absent gets an empty result, never a server string.
		return keeperError(types.CodeInternal)
	default:
		return keeperError(types.CodeInternal)
	}
}

// codeForSQLState maps a SQLSTATE to keeper's coarser closed set. The mapping is
// by class where a class is uniform and by exact code where it is not.
func codeForSQLState(sqlstate, routine string) types.Code {
	switch sqlstate {
	case "57014": // query_canceled — statement_timeout fired
		return types.CodeTimeout
	case "25006": // read_only_sql_transaction
		return types.CodeApprovalRequired
	case "42501": // insufficient_privilege
		return types.CodePermissionDenied
	case "42601": // syntax_error
		// exec_parse_message raises SQLSTATE 42601 for exactly one condition:
		// "cannot insert multiple commands into a prepared statement". Every
		// other 42601 comes from the grammar.
		if routine == "exec_parse_message" {
			return types.CodeMultiStatement
		}
		return types.CodeSyntax
	}

	switch class := className(sqlstate); class {
	case "28": // invalid_authorization_specification
		return types.CodePermissionDenied
	case "42": // syntax_error_or_access_rule_violation — undefined table, column, function
		return types.CodeSyntax
	case "3D", "3F": // invalid_catalog_name, invalid_schema_name
		return types.CodeSyntax
	case "53", "54", "55", "57", "58", "08", "XX", "F0": // resources, intervention, connection, internal
		return types.CodeInternal
	default:
		// Data exceptions (22), constraint violations (23), transaction state
		// (25), and everything else collapse to one code. Distinguishing them
		// would disclose why a statement failed, which is the channel R6.4a and
		// §11.5 narrow rather than widen.
		return types.CodeInternal
	}
}

func className(sqlstate string) string {
	if len(sqlstate) < 2 {
		return ""
	}
	return sqlstate[:2]
}

// isCode reports whether a converted error carries a particular keeper code.
func isCode(err error, code types.Code) bool {
	ke, ok := errors.AsType[*types.Error](err)
	return ok && ke.Code == code
}

// notExplainable reports whether an EXPLAIN attempt failed because the statement
// is not an ExplainableStmt in PostgreSQL's grammar. The statement itself has
// already been Parsed successfully by the time this is asked, so a syntax error
// on the EXPLAIN-prefixed form can only mean the grammar refused the prefix —
// which is the server's own answer to "is this DDL or another utility command".
// No keeper-authored lexer is involved (R7.2).
func notExplainable(err error) bool {
	pg, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pg.Code == "42601" && !strings.EqualFold(pg.Routine, "exec_parse_message")
}
