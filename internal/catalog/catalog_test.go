package catalog

import (
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func resolvedTestCatalog(t *testing.T, priv PrivilegeChecker, fp Fingerprinter, committed map[columnKey]types.ColumnPolicy) *Catalog {
	t.Helper()
	paths := testPaths(t)
	if committed != nil {
		if err := saveYAMLFile(paths.Committed, committed); err != nil {
			t.Fatal(err)
		}
	}
	c, err := openCatalog("conn1", paths, priv, fp)
	if err != nil {
		t.Fatalf("openCatalog: %v", err)
	}
	t.Cleanup(func() { c.close() })

	rel := ports.Relation{
		Ref:  types.RelationRef{Schema: "public", Relation: "users"},
		OID:  100,
		Kind: 'r',
		Columns: []ports.Column{
			{Name: "id", AttNum: 1, TypeOID: 23},
			{Name: "email", AttNum: 2, TypeOID: 25},
			{Name: "ssn", AttNum: 3, TypeOID: 20},
		},
	}
	if err := c.resolve(t.Context(), []ports.Relation{rel}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return c
}

func TestLookupResolvesByIdentityNotName(t *testing.T) {
	c := resolvedTestCatalog(t, nil, nil, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyToken, Namespace: "email"},
	})

	p, ok := c.Lookup(100, 2)
	if !ok || p.Policy != types.PolicyToken {
		t.Errorf("Lookup(100,2) = %+v (ok=%v), want token", p, ok)
	}
	if _, ok := c.Lookup(100, 99); ok {
		t.Errorf("Lookup of an unresolved attnum should report false")
	}
	if _, ok := c.Lookup(999, 2); ok {
		t.Errorf("Lookup of an unresolved table OID should report false")
	}
}

func TestRelationNamesATableOID(t *testing.T) {
	c := resolvedTestCatalog(t, nil, nil, nil)
	ref, ok := c.Relation(100)
	if !ok || ref.Schema != "public" || ref.Relation != "users" {
		t.Errorf("Relation(100) = %+v (ok=%v)", ref, ok)
	}
	if _, ok := c.Relation(404); ok {
		t.Errorf("Relation of an unknown OID should report false")
	}
}

func TestRelationPoliciesExcludesAllowAndCarriesFamily(t *testing.T) {
	c := resolvedTestCatalog(t, nil, nil, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "id"}:    {Policy: types.PolicyAllow},
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyToken, Namespace: "email"},
		{Schema: "public", Table: "users", Column: "ssn"}:   {Policy: types.PolicyDrop},
	})

	got := c.RelationPolicies(100)
	if len(got) != 2 {
		t.Fatalf("RelationPolicies(100) = %+v, want 2 entries (email token, ssn drop)", got)
	}
	byColumn := make(map[string]ports.FamilyPolicy, len(got))
	for _, fp := range got {
		byColumn[fp.Column] = fp
	}
	if _, ok := byColumn["id"]; ok {
		t.Errorf("allow column must not appear in RelationPolicies")
	}
	if fp, ok := byColumn["email"]; !ok || fp.Family != ports.FamilyText || fp.Policy.Policy != types.PolicyToken {
		t.Errorf("email: got %+v (ok=%v), want FamilyText/token", fp, ok)
	}
	if fp, ok := byColumn["ssn"]; !ok || fp.Family != ports.FamilyNumeric || fp.Policy.Policy != types.PolicyDrop {
		t.Errorf("ssn: got %+v (ok=%v), want FamilyNumeric/drop", fp, ok)
	}
}

func TestReadableDelegatesToPrivilegeChecker(t *testing.T) {
	priv := &fakePrivileges{columns: map[string][]string{
		"public.users": {"id", "email"},
	}}
	c := resolvedTestCatalog(t, priv, nil, nil)

	got, err := c.Readable(t.Context(), types.RelationRef{Schema: "public", Relation: "users"})
	if err != nil {
		t.Fatalf("Readable: %v", err)
	}
	if len(got) != 2 || got[0] != "id" || got[1] != "email" {
		t.Errorf("Readable() = %v, want [id email]", got)
	}
}

func TestReadableWithoutAPrivilegeCheckerErrors(t *testing.T) {
	c := resolvedTestCatalog(t, nil, nil, nil)
	if _, err := c.Readable(t.Context(), types.RelationRef{Schema: "public", Relation: "users"}); err == nil {
		t.Errorf("expected an error with no PrivilegeChecker configured")
	}
}

func TestFreshWithoutFingerprinterIsUncertain(t *testing.T) {
	c := resolvedTestCatalog(t, nil, nil, nil)
	fresh, err := c.Fresh(t.Context(), []uint32{100})
	if err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if fresh {
		t.Errorf("Fresh() with no Fingerprinter configured must report false (uncertainty, not permission — R5.6b)")
	}
}

func TestFreshComparesLiveAgainstStoredFingerprint(t *testing.T) {
	fp := &fakeFingerprints{byOID: map[uint32]string{100: "v1"}}
	c := resolvedTestCatalog(t, nil, fp, nil) // resolve() stores "v1" as the baseline

	fresh, err := c.Fresh(t.Context(), []uint32{100})
	if err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if !fresh {
		t.Errorf("Fresh() should report true when the live fingerprint matches the stored one")
	}

	fp.byOID[100] = "v2" // the view or table was redefined
	fresh, err = c.Fresh(t.Context(), []uint32{100})
	if err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if fresh {
		t.Errorf("Fresh() should report false once the live fingerprint diverges")
	}
}

func TestFreshOfAnUnresolvedRelationIsUncertain(t *testing.T) {
	fp := &fakeFingerprints{byOID: map[uint32]string{100: "v1"}}
	c := resolvedTestCatalog(t, nil, fp, nil)

	fresh, err := c.Fresh(t.Context(), []uint32{404})
	if err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if fresh {
		t.Errorf("Fresh() of a relation the cache never resolved must report false")
	}
}
