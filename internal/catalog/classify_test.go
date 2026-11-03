package catalog

import (
	"reflect"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func classifyRelations() []ports.Relation {
	return []ports.Relation{{
		Ref: types.RelationRef{Schema: "public", Relation: "staff"}, OID: 100, Kind: 'r',
		Columns: []ports.Column{
			{Name: "id", AttNum: 1, TypeOID: 23},
			{Name: "email", AttNum: 2, TypeOID: 25},
			{Name: "notes", AttNum: 3, TypeOID: 25},
			{Name: "salary", AttNum: 4, TypeOID: 1700},
			{Name: "date_of_birth", AttNum: 5, TypeOID: 1082},
			{Name: "balance", AttNum: 6, TypeOID: 1700},
			{Name: "phone", AttNum: 7, TypeOID: 23},
		},
	}}
}

func TestMask_MASK_C18_ClassifyingAgainKeepsWhatAPersonSet(t *testing.T) {
	deps := Dependencies{Introspector: &fakeIntrospector{relations: classifyRelations()}}
	s, paths := openTestStore(t, deps, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "staff", Column: "email"}: {Policy: types.PolicyAllow},
	})
	first, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if got := first["public.staff.email"]; got.Policy != types.PolicyAllow {
		t.Fatalf("email = %+v, want the person's allow", got)
	}
	if _, err := s.Open(t.Context(), "conn1", paths); err != nil {
		t.Fatal(err)
	}
	second, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("classifying again changed the catalog:\n%+v\n%+v", first, second)
	}
}

func TestMask_MASK_C19_PayAndBirthDateAreMaskedNotAllowed(t *testing.T) {
	deps := Dependencies{Introspector: &fakeIntrospector{relations: classifyRelations()}}
	s, _ := openTestStore(t, deps, nil)
	entries, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]types.Policy{
		"public.staff.id":            types.PolicyAllow,
		"public.staff.balance":       types.PolicyAllow,
		"public.staff.salary":        types.PolicyRedact,
		"public.staff.date_of_birth": types.PolicyRedact,
		"public.staff.notes":         types.PolicyRedact,
		"public.staff.email":         types.PolicyToken,
		"public.staff.phone":         types.PolicyRedact,
	}
	for key, policy := range want {
		if got := entries[key].Policy; got != policy {
			t.Errorf("%s = %s, want %s", key, got, policy)
		}
	}
	if got := entries["public.staff.email"].Namespace; got != "email" {
		t.Errorf("email namespace = %q", got)
	}
}

func TestViewColumnsAreRedactedUnlessDeclared(t *testing.T) {
	rels := []ports.Relation{
		{
			Ref: types.RelationRef{Schema: "public", Relation: "emp_view"}, OID: 200, Kind: 'v',
			Columns: []ports.Column{{Name: "amount", AttNum: 1, TypeOID: 1700}, {Name: "n", AttNum: 2, TypeOID: 23}},
		},
		{
			Ref: types.RelationRef{Schema: "public", Relation: "emp_mat"}, OID: 201, Kind: 'm',
			Columns: []ports.Column{{Name: "amount", AttNum: 1, TypeOID: 1700}},
		},
	}
	s, _ := openTestStore(t, Dependencies{Introspector: &fakeIntrospector{relations: rels}}, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "emp_view", Column: "n"}: {Policy: types.PolicyAllow},
	})
	entries, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]types.Policy{
		"public.emp_view.amount": types.PolicyRedact,
		"public.emp_view.n":      types.PolicyAllow,
		"public.emp_mat.amount":  types.PolicyRedact,
	}
	for k, w := range want {
		if got := entries[k].Policy; got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
}
