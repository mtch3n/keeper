package pipeline

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// --- R7.6 type-family inheritance ------------------------------------------

// A computed column of numeric, boolean, date/time or uuid type inherits the
// strictest policy among the same-type-family non-allow columns of the
// referenced relations. users holds salary numeric redact, so every aggregate on
// users — and on anything joined to it — is masked.
func TestComputedNumericInheritsWithinItsFamily(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("count", "int8")}
		h.exec.rows = [][]any{{int64(42)}}
		h.exec.plan = readPlan(1, usersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT count(*) FROM users"})

	if dec.Error != nil {
		t.Fatalf("unexpected refusal: %v", dec.Error)
	}
	tr := transformFor(t, dec, "count")
	if tr.Policy != types.PolicyRedact {
		t.Errorf("count(*) over users = %q, want redact (salary numeric redact is in the same family)", tr.Policy)
	}
	if tr.Basis != types.BasisInherited {
		t.Errorf("basis = %q, want inherited", tr.Basis)
	}
	if dec.Tier != types.Tier1Record {
		t.Errorf("tier = %d, want 1", dec.Tier)
	}
}

// The same aggregate over a relation with no sensitive numeric column is allow
// with tripwires — this is the half of R7.6 that keeps ordinary aggregates
// usable. orders holds a token text column, and the numeric branch ignores it.
func TestComputedNumericIgnoresOtherFamilies(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("count", "int8")}
		h.exec.rows = [][]any{{int64(7)}}
		h.exec.plan = readPlan(1, ordersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT count(DISTINCT user_email) FROM orders"})

	if dec.Error != nil {
		t.Fatalf("unexpected refusal: %v", dec.Error)
	}
	if got := transformFor(t, dec, "count").Policy; got != types.PolicyAllow {
		t.Errorf("count over orders = %q, want allow", got)
	}
	if dec.Tier != types.Tier0Run {
		t.Errorf("tier = %d, want 0", dec.Tier)
	}
	if dec.Result.Rows[0][0] != int64(7) {
		t.Errorf("an allow column was masked: %v", dec.Result.Rows[0][0])
	}
}

// A computed column of text-like or unknown type inherits the strictest policy
// among ALL non-allow columns of the referenced relations. orders' only
// sensitive column is text, and it still poisons a text expression.
func TestComputedTextInheritsFromEveryFamily(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("upper", "text")}
		h.exec.rows = [][]any{{"NEW"}}
		h.exec.plan = readPlan(1, ordersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT upper(status) FROM orders"})

	tr := transformFor(t, dec, "upper")
	// token participates in the ordering and collapses to redact on inheritance
	// (R7.6b): a computed value has no namespace to tokenize under.
	if tr.Policy != types.PolicyRedact {
		t.Errorf("upper(...) over orders = %q, want redact", tr.Policy)
	}
	if dec.Result.Rows[0][0] != maskMarker {
		t.Errorf("a redacted value survived: %v", dec.Result.Rows[0][0])
	}
}

// With nothing sensitive in the referenced relation, the text branch is allow
// too — the rule masks broadly, not unconditionally.
func TestComputedTextOverACleanRelationIsAllowed(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("label", "text")}
		h.exec.rows = [][]any{{"ok"}}
		h.exec.plan = readPlan(1, cleanRef)
	})

	dec := h.query(t, Request{SQL: "SELECT label || '-x' FROM clean_ids"})

	if got := transformFor(t, dec, "label").Policy; got != types.PolicyAllow {
		t.Errorf("a computed text column over a clean relation = %q, want allow", got)
	}
}

// drop is never inherited (R7.6b). users.ssn is drop, and a computed text column
// over users must land on redact rather than deleting itself from the response —
// measured, SELECT upper(status), count(*) ... GROUP BY 1 returned counts with no
// group key when the rule got this wrong.
func TestDropIsNeverInheritedByAComputedColumn(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{computed("upper", "text"), computed("count", "int8")}
		h.exec.rows = [][]any{{"ACTIVE", int64(3)}}
		h.exec.plan = readPlan(1, usersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT upper(status), count(*) FROM users GROUP BY 1"})

	if got := transformFor(t, dec, "upper").Policy; got != types.PolicyRedact {
		t.Errorf("computed text over users = %q, want redact", got)
	}
	if names := columnNames(dec.Result.Columns); len(names) != 2 {
		t.Fatalf("the group key was dropped from the response: %v", names)
	}
	if dec.Result.Rows[0][1] != maskMarker {
		t.Error("count(*) over users should be redacted, not returned")
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
		SQL:           "SELECT 'Hi ' || $1::text, count(*) FROM clean_ids",
		Params:        []ports.Param{{Value: "Ada"}},
		ParamPolicies: []types.Policy{types.PolicyToken},
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

func TestUnresolvedColumnRedactsAtTierOne(t *testing.T) {
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
		t.Fatal("an unresolved column must not escalate: the judge cannot classify it and a human cannot decide it from a prompt")
	}
	if dec.Tier != types.Tier1Record {
		t.Errorf("tier = %d, want 1 — never tier 2 or 3 (R7.7d)", dec.Tier)
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

// --- R7.7a: a drop column is tier 1 and simply absent ------------------------

func TestDropColumnRunsAtTierOneAndIsAbsentFromTheResponse(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{
			fromColumn("id", "int8", usersOID, 1),
			fromColumn("ssn", "text", usersOID, 3),
		}
		h.exec.rows = [][]any{{int64(1), "123-45-6789"}, {int64(2), "987-65-4321"}}
		h.exec.plan = readPlan(1, usersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT id, ssn FROM users"})

	if dec.Error != nil || dec.Escalation != nil {
		t.Fatalf("a drop column is not an escalation: err=%v esc=%v", dec.Error, dec.Escalation)
	}
	if dec.Tier != types.Tier1Record {
		t.Errorf("tier = %d, want 1", dec.Tier)
	}
	if names := columnNames(dec.Result.Columns); len(names) != 1 || names[0] != "id" {
		t.Errorf("columns = %v, want only id", names)
	}
	for _, row := range dec.Result.Rows {
		if len(row) != 1 {
			t.Fatalf("row still carries the dropped column: %v", row)
		}
		for _, v := range row {
			if s, ok := v.(string); ok && strings.Contains(s, "-") {
				t.Fatalf("an SSN survived into the response: %v", v)
			}
		}
	}
	// transforms still reports the removal, which is what tells the agent the
	// column exists and why it is not here.
	if got := transformFor(t, dec, "ssn").Policy; got != types.PolicyDrop {
		t.Errorf("transforms[ssn] = %q, want drop", got)
	}
}

// --- R6.4b: a hidden name is hidden on every surface -------------------------

func TestHideNameIsSuppressedInColumnsAndTransforms(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{
			fromColumn("id", "int8", usersOID, 1),
			fromColumn("internal_flag", "bool", usersOID, 6),
		}
		h.exec.rows = [][]any{{int64(1), true}}
		h.exec.plan = readPlan(1, usersRef)
	})

	dec := h.query(t, Request{SQL: "SELECT id, internal_flag FROM users"})

	for _, name := range columnNames(dec.Result.Columns) {
		if name == "internal_flag" {
			t.Fatal("a hide_name column's name reached the response")
		}
	}
	for name := range dec.Result.Transforms {
		if name == "internal_flag" {
			t.Fatal("a hide_name column's name reached the transforms map")
		}
	}
	if len(dec.Result.Columns) != 2 {
		t.Errorf("columns = %v, want the hidden column kept under a placeholder", columnNames(dec.Result.Columns))
	}
}

// --- R4.2c: DDL is refused at every tier, in every mode, with any approval ----

func TestDDLIsRefusedEvenWithAnApprovalAndInPermissiveMode(t *testing.T) {
	for _, mode := range []types.Mode{types.ModeStrict, types.ModeAssisted, types.ModePermissive} {
		h := newHarness(t, func(h *harness) {
			h.authority.conn.Mode = mode
			h.exec.cols = nil
			h.exec.plan = &ports.PlanFacts{StatementType: pgdb.StmtDDL}
		})

		dec := h.query(t, Request{
			SQL:      "ALTER TABLE users ADD COLUMN x int",
			Approval: &Approval{TicketID: "t-1", Approver: "ada"},
		})

		if dec.Error == nil || dec.Error.Code != types.CodeDDLRefused {
			t.Fatalf("mode %s: got %v, want ddl_refused", mode, dec.Error)
		}
		if dec.Tier != types.Tier4Refuse {
			t.Errorf("mode %s: tier = %d, want 4", mode, dec.Tier)
		}
		if h.exec.called("Run") || h.exec.called("CommitWrite") || h.exec.called("PreviewWrite") {
			t.Errorf("mode %s: DDL reached the database: %v", mode, h.exec.calls)
		}
	}
}

// --- R4.2b: a write outside the recorded scope ------------------------------

func TestWriteOutsideTheRecordedScopeIsRefusedNamingTheRelation(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.authority.conn.HasWriteCredential = true
		h.authority.conn.WriteScope = []types.WriteScopeEntry{
			{Relation: usersRef, Operations: []types.WriteOp{types.WriteUpdate}},
		}
		h.exec.cols = nil
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtUpdate,
			Writes:        true,
			RelationNames: []types.RelationRef{ordersRef},
			Relations:     []uint32{ordersOID},
			EstimatedRows: 3,
		}
	})

	dec := h.query(t, Request{SQL: "UPDATE orders SET status = 'x'"})

	if dec.Error == nil || dec.Error.Code != types.CodeOutOfWriteScope {
		t.Fatalf("got %v, want out_of_write_scope", dec.Error)
	}
	if !strings.Contains(dec.Error.Summary, ordersRef.String()) {
		t.Errorf("the refusal must name the relation; got %q", dec.Error.Summary)
	}
	if dec.Tier != types.Tier4Refuse {
		t.Errorf("tier = %d, want 4", dec.Tier)
	}
	// Refused before the database would refuse it.
	if h.exec.called("PreviewWrite") || h.exec.called("CommitWrite") {
		t.Errorf("the write reached the database: %v", h.exec.calls)
	}
}

func TestWriteWithoutACredentialIsRefused(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.plan = &ports.PlanFacts{
			StatementType: pgdb.StmtUpdate, Writes: true,
			RelationNames: []types.RelationRef{usersRef}, Relations: []uint32{usersOID},
		}
	})
	dec := h.query(t, Request{SQL: "UPDATE users SET status = 'x'"})
	if dec.Error == nil || dec.Error.Code != types.CodeNoWriteCredential {
		t.Fatalf("got %v, want no_write_credential", dec.Error)
	}
}

// --- R4.2d–f: the write path ------------------------------------------------

func TestWriteEscalatesToTierThreeWithAPreviewAndCommitsOnlyOnApproval(t *testing.T) {
	setup := func(h *harness) {
		h.authority.conn.Mode = types.ModePermissive
		h.authority.conn.HasWriteCredential = true
		h.authority.conn.WriteScope = []types.WriteScopeEntry{
			{Relation: ordersRef, Operations: []types.WriteOp{types.WriteUpdate}},
		}
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
		// A standing grant exists for the relation; it must not lower a write.
		h.authority.grants[types.PathRef{ConnectionID: "acme_prod", Relation: ordersRef}] =
			&types.Grant{ID: "g-1", RowCeiling: 1000}
	}

	h := newHarness(t, setup)
	dec := h.query(t, Request{SQL: "UPDATE orders SET status='x' RETURNING user_email"})

	if dec.Escalation == nil {
		t.Fatalf("a write must reach tier 3: err=%v", dec.Error)
	}
	if dec.Tier != types.Tier3Approve {
		t.Errorf("tier = %d, want 3 — no mode and no allow rule authorizes a write", dec.Tier)
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

// --- R4.5: the denylist, through a view -------------------------------------

func TestDenylistedRelationReachedThroughAViewIsRefused(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.authority.conn.Denylist = []types.RelationRef{usersRef}
		h.authority.conn.Mode = types.ModePermissive
		h.exec.cols = []types.ColumnMeta{computed("n", "int8")}
		// The statement names public.user_stats. EXPLAIN expands the view, so
		// the plan names the base table — which is the point of R7.4c.
		h.exec.plan = readPlan(1, usersRef)
	})

	dec := h.query(t, Request{
		SQL:      "SELECT count(*) FROM public.user_stats",
		Approval: &Approval{TicketID: "t-1", Approver: "ada"},
	})

	if dec.Error == nil || dec.Error.Code != types.CodeDenylisted {
		t.Fatalf("got %v, want denylisted", dec.Error)
	}
	if !strings.Contains(dec.Error.Summary, usersRef.String()) {
		t.Errorf("the refusal must name the base table it caught; got %q", dec.Error.Summary)
	}
	if dec.Tier != types.Tier4Refuse {
		t.Errorf("tier = %d, want 4 — no approval overrides the denylist and no mode relaxes it", dec.Tier)
	}
	if h.exec.called("Run") {
		t.Error("a denylisted statement reached the database")
	}
}

// --- R9.3c: a grant covers exactly the path it names -------------------------

func grantPath(rel types.RelationRef) types.PathRef {
	return types.PathRef{ConnectionID: "acme_prod", Relation: rel}
}

func TestGrantForOneRelationDoesNotCoverAJoinToAnother(t *testing.T) {
	// A statement near the row cap is tier 2; a complete set of grants lowers it
	// to tier 1, and an incomplete set does not.
	base := func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", ordersOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(9000, ordersRef, cleanRef) // well past 80% of the 1000-row ceiling
	}

	// Only one of the two relations is granted.
	partial := newHarness(t, func(h *harness) {
		base(h)
		h.authority.grants[grantPath(ordersRef)] = &types.Grant{ID: "g-orders", RowCeiling: 5000}
	})
	dec := partial.query(t, Request{SQL: "SELECT o.id FROM orders o JOIN clean_ids c ON c.id = o.id"})
	if dec.Tier != types.Tier2Judge {
		t.Errorf("tier = %d, want 2: one unlisted relation sends the statement back to its normal tier", dec.Tier)
	}
	if strings.HasPrefix(dec.Result.Authorization, "grant:") {
		t.Errorf("authorization = %q: a partial match is not a match", dec.Result.Authorization)
	}

	// Both relations granted.
	full := newHarness(t, func(h *harness) {
		base(h)
		h.authority.grants[grantPath(ordersRef)] = &types.Grant{ID: "g-orders", RowCeiling: 5000}
		h.authority.grants[grantPath(cleanRef)] = &types.Grant{ID: "g-clean", RowCeiling: 5000}
	})
	dec = full.query(t, Request{SQL: "SELECT o.id FROM orders o JOIN clean_ids c ON c.id = o.id"})
	if dec.Tier != types.Tier1Record {
		t.Errorf("tier = %d, want 1: a grant lowers the tier but never below 1", dec.Tier)
	}
	if !strings.HasPrefix(dec.Result.Authorization, "grant:") {
		t.Errorf("authorization = %q, want a grant", dec.Result.Authorization)
	}

	// A suspended grant is not a grant (R9.3b).
	suspended := newHarness(t, func(h *harness) {
		base(h)
		h.authority.grants[grantPath(ordersRef)] = &types.Grant{ID: "g-orders", RowCeiling: 5000, Suspended: true}
		h.authority.grants[grantPath(cleanRef)] = &types.Grant{ID: "g-clean", RowCeiling: 5000}
	})
	dec = suspended.query(t, Request{SQL: "SELECT o.id FROM orders o JOIN clean_ids c ON c.id = o.id"})
	if dec.Tier != types.Tier2Judge {
		t.Errorf("tier = %d, want 2: a schema change suspends the rule pending review", dec.Tier)
	}
}

func TestGrantRowCeilingBoundsTheRequest(t *testing.T) {
	h := newHarness(t)
	conn := testConnection()
	conn.Limits.MaxRowsCeiling = 1000
	got := h.pipe.rowCeiling(Request{MaxRows: 900}, conn, []*types.Grant{{RowCeiling: 50}})
	if got != 50 {
		t.Errorf("rowCeiling = %d, want 50", got)
	}
	if got := h.pipe.rowCeiling(Request{MaxRows: 99999}, conn, nil); got != 1000 {
		t.Errorf("rowCeiling = %d, want the operator ceiling: the agent cannot raise it", got)
	}
}

// --- G6 tiers and the judge -------------------------------------------------

func TestJudgeFailureNeverCountsAsAFavourableVerdict(t *testing.T) {
	base := func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(9000, cleanRef)
	}

	// Assisted: deterministic masking continues, and the degradation is recorded.
	assisted := newHarness(t, func(h *harness) {
		base(h)
		h.judge = &fakeJudge{available: true, err: errors.New("endpoint down")}
	})
	dec := assisted.query(t, Request{SQL: "SELECT id FROM clean_ids"})
	if dec.Result == nil {
		t.Fatalf("an unreachable judge must not block an ordinary read: %v", dec.Error)
	}
	if dec.Tier != types.Tier2Judge {
		t.Errorf("tier = %d, want 2", dec.Tier)
	}
	if len(dec.Result.Degradations) == 0 || dec.Result.Degradations[0].Layer != "judge" {
		t.Errorf("degradations = %v, want the judge recorded as configured-but-down", dec.Result.Degradations)
	}

	// Strict: the same uncertainty is a human decision.
	strict := newHarness(t, func(h *harness) {
		base(h)
		h.authority.conn.Mode = types.ModeStrict
		h.judge = &fakeJudge{available: false}
	})
	dec = strict.query(t, Request{SQL: "SELECT id FROM clean_ids"})
	if dec.Escalation == nil || dec.Tier != types.Tier3Approve {
		t.Errorf("strict mode: tier = %d esc = %v, want an escalation", dec.Tier, dec.Escalation)
	}
}

func TestJudgeMayRaiseTheTierButNotRefuse(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.rows = [][]any{{int64(1)}}
		h.exec.plan = readPlan(9000, cleanRef)
		h.judge = &fakeJudge{available: true, verdict: &ports.JudgeVerdict{
			Tier: types.Tier4Refuse, ReasonCodes: []string{"intent_mismatch"}, Uncertainty: 0.9,
		}}
	})

	dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})

	if dec.Tier != types.Tier3Approve {
		t.Errorf("tier = %d, want 3: tier 4 belongs to the denylist, DDL and the write scope", dec.Tier)
	}
	if dec.Escalation == nil {
		t.Fatal("a raised tier should escalate")
	}
	if !hasReason(dec.Escalation.Facts.Reasons, "intent_mismatch") {
		t.Errorf("the judge's reason codes are missing from the approval facts: %v", dec.Escalation.Facts.Reasons)
	}
}

func TestJudgeReleaseNeedsPermissiveModeAndADelegation(t *testing.T) {
	build := func(mode types.Mode) *harness {
		return newHarness(t, func(h *harness) {
			h.authority.conn.Mode = mode
			h.exec.cols = []types.ColumnMeta{computed("total", "int8")}
			h.exec.rows = [][]any{{int64(5)}}
			h.exec.plan = readPlan(9000, usersRef) // near the cap → tier 2
			h.judge = &fakeJudge{available: true, verdict: &ports.JudgeVerdict{
				Tier: types.Tier1Record, Release: true, Uncertainty: 0.1,
			}}
		})
	}
	del := &Delegation{ID: "d-1", Relations: []types.RelationRef{usersRef}, ExpiresAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}

	// Assisted mode: the verdict cannot release anything.
	h := build(types.ModeAssisted)
	dec := h.query(t, Request{SQL: "SELECT sum(salary) FROM users", Delegation: del})
	if got := transformFor(t, dec, "total").Policy; got != types.PolicyRedact {
		t.Errorf("assisted mode released uncertain output: %q", got)
	}

	// Permissive mode with no delegation: still masked. Model absence and model
	// presence are both short of authorization.
	h = build(types.ModePermissive)
	dec = h.query(t, Request{SQL: "SELECT sum(salary) FROM users"})
	if got := transformFor(t, dec, "total").Policy; got != types.PolicyRedact {
		t.Errorf("permissive mode released without a delegation: %q", got)
	}

	// Permissive mode inside an explicit delegation: released, and the basis is
	// recorded.
	h = build(types.ModePermissive)
	dec = h.query(t, Request{SQL: "SELECT sum(salary) FROM users", Delegation: del})
	if got := transformFor(t, dec, "total").Policy; got != types.PolicyAllow {
		t.Errorf("a delegated release did not happen: %q", got)
	}
	if dec.Result.Authorization != "delegation:d-1" {
		t.Errorf("authorization = %q, want delegation:d-1", dec.Result.Authorization)
	}
}

func TestPinnedCatalogPolicyIsNeverReleasedByTheJudge(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.authority.conn.Mode = types.ModePermissive
		h.exec.cols = []types.ColumnMeta{fromColumn("email", "text", usersOID, 2)}
		h.exec.rows = [][]any{{"ada@example.com"}}
		h.exec.plan = readPlan(9000, usersRef)
		h.judge = &fakeJudge{available: true, verdict: &ports.JudgeVerdict{Tier: types.Tier1Record, Release: true}}
	})

	dec := h.query(t, Request{
		SQL:        "SELECT email FROM users",
		Delegation: &Delegation{ID: "d-1", Relations: []types.RelationRef{usersRef}},
	})

	if got := transformFor(t, dec, "email").Policy; got != types.PolicyToken {
		t.Errorf("a pinned catalog policy was lowered to %q", got)
	}
	if dec.Result.Rows[0][0] != maskMarker {
		t.Error("a token column's value was released")
	}
}

func TestStaleCatalogIsUncertaintyNotPermission(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.catalog.fresh = false
		h.authority.conn.Mode = types.ModeStrict
		h.exec.cols = []types.ColumnMeta{fromColumn("id", "int8", cleanOID, 1)}
		h.exec.plan = readPlan(1, cleanRef)
	})

	dec := h.query(t, Request{SQL: "SELECT id FROM clean_ids"})

	if dec.Escalation == nil {
		t.Fatalf("a changed view definition in strict mode should reach a human: tier %d", dec.Tier)
	}
	if !hasReason(dec.Escalation.Facts.Reasons, ReasonStaleCatalog) {
		t.Errorf("reasons = %v, want stale_catalog", dec.Escalation.Facts.Reasons)
	}
}

// --- G1, G3 and G9 ----------------------------------------------------------

func TestMultiStatementInputIsRefusedAtTierFour(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.exec.describeErr = &types.Error{Code: types.CodeMultiStatement, Summary: "one statement only"}
	})
	dec := h.query(t, Request{SQL: "SELECT 1; DROP TABLE users"})
	if dec.Error == nil || dec.Error.Code != types.CodeMultiStatement {
		t.Fatalf("got %v, want multi_statement", dec.Error)
	}
	if dec.Tier != types.Tier4Refuse {
		t.Errorf("tier = %d, want 4", dec.Tier)
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
	if strings.Contains(dec.Error.Summary, "internal_flag") {
		t.Error("a hide_name column was disclosed in an error message")
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
			h.exec.plan = readPlan(9000, cleanRef)
			h.authority.conn.Mode = types.ModeStrict
			h.catalog.fresh = false
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
		if rec.SessionID != "sess-1" || rec.Intent == "" {
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

func TestDisabledConnectionIsRefused(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.authority.conn.Enabled = false
	})
	dec := h.query(t, Request{SQL: "SELECT 1"})
	if dec.Error == nil || dec.Error.Code != types.CodeConnectionDisabled {
		t.Fatalf("got %v, want connection_disabled", dec.Error)
	}
	if h.exec.called("Describe") {
		t.Error("a disabled connection reached the database")
	}
}

// --- Explain ----------------------------------------------------------------

func TestExplainPredictsTheTierWithoutExecuting(t *testing.T) {
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
	if res.PredictedTier != types.Tier1Record {
		t.Errorf("predicted tier = %d, want 1", res.PredictedTier)
	}
	if names := columnNames(res.OutputColumns); len(names) != 2 {
		t.Errorf("output columns = %v, want the drop column omitted", names)
	}
	if !hasReason(res.Reasons, ReasonDropped) || !hasReason(res.Reasons, ReasonMasked) {
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
