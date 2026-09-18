package catalog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestYAMLRoundTripIncludingNestedJSONPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.yaml")

	entries := map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "id"}:    {Policy: types.PolicyAllow},
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyToken, Namespace: "email"},
		{Schema: "public", Table: "users", Column: "ssn"}:   {Policy: types.PolicyDrop},
		{Schema: "public", Table: "users", Column: "notes"}: {Policy: types.PolicyScan},
		{Schema: "public", Table: "users", Column: "dob"}:   {Policy: types.PolicyRedact, HideName: true},
		{Schema: "public", Table: "users", Column: "card"}:  {Policy: types.PolicyPartial, Form: types.FormCardBINLast4},
		{Schema: "public", Table: "users", Column: "metadata"}: {
			Policy: types.PolicyScan,
			Paths: map[string]types.ColumnPolicy{
				"$.customer.email": {Policy: types.PolicyToken, Namespace: "email"},
				"$.order_id":       {Policy: types.PolicyAllow},
				"$.internal.notes": {Policy: types.PolicyScan},
			},
		},
	}

	if err := saveYAMLFile(path, entries); err != nil {
		t.Fatalf("saveYAMLFile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	// SPEC §5.2: nested, snake_case, never dotted.
	for _, want := range []string{"hide_name: true", "$.customer.email", "schemas:"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("saved file missing %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "public.users.") {
		t.Errorf("saved file uses a dotted key, want nested:\n%s", raw)
	}

	got, err := loadYAMLFile(path)
	if err != nil {
		t.Fatalf("loadYAMLFile: %v", err)
	}
	if !reflect.DeepEqual(got, entries) {
		t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got, entries)
	}
}

func TestLoadYAMLFileMissingIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	got, err := loadYAMLFile(path)
	if err != nil {
		t.Fatalf("loadYAMLFile of a missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected an empty catalog, got %+v", got)
	}
}

func TestYAMLValidationRejections(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "token without namespace",
			yaml: "version: 1\nschemas:\n  public:\n    users:\n      email: {policy: token}\n",
		},
		{
			name: "partial without form",
			yaml: "version: 1\nschemas:\n  public:\n    users:\n      card: {policy: partial}\n",
		},
		{
			name: "namespace on a non-token policy",
			yaml: "version: 1\nschemas:\n  public:\n    users:\n      id: {policy: allow, namespace: email}\n",
		},
		{
			name: "form on a non-partial policy",
			yaml: "version: 1\nschemas:\n  public:\n    users:\n      id: {policy: allow, form: card_bin_last4}\n",
		},
		{
			name: "unknown policy",
			yaml: "version: 1\nschemas:\n  public:\n    users:\n      id: {policy: obfuscate}\n",
		},
		{
			name: "invalid nested json path policy",
			yaml: "version: 1\nschemas:\n  public:\n    users:\n      metadata:\n        policy: scan\n        paths:\n          \"$.x\": {policy: token}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadYAMLFile(path); err == nil {
				t.Errorf("expected a validation error, got none")
			}
		})
	}
}

func TestSaveYAMLFileRejectsInvalidEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	entries := map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "email"}: {Policy: types.PolicyToken}, // no namespace
	}
	if err := saveYAMLFile(path, entries); err == nil {
		t.Errorf("expected saveYAMLFile to reject an invalid entry")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("saveYAMLFile must not leave a partial file behind on validation failure")
	}
}

func TestSaveYAMLFileIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	entries := map[columnKey]types.ColumnPolicy{
		{Schema: "public", Table: "users", Column: "id"}: {Policy: types.PolicyAllow},
	}
	if err := saveYAMLFile(path, entries); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".catalog-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("temp file left behind: %v", matches)
	}
}
