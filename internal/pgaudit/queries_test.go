package pgaudit

import (
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func TestFindingIDFormatting(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"attribute", attributeID("rolsuper"), "rolsuper"},
		{"membership", membershipID("pg_read_server_files"), "membership:pg_read_server_files"},
		{"relation write", relationWriteID("public", "orders"), "relation-write:public.orders"},
		{"schema create", schemaCreateID("app"), "schema-create:app"},
		{"function exec", functionExecID("pg_catalog", "pg_read_file", "text"), "function-exec:pg_catalog.pg_read_file(text)"},
		{"function exec, no args", functionExecID("pg_catalog", "pg_ls_dir", ""), "function-exec:pg_catalog.pg_ls_dir()"},
		{"security definer", securityDefinerID("pg_catalog", "azure_sys_fn", ""), "security-definer:pg_catalog.azure_sys_fn()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestAttributeFindings(t *testing.T) {
	tests := []struct {
		name    string
		row     roleAttributesRow
		wantIDs []string
	}{
		{
			name: "no attributes",
			row:  roleAttributesRow{RoleName: "app_ro"},
		},
		{
			name:    "superuser only",
			row:     roleAttributesRow{RoleName: "app_ro", Super: true},
			wantIDs: []string{"rolsuper"},
		},
		{
			name: "every attribute",
			row: roleAttributesRow{
				RoleName: "app_ro", Super: true, CreateDB: true, CreateRole: true,
				BypassRLS: true, Replication: true,
			},
			wantIDs: []string{"rolsuper", "rolcreatedb", "rolcreaterole", "rolbypassrls", "rolreplication"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFindings(tt.row)
			assertIDs(t, got, tt.wantIDs)
			for _, f := range got {
				if f.Kind != types.FindingAttribute {
					t.Errorf("finding %s has kind %v, want FindingAttribute", f.ID, f.Kind)
				}
				if f.Narrower == "" {
					t.Errorf("finding %s has no Narrower", f.ID)
				}
			}
		})
	}
}

func TestMembershipFindings(t *testing.T) {
	rows := []membershipRow{
		{RoleName: "pg_read_server_files", Member: true},
		{RoleName: "pg_monitor", Member: false},
		{RoleName: "pg_signal_backend", Member: true},
	}
	got := membershipFindings("app_ro", rows)
	assertIDs(t, got, []string{"membership:pg_read_server_files", "membership:pg_signal_backend"})
	for _, f := range got {
		if !strings.Contains(f.Narrower, `"app_ro"`) {
			t.Errorf("finding %s Narrower does not name the role: %q", f.ID, f.Narrower)
		}
	}
}

func TestRelationWriteFindings(t *testing.T) {
	rows := []relationPrivRow{
		{Schema: "public", Table: "orders", Insert: true},
		{Schema: "public", Table: "read_only_view"},
		{Schema: "public", Table: "audit_trail", Truncate: true, References: true},
	}

	t.Run("read role: INSERT is a finding", func(t *testing.T) {
		got := relationWriteFindings("app_ro", rows, ports.RoleRead)
		assertIDs(t, got, []string{"relation-write:public.orders", "relation-write:public.audit_trail"})
		for _, f := range got {
			if f.ID == "relation-write:public.orders" && !strings.Contains(f.Detail, "INSERT") {
				t.Errorf("detail missing INSERT: %q", f.Detail)
			}
		}
	})

	t.Run("write role: INSERT is not a finding, TRUNCATE and REFERENCES still are", func(t *testing.T) {
		rowsWithWrite := []relationPrivRow{
			{Schema: "public", Table: "orders", Insert: true, Update: true, Delete: true},
			{Schema: "public", Table: "audit_trail", Truncate: true, References: true},
		}
		got := relationWriteFindings("app_rw", rowsWithWrite, ports.RoleWrite)
		// orders has only INSERT/UPDATE/DELETE, none of which count for a write
		// credential: SPEC R4.2a.
		assertIDs(t, got, []string{"relation-write:public.audit_trail"})
	})
}

func TestSchemaCreateFindings(t *testing.T) {
	rows := []schemaPrivRow{
		{Schema: "public", Create: true},
		{Schema: "reporting", Create: false},
	}
	got := schemaCreateFindings("app_ro", rows)
	assertIDs(t, got, []string{"schema-create:public"})
}

func TestFunctionExecFindings(t *testing.T) {
	rows := []functionPrivRow{
		{Schema: "pg_catalog", Name: "pg_read_file", Args: "text", Exec: true},
		{Schema: "pg_catalog", Name: "pg_read_file", Args: "text, bigint, bigint", Exec: false},
		{Schema: "pg_catalog", Name: "lo_export", Args: "oid, text", Exec: true},
	}
	got := functionExecFindings("app_ro", rows)
	assertIDs(t, got, []string{
		"function-exec:pg_catalog.pg_read_file(text)",
		"function-exec:pg_catalog.lo_export(oid, text)",
	})
}

func TestSecurityDefinerFindings(t *testing.T) {
	rows := []secDefRow{
		{Schema: "pg_catalog", Name: "azure_sys_fn", Args: "", Source: "body v1"},
	}
	got := securityDefinerFindings(rows)
	assertIDs(t, got, []string{"security-definer:pg_catalog.azure_sys_fn()"})
	if got[0].Hash == "" {
		t.Fatal("expected a non-empty Hash")
	}

	// A redefinition (same signature, different source) must change the hash:
	// SPEC R4.1g.
	changed := []secDefRow{
		{Schema: "pg_catalog", Name: "azure_sys_fn", Args: "", Source: "body v2"},
	}
	got2 := securityDefinerFindings(changed)
	if got2[0].Hash == got[0].Hash {
		t.Error("redefining the function body must change the hash")
	}

	// A changed signature (different args) must also change the hash even with
	// identical source, since it is a different overload.
	sameSourceDifferentArgs := []secDefRow{
		{Schema: "pg_catalog", Name: "azure_sys_fn", Args: "text", Source: "body v1"},
	}
	got3 := securityDefinerFindings(sameSourceDifferentArgs)
	if got3[0].Hash == got[0].Hash {
		t.Error("a different signature must change the hash")
	}
}

func assertIDs(t *testing.T, findings []types.Finding, want []string) {
	t.Helper()
	if len(findings) != len(want) {
		t.Fatalf("got %d findings, want %d: %+v", len(findings), len(want), findings)
	}
	seen := make(map[string]bool, len(findings))
	for _, f := range findings {
		seen[f.ID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Errorf("missing finding %q, got %+v", id, findings)
		}
	}
}
