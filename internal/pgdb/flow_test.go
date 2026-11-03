package pgdb

import (
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

// flow_plans.json holds EXPLAIN (VERBOSE, FORMAT JSON) output captured from
// PostgreSQL 18 for each statement, so the flow reader is tested against what
// the server prints and not against what a hand-written plan assumes.
type flowCase struct {
	Name string        `json:"name"`
	SQL  string        `json:"sql"`
	Plan []explainRoot `json:"plan"`
}

var flowSchema = map[string][]string{
	"customers": {"id", "email", "ssn", "dob", "tags"},
	"orders":    {"id", "customer_id", "total", "note"},
}

func TestFlowsOfRealPlans(t *testing.T) {
	raw, err := os.ReadFile("testdata/flow_plans.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []flowCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		flows  []string
		opaque bool
	}{
		"count star with filter on masked column": {},
		"count star":                  {},
		"sum of allowed column":       {flows: []string{"orders.total"}},
		"upper of one column":         {flows: []string{"orders.note"}},
		"join with aliases":           {flows: []string{"customers.email", "customers.id", "orders.total"}},
		"count over a join":           {flows: []string{"customers.id"}},
		"whole row":                   {opaque: true},
		"whole row inside a function": {opaque: true},
		"group by with sort":          {flows: []string{"orders.customer_id", "orders.total"}},
		"renaming subquery":           {flows: []string{"orders.total"}},
		"subquery with limit":         {flows: []string{"orders.id", "orders.total"}},
		"materialized cte":            {flows: []string{"customers.dob", "customers.email", "customers.id", "customers.ssn", "customers.tags"}},
		"union":                       {flows: []string{"customers.id", "orders.id"}},
		"lateral join":                {flows: []string{"customers.id", "customers.ssn", "orders.id"}},
		"scalar subquery":             {flows: []string{"customers.ssn"}},
		"sql function inlined":        {flows: []string{"orders.id"}},
		"plpgsql call":                {flows: []string{"orders.id"}},
		"quoted alias":                {flows: []string{"orders.id"}},
		"sort by unselected column":   {flows: []string{"orders.id", "orders.note"}},
		"date arithmetic":             {flows: []string{"customers.dob"}},
		"unnest of a column":          {flows: []string{"customers.tags"}},
		"values lateral":              {flows: []string{"customers.ssn"}},
	}
	for _, c := range cases {
		w, ok := want[c.Name]
		if !ok {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			r := walkPlan(c.Plan)
			var got []string
			for _, f := range r.flows {
				if slices.Contains(flowSchema[f.rel.name], f.column) {
					got = append(got, f.rel.name+"."+f.column)
				}
			}
			slices.Sort(got)
			if !slices.Equal(got, w.flows) || r.opaque != w.opaque {
				t.Errorf("%s\nflows = %v opaque = %v, want %v opaque = %v", c.SQL, got, r.opaque, w.flows, w.opaque)
			}
		})
	}
}

func TestFlowsAnUnreadableNodeIsOpaque(t *testing.T) {
	for name, node := range map[string]string{
		"foreign scan without output": `{"Node Type": "Foreign Scan"}`,
		"table function scan":         `{"Node Type": "Table Function Scan"}`,
		"unknown node":                `{"Node Type": "Future Scan", "Output": ["a"]}`,
	} {
		var roots []explainRoot
		if err := json.Unmarshal([]byte(`[{"Plan": `+node+`}]`), &roots); err != nil {
			t.Fatal(err)
		}
		if !walkPlan(roots).opaque {
			t.Errorf("%s: plan not opaque", name)
		}
	}
}

func TestWalkPlanReadsEveryRoot(t *testing.T) {
	var roots []explainRoot
	raw := `[{"Plan": {"Node Type": "ModifyTable", "Operation": "Update", "Relation Name": "a", "Schema": "public"}},
	         {"Plan": {"Node Type": "ModifyTable", "Operation": "Delete", "Relation Name": "b", "Schema": "public"}}]`
	if err := json.Unmarshal([]byte(raw), &roots); err != nil {
		t.Fatal(err)
	}
	if got := len(walkPlan(roots).targets); got != 2 {
		t.Errorf("targets = %d, want 2: a rule's second statement writes to another table", got)
	}
}

func TestFlowsAHugeFoldedConstantIsOpaque(t *testing.T) {
	var roots []explainRoot
	huge := strings.Repeat("x", maxScannedPlan+1)
	raw := `[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "a", "Schema": "public", "Alias": "a", "Output": ["'` + huge + `'::text"]}}]`
	if err := json.Unmarshal([]byte(raw), &roots); err != nil {
		t.Fatal(err)
	}
	if !walkPlan(roots).opaque {
		t.Error("a plan with a huge constant was read")
	}
}
