//go:build integration

package integration

import (
	"strings"
	"testing"
)

// A database's own statistics hold no customer data, so a diagnostic query
// over them returns its values. A session's query text is the exception: it
// holds other statements' literals, and a definition can hold a secret.
func TestMask_MASK_C24_SystemCatalogsAnswerDiagnosticsWithoutAsking(t *testing.T) {
	e := newLeanEnv(t)
	body := e.query(t, e.agent, e.ro, "SELECT datname, numbackends, xact_commit, pg_database_size(datname) AS bytes FROM pg_stat_database WHERE datname = current_database()")
	q := parseBody(t, body)
	if len(q.Rows) != 1 {
		t.Fatalf("no row: %s", body)
	}
	if name, _ := q.Rows[0][0].(string); name == "" || strings.Contains(name, "REDACTED") {
		t.Errorf("datname = %v, want the database name: %s", q.Rows[0][0], body)
	}
	if strings.Contains(body, "REDACTED") {
		t.Errorf("a statistics column was redacted: %s", body)
	}
	if got := firstCell(t, e.query(t, e.agent, e.ro, "SELECT count(*)::text AS n FROM pg_stat_activity")); got == "" || strings.Contains(got, "REDACTED") {
		t.Errorf("count over pg_stat_activity = %q, want a number", got)
	}
}

func TestMask_MASK_C25_SystemTextKeeperDoesNotClassifyStaysHidden(t *testing.T) {
	e := newLeanEnv(t)
	for _, sql := range []string{
		"SELECT query FROM pg_stat_activity WHERE pid = pg_backend_pid()",
		"SELECT pg_get_functiondef('public.luhn(text)'::regprocedure) AS def",
		"SELECT query FROM pg_stat_get_activity(pg_backend_pid())",
	} {
		body := e.query(t, e.agent, e.ro, sql)
		if strings.Contains(body, "pg_backend_pid") || strings.Contains(body, "generate_series") {
			t.Errorf("%s: text keeper does not classify came back: %s", sql, body)
		}
	}
}

// A table created after the catalog opened is classified on its first use, by
// the same defaults as every other table: ids show, short few-valued text is
// scanned so an email inside it is masked and the rest shows, and a key and free
// text stay redacted.
func TestMask_MASK_C23_ATableCreatedLaterIsClassifiedAndItsTextIsScanned(t *testing.T) {
	e := newLeanEnv(t)
	e.query(t, e.agent, e.ro, "SELECT 1 AS warm")
	e.super(t,
		`CREATE TABLE public.carrier (id int PRIMARY KEY, carrier_name text, api_key text, notes text)`,
		`INSERT INTO public.carrier VALUES (1, 'Blue Cross ann@ex.io', 'sk_live_abc', 'Spoke to Ann Lee about the renewal, she will call back next week')`,
		`GRANT SELECT ON public.carrier TO PUBLIC`,
		`ANALYZE public.carrier`,
	)
	body := e.query(t, e.agent, e.ro, "SELECT id, carrier_name, api_key, notes FROM carrier")
	q := parseBody(t, body)
	if len(q.Rows) != 1 {
		t.Fatalf("no row: %s", body)
	}
	if id, _ := q.Rows[0][0].(float64); id != 1 {
		t.Errorf("id = %v, want 1: %s", q.Rows[0][0], body)
	}
	name, _ := q.Rows[0][1].(string)
	if !strings.Contains(name, "Blue Cross") || strings.Contains(name, "ann@ex.io") {
		t.Errorf("carrier_name = %q, want the text with the email masked", name)
	}
	if strings.Contains(body, "sk_live_abc") {
		t.Errorf("the key came back: %s", body)
	}
	if strings.Contains(body, "Ann Lee") {
		t.Errorf("free text came back, and with it a name detection cannot find: %s", body)
	}
}

// A folded call inside a statement that also reads a table is caught the same
// way as one with no table, and a pure helper does not cost an aggregate.
func TestMask_MASK_C26_AFoldedCallBesideATableIsNotAllowedAndAPureHelperIsHarmless(t *testing.T) {
	e := newLeanEnv(t)
	email := customerFourEmail(t, e)
	if got := firstCell(t, e.query(t, e.agent, e.ro, "SELECT count(*)::text AS n FROM customers")); got != "200" {
		t.Fatalf("count(*) with luhn() callable = %q, want 200", got)
	}
	if got := firstCell(t, e.query(t, e.agent, e.ro, "SELECT 'plain' AS v")); got != "plain" {
		t.Fatalf("a constant with luhn() callable = %q, want plain", got)
	}
	e.super(t, `CREATE FUNCTION public.leak_email() RETURNS text LANGUAGE sql IMMUTABLE AS 'SELECT email FROM public.customers WHERE id = 4'`)
	body := e.query(t, e.agent, e.ro, "SELECT id, leak_email() AS v FROM customers WHERE id = 1")
	if strings.Contains(strings.ToLower(body), email) {
		t.Errorf("the folded function returned the email beside a table: %s", body)
	}
}
