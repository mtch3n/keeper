package vault

import (
	"context"
	"errors"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

// registerWithFindings registers a connection carrying the given findings and
// returns its id.
func registerWithFindings(t *testing.T, v *Vault, findings []types.Finding) string {
	t.Helper()
	conn := testConnection("")
	conn.Findings = findings
	if err := v.Register(context.Background(), &conn, "postgres://ro@host/db", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return conn.ID
}

func TestAcceptRules(t *testing.T) {
	findings := []types.Finding{
		{ID: "rolsuper", Kind: types.FindingAttribute, Subject: "rolsuper", Detail: "superuser"},
		{ID: "relation-write:public.orders", Kind: types.FindingRelationWrite, Subject: "public.orders", Detail: "writes"},
		{ID: "security-definer:pg_catalog.azure_sys_fn()", Kind: types.FindingSecurityDefine, Subject: "azure_sys_fn", Hash: "hash-v1"},
	}

	t.Run("unknown finding id is a conflict and changes nothing", func(t *testing.T) {
		v, _ := newTestVault(t)
		id := registerWithFindings(t, v, findings)

		_, err := v.Accept(context.Background(), id, []string{"no-such-finding"}, "ming", "cli")
		if _, ok := errors.AsType[*ConflictError](err); !ok {
			t.Fatalf("err = %v, want *ConflictError", err)
		}

		got, err := v.Connection(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Acceptances) != 0 {
			t.Errorf("acceptances = %v, want none (rejected batch must not partially apply)", got.Acceptances)
		}
		if got.Enabled {
			t.Error("connection must remain disabled")
		}
	})

	t.Run("hash-qualified finding whose hash changed is a conflict", func(t *testing.T) {
		v, _ := newTestVault(t)
		id := registerWithFindings(t, v, findings)

		_, err := v.Accept(context.Background(), id,
			[]string{"security-definer:pg_catalog.azure_sys_fn()@hash-stale"}, "ming", "cli")
		if _, ok := errors.AsType[*ConflictError](err); !ok {
			t.Fatalf("err = %v, want *ConflictError", err)
		}

		got, err := v.Connection(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Acceptances) != 0 {
			t.Errorf("acceptances = %v, want none", got.Acceptances)
		}
	})

	t.Run("hash-qualified finding matching the current hash succeeds", func(t *testing.T) {
		v, _ := newTestVault(t)
		id := registerWithFindings(t, v, findings)

		got, err := v.Accept(context.Background(), id,
			[]string{"security-definer:pg_catalog.azure_sys_fn()@hash-v1"}, "ming", "cli")
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
		if len(got.Acceptances) != 1 {
			t.Fatalf("acceptances = %v", got.Acceptances)
		}
	})

	t.Run("partial acceptance leaves the connection disabled", func(t *testing.T) {
		v, _ := newTestVault(t)
		id := registerWithFindings(t, v, findings)

		got, err := v.Accept(context.Background(), id, []string{"rolsuper"}, "ming", "cli")
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
		if got.Enabled {
			t.Error("connection must stay disabled while findings remain unaccepted")
		}
		if len(got.Unaccepted()) != 2 {
			t.Errorf("unaccepted = %v, want 2 remaining", got.Unaccepted())
		}
	})

	t.Run("accepting every finding enables the connection", func(t *testing.T) {
		v, _ := newTestVault(t)
		id := registerWithFindings(t, v, findings)

		var got *types.Connection
		var err error
		for _, f := range findings {
			got, err = v.Accept(context.Background(), id, []string{f.ID}, "ming", "cli")
			if err != nil {
				t.Fatalf("Accept(%s): %v", f.ID, err)
			}
		}
		if !got.Enabled {
			t.Error("connection should be enabled once every finding is accepted")
		}
		if len(got.Unaccepted()) != 0 {
			t.Errorf("unaccepted = %v, want none", got.Unaccepted())
		}
	})

	t.Run("via must be cli or ui, never mcp", func(t *testing.T) {
		v, _ := newTestVault(t)
		id := registerWithFindings(t, v, findings)

		if _, err := v.Accept(context.Background(), id, []string{"rolsuper"}, "an-agent", "mcp"); err == nil {
			t.Fatal("expected an error for via=mcp")
		}
		if _, err := v.Accept(context.Background(), id, []string{"rolsuper"}, "ming", "ui"); err != nil {
			t.Errorf("via=ui should be accepted: %v", err)
		}
	})

	t.Run("a redefined function invalidates its acceptance until re-accepted", func(t *testing.T) {
		v, _ := newTestVault(t)
		id := registerWithFindings(t, v, findings)

		got, err := v.Accept(context.Background(), id,
			[]string{"security-definer:pg_catalog.azure_sys_fn()"}, "ming", "cli")
		if err != nil {
			t.Fatalf("Accept: %v", err)
		}
		if len(got.Unaccepted()) != 2 {
			t.Fatalf("unaccepted = %v", got.Unaccepted())
		}

		// Simulate a re-audit that finds the function redefined: same id, new
		// hash. Update is what the daemon calls after a re-audit.
		redefined := *got
		for i, f := range redefined.Findings {
			if f.ID == "security-definer:pg_catalog.azure_sys_fn()" {
				redefined.Findings[i].Hash = "hash-v2"
			}
		}
		if err := v.Update(context.Background(), &redefined); err != nil {
			t.Fatalf("Update: %v", err)
		}

		after, err := v.Connection(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if after.Enabled {
			t.Error("connection must be disabled again after the function's hash changed")
		}
		found := false
		for _, f := range after.Unaccepted() {
			if f.ID == "security-definer:pg_catalog.azure_sys_fn()" {
				found = true
			}
		}
		if !found {
			t.Error("the redefined function's finding should be unaccepted again")
		}

		// Re-accepting with a bare id (no hash qualifier) succeeds and binds
		// to the new hash: the remediation path.
		reaccepted, err := v.Accept(context.Background(), id,
			[]string{"security-definer:pg_catalog.azure_sys_fn()"}, "ming", "cli")
		if err != nil {
			t.Fatalf("re-accept: %v", err)
		}
		if len(reaccepted.Unaccepted()) != 2 {
			t.Errorf("unaccepted after re-accept = %v", reaccepted.Unaccepted())
		}
	})

	t.Run("unknown connection id", func(t *testing.T) {
		v, _ := newTestVault(t)
		_, err := v.Accept(context.Background(), "does-not-exist", []string{"rolsuper"}, "ming", "cli")
		if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})

	t.Run("locked vault refuses", func(t *testing.T) {
		v := New(t.TempDir())
		_, err := v.Accept(context.Background(), "any", []string{"rolsuper"}, "ming", "cli")
		kerr, ok := errors.AsType[*types.Error](err)
		if !ok || kerr.Code != types.CodeVaultLocked {
			t.Fatalf("err = %v, want *types.Error{Code: CodeVaultLocked}", err)
		}
	})
}
