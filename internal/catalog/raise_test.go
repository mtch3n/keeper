package catalog

import (
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func openTestStore(t *testing.T, deps Dependencies, committed map[columnKey]types.ColumnPolicy) (*Store, Paths) {
	t.Helper()
	paths := testPaths(t)
	if committed != nil {
		if err := saveYAMLFile(paths.Committed, committed); err != nil {
			t.Fatal(err)
		}
	}
	s := NewStore(deps)
	if _, err := s.Open(t.Context(), "conn1", paths); err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, paths
}

func TestRaiseNeverOverwritesHumanAuthoredEntry(t *testing.T) {
	s, _ := openTestStore(t, Dependencies{}, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyAllow},
	})
	ctx := t.Context()

	if err := s.Raise(ctx, "conn1", "public.users.email", types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "email"}); err != nil {
		t.Fatalf("Raise: %v", err)
	}

	entries, err := s.Entries(ctx, "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["public.users.email"]; got.Policy != types.PolicyAllow {
		t.Errorf("Raise overwrote a human-authored committed entry: got %+v", got)
	}
}

func TestRaiseNeverLowers(t *testing.T) {
	s, _ := openTestStore(t, Dependencies{}, nil)
	ctx := t.Context()

	if err := s.Raise(ctx, "conn1", "public.users.notes", types.ColumnPolicy{Policy: types.PolicyRedact}); err != nil {
		t.Fatalf("first Raise: %v", err)
	}
	if err := s.Raise(ctx, "conn1", "public.users.notes", types.ColumnPolicy{Policy: types.PolicyScan}); err != nil {
		t.Fatalf("second Raise: %v", err)
	}

	entries, err := s.Entries(ctx, "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["public.users.notes"]; got.Policy != types.PolicyRedact {
		t.Errorf("Raise lowered an existing overlay policy: got %+v, want redact to survive", got)
	}
}

func TestRaiseAllowsAStrictIncrease(t *testing.T) {
	s, _ := openTestStore(t, Dependencies{}, nil)
	ctx := t.Context()

	if err := s.Raise(ctx, "conn1", "public.users.contact", types.ColumnPolicy{Policy: types.PolicyScan}); err != nil {
		t.Fatalf("first Raise: %v", err)
	}
	if err := s.Raise(ctx, "conn1", "public.users.contact", types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "email"}); err != nil {
		t.Fatalf("second Raise: %v", err)
	}

	entries, err := s.Entries(ctx, "conn1")
	if err != nil {
		t.Fatal(err)
	}
	got := entries["public.users.contact"]
	if got.Policy != types.PolicyToken || got.Namespace != "email" {
		t.Errorf("Raise should allow scan -> token: got %+v", got)
	}
}

func TestRaiseNeverTouchesDrop(t *testing.T) {
	s, _ := openTestStore(t, Dependencies{}, map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "ssn"}: {Policy: types.PolicyDrop},
	})
	ctx := t.Context()

	// ssn is human-authored (committed) drop; Raise must be a no-op regardless
	// of the target policy's rank.
	if err := s.Raise(ctx, "conn1", "public.users.ssn", types.ColumnPolicy{Policy: types.PolicyRedact}); err != nil {
		t.Fatalf("Raise: %v", err)
	}
	entries, err := s.Entries(ctx, "conn1")
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["public.users.ssn"]; got.Policy != types.PolicyDrop {
		t.Errorf("Raise touched a drop column: got %+v", got)
	}
}

func TestRaiseWritesOverlayNotCommitted(t *testing.T) {
	s, paths := openTestStore(t, Dependencies{}, nil)
	ctx := t.Context()

	if err := s.Raise(ctx, "conn1", "public.users.contact", types.ColumnPolicy{Policy: types.PolicyScan}); err != nil {
		t.Fatal(err)
	}

	committed, err := loadYAMLFile(paths.Committed)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 0 {
		t.Errorf("Raise must never write the committed file, got %+v", committed)
	}
	overlay, err := loadYAMLFile(paths.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := overlay[columnKey{Schema: "public", Table: "users", Column: "contact"}]; !ok {
		t.Errorf("Raise should have written the overlay file")
	}
}

func TestPutPromotesOverlayIntoCommitted(t *testing.T) {
	s, paths := openTestStore(t, Dependencies{}, nil)
	ctx := t.Context()

	if err := s.Raise(ctx, "conn1", "public.users.contact", types.ColumnPolicy{Policy: types.PolicyToken, Namespace: "email"}); err != nil {
		t.Fatal(err)
	}

	if err := s.Put(ctx, "conn1", map[string]types.ColumnPolicy{
		"public.users.contact": {Policy: types.PolicyToken, Namespace: "email"},
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	committed, err := loadYAMLFile(paths.Committed)
	if err != nil {
		t.Fatal(err)
	}
	key := columnKey{Schema: "public", Table: "users", Column: "contact"}
	if got, ok := committed[key]; !ok || got.Policy != types.PolicyToken {
		t.Errorf("Put should have written the committed file, got %+v (ok=%v)", got, ok)
	}

	overlay, err := loadYAMLFile(paths.Overlay)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := overlay[key]; ok {
		t.Errorf("Put should promote the entry out of the overlay, still found %+v", overlay[key])
	}
}

func TestPutRejectsInvalidPolicy(t *testing.T) {
	s, _ := openTestStore(t, Dependencies{}, nil)
	err := s.Put(t.Context(), "conn1", map[string]types.ColumnPolicy{
		"public.users.email": {Policy: types.PolicyToken}, // missing namespace
	})
	if err == nil {
		t.Errorf("expected Put to reject a token with no namespace (R5.2b)")
	}
}
