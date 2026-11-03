//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

func emailDeclared() map[string]types.ColumnPolicy {
	return map[string]types.ColumnPolicy{"public.customers.email": token("email"), "public.customers.id": {Policy: types.PolicyAllow}}
}

func TestMaskTokens_MASK_C12_two_sessions_read_one_value_and_get_different_tokens(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.ro, emailDeclared())
	other := mcpAgent(t)

	first := e.mintEmailToken(t, e.agent, e.ro)
	second := e.mintEmailToken(t, other, e.ro)
	if first == second {
		t.Fatalf("two sessions got the same token %q", first)
	}
	lookup := "SELECT id FROM customers WHERE email = $1"
	for name, tc := range map[string]struct {
		a    *agentSession
		tok  string
		want bool
	}{
		"first in first":   {e.agent, first, true},
		"second in second": {other, second, true},
		"second in first":  {e.agent, second, false},
		"first in second":  {other, first, false},
	} {
		body := e.query(t, tc.a, e.ro, lookup, map[string]any{"token": tc.tok})
		resolved := strings.Contains(body, `"rows"`) && !strings.Contains(body, string(types.CodeStaleToken))
		if resolved != tc.want {
			t.Errorf("%s: resolved = %v, want %v: %s", name, resolved, tc.want, body)
		}
	}
}

func exportShop(t *testing.T, e *leanEnv, pass string) []byte {
	t.Helper()
	out, err := e.op.ExportVault(t.Context(), pass)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	return out.Data
}

func connectionNames(t *testing.T, c *client.Client) []string {
	t.Helper()
	list, err := c.ListConnections(t.Context())
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	var names []string
	for _, conn := range list {
		names = append(names, conn.Name)
	}
	return names
}

func TestMaskTokens_MASK_C13_an_export_imports_into_a_second_daemon_and_runs(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.ro, emailDeclared())
	data := exportShop(t, e, "correct horse battery staple")
	if strings.Contains(string(data), "shop_eval_ro") || strings.Contains(string(data), e.shop.roPass) {
		t.Fatal("the export is readable without the passphrase")
	}

	sock := startDaemon(t)
	op2, err := client.DialHuman(t.Context(), sock)
	if err != nil {
		t.Fatalf("second operator dial: %v", err)
	}
	t.Cleanup(func() { op2.Close() })
	if names := connectionNames(t, op2); len(names) != 0 {
		t.Fatalf("a fresh daemon already holds %v", names)
	}
	if err := op2.ImportVault(t.Context(), data, "correct horse battery staple"); err != nil {
		t.Fatalf("import: %v", err)
	}
	if names := connectionNames(t, op2); !strings.Contains(strings.Join(names, ","), "shop_ro") {
		t.Fatalf("the profile is not listed after import: %v", names)
	}
	agent2 := mcpAgent(t)
	body := e.query(t, agent2, e.ro, "SELECT id, email FROM customers ORDER BY id LIMIT 3")
	if q := parseBody(t, body); len(q.Rows) != 3 {
		t.Fatalf("a query on the imported profile: %s", body)
	}
	if leaks := e.truth.leaks(body); len(leaks) != 0 {
		t.Errorf("personal data in the result: %v", leaks)
	}
}

func TestMaskTokens_MASK_C14_a_wrong_passphrase_is_refused_and_the_vault_is_unchanged(t *testing.T) {
	e := newLeanEnv(t)
	data := exportShop(t, e, "right passphrase")
	before := connectionNames(t, e.op)

	if err := e.op.ImportVault(t.Context(), data, "wrong passphrase"); err == nil {
		t.Fatal("an import under the wrong passphrase was accepted")
	}
	after := connectionNames(t, e.op)
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("connections changed: %v -> %v", before, after)
	}
	body := e.query(t, e.agent, e.ro, "SELECT id FROM customers LIMIT 1")
	if !strings.Contains(body, `"rows"`) {
		t.Errorf("the vault does not run after a refused import: %s", body)
	}
}

func TestMaskTokens_a_declared_column_is_still_masked_after_a_vault_move(t *testing.T) {
	e := newLeanEnv(t)
	hosts, err := e.op.ListHosts(t.Context())
	if err != nil || len(hosts) != 1 {
		t.Fatalf("hosts: %v %v", hosts, err)
	}
	local, err := e.op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: "shop_local", HostID: hosts[0].ID, Database: "shop", Writes: types.WritesOff,
		Credential: client.Credential{User: "shop_eval_ro", Password: e.shop.roPass},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	priceOf := func(a *agentSession) any {
		t.Helper()
		q := parseBody(t, e.query(t, a, local.ID, "SELECT price FROM products WHERE id = 1"))
		if len(q.Rows) != 1 {
			t.Fatalf("no row: %+v", q)
		}
		return q.Rows[0][0]
	}
	isToken := func(v any) bool { s, _ := v.(string); return strings.HasPrefix(s, "⟨") }
	if plain := priceOf(e.agent); isToken(plain) {
		t.Fatalf("price is masked with nothing declared, so this test shows nothing: %v", plain)
	}
	e.declare(t, local.ID, map[string]types.ColumnPolicy{"public.products.price": token("price")})
	if before := priceOf(e.agent); !isToken(before) {
		t.Fatalf("price is not a token before the export: %v", before)
	}
	data := exportShop(t, e, "pass")

	sock := startDaemon(t)
	op2, err := client.DialHuman(t.Context(), sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { op2.Close() })
	if err := op2.ImportVault(t.Context(), data, "pass"); err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := priceOf(mcpAgent(t)); !isToken(got) {
		t.Errorf("a declared column came back unmasked after the import: %v", got)
	}
}
