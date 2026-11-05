//go:build integration

package integration

import (
	"strings"
	"testing"
)

// PostgreSQL evaluates an IMMUTABLE function with constant arguments while it
// plans, so EXPLAIN shows a constant and neither the function nor the table it
// read. A statement that reads no relation is allowed only when no such
// function is callable, because its plan cannot say where a constant came from.
func TestAFoldedImmutableFunctionThatReadsATableIsNotAllowed(t *testing.T) {
	e := newLeanEnv(t)
	email := customerFourEmail(t, e)
	e.super(t, `CREATE FUNCTION public.leak_email() RETURNS text LANGUAGE sql IMMUTABLE AS 'SELECT email FROM public.customers WHERE id = 4'`)
	for _, sql := range []string{"SELECT leak_email() AS v", "SELECT v FROM (SELECT leak_email() AS v) s"} {
		if body := e.query(t, e.agent, e.ro, sql); strings.Contains(strings.ToLower(body), strings.ToLower(email)) {
			t.Errorf("%s: the folded function returned the email: %s", sql, body)
		}
	}
}

// With only pg_catalog functions callable, a constant is a constant.
func TestAConstantIsAllowedWhenNoUserImmutableFunctionIsCallable(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t, `REVOKE EXECUTE ON FUNCTION public.luhn(text) FROM PUBLIC`)
	if got := firstCell(t, e.query(t, e.agent, e.ro, "SELECT 'plain' AS v")); got != "plain" {
		t.Fatalf("a constant = %q, want plain", got)
	}
}
