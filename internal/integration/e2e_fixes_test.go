//go:build integration

package integration

import (
	"github.com/jackc/pgx/v5"
	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2E_NestedFunctionCallsOnReadProfile(t *testing.T) {
	e := newLeanEnv(t)
	for _, conn := range []string{e.ro, e.rw} {
		for _, sql := range []string{
			"SELECT pg_notify('c','x')::text AS n",
			"SELECT pg_notify(chr(99),chr(120))::text AS n",
			"SELECT pg_notify(chr(99),chr(120))::text AS n;",
			"select pg_notify(chr(99), chr(120))::text as n",
			"SELECT pg_notify(chr(99),chr(120))",
			"SELECT upper(lower(chr(99))) AS n",
			"SELECT length(md5(lower('A'))) AS n",
			"SELECT coalesce(nullif(lower('A'), 'a'), upper('b')) AS n",
		} {
			body := e.query(t, e.agent, conn, sql)
			if parseBody2(body).State == "pending_approval" {
				body = e.approveAndWait(t, e.agent, body)
			}
			if strings.Contains(body, "did not parse") {
				t.Errorf("%s\n  -> %s", sql, body)
			}
		}
	}
}

func TestE2E_FailedVolatileLookupIsUncertainNotSyntax(t *testing.T) {
	e := newLeanEnv(t)
	admin, err := pgx.Connect(t.Context(), e.shop.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(t.Context())
	if _, err := admin.Exec(t.Context(), "REVOKE SELECT ON pg_catalog.pg_proc FROM PUBLIC"); err != nil {
		t.Fatal(err)
	}
	body := e.query(t, e.agent, e.ro, "SELECT lower(current_user) AS n")
	if strings.Contains(body, "did not parse") || parseBody2(body).State != "pending_approval" {
		t.Fatalf("a failed lookup must ask, not fail: %s", body)
	}
}

func TestE2E_ConnectionByProfileName(t *testing.T) {
	e := newLeanEnv(t)
	sql := "SELECT 1 AS n"
	byID := e.query(t, e.agent, e.ro, sql)
	byName := e.query(t, e.agent, "shop_ro", sql)
	if len(parseBody(t, byName).Rows) != 1 || byName == "" || strings.Contains(byName, "unknown") {
		t.Fatalf("by name: %s (by id: %s)", byName, byID)
	}
	for _, tool := range []string{"get_schema", "explain"} {
		args := map[string]any{"connection_id": "shop_ro"}
		if tool == "explain" {
			args["sql"] = sql
		}
		if out := e.agent.call(t, tool, args); strings.Contains(out, "unknown_connection") || strings.Contains(out, "ticket_unknown") {
			t.Errorf("%s by name: %s", tool, out)
		}
	}
	if out := e.agent.call(t, "describe_connection", map[string]any{"id": "shop_ro"}); strings.Contains(out, "unknown") {
		t.Errorf("describe by name: %s", out)
	}
}

func TestE2E_UnknownConnectionHasItsOwnCode(t *testing.T) {
	e := newLeanEnv(t)
	body := e.query(t, e.agent, "no_such_profile", "SELECT 1")
	if !strings.Contains(body, "unknown_connection") || strings.Contains(body, "ticket_unknown") {
		t.Fatalf("got %s", body)
	}
}

func TestE2E_AmbiguousProfileNameListsIds(t *testing.T) {
	e := newLeanEnv(t)
	hosts, err := e.op.ListHosts(t.Context())
	if err != nil || len(hosts) == 0 {
		t.Fatalf("hosts: %v", err)
	}
	other, err := e.op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: "shop_ro", HostID: hosts[0].ID, Database: "shop", Writes: types.WritesOff,
		Credential:  client.Credential{User: "shop_eval_ro", Password: e.shop.roPass},
		CatalogPath: filepath.Join(t.TempDir(), "dup.yaml"),
	})
	if err != nil {
		t.Skipf("duplicate names are refused at registration: %v", err)
	}
	body := e.query(t, e.agent, "shop_ro", "SELECT 1")
	if !strings.Contains(body, e.ro) || !strings.Contains(body, other.ID) {
		t.Fatalf("ambiguous name must list both ids: %s", body)
	}
}
