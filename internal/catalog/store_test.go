package catalog

import (
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func TestEntriesReturnsDottedMergedKeys(t *testing.T) {
	s, _ := openTestStore(t, Dependencies{}, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "id"}: {Policy: types.PolicyAllow},
	})
	entries, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["public.users.id"]; got.Policy != types.PolicyAllow {
		t.Errorf("Entries()[%q] = %+v, want allow", "public.users.id", got)
	}
}

func TestUnclassifiedListsUncataloguedColumns(t *testing.T) {
	introspector := &fakeIntrospector{relations: []ports.Relation{
		{
			Ref:  types.RelationRef{Schema: "public", Relation: "users"},
			OID:  100,
			Kind: 'r',
			Columns: []ports.Column{
				{Name: "id", AttNum: 1, TypeOID: 23},
				{Name: "email", AttNum: 2, TypeOID: 25},
			},
		},
	}}
	s, _ := openTestStore(t, Dependencies{Introspector: introspector}, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "id"}: {Policy: types.PolicyAllow},
	})

	got, err := s.Unclassified(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "public.users.email" {
		t.Errorf("Unclassified() = %v, want [public.users.email]", got)
	}
}

func TestSuggestGrantsOmitsDropColumns(t *testing.T) {
	introspector := &fakeIntrospector{relations: []ports.Relation{
		{
			Ref:  types.RelationRef{Schema: "public", Relation: "users"},
			OID:  100,
			Kind: 'r',
			Columns: []ports.Column{
				{Name: "id", AttNum: 1, TypeOID: 23},
				{Name: "email", AttNum: 2, TypeOID: 25},
				{Name: "ssn", AttNum: 3, TypeOID: 25},
			},
		},
	}}
	s, _ := openTestStore(t, Dependencies{Introspector: introspector}, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "ssn"}: {Policy: types.PolicyDrop},
	})

	stmts, err := s.SuggestGrants(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 1 {
		t.Fatalf("SuggestGrants() = %v, want exactly one statement", stmts)
	}
	stmt := stmts[0]
	if !strings.Contains(stmt, `"id"`) || !strings.Contains(stmt, `"email"`) {
		t.Errorf("statement missing a kept column: %s", stmt)
	}
	if strings.Contains(stmt, `"ssn"`) {
		t.Errorf("statement includes a dropped column: %s", stmt)
	}
	if !strings.Contains(stmt, `GRANT SELECT`) || !strings.Contains(stmt, `"public"."users"`) {
		t.Errorf("statement is not a GRANT SELECT on public.users: %s", stmt)
	}
}
