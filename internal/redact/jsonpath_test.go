package redact

import (
	"strings"
	"sync"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestJSONKeyPaths(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {
		Policy: types.PolicyScan,
		Paths: map[string]types.ColumnPolicy{
			"$.customer.email": {Policy: types.PolicyToken, Namespace: "email"},
			"$.customer.name":  {Policy: types.PolicyRedact},
			"$.order_id":       {Policy: types.PolicyAllow},
			"$.internal.notes": {Policy: types.PolicyScan},
			"$.secret":         {Policy: types.PolicyDrop},
			"$.tags[*]":        {Policy: types.PolicyAllow},
		},
	}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("metadata", "jsonb", types.PolicyScan, 1, 1)}
	doc := `{"order_id":1004,"customer":{"email":"jane@example.com","name":"Jane Doe"},` +
		`"internal":{"notes":"ping jane@example.com"},"secret":"x","tags":["a","b"],"extra":"unclassified"}`
	rows := [][]any{{doc}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	out, ok := rows[0][0].(string)
	if !ok {
		t.Fatalf("cell is %T, want string", rows[0][0])
	}
	if strings.Contains(out, "jane@example.com") {
		t.Errorf("an email survived: %s", out)
	}
	if strings.Contains(out, "Jane Doe") {
		t.Errorf("a redact path survived: %s", out)
	}
	if strings.Contains(out, `"secret"`) {
		t.Errorf("a drop path survived: %s", out)
	}
	if !strings.Contains(out, `"order_id":1004`) {
		t.Errorf("an allow path was masked, or the number was reformatted: %s", out)
	}
	if !strings.Contains(out, `"tags":["a","b"]`) {
		t.Errorf("an array path was masked: %s", out)
	}
	// R5.7: an uncatalogued path is an unknown column. It redacts.
	if !strings.Contains(out, `"extra":"`+RedactedMarker+`"`) {
		t.Errorf("an uncatalogued path was not redacted: %s", out)
	}
	// Member order survives, because the document is rewritten through the
	// token stream rather than decoded into a map and re-encoded.
	if !strings.HasPrefix(out, `{"order_id":`) {
		t.Errorf("member order changed: %s", out)
	}
	// The scan path redacted a span, not the whole value.
	if !strings.Contains(out, "ping "+RedactedSpan("email")) {
		t.Errorf("scan path did not redact a span: %s", out)
	}
}

func TestJSONKeyPathsOnAnInvalidDocumentFallBack(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {
		Policy: types.PolicyScan,
		Paths:  map[string]types.ColumnPolicy{"$.a": {Policy: types.PolicyAllow}},
	}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("doc", "jsonb", types.PolicyScan, 1, 1)}
	rows := [][]any{{"this is not json, but it does hold jane@example.com"}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rows[0][0].(string), "jane@example.com") {
		t.Errorf("fallback did not apply the column policy: %v", rows[0][0])
	}
}

func TestConcurrentApplyOnOneSession(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyToken, Namespace: "email"}}
	r := newRedactor(t, cat, nil)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			cols := []types.ColumnMeta{col("email", "text", types.PolicyToken, 1, 1)}
			rows := [][]any{{"user" + string(rune('a'+i)) + "@example.com"}}
			if _, err := r.Apply(stmt(t.Context(), "c1"), "shared", cols, rows); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
