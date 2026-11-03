package pipeline

import (
	"context"
	"testing"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func writeSetup(h *harness) {
	h.authority.conn.Writes = types.WritesAsk
	h.exec.previewTag = 4
	h.exec.commitTag = 4
	h.exec.plan = &ports.PlanFacts{
		StatementType: pgdb.StmtUpdate, Writes: true,
		RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID},
		EstimatedRows: 7,
	}
}

func TestAWriteThatCallsAFunctionWithEffectsIsNotRunInAPreview(t *testing.T) {
	h := newHarness(t, writeSetup, func(h *harness) {
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtUpdate, Writes: true, Uncertain: true,
			RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID},
			EstimatedRows: 7,
		}
	})
	dec := h.query(t, Request{SQL: "UPDATE orders SET status='x' WHERE pg_terminate_backend(1)"})
	if dec.Escalation == nil {
		t.Fatalf("the write must wait: %v", dec.Error)
	}
	if h.exec.called("PreviewWrite") {
		t.Fatal("the statement ran in a preview")
	}
	w := dec.Escalation.Write
	if w == nil || !w.Unpreviewed || w.RowCount != 7 {
		t.Errorf("preview = %+v, want the estimate marked unpreviewed", w)
	}
}

func TestACommitIsOnRecordBeforeItStarts(t *testing.T) {
	h := newHarness(t, writeSetup)
	var recorded int
	h.exec.onCommit = func() { recorded = len(h.audit.records) }
	dec := h.query(t, Request{SQL: "UPDATE orders SET status='x'", Approval: &Approval{TicketID: "t", Approver: "ada", PreviewedRows: 4}})
	if dec.Result == nil {
		t.Fatalf("not committed: %v", dec.Error)
	}
	if recorded != 1 {
		t.Errorf("%d records existed when the commit started, want the intent record", recorded)
	}
	if len(h.audit.records) != 2 || h.audit.records[0].ID != h.audit.records[1].ID {
		t.Errorf("the intent and the final record must share an id: %+v", h.audit.records)
	}
}

func TestAWriteWhoseIntentCannotBeRecordedDoesNotCommit(t *testing.T) {
	h := newHarness(t, writeSetup, func(h *harness) { h.audit.failFrom = 1 })
	req := Request{SQL: "UPDATE orders SET status='x'", Approval: &Approval{TicketID: "t"}, ConnID: "acme_prod", Session: testSession()}
	h.pipe.Query(t.Context(), req)
	if h.exec.called("CommitWrite") {
		t.Fatal("a write committed with nothing on record")
	}
}

func TestACommittedWriteIsReportedEvenIfItsFinalRecordFails(t *testing.T) {
	h := newHarness(t, writeSetup, func(h *harness) { h.audit.failFrom = 2 })
	dec, err := h.pipe.Query(t.Context(), Request{SQL: "UPDATE orders SET status='x'", Approval: &Approval{TicketID: "t"}, ConnID: "acme_prod", Session: testSession()})
	if err != nil {
		t.Fatalf("a committed write was reported as an error: %v", err)
	}
	if dec.Result == nil {
		t.Fatalf("no result: %+v", dec)
	}
	var found bool
	for _, d := range dec.Result.Degradations {
		found = found || d.Layer == "audit"
	}
	if !found {
		t.Errorf("no audit degradation: %+v", dec.Result.Degradations)
	}
}

func TestACancelledCallStillLeavesItsAuditRecord(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.exec.plan = readPlan(1, usersRef) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h.exec.describeErr = ctx.Err()
	if _, err := h.pipe.Query(ctx, Request{SQL: "SELECT 1", ConnID: "acme_prod", Session: testSession()}); err != nil {
		t.Fatalf("a cancelled call lost its record: %v", err)
	}
	if len(h.audit.records) != 1 || h.audit.cancelled {
		t.Errorf("records = %d, write saw a cancelled context = %v", len(h.audit.records), h.audit.cancelled)
	}
}

func TestAnApprovedWritesRecordCountsTheRowsItChanged(t *testing.T) {
	h := newHarness(t, writeSetup)
	dec := h.query(t, Request{SQL: "UPDATE orders SET status='x'", Approval: &Approval{TicketID: "t", PreviewedRows: 4}})
	if dec.Result == nil {
		t.Fatalf("not committed: %v", dec.Error)
	}
	if got := h.audit.last(t).RowCount; got != 4 {
		t.Errorf("the record counts %d rows, want the command tag's 4", got)
	}
	if dec.Result.ExecutedRows != 4 || dec.Result.PreviewedRows != 4 {
		t.Errorf("executed %d, previewed %d", dec.Result.ExecutedRows, dec.Result.PreviewedRows)
	}
}

func TestFactsWritesNoAuditRecord(t *testing.T) {
	h := newHarness(t, func(h *harness) { h.exec.plan = readPlan(1, usersRef) })
	if _, kerr, err := h.pipe.Facts(t.Context(), Request{SQL: "SELECT 1", ConnID: "acme_prod", Session: testSession()}); err != nil || kerr != nil {
		t.Fatalf("%v %v", kerr, err)
	}
	if len(h.audit.records) != 0 {
		t.Errorf("Facts wrote %d records", len(h.audit.records))
	}
}
