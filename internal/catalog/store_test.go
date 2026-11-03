package catalog

import (
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
	users := ports.Relation{
		Ref:     types.RelationRef{Schema: "public", Relation: "users"},
		OID:     100,
		Kind:    'r',
		Columns: []ports.Column{{Name: "id", AttNum: 1, TypeOID: 23}},
	}
	introspector := &fakeIntrospector{relations: []ports.Relation{users}}
	s, _ := openTestStore(t, Dependencies{Introspector: introspector}, nil)
	users.Columns = append(users.Columns, ports.Column{Name: "email", AttNum: 2, TypeOID: 25})
	introspector.relations = []ports.Relation{users}

	got, err := s.Unclassified(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "public.users.email" {
		t.Errorf("Unclassified() = %v, want [public.users.email]", got)
	}
}
