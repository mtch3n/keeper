package main

import (
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestRenderSQLShowsTerminalControlBytesAsText(t *testing.T) {
	got := renderSQL("SELECT 1\x1b[2J\x07\r\u009b\u202e\nFROM t")
	for _, bad := range []string{"\x1b", "\x07", "\r", "\u009b", "\u202e"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	if !strings.Contains(got, "[U+001B]") || !strings.Contains(got, "\nFROM t") {
		t.Errorf("got %q", got)
	}
}

func TestActivityOutcomeIsTheDecisionOrTheError(t *testing.T) {
	for _, c := range []struct {
		r    types.AuditRecord
		want string
	}{
		{types.AuditRecord{Decision: types.DecisionRefused, Approver: "ana", ErrorCode: types.CodeApprovalRefused}, "refused by ana"},
		{types.AuditRecord{Decision: types.DecisionExpired, Approver: "keeper", ErrorCode: types.CodeApprovalRefused}, "expired"},
		{types.AuditRecord{Decision: types.DecisionApproved, Approver: "ana"}, "approved by ana"},
		{types.AuditRecord{ErrorCode: types.CodeApprovalRequired}, "asked"},
		{types.AuditRecord{ErrorCode: types.CodeInternal}, string(types.CodeInternal)},
		{types.AuditRecord{}, "ran"},
	} {
		if got := outcomeOf(c.r); got != c.want {
			t.Errorf("outcomeOf(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}

func TestActivityRowsAreADashForAStatementThatDidNotRun(t *testing.T) {
	if got := rowsOf(types.AuditRecord{ErrorCode: types.CodeApprovalRequired}); got != "-" {
		t.Errorf("an asked statement shows %q rows", got)
	}
	if got := rowsOf(types.AuditRecord{RowCount: 3}); got != "3" {
		t.Errorf("a run statement shows %q rows", got)
	}
}
