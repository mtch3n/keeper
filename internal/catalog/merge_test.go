package catalog

import (
	"path/filepath"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{
		Committed: filepath.Join(dir, "catalog.yaml"),
		Overlay:   filepath.Join(dir, "catalog.local.yaml"),
		Cache:     filepath.Join(dir, "catalog.db"),
	}
}

func TestMergeOverlayPrecedence(t *testing.T) {
	paths := testPaths(t)

	committed := map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyScan},
		{Schema: "public", Table: "users", Column: "ssn"}:   {Policy: types.PolicyDrop},
	}
	overlay := map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyToken, Namespace: "email"},
	}
	if err := saveYAMLFile(paths.Committed, committed); err != nil {
		t.Fatal(err)
	}
	if err := saveYAMLFile(paths.Overlay, overlay); err != nil {
		t.Fatal(err)
	}

	c, err := openCatalog("conn1", paths, nil, nil)
	if err != nil {
		t.Fatalf("openCatalog: %v", err)
	}
	defer c.close()

	rel := types.RelationRef{Schema: "public", Relation: "users"}

	got, ok := c.LookupName(rel, "email")
	if !ok {
		t.Fatal("email: not found")
	}
	if got.Policy != types.PolicyToken || got.Namespace != "email" {
		t.Errorf("overlay should win over committed: got %+v", got)
	}

	got, ok = c.LookupName(rel, "ssn")
	if !ok {
		t.Fatal("ssn: not found")
	}
	if got.Policy != types.PolicyDrop {
		t.Errorf("committed-only entry should survive the merge: got %+v", got)
	}

	if _, ok := c.LookupName(rel, "nope"); ok {
		t.Errorf("uncatalogued column should not resolve")
	}
}

func TestMergeSurvivesReload(t *testing.T) {
	paths := testPaths(t)
	c, err := openCatalog("conn1", paths, nil, nil)
	if err != nil {
		t.Fatalf("openCatalog on empty files: %v", err)
	}
	defer c.close()

	rel := types.RelationRef{Schema: "public", Relation: "users"}
	if _, ok := c.LookupName(rel, "email"); ok {
		t.Fatal("expected no entries in a fresh catalog")
	}

	if err := saveYAMLFile(paths.Overlay, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyScan},
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := c.LookupName(rel, "email"); !ok {
		t.Errorf("reload should pick up the newly written overlay entry")
	}
}
