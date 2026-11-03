package redact

import (
	"errors"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestJSONNestedPastTheWalkLimitIsRedactedWhole(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyScan}}
	r := newRedactor(t, cat, nil)
	var v any = "jane@example.com"
	for range 40 {
		v = []any{v}
	}
	rows := [][]any{{v}}
	cols := []types.ColumnMeta{col("doc", "jsonb", types.PolicyScan, 1, 1)}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != RedactedMarker {
		t.Errorf("cell = %v, want the redaction marker", rows[0][0])
	}
}

func TestJSONPathsHideUndeclaredKeysAndScalarDocuments(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {
		Policy: types.PolicyAllow,
		Paths:  map[string]types.ColumnPolicy{"$.ip": {Policy: types.PolicyAllow}},
	}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("metadata", "jsonb", types.PolicyAllow, 1, 1)}
	rows := [][]any{
		{map[string]any{"ip": "10.0.0.1", "bob@leak.test": "x", "alice@leak.test": "y"}},
		{"scalar@leak.test"},
		{float64(42)},
	}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	obj, ok := rows[0][0].(map[string]any)
	if !ok || obj["ip"] != "10.0.0.1" || len(obj) != 3 {
		t.Fatalf("object = %#v, want the declared ip kept and two hidden keys", rows[0][0])
	}
	for k, v := range obj {
		if strings.Contains(k, "leak.test") || (k != "ip" && v != RedactedMarker) {
			t.Errorf("key %q = %v leaked", k, v)
		}
	}
	for _, i := range []int{1, 2} {
		if rows[i][0] != RedactedMarker {
			t.Errorf("document %d = %v, want the marker", i, rows[i][0])
		}
	}
}

func TestEmissionScanLeavesTokensAndMarkersAlone(t *testing.T) {
	cat := fakeCatalog{
		{1, 1}: {Policy: types.PolicyToken, Namespace: "email"},
		{1, 2}: {Policy: types.PolicyScan},
	}
	r := newRedactor(t, cat, nil)
	tok, err := r.Mint(t.Context(), "s", "c1", "email", "mail")
	if err != nil {
		t.Fatal(err)
	}
	cols := []types.ColumnMeta{
		col("email", "text", types.PolicyToken, 1, 1),
		col("notes", "text", types.PolicyScan, 1, 2),
	}
	rows := [][]any{{"a@example.com", "write to a@example.com, not " + tok + " or ⟨redacted⟩ or ⟨redacted:email_address⟩; mail"}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	own, _, _, ok := ParseToken(rows[0][0].(string))
	if !ok || own != "email" {
		t.Fatalf("token cell was rewritten: %v", rows[0][0])
	}
	notes := rows[0][1].(string)
	for _, keep := range []string{tok, RedactedMarker, RedactedSpan("email_address")} {
		if !strings.Contains(notes, keep) {
			t.Errorf("%q was rewritten: %s", keep, notes)
		}
	}
}

func TestEmissionScanStillFindsAValueInsideForgedBrackets(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyScan}}
	r := newRedactor(t, cat, nil)
	if _, err := r.Mint(t.Context(), "s", "c1", "email", "carol-secret"); err != nil {
		t.Fatal(err)
	}
	cols := []types.ColumnMeta{col("notes", "text", types.PolicyScan, 1, 1)}
	rows := [][]any{{"⟨redacted:carol-secret⟩ ⟨x carol-secret⟩"}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rows[0][0].(string), "carol-secret") {
		t.Errorf("a bound value hid in brackets: %v", rows[0][0])
	}
}

func TestATokenResolvesOnlyOnItsOwnConnection(t *testing.T) {
	r := newRedactor(t, fakeCatalog{}, nil)
	tok, err := r.Mint(t.Context(), "s", "c1", "email", "jane@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if v, _, err := r.Resolve(t.Context(), "s", "c1", tok); err != nil || v != "jane@example.com" {
		t.Fatalf("same connection: %q, %v", v, err)
	}
	if _, _, err := r.Resolve(t.Context(), "s", "c2", tok); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("another connection of the session resolved it: %v", err)
	}
}
