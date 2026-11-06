package catalog

import (
	"context"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

type fakeSystem struct{ relations []ports.Relation }

type fakeStats map[types.RelationRef]map[string]TextShape

func (f fakeStats) TextShapes(context.Context, string) (map[types.RelationRef]map[string]TextShape, error) {
	return f, nil
}

func (f fakeSystem) IntrospectSystem(context.Context, string) ([]ports.Relation, error) {
	return f.relations, nil
}

func TestMask_MASK_C22_ShortFewValuedTextIsScannedAndFreeTextAndCredentialsAreRedacted(t *testing.T) {
	carrier := types.RelationRef{Schema: "public", Relation: "carrier"}
	rels := []ports.Relation{{
		Ref: carrier, OID: 100, Kind: 'r',
		Columns: []ports.Column{
			{Name: "carrier_name", AttNum: 1, TypeOID: 25},
			{Name: "api_key", AttNum: 2, TypeOID: 25},
			{Name: "password_hash", AttNum: 3, TypeOID: 1043},
			{Name: "notes", AttNum: 4, TypeOID: 25},
			{Name: "tags", AttNum: 5, TypeOID: 1009},
			{Name: "status", AttNum: 6, TypeOID: 25},
			{Name: "name", AttNum: 7, TypeOID: 25},
		},
	}}
	stats := fakeStats{carrier: {
		"carrier_name":  {Distinct: 40, AvgWidth: 12},
		"api_key":       {Distinct: 3, AvgWidth: 20},
		"password_hash": {Distinct: 3, AvgWidth: 20},
		"notes":         {Distinct: 900, AvgWidth: 80},
		"name":          {Distinct: 20, AvgWidth: 12},
	}}
	s, _ := openTestStore(t, Dependencies{Introspector: &fakeIntrospector{relations: rels}, Stats: stats}, nil)
	entries, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]types.Policy{
		"public.carrier.carrier_name":  types.PolicyScan,
		"public.carrier.api_key":       types.PolicyRedact,
		"public.carrier.password_hash": types.PolicyRedact,
		"public.carrier.notes":         types.PolicyRedact,
		"public.carrier.tags":          types.PolicyRedact,
		"public.carrier.status":        types.PolicyRedact,
		"public.carrier.name":          types.PolicyRedact,
	}
	for k, w := range want {
		if got := entries[k].Policy; got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}
}

func TestADefaultFromAnOlderRuleIsReplacedAndAPersonsEntryIsKept(t *testing.T) {
	rels := []ports.Relation{{
		Ref: types.RelationRef{Schema: "public", Relation: "carrier"}, OID: 100, Kind: 'r',
		Columns: []ports.Column{{Name: "carrier_name", AttNum: 1, TypeOID: 25}, {Name: "iin", AttNum: 2, TypeOID: 25}},
	}}
	paths := testPaths(t)
	old := map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "carrier", Column: "carrier_name"}: {Policy: types.PolicyRedact},
		{Schema: "public", Table: "gone", Column: "x"}:               {Policy: types.PolicyRedact},
	}
	if err := saveYAMLFile(paths.Overlay, old); err != nil {
		t.Fatal(err)
	}
	if err := saveYAMLFile(paths.Committed, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "carrier", Column: "iin"}: {Policy: types.PolicyRedact},
	}); err != nil {
		t.Fatal(err)
	}
	stats := fakeStats{{Schema: "public", Relation: "carrier"}: {"carrier_name": {Distinct: 40, AvgWidth: 12}}}
	s := NewStore(Dependencies{Introspector: &fakeIntrospector{relations: rels}, Stats: stats})
	if _, err := s.Open(t.Context(), "conn1", paths); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["public.carrier.carrier_name"].Policy; got != types.PolicyScan {
		t.Errorf("carrier_name = %q, want the current default scan", got)
	}
	if got := entries["public.carrier.iin"].Policy; got != types.PolicyRedact {
		t.Errorf("iin = %q, want the person's redact", got)
	}
	if _, ok := entries["public.gone.x"]; ok {
		t.Error("a default for a dropped table survived")
	}
}

func TestSystemCatalogColumnsAreClassified(t *testing.T) {
	sys := []ports.Relation{
		{
			Ref: types.RelationRef{Schema: "pg_catalog", Relation: "pg_stat_activity"}, OID: 12000, Kind: 'v',
			Columns: []ports.Column{
				{Name: "datname", AttNum: 1, TypeOID: 19},
				{Name: "numbackends", AttNum: 2, TypeOID: 23},
				{Name: "client_addr", AttNum: 3, TypeOID: 869},
				{Name: "query", AttNum: 4, TypeOID: 25},
			},
		},
		{
			Ref: types.RelationRef{Schema: "pg_catalog", Relation: "pg_stats"}, OID: 12001, Kind: 'v',
			Columns: []ports.Column{{Name: "most_common_vals", AttNum: 1, TypeOID: 2277}},
		},
	}
	s, _ := openTestStore(t, Dependencies{Introspector: &fakeIntrospector{}, System: fakeSystem{sys}, Fingerprints: &fakeFingerprints{}}, nil)
	c, err := s.get("conn1")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		oid    uint32
		att    uint16
		policy types.Policy
	}{
		{12000, 1, types.PolicyAllow},
		{12000, 2, types.PolicyAllow},
		{12000, 3, types.PolicyAllow},
		{12000, 4, types.PolicyRedact},
		{12001, 1, types.PolicyRedact},
	}
	for _, w := range want {
		got, ok := c.Lookup(w.oid, w.att)
		if !ok || got.Policy != w.policy {
			t.Errorf("(%d,%d) = %v %v, want %q", w.oid, w.att, got.Policy, ok, w.policy)
		}
	}
	if ref, ok := c.Relation(12000); !ok || ref.Relation != "pg_stat_activity" {
		t.Errorf("Relation(12000) = %v %v", ref, ok)
	}
	entries, err := s.Entries(t.Context(), "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("system columns reached the editable catalog: %v", entries)
	}
	fresh, err := c.Fresh(t.Context(), []uint32{12000})
	if err == nil && fresh {
		return
	}
	t.Errorf("Fresh over a system relation = %v %v, want true", fresh, err)
}

func TestATableCreatedAfterOpeningIsClassifiedOnFirstUse(t *testing.T) {
	in := &fakeIntrospector{}
	fp := &fakeFingerprints{byOID: map[uint32]string{}}
	s, _ := openTestStore(t, Dependencies{Introspector: in, Fingerprints: fp}, nil)
	c, err := s.get("conn1")
	if err != nil {
		t.Fatal(err)
	}
	in.relations = []ports.Relation{{
		Ref: types.RelationRef{Schema: "public", Relation: "carrier"}, OID: 100, Kind: 'r',
		Columns: []ports.Column{{Name: "id", AttNum: 1, TypeOID: 23}},
	}}
	fp.byOID[100] = "v1"
	if _, ok := c.Lookup(100, 1); ok {
		t.Fatal("the new table is known before any refresh")
	}
	fresh, err := c.Fresh(t.Context(), []uint32{100})
	if err != nil || !fresh {
		t.Fatalf("Fresh = %v %v, want true after the refresh", fresh, err)
	}
	if got, ok := c.Lookup(100, 1); !ok || got.Policy != types.PolicyAllow {
		t.Errorf("id = %v %v, want allow", got, ok)
	}
}

func TestARelationThatCannotResolveDoesNotRefreshAgain(t *testing.T) {
	in := &countingIntrospector{}
	fp := &fakeFingerprints{byOID: map[uint32]string{}}
	s, _ := openTestStore(t, Dependencies{Introspector: in, Fingerprints: fp}, nil)
	c, err := s.get("conn1")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if fresh, _ := c.Fresh(t.Context(), []uint32{999}); fresh {
			t.Fatal("an unresolvable relation reported fresh")
		}
	}
	if in.calls != 2 {
		t.Errorf("introspected %d times, want 2: one to open, one to try the relation", in.calls)
	}
}

type countingIntrospector struct{ calls int }

func (c *countingIntrospector) Introspect(context.Context, string) ([]ports.Relation, error) {
	c.calls++
	return nil, nil
}
