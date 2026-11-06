package pipeline

import (
	"testing"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func TestPerm_PERM_C3_aStoredReadsValueKeeperDoesNotKnowAsks(t *testing.T) {
	for _, stored := range []types.Reads{"sometimes", "ALWAYS", "permissive"} {
		h := newHarness(t, func(h *harness) {
			h.authority.conn.Reads = stored
			h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
			h.exec.rows = [][]any{{int64(1)}}
			h.exec.plan = readPlan(1, cleanRef)
		})
		dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})
		if dec.Escalation == nil || dec.Result != nil {
			t.Errorf("reads=%q ran without a question: result %v, error %v", stored, dec.Result, dec.Error)
		}
		if h.exec.called("Run") {
			t.Errorf("reads=%q reached the database", stored)
		}
	}
}

func TestPerm_PERM_C20_aProfileWithNoStoredReadsUsesTheRegistrationDefault(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.authority.conn.Reads = ""
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(1, cleanRef)
	})
	dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})
	if dec.Result == nil || dec.Escalation != nil {
		t.Errorf("reads unset asked for a statement with no volatile call: %+v", dec)
	}
	h = newHarness(t, func(h *harness) {
		h.authority.conn.Reads = ""
		h.exec.plan = readPlan(1, cleanRef)
		h.exec.plan.Uncertain = true
	})
	if dec := h.query(t, Request{SQL: "SELECT random()"}); dec.Escalation == nil {
		t.Errorf("reads unset ran a volatile call without asking: %+v", dec)
	}
}

func TestPerm_PERM_C6_aStoredWritesValueKeeperDoesNotKnowIsRefused(t *testing.T) {
	for _, stored := range []types.Writes{"", "approve", "yes", "ALLOW"} {
		h := newHarness(t, func(h *harness) {
			h.authority.conn.Writes = stored
			h.exec.plan = &ports.PlanFacts{
				StatementType: pgdb.StmtUpdate, Writes: true,
				RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID},
			}
		})
		dec := h.query(t, Request{SQL: "UPDATE orders SET status = 'x'"})
		if dec.Error == nil || dec.Error.Code != types.CodeWritesOff || dec.Escalation != nil {
			t.Errorf("writes=%q: decision = %+v, want a writes_off refusal", stored, dec)
		}
		if h.exec.called("PreviewWrite") || h.exec.called("CommitWrite") {
			t.Errorf("writes=%q reached the database", stored)
		}
	}
}
