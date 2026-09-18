package pgdb

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mtchen/keeper/internal/types"
)

// sentinelPgError fills every string field of a PgError with a distinct marker,
// so a test can assert that none of them survives conversion. The markers stand
// in for what PostgreSQL actually puts there: SELECT ssn::integer FROM users
// interpolates the SSN into Message, and a user-chosen constraint name lands in
// ConstraintName, which is the field a denylist already missed once.
func sentinelPgError(sqlstate string) (*pgconn.PgError, map[string]string) {
	pg := &pgconn.PgError{
		Severity:            "SEV-111-111-1111",
		SeverityUnlocalized: "SEVU-222-22-2222",
		Code:                sqlstate,
		Message:             "MSG-333-33-3333",
		Detail:              "DET-444-44-4444",
		Hint:                "HINT-555-55-5555",
		Position:            42,
		InternalPosition:    43,
		InternalQuery:       "IQ-666-66-6666",
		Where:               "WHERE-777-77-7777",
		SchemaName:          "SCHEMA-888-88-8888",
		TableName:           "TABLE-999-99-9999",
		ColumnName:          "COLUMN-101-01-0101",
		DataTypeName:        "DTYPE-121-21-2121",
		ConstraintName:      "CONSTRAINT-131-31-3131",
		File:                "FILE-141-41-4141",
		Line:                99,
		Routine:             "ROUTINE-151-51-5151",
	}
	markers := map[string]string{}
	v := reflect.ValueOf(*pg)
	t := v.Type()
	for i := range t.NumField() {
		if t.Field(i).Type.Kind() != reflect.String {
			continue
		}
		name := t.Field(i).Name
		if name == "Code" {
			continue // SQLSTATE is read; the test asserts separately that it is not emitted
		}
		markers[name] = v.Field(i).String()
	}
	return pg, markers
}

func TestConvertDiscardsEveryPostgresField(t *testing.T) {
	for _, sqlstate := range []string{"42601", "42501", "23505", "22P02", "57014", "08006", "28000", "XX000"} {
		pg, markers := sentinelPgError(sqlstate)

		// Wrapped, because that is how the error actually arrives from the driver.
		got := convert(fmt.Errorf("executing statement: %w", pg))

		ke, ok := errors.AsType[*types.Error](got)
		if !ok {
			t.Fatalf("sqlstate %s: convert returned %T, want *types.Error", sqlstate, got)
		}

		// Nothing can unwrap its way back to the PgError.
		if _, ok := errors.AsType[*pgconn.PgError](got); ok {
			t.Fatalf("sqlstate %s: the PgError is still reachable through the returned error", sqlstate)
		}

		surface := strings.Join([]string{string(ke.Code), ke.Summary, ke.Action, ke.AuditID, ke.Error()}, "\x00")
		for field, marker := range markers {
			if strings.Contains(surface, marker) {
				t.Errorf("sqlstate %s: PgError.%s leaked into the response: %q", sqlstate, field, surface)
			}
		}
		if strings.Contains(surface, sqlstate) {
			t.Errorf("sqlstate %s: SQLSTATE was forwarded: %q", sqlstate, surface)
		}
		if ke.Summary == "" {
			t.Errorf("sqlstate %s: no keeper-composed summary", sqlstate)
		}
		if !closedCodes[ke.Code] {
			t.Errorf("sqlstate %s: code %q is not in the closed set", sqlstate, ke.Code)
		}
	}
}

// closedCodes is SPEC R6.4a's enumerated set, written out here so that anything
// convert produces has to be on the list.
var closedCodes = map[types.Code]bool{
	types.CodeSyntax: true, types.CodeMultiStatement: true, types.CodePermissionDenied: true,
	types.CodeUnclassified: true, types.CodeDenylisted: true, types.CodeDDLRefused: true,
	types.CodeOutOfWriteScope: true, types.CodeNoWriteCredential: true, types.CodeVaultLocked: true,
	types.CodeConnectionDisabled: true, types.CodeApprovalRequired: true, types.CodeApprovalRefused: true,
	types.CodeTicketUnknown: true, types.CodeTimeout: true, types.CodeRowCap: true,
	types.CodeStaleToken: true, types.CodeInternal: true,
}

func TestConvertSQLStateMapping(t *testing.T) {
	cases := []struct {
		sqlstate string
		routine  string
		want     types.Code
	}{
		{"42601", "scanner_yyerror", types.CodeSyntax},
		{"42601", "exec_parse_message", types.CodeMultiStatement},
		{"42501", "aclcheck_error", types.CodePermissionDenied},
		{"28P01", "auth_failed", types.CodePermissionDenied},
		{"42P01", "parserOpenTable", types.CodeSyntax},
		{"42703", "errorMissingColumn", types.CodeSyntax},
		{"3F000", "get_namespace_oid", types.CodeSyntax},
		{"57014", "ProcessInterrupts", types.CodeTimeout},
		{"25006", "PreventCommandIfReadOnly", types.CodeApprovalRequired},
		{"23505", "_bt_check_unique", types.CodeInternal},
		{"22P02", "pg_strtoint32", types.CodeInternal},
		{"53300", "InitProcess", types.CodeInternal},
		{"08006", "pqReadData", types.CodeInternal},
	}
	for _, c := range cases {
		got := codeForSQLState(c.sqlstate, c.routine)
		if got != c.want {
			t.Errorf("codeForSQLState(%q, %q) = %q, want %q", c.sqlstate, c.routine, got, c.want)
		}
		if !closedCodes[got] {
			t.Errorf("codeForSQLState(%q, %q) produced %q, which is not in the closed set", c.sqlstate, c.routine, got)
		}
	}
}

func TestConvertNonPostgresErrors(t *testing.T) {
	if got := convert(nil); got != nil {
		t.Fatalf("convert(nil) = %v, want nil", got)
	}
	if !isCode(convert(context.DeadlineExceeded), types.CodeTimeout) {
		t.Error("a deadline is not a timeout")
	}
	if !isCode(convert(context.Canceled), types.CodeTimeout) {
		t.Error("a cancellation is not a timeout")
	}
	if !isCode(convert(errors.New("some driver detail with a value 123-45-6789 in it")), types.CodeInternal) {
		t.Error("an unrecognised error is not internal")
	}
	if got := convert(errors.New("some driver detail with a value 123-45-6789 in it")); strings.Contains(got.Error(), "123-45-6789") {
		t.Errorf("an unrecognised error's text was copied into the response: %q", got)
	}

	// An error this package already composed passes through unchanged.
	own := keeperError(types.CodeDDLRefused)
	if got := convert(fmt.Errorf("wrapped: %w", own)); got != error(own) {
		t.Errorf("convert rebuilt a keeper error: %v", got)
	}
}

func TestNotExplainable(t *testing.T) {
	if !notExplainable(&pgconn.PgError{Code: "42601", Routine: "scanner_yyerror"}) {
		t.Error("a grammar rejection of the EXPLAIN prefix should read as not-explainable")
	}
	if notExplainable(&pgconn.PgError{Code: "42601", Routine: "exec_parse_message"}) {
		t.Error("multi-statement input is not a utility statement")
	}
	if notExplainable(&pgconn.PgError{Code: "42501", Routine: "aclcheck_error"}) {
		t.Error("a permission denial is not a utility statement")
	}
	if notExplainable(errors.New("not a pg error")) {
		t.Error("a non-server error is not a utility statement")
	}
}

func TestEveryKeeperCodeHasASummaryAndAction(t *testing.T) {
	for code := range summaries {
		if summaries[code] == "" {
			t.Errorf("%s has no summary", code)
		}
		if actions[code] == "" {
			t.Errorf("%s has no action", code)
		}
		if !closedCodes[code] {
			t.Errorf("%s is not in the closed set", code)
		}
	}
}
