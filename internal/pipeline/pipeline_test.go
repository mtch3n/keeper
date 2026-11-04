package pipeline

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// --- computed-column inheritance --------------------------------------------

func flowing(oid uint32, atts ...uint16) []ports.ColumnRef {
	var out []ports.ColumnRef
	for _, a := range atts {
		out = append(out, ports.ColumnRef{TableOID: oid, AttNum: a})
	}
	return out
}

// A computed column takes the strictest non-allow policy among the columns that
// flow out of a scan, whatever the type of either. A column that only filters,
// or is not selected, does not flow, so aggregates over a table with masked
// columns stay usable.
func TestComputedColumnInheritsFromTheColumnsThatFlow(t *testing.T) {
	unknownRel := ports.PlanFacts{Relations: []uint32{9999}}
	for _, tc := range []struct {
		name  string
		typ   string
		plan  func(*ports.PlanFacts)
		want  types.Policy
		basis types.Basis
	}{
		{"count star flows nothing", "int8", func(p *ports.PlanFacts) {}, types.PolicyAllow, types.BasisInherited},
		{"sum of an allowed column", "numeric", func(p *ports.PlanFacts) { p.Flows = flowing(ordersOID, 2) }, types.PolicyAllow, types.BasisInherited},
		{"number from a redacted number", "int8", func(p *ports.PlanFacts) { p.Flows = flowing(usersOID, 4) }, types.PolicyRedact, types.BasisInherited},
		{"number from a redacted text", "int8", func(p *ports.PlanFacts) { p.Flows = flowing(usersOID, 3) }, types.PolicyRedact, types.BasisInherited},
		{"date from a token column", "date", func(p *ports.PlanFacts) { p.Flows = flowing(usersOID, 2) }, types.PolicyRedact, types.BasisInherited},
		{"text from a scan column", "text", func(p *ports.PlanFacts) { p.Flows = flowing(usersOID, 5) }, types.PolicyScan, types.BasisInherited},
		{"allowed and masked together", "text", func(p *ports.PlanFacts) { p.Flows = flowing(usersOID, 1, 2) }, types.PolicyRedact, types.BasisInherited},
		{"a column the catalog does not know", "int8", func(p *ports.PlanFacts) { p.Flows = flowing(usersOID, 77) }, types.PolicyRedact, types.BasisInherited},
		{"a relation the catalog does not know", "int8", func(p *ports.PlanFacts) { p.Relations = append(p.Relations, unknownRel.Relations...) }, types.PolicyRedact, types.BasisInherited},
		{"a function outside pg_catalog", "text", func(p *ports.PlanFacts) { p.Opaque = true }, types.PolicyRedact, types.BasisInherited},
		{"a volatile function", "int8", func(p *ports.PlanFacts) { p.Uncertain = true }, types.PolicyRedact, types.BasisInherited},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(h *harness) {
				h.exec.cols = []types.ColumnMeta{computed("v", tc.typ)}
				h.exec.rows = [][]any{{"x"}}
				h.authority.conn.Reads = types.ReadsNever
				h.exec.plan = readPlan(1, usersRef, ordersRef)
				tc.plan(h.exec.plan)
			})
			dec := h.query(t, Request{SQL: "SELECT 1"})
			if dec.Error != nil {
				t.Fatalf("unexpected refusal: %v", dec.Error)
			}
			tr := transformFor(t, dec, "v")
			if tr.Policy != tc.want || tr.Basis != tc.basis {
				t.Errorf("policy = %q basis = %q, want %q %q", tr.Policy, tr.Basis, tc.want, tc.basis)
			}
		})
	}
}

// A JSON column allowed with only some paths is not an allowed column once its
// text flows into a computed value.
func TestComputedColumnFromAJSONColumnWithPathsRedacts(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.catalog.byIdentity[colKey{ordersOID, 2}] = types.ColumnPolicy{Policy: types.PolicyAllow, Paths: map[string]types.ColumnPolicy{"$.ip": {Policy: types.PolicyAllow}}}
		h.exec.cols = []types.ColumnMeta{computed("v", "text")}
		h.exec.rows = [][]any{{"x"}}
		h.exec.plan = readPlan(1, ordersRef)
		h.exec.plan.Flows = flowing(ordersOID, 2)
	})
	dec := h.query(t, Request{SQL: "SELECT metadata::text FROM orders"})
	if got := transformFor(t, dec, "v").Policy; got != types.PolicyRedact {
		t.Errorf("policy = %q, want redact", got)
	}
}

// With nothing sensitive in the referenced relation, the computed column is
// allow too.
func TestComputedTextOverACleanRelationIsAllowed(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("label", "text")}
		h.exec.rows = [][]any{{"ok"}}
		h.exec.plan = readPlan(1, cleanRef)
		h.exec.plan.Flows = flowing(cleanOID, 2)
	})

	dec := h.query(t, Request{SQL: "SELECT label || '-x' FROM clean_ids"})

	if got := transformFor(t, dec, "label").Policy; got != types.PolicyAllow {
		t.Errorf("a computed text column over a clean relation = %q, want allow", got)
	}
}

// R7.6's third branch: a computed column with no referenced relation inherits
// from its parameters, and is redact if that is indeterminable.
func TestComputedColumnWithNoRelationRedactsWhenIndeterminable(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("now", "timestamptz")}
		h.exec.rows = [][]any{{time.Unix(0, 0)}}
		h.exec.plan = readPlan(1)
	})

	dec := h.query(t, Request{SQL: "SELECT now()"})

	tr := transformFor(t, dec, "now")
	if tr.Policy != types.PolicyRedact {
		t.Errorf("a computed column with no relation and no parameter = %q, want redact", tr.Policy)
	}
	if tr.Basis != types.BasisUnknown {
		t.Errorf("basis = %q, want unknown", tr.Basis)
	}
}

// --- R8.4b parameter inheritance -------------------------------------------

// The leak R8.4b exists to close: clean_ids is entirely allow, so R7.6's
// relation-based rule offers nothing, and only a relation-independent rule
// stops a token-resolved name returning in cleartext. The bound is by type
// family: a text email cannot appear inside a bigint, so count(*) stays usable.
func TestTokenParameterRaisesTextOutputOnly(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("greeting", "text"), computed("count", "int8")}
		h.exec.rows = [][]any{{"Hi Ada", int64(1)}}
		h.exec.plan = readPlan(1, cleanRef)
	})

	dec := h.query(t, Request{
		SQL:    "SELECT 'Hi ' || $1::text, count(*) FROM clean_ids",
		Params: []ports.Param{{Value: "Ada", Policy: types.PolicyToken}},
	})

	greeting := transformFor(t, dec, "greeting")
	if greeting.Policy != types.PolicyRedact {
		t.Errorf("a text output of a statement with a token-resolved parameter = %q, want redact", greeting.Policy)
	}
	if greeting.Basis != types.BasisParameter {
		t.Errorf("basis = %q, want parameter", greeting.Basis)
	}
	if got := transformFor(t, dec, "count").Policy; got != types.PolicyAllow {
		t.Errorf("a numeric output = %q, want allow: a text value cannot appear inside a bigint", got)
	}
}

// --- R5.4a and R7.7d: the unresolved column ---------------------------------

func TestUnresolvedColumnRedactsAndStillRuns(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{
			fromColumn("id", "int8", usersOID, 1),
			fromColumn("payload", "jsonb", usersOID, 99), // a migration added it; no catalog entry
		}
		h.exec.rows = [][]any{{int64(1), `{"a":1}`}}
		h.exec.plan = readPlan(1, usersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT id, payload FROM users"})

	if dec.Error != nil {
		t.Fatalf("an unresolved column must not refuse the statement: %v", dec.Error)
	}
	if dec.Escalation != nil {
		t.Fatal("an unresolved column must not escalate: a human cannot decide it from a prompt")
	}
	tr := transformFor(t, dec, "payload")
	if tr.Policy != types.PolicyRedact || tr.Basis != types.BasisUnknown {
		t.Errorf("payload = %q/%q, want redact/unknown", tr.Policy, tr.Basis)
	}
	if dec.Result.Rows[0][1] != maskMarker {
		t.Error("the unresolved column's value was released")
	}
	if got := h.audit.last(t).Transforms["payload"].Basis; got != types.BasisUnknown {
		t.Errorf("the audit record's basis for the unresolved column = %q, want unknown", got)
	}
}

// --- R4.2c: DDL is refused under every setting, with any approval ---

func TestDDLIsRefusedEvenWithAnApprovalUnderEveryReadsSetting(t *testing.T) {
	for _, reads := range []types.Reads{types.ReadsAlways, types.ReadsUncertain, types.ReadsNever} {
		h := newHarness(t, func(h *harness) {
			h.authority.conn.Reads = reads
			h.authority.conn.Writes = types.WritesAllow
			h.exec.cols = nil
			h.exec.plan = &ports.PlanFacts{StatementType: pgdb.StmtDDL}
		})

		dec := h.query(t, Request{
			SQL:      "ALTER TABLE users ADD COLUMN x int",
			Approval: &Approval{TicketID: "t-1", Approver: "ada"},
		})

		if dec.Error == nil || dec.Error.Code != types.CodeDDLRefused {
			t.Fatalf("reads %s: got %v, want ddl_refused", reads, dec.Error)
		}
		if h.exec.called("Run") || h.exec.called("CommitWrite") || h.exec.called("PreviewWrite") {
			t.Errorf("reads %s: DDL reached the database: %v", reads, h.exec.calls)
		}
	}
}

// --- R4.2d–f: the write path ------------------------------------------------

func TestWriteAskWaitsWithAPreviewAndCommitsOnlyOnApproval(t *testing.T) {
	setup := func(h *harness) {
		h.authority.conn.Writes = types.WritesAsk
		// UPDATE ... RETURNING user_email: rows exist, and they are output in
		// exactly the sense §8 means.
		h.exec.cols = []types.ColumnMeta{fromColumn("user_email", "text", ordersOID, 3)}
		h.exec.rows = [][]any{{"ada@example.com"}}
		h.exec.previewTag = 4
		h.exec.commitTag = 4
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtUpdate, Writes: true,
			RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID},
			EstimatedRows: 4,
		}
	}

	h := newHarness(t, setup)
	dec := h.query(t, Request{SQL: "UPDATE orders SET status='x' RETURNING user_email"})

	if dec.Escalation == nil {
		t.Fatalf("a write on writes=ask must wait: err=%v", dec.Error)
	}
	if dec.Error == nil || dec.Error.Code != types.CodeApprovalRequired {
		t.Errorf("error = %v, want approval_required", dec.Error)
	}
	if dec.Escalation.Write == nil || dec.Escalation.Write.RowCount != 4 {
		t.Fatalf("preview = %+v, want the command tag's count", dec.Escalation.Write)
	}
	if got := dec.Escalation.Write.Returning["user_email"].Policy; got != types.PolicyToken {
		t.Errorf("a RETURNING column's transform = %q, want token: a write is not a hole in Invariant B", got)
	}
	if h.exec.called("CommitWrite") {
		t.Fatal("nothing may be committed before the approval")
	}
	if !h.exec.called("PreviewWrite") {
		t.Fatal("the preview never ran")
	}

	// Now with the approval.
	h2 := newHarness(t, setup)
	dec2 := h2.query(t, Request{
		SQL:      "UPDATE orders SET status='x' RETURNING user_email",
		Approval: &Approval{TicketID: "tk-9", Approver: "ada"},
	})
	if dec2.Result == nil {
		t.Fatalf("an approved write did not run: %v", dec2.Error)
	}
	if !h2.exec.called("CommitWrite") {
		t.Fatalf("the commit never ran: %v", h2.exec.calls)
	}
	if dec2.Result.ExecutedRows != 4 {
		t.Errorf("executed rows = %d, want 4", dec2.Result.ExecutedRows)
	}
	if dec2.Result.Authorization != "ticket:tk-9" {
		t.Errorf("authorization = %q, want ticket:tk-9", dec2.Result.Authorization)
	}
	if dec2.Result.Rows[0][0] != maskMarker {
		t.Error("RETURNING rows must pass through G7")
	}
	if h2.audit.last(t).Approver != "ada" {
		t.Error("the approver is not in the audit record")
	}
}

func TestRowCeilingBoundsTheRequest(t *testing.T) {
	h := newHarness(t)
	conn := testConnection()
	conn.Limits.MaxRowsCeiling = 1000
	if got := h.pipe.rowCeiling(Request{MaxRows: 900}, conn); got != 900 {
		t.Errorf("rowCeiling = %d, want 900", got)
	}
	if got := h.pipe.rowCeiling(Request{MaxRows: 99999}, conn); got != 1000 {
		t.Errorf("rowCeiling = %d, want the operator ceiling: the agent cannot raise it", got)
	}
}

// --- stale catalog ---

func TestStaleCatalogIsNotedAndTheStatementStillFollowsTheSettings(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.catalog.fresh = false
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(1, cleanRef)
	})

	dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})

	if dec.Result == nil {
		t.Fatalf("a stale catalog must not change the outcome of reads=uncertain: %v", dec.Error)
	}
	if !hasLayer(dec.Result.Degradations, "catalog") {
		t.Errorf("degradations = %v, want the catalog named", dec.Result.Degradations)
	}
}

// --- G1, G3 and G9 ----------------------------------------------------------

func TestMultiStatementInputIsRefused(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.describeErr = &types.Error{Code: types.CodeMultiStatement, Summary: "one statement only"}
	})
	dec := h.query(t, Request{SQL: "SELECT 1; DROP TABLE users"})
	if dec.Error == nil || dec.Error.Code != types.CodeMultiStatement {
		t.Fatalf("got %v, want multi_statement", dec.Error)
	}
	if h.exec.called("Plan") {
		t.Error("an inadmissible statement was planned")
	}
}

func TestPermissionDeniedNamesTheReadableColumns(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", usersOID, 1)}
		h.exec.plan = readPlan(1, usersRef)
		h.exec.runErr = &types.Error{Code: types.CodePermissionDenied, Summary: "refused"}
	})

	dec := h.query(t, Request{SQL: "SELECT * FROM users"})

	if dec.Error == nil || dec.Error.Code != types.CodePermissionDenied {
		t.Fatalf("got %v, want permission_denied", dec.Error)
	}
	if !strings.Contains(dec.Error.Summary, "email") || !strings.Contains(dec.Error.Summary, "status") {
		t.Errorf("the error must name what may be read; got %q", dec.Error.Summary)
	}
	if h.audit.last(t).ErrorCode != types.CodePermissionDenied {
		t.Error("a permission denial must be logged as Invariant A working")
	}
}

func TestAuditIsNeverSkipped(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*harness)
		req   Request
	}{
		{"refusal", func(h *harness) {
			h.exec.plan = &ports.PlanFacts{StatementType: pgdb.StmtDDL}
		}, Request{SQL: "DROP TABLE users"}},
		{"escalation", func(h *harness) {
			h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
			h.exec.plan = readPlan(1, cleanRef)
			h.authority.conn.Reads = types.ReadsNever
		}, Request{SQL: "SELECT id FROM clean_ids"}},
		{"result", func(h *harness) {
			h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
			h.exec.rows = [][]any{{int64(1)}}
			h.exec.plan = readPlan(1, cleanRef)
		}, Request{SQL: "SELECT id FROM clean_ids"}},
	}
	for _, c := range cases {
		h := newHarness(t, c.setup)
		dec := h.query(t, c.req)
		rec := h.audit.last(t)
		if rec.ID != dec.AuditID {
			t.Errorf("%s: audit id %q does not match the decision's %q", c.name, rec.ID, dec.AuditID)
		}
		if rec.SessionID != "sess-1" || rec.Client.Name == "" {
			t.Errorf("%s: the session is missing from the record", c.name)
		}
		if !strings.HasPrefix(rec.Statement, "NORMALIZED:") {
			t.Errorf("%s: the statement was not normalized: %q", c.name, rec.Statement)
		}
	}
}

func TestAuditFailureStopsTheResponse(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(1, cleanRef)
		h.audit.writeErr = errors.New("disk full")
	})
	if _, err := h.pipe.Query(t.Context(), Request{
		ConnID: "acme_prod", Session: testSession(), SQL: "SELECT id FROM clean_ids",
	}); err == nil {
		t.Fatal("a result was returned although G9 could not be written")
	}
}

// --- Explain ----------------------------------------------------------------

func TestExplainPredictsTheOutcomeWithoutExecuting(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{
			fromColumn("id", "int8", usersOID, 1),
			fromColumn("ssn", "text", usersOID, 3),
			fromColumn("email", "text", usersOID, 2),
		}
		h.exec.plan = readPlan(1, usersRef)
	})

	res, ke, err := h.pipe.Explain(t.Context(), Request{
		ConnID: "acme_prod", Session: testSession(), SQL: "SELECT id, ssn, email FROM users",
	})
	if err != nil || ke != nil {
		t.Fatalf("Explain: %v %v", err, ke)
	}
	if h.exec.called("Run") {
		t.Fatal("explain executed the statement")
	}
	if res.Outcome != types.OutcomeRun {
		t.Errorf("outcome = %q, want run", res.Outcome)
	}
	if names := columnNames(res.OutputColumns); len(names) != 3 {
		t.Errorf("output columns = %v, want all three", names)
	}
	if !hasReason(res.Reasons, ReasonMasked) {
		t.Errorf("reasons = %v", res.Reasons)
	}
}

// --- output identity --------------------------------------------------------

// R7.5a: a column is matched by the server's own (TableOID, AttNum) and never by
// name. An output aliased to another column's name must still get its own policy.
func TestPolicyFollowsIdentityNotOutputName(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		// SELECT email AS id, id AS email FROM users
		h.exec.cols = []types.ColumnMeta{
			{Name: "id", Type: "text", TableOID: usersOID, AttNum: 2},    // really email
			{Name: "email", Type: "int8", TableOID: usersOID, AttNum: 1}, // really id
		}
		h.exec.rows = [][]any{{"ada@example.com", int64(1)}}
		h.exec.plan = readPlan(1, usersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT email AS id, id AS email FROM users"})

	if got := transformFor(t, dec, "id").Policy; got != types.PolicyToken {
		t.Errorf("the column aliased to id = %q, want token: it is really users.email", got)
	}
	if got := transformFor(t, dec, "email").Policy; got != types.PolicyAllow {
		t.Errorf("the column aliased to email = %q, want allow: it is really users.id", got)
	}
	if dec.Result.Rows[0][0] != maskMarker {
		t.Error("an aliased token column was released")
	}
	if dec.Result.Rows[0][1] != int64(1) {
		t.Error("an aliased allow column was masked")
	}
}

func TestDuplicateOutputNamesStayDistinct(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{
			fromColumn("id", "int8", usersOID, 1),
			fromColumn("id", "int8", ordersOID, 1),
		}
		h.exec.rows = [][]any{{int64(1), int64(2)}}
		h.exec.plan = readPlan(1, usersRef, ordersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT u.id, o.id FROM users u JOIN orders o ON o.id = u.id"})

	names := columnNames(dec.Result.Columns)
	if names[0] == names[1] {
		t.Fatalf("two output columns share a name, so transforms would lose one: %v", names)
	}
	if len(dec.Result.Transforms) != 2 {
		t.Errorf("transforms = %v, want one entry per column", dec.Result.Transforms)
	}
}

func Test_DET_C7_AnUnexaminedScanColumnIsReportedAsADegradation(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(10, cleanRef)
		h.redactor.unexamined = true
	})
	dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})
	if dec.Result == nil {
		t.Fatalf("a statement with unexamined free text must still complete: %v", dec.Error)
	}
	if !slices.ContainsFunc(dec.Result.Degradations, func(d types.Degradation) bool { return d.Layer == "detector" }) {
		t.Errorf("degradations = %v, want the detector named", dec.Result.Degradations)
	}
}

func hasLayer(ds []types.Degradation, layer string) bool {
	return slices.ContainsFunc(ds, func(d types.Degradation) bool { return d.Layer == layer })
}

func TestAChangedViewInAssistedReportsOnlyTheCatalog(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(10, cleanRef)
		h.catalog.fresh = false
	})
	dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})
	if dec.Result == nil {
		t.Fatalf("a read over a changed view did not run in assisted mode: %v", dec.Error)
	}
	if !hasLayer(dec.Result.Degradations, "catalog") || hasLayer(dec.Result.Degradations, "judge") {
		t.Errorf("degradations = %v, want the catalog and nothing about a judge", dec.Result.Degradations)
	}
}

func Test_WRITE_C1_AnAllowedWriteWaitsThenRunsOnApproval(t *testing.T) {
	setup := func(h *harness) {
		h.authority.conn.Writes = types.WritesAsk
		h.exec.previewTag, h.exec.commitTag = 2, 2
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtUpdate, Writes: true,
			RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID}, EstimatedRows: 2,
		}
	}
	h := newHarness(t, setup)
	dec := h.query(t, Request{SQL: "UPDATE orders SET status = 'x'"})
	if dec.Escalation == nil {
		t.Fatalf("an allowed write did not wait for approval: err %v", dec.Error)
	}
	if h.exec.called("CommitWrite") {
		t.Fatal("the write ran before its approval")
	}
	h2 := newHarness(t, setup)
	dec2 := h2.query(t, Request{SQL: "UPDATE orders SET status = 'x'", Approval: &Approval{TicketID: "tk", Approver: "ming"}})
	if dec2.Result == nil || !h2.exec.called("CommitWrite") {
		t.Fatalf("an approved write did not run: %v", dec2.Error)
	}
}

func TestAWriteOnAReadOnlyProfileIsRefusedAtOnce(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.authority.conn.Writes = types.WritesOff
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtUpdate, Writes: true,
			RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID},
		}
	})
	dec := h.query(t, Request{SQL: "UPDATE orders SET status = 'x'"})
	if dec.Error == nil || dec.Error.Code != types.CodeWritesOff || dec.Escalation != nil {
		t.Fatalf("decision = %+v, want an immediate writes_off refusal with nothing waiting", dec)
	}
	if h.exec.called("PreviewWrite") || h.exec.called("CommitWrite") {
		t.Error("a write on a read-only profile reached the database")
	}
}

func changedRows(n int) *types.WriteChanges {
	c := &types.WriteChanges{Columns: []string{"id", "user_email"}}
	for i := range n {
		c.Rows = append(c.Rows, types.ChangedRow{New: []string{strconv.Itoa(i + 1), "ada@example.com"}})
	}
	return c
}

func writeHarness(t *testing.T, rows int) *harness {
	return newHarness(t, func(h *harness) {
		h.authority.conn.Writes = types.WritesAsk
		h.exec.previewTag = int64(rows)
		h.exec.changes = changedRows(rows)
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtUpdate, Writes: true,
			RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID}, EstimatedRows: int64(rows),
		}
	})
}

func Test_WRITE_C4_AWaitingWriteCarriesItsChangedRowsInCleartext(t *testing.T) {
	h := writeHarness(t, 3)
	dec := h.query(t, Request{SQL: "UPDATE orders SET status = 'x'"})
	if dec.Escalation == nil || dec.Escalation.Write == nil {
		t.Fatalf("no write preview: %+v", dec)
	}
	w := dec.Escalation.Write
	if w.RowCount != 3 || w.Changes == nil || len(w.Changes.Rows) != 3 || w.Changes.Omitted != 0 {
		t.Fatalf("preview = %+v, want 3 affected and 3 changed rows", w)
	}
	// user_email is a token column for an agent; the approver sees the value.
	if got := w.Changes.Rows[0].New[1]; got != "ada@example.com" {
		t.Errorf("changed value = %q, want the cleartext", got)
	}
}

func Test_WRITE_C5_ALargeWriteShowsTheFirstFiftyRows(t *testing.T) {
	h := writeHarness(t, 500)
	dec := h.query(t, Request{SQL: "UPDATE orders SET status = 'x'"})
	if dec.Escalation == nil || dec.Escalation.Write == nil || dec.Escalation.Write.Changes == nil {
		t.Fatalf("no write preview: %+v", dec)
	}
	w := dec.Escalation.Write
	if w.RowCount != 500 || len(w.Changes.Rows) != types.PreviewRows || w.Changes.Omitted != 450 {
		t.Errorf("preview = %d affected, %d shown, %d omitted; want 500, 50, 450", w.RowCount, len(w.Changes.Rows), w.Changes.Omitted)
	}
}

func Test_WRITE_C7_ADeleteIsMarkedDestructiveWithItsTargetAndProtection(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.authority.conn.Writes = types.WritesAsk
		h.exec.previewTag = 1
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtDelete, Writes: true,
			RelationNames: []types.RelationRef{ordersRef}, Relations: []uint32{ordersOID}, EstimatedRows: 1,
		}
	})
	dec := h.query(t, Request{SQL: "DELETE FROM orders WHERE id = 1"})
	if dec.Escalation == nil {
		t.Fatalf("not escalated: %v", dec.Error)
	}
	f := dec.Escalation.Facts
	if f.Risk != types.RiskDestructive || f.StatementType != "DELETE" || !slices.Equal(f.Targets, []types.RelationRef{ordersRef}) {
		t.Errorf("facts = risk %q, verb %q, targets %v; want destructive DELETE on orders", f.Risk, f.StatementType, f.Targets)
	}
}

func TestAStatementKeeperCannotClassifyIsDestructive(t *testing.T) {
	for _, stmt := range []string{"", "COPY", "CALL"} {
		risk, unreadable := riskOf(stmt)
		if risk != types.RiskDestructive || !unreadable {
			t.Errorf("riskOf(%q) = %q, unreadable %v; want destructive and unreadable", stmt, risk, unreadable)
		}
	}
	if risk, unreadable := riskOf(pgdb.StmtSelect); risk != types.RiskRead || unreadable {
		t.Errorf("a SELECT is %q, unreadable %v", risk, unreadable)
	}
}

func filterHarness(t *testing.T, filters ...ports.ColumnRef) *harness {
	return newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{
			fromColumn("id", "int8", usersOID, 1), fromColumn("email", "text", usersOID, 2), fromColumn("status", "text", usersOID, 5),
		}
		h.exec.plan = readPlan(1, usersRef)
		h.exec.plan.Filters = filters
	})
}

func Test_WRITE_C8_TheCardListsExpandedColumnsAndFlagsASensitiveFilter(t *testing.T) {
	h := filterHarness(t, ports.ColumnRef{TableOID: usersOID, AttNum: 3, Relation: usersRef, Column: "ssn"})
	f, kerr, err := h.pipe.Facts(t.Context(), Request{ConnID: "acme_prod", Session: testSession(), SQL: "SELECT * FROM users WHERE ssn = $1"})
	if err != nil || kerr != nil {
		t.Fatalf("facts: %v %v", kerr, err)
	}
	if !slices.Equal(f.Columns, []string{"id", "email", "status"}) {
		t.Errorf("columns = %v, want SELECT * expanded", f.Columns)
	}
	want := []types.FilterColumn{{Relation: usersRef, Column: "ssn", Flag: "sensitive", Policy: types.PolicyRedact}}
	if !slices.Equal(f.Filters, want) {
		t.Errorf("filters = %+v, want ssn flagged sensitive", f.Filters)
	}
}

func Test_WRITE_C13_AFilterOnAnUncataloguedColumnIsFlaggedUnclassified(t *testing.T) {
	h := filterHarness(t,
		ports.ColumnRef{TableOID: usersOID, AttNum: 9, Relation: usersRef, Column: "dob"},
		ports.ColumnRef{TableOID: usersOID, AttNum: 1, Relation: usersRef, Column: "id"},
	)
	f, kerr, err := h.pipe.Facts(t.Context(), Request{ConnID: "acme_prod", Session: testSession(), SQL: "SELECT id FROM users WHERE dob > $1 AND id > 1"})
	if err != nil || kerr != nil {
		t.Fatalf("facts: %v %v", kerr, err)
	}
	want := []types.FilterColumn{{Relation: usersRef, Column: "dob", Flag: "unclassified"}}
	if !slices.Equal(f.Filters, want) {
		t.Errorf("filters = %+v, want only dob flagged unclassified (id is allow)", f.Filters)
	}
}

func sizedRows(n, size int) [][]any {
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{int64(i), strings.Repeat("x", size)}
	}
	return rows
}

func sizeHarness(t *testing.T, rows [][]any) *harness {
	return newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1), fromColumn("label", "text", cleanOID, 2)}
		h.exec.rows = rows
		h.exec.plan = readPlan(int64(len(rows)), cleanRef)
	})
}

func Test_WRITE_C11_AResultIsCutBySizeUnderTheCap(t *testing.T) {
	h := sizeHarness(t, sizedRows(30, 100<<10)) // 3 MiB
	if h.authority.conn.Limits.MaxBytes != 1<<20 {
		t.Fatalf("default size cap = %d, want 1 MiB", h.authority.conn.Limits.MaxBytes)
	}
	dec := h.query(t, Request{SQL: "SELECT id, label FROM clean_ids"})
	if dec.Result == nil {
		t.Fatalf("no result: %v", dec.Error)
	}
	body, _ := json.Marshal(dec.Result.Rows)
	if len(body) > 1<<20 || len(dec.Result.Rows) == 0 || len(dec.Result.Rows) == 30 {
		t.Errorf("rows = %d, %d bytes; want some rows, under 1 MiB", len(dec.Result.Rows), len(body))
	}
	if !dec.Result.Truncated || dec.Result.TruncatedBy != types.CutBySize || dec.Result.RowCount != len(dec.Result.Rows) {
		t.Errorf("result says truncated %v by %q with row_count %d; want cut by size", dec.Result.Truncated, dec.Result.TruncatedBy, dec.Result.RowCount)
	}
}

func Test_WRITE_C14_ARowLargerThanTheCapLeavesNoRows(t *testing.T) {
	h := sizeHarness(t, sizedRows(1, 2<<20))
	dec := h.query(t, Request{SQL: "SELECT id, label FROM clean_ids"})
	if dec.Result == nil {
		t.Fatalf("no result: %v", dec.Error)
	}
	if len(dec.Result.Rows) != 0 || !dec.Result.Truncated || dec.Result.TruncatedBy != types.CutBySize {
		t.Errorf("rows = %d, truncated %v by %q; want none, cut by size", len(dec.Result.Rows), dec.Result.Truncated, dec.Result.TruncatedBy)
	}
}

func Test_EVAL_C1_AColumnWithNoEntryRedactsAComputedColumnThatReadsIt(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("v", "text")}
		h.exec.rows = [][]any{{"ada@example.com"}}
		h.authority.conn.Reads = types.ReadsNever
		h.exec.plan = readPlan(1, usersRef)
		h.exec.plan.Flows = flowing(usersOID, 77)
	})
	dec := h.query(t, Request{SQL: "SELECT upper(email) FROM users"})
	if dec.Error != nil {
		t.Fatalf("unexpected refusal: %v", dec.Error)
	}
	if got := transformFor(t, dec, "v").Policy; got != types.PolicyRedact {
		t.Errorf("policy = %q, want redact", got)
	}
}
