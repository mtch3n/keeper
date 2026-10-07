package daemonapp

import (
	"testing"

	"github.com/mtchen/keeper/internal/pipeline"
	"github.com/mtchen/keeper/internal/types"
)

func Test_EVAL_C2_AnEscalationIsATicketNotAnError(t *testing.T) {
	dec := &pipeline.Decision{
		AuditID:    "a-1",
		Tier:       types.Tier3Approve,
		Escalation: &pipeline.Escalation{Facts: types.ApprovalFacts{Reasons: []string{"write"}}},
		Error:      &types.Error{Code: types.CodeApprovalRequired, Summary: "this statement needs a human decision before it runs"},
	}
	res, tk, err := queryOutcome(dec)
	if err != nil || res != nil || tk == nil {
		t.Fatalf("outcome = %v, %+v, %v; want a ticket and no error", res, tk, err)
	}
	if tk.State != types.TicketPending || tk.Tier != types.Tier3Approve || tk.Reason != "write" {
		t.Errorf("ticket = %+v", tk)
	}
}
