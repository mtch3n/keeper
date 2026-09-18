package pgdb

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func parsePlan(t *testing.T, raw string) walkResult {
	t.Helper()
	var roots []explainRoot
	if err := json.Unmarshal([]byte(raw), &roots); err != nil {
		t.Fatalf("decoding EXPLAIN output: %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("EXPLAIN output had no plan")
	}
	return walkPlan(&roots[0].Plan)
}

// A view is expanded by EXPLAIN, so the plan names the base tables and never the
// view. This is why the denylist and the grants are evaluated against the plan's
// relation list rather than statement text (R7.4c).
const viewPlan = `[
  {
    "Plan": {
      "Node Type": "Hash Join",
      "Total Cost": 91.2,
      "Plan Rows": 1420,
      "Hash Cond": "(o.user_id = u.id)",
      "Plans": [
        {"Node Type": "Seq Scan", "Relation Name": "orders", "Schema": "public", "Alias": "o", "Plan Rows": 1420, "Total Cost": 30.1},
        {"Node Type": "Hash", "Plans": [
          {"Node Type": "Seq Scan", "Relation Name": "users", "Schema": "public", "Alias": "u", "Plan Rows": 200, "Total Cost": 12.0, "Filter": "(deleted_at IS NULL)"}
        ]}
      ]
    }
  }
]`

func TestWalkPlanCollectsEveryRelationBehindAView(t *testing.T) {
	w := parsePlan(t, viewPlan)

	want := []relRef{{schema: "public", name: "orders"}, {schema: "public", name: "users"}}
	if len(w.relations) != len(want) {
		t.Fatalf("relations = %v, want %v", w.relations, want)
	}
	for i := range want {
		if w.relations[i] != want[i] {
			t.Errorf("relations[%d] = %v, want %v", i, w.relations[i], want[i])
		}
	}
	if len(w.targets) != 0 {
		t.Errorf("a join of two scans is not a write: targets = %v", w.targets)
	}
	if !w.filtered {
		t.Error("a plan with a Filter and a Hash Cond should report HasFilter")
	}
	if w.rows != 1420 || w.cost != 91.2 {
		t.Errorf("estimates = (%d, %v), want (1420, 91.2)", w.rows, w.cost)
	}
}

const updatePlan = `[
  {
    "Plan": {
      "Node Type": "ModifyTable",
      "Operation": "Update",
      "Relation Name": "orders",
      "Schema": "public",
      "Plan Rows": 0,
      "Total Cost": 44.5,
      "Plans": [
        {"Node Type": "Nested Loop", "Plan Rows": 12, "Plans": [
          {"Node Type": "Seq Scan", "Relation Name": "orders", "Schema": "public", "Plan Rows": 12, "Filter": "(status = 'new'::text)"},
          {"Node Type": "Index Scan", "Relation Name": "users", "Schema": "public", "Plan Rows": 1, "Index Cond": "(id = orders.user_id)"}
        ]}
      ]
    }
  }
]`

func TestWalkPlanIdentifiesTheWriteTarget(t *testing.T) {
	w := parsePlan(t, updatePlan)

	if w.operation != "UPDATE" {
		t.Errorf("operation = %q, want UPDATE", w.operation)
	}
	if len(w.targets) != 1 || w.targets[0] != (relRef{schema: "public", name: "orders"}) {
		t.Fatalf("targets = %v, want one entry for public.orders", w.targets)
	}
	// The source relation is still in the plan: it is what the denylist and the
	// grants are checked against.
	if len(w.relations) != 2 {
		t.Errorf("relations = %v, want orders and users", w.relations)
	}
	// A ModifyTable with no RETURNING estimates zero rows; the count a human
	// needs is the rows feeding it.
	if w.rows != 12 {
		t.Errorf("estimated rows = %d, want the 12 rows feeding the ModifyTable", w.rows)
	}
}

const dataModifyingCTEPlan = `[
  {
    "Plan": {
      "Node Type": "ModifyTable",
      "Operation": "Update",
      "Relation Name": "b",
      "Schema": "public",
      "Plan Rows": 1,
      "Total Cost": 10,
      "Plans": [
        {"Node Type": "ModifyTable", "Operation": "Delete", "Relation Name": "a", "Schema": "public", "Plan Rows": 1}
      ]
    }
  }
]`

func TestWalkPlanSeesBothTargetsOfADataModifyingCTE(t *testing.T) {
	w := parsePlan(t, dataModifyingCTEPlan)
	if len(w.targets) != 2 {
		t.Fatalf("targets = %v, want both relations the statement modifies", w.targets)
	}
}

func TestExplainPrefixNeverAnalyzes(t *testing.T) {
	// CONTRACT §4 rule 3 and SPEC R7.4b. Constant folding already evaluates
	// IMMUTABLE functions at plan time; ANALYZE would run the statement.
	if strings.Contains(strings.ToUpper(explainPrefix), "ANALYZE") {
		t.Fatalf("explainPrefix contains ANALYZE: %q", explainPrefix)
	}
	if !strings.Contains(explainPrefix, "FORMAT JSON") {
		t.Fatalf("explainPrefix does not ask for JSON: %q", explainPrefix)
	}
	if !strings.Contains(explainPrefix, "VERBOSE") {
		t.Fatalf("explainPrefix does not ask for VERBOSE, which is what supplies the schema name: %q", explainPrefix)
	}
}

func TestTypeFamiliesAreEnumeratedNotCategorised(t *testing.T) {
	// SPEC R7.6c: PostgreSQL puts uuid, json, jsonb, bytea and xml all in
	// typcategory U. A category test would put json_agg in the uuid family.
	cases := []struct {
		oid  uint32
		name string
		want ports.TypeFamily
	}{
		{2950, "uuid", ports.FamilyUUID},
		{3802, "jsonb", ports.FamilyText},
		{114, "json", ports.FamilyText},
		{142, "xml", ports.FamilyText},
		{17, "bytea", ports.FamilyText},
		{20, "int8", ports.FamilyNumeric},
		{1700, "numeric", ports.FamilyNumeric},
		{16, "bool", ports.FamilyBoolean},
		{1082, "date", ports.FamilyDateTime},
		{1184, "timestamptz", ports.FamilyDateTime},
		{25, "text", ports.FamilyText},
		{869, "inet", ports.FamilyText},
	}
	for _, c := range cases {
		if got := FamilyForOID(c.oid); got != c.want {
			t.Errorf("FamilyForOID(%d) = %q, want %q", c.oid, got, c.want)
		}
		if got := FamilyForName(c.name); got != c.want {
			t.Errorf("FamilyForName(%q) = %q, want %q", c.name, got, c.want)
		}
		if nameByOID[c.oid] != c.name {
			t.Errorf("nameByOID[%d] = %q, want %q", c.oid, nameByOID[c.oid], c.name)
		}
	}

	if got := FamilyForOID(999999); got != ports.FamilyUnknown {
		t.Errorf("an unenumerated OID should be unknown, got %q", got)
	}
	if got := FamilyForName("citext"); got != ports.FamilyUnknown {
		t.Errorf("an unenumerated name should be unknown, got %q", got)
	}

	// Unknown takes R7.6's broad branch, which is the branch that masks more.
	for _, f := range []ports.TypeFamily{ports.FamilyText, ports.FamilyUnknown} {
		if !TextLike(f) {
			t.Errorf("TextLike(%q) = false", f)
		}
	}
	for _, f := range []ports.TypeFamily{ports.FamilyNumeric, ports.FamilyBoolean, ports.FamilyDateTime, ports.FamilyUUID} {
		if TextLike(f) {
			t.Errorf("TextLike(%q) = true", f)
		}
	}
}

func TestEffectiveMaxRowsRespectsTheOperatorCeiling(t *testing.T) {
	cases := []struct {
		requested int
		ceiling   int
		want      int
	}{
		{0, 500, 500},    // no request means the ceiling
		{100, 500, 100},  // under the ceiling
		{5000, 500, 500}, // the agent cannot raise it
		{-1, 500, 500},
	}
	for _, c := range cases {
		lim := types.DefaultLimits()
		lim.MaxRowsCeiling = c.ceiling
		got := effectiveMaxRows(c.requested, lim)
		if got != c.want {
			t.Errorf("effectiveMaxRows(%d, ceiling %d) = %d, want %d", c.requested, c.ceiling, got, c.want)
		}
	}
}
