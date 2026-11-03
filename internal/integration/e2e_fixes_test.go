//go:build integration

package integration

import (
	"github.com/jackc/pgx/v5"
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
				t.Logf("approved -> %s", body)
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
