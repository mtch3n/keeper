//go:build integration

package integration

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

// newFlowEnv is newLeanEnv with extra schema in place before the connections
// are registered, so the catalog knows it.
func newFlowEnv(t *testing.T, extra ...string) *leanEnv {
	t.Helper()
	f := NewOn(t, "postgres:18-alpine")
	shop := seedShop(t, f)
	admin, err := pgx.Connect(t.Context(), shop.superDSN)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range extra {
		if _, err := admin.Exec(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	admin.Close(t.Context())
	sock := startDaemon(t)
	op, err := client.DialHuman(t.Context(), sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { op.Close() })
	ro, rw := register(t, op, shop)
	e := &leanEnv{f: f, shop: shop, sock: sock, op: op, agent: mcpAgent(t), ro: ro, rw: rw, truth: groundTruth(t, shop.superDSN)}
	e.setReads(t, ro, types.ReadsNever)
	return e
}

var ssnShape = regexp.MustCompile(`\d{3}-\d{2}-\d{4}`)

// everyCellMasked fails unless a statement ran and each cell it returned is a
// redaction marker.
func everyCellMasked(t *testing.T, label, body string) {
	t.Helper()
	q := parseBody(t, body)
	if len(q.Rows) == 0 {
		t.Errorf("%s: no rows came back: %s", label, body)
		return
	}
	for _, row := range q.Rows {
		for _, cell := range row {
			if s, _ := cell.(string); !strings.Contains(s, "redacted") {
				t.Errorf("%s: cell %v is not masked: %.300s", label, cell, body)
				return
			}
		}
	}
}

func TestComputedColumnsDoNotCarryDataFromHiddenRelations(t *testing.T) {
	e := newFlowEnv(t,
		`CREATE FUNCTION cust_ssn(i int) RETURNS text LANGUAGE sql STABLE AS 'select ssn from customers where id = i'`,
		`CREATE DOMAIN posint AS int`,
		`CREATE TABLE dom (id int PRIMARY KEY, code posint, scores int[])`,
		`INSERT INTO dom VALUES (1, 424242, '{777777,2}')`,
		`CREATE VIEW emp_view AS SELECT id, salary AS amount FROM employees`,
		`CREATE VIEW cust_only AS SELECT id, ssn FROM customers`,
		`CREATE ROLE cust_only_ro LOGIN PASSWORD 'x'`,
		`GRANT USAGE ON SCHEMA public TO cust_only_ro`,
		`GRANT SELECT ON cust_only TO cust_only_ro`,
		`GRANT SELECT ON dom, emp_view TO shop_eval_ro`,
		`ANALYZE customers`,
	)
	e.declare(t, e.ro, map[string]types.ColumnPolicy{
		"public.products.id":    {Policy: types.PolicyAllow},
		"public.products.sku":   {Policy: types.PolicyAllow},
		"public.products.name":  {Policy: types.PolicyAllow},
		"public.products.price": {Policy: types.PolicyAllow},
		"public.dom.id":         {Policy: types.PolicyAllow},
		"public.dom.code":       {Policy: types.PolicyRedact},
		"public.dom.scores":     {Policy: types.PolicyRedact},
		"public.support_tickets.metadata": {Policy: types.PolicyAllow, Paths: map[string]types.ColumnPolicy{
			"$.channel": {Policy: types.PolicyAllow},
		}},
		"public.support_tickets.id": {Policy: types.PolicyAllow},
	})
	ssn := e.superValue(t, "SELECT ssn FROM customers WHERE id = 1")
	digits := strings.ReplaceAll(ssn, "-", "")

	// Every statement must run and return rows, each cell masked. A statement
	// whose refusal is the intended outcome says so in refused.
	cases := []struct {
		label, sql string
		refused    bool
	}{
		{"user function", "SELECT cust_ssn(id) FROM products", false},
		{"query_to_xml", "SELECT query_to_xml('select email,ssn from customers',true,true,'')::text FROM products", false},
		{"pg_stats", "SELECT histogram_bounds::text FROM pg_stats WHERE tablename='customers' AND attname='ssn'", false},
		{"digits of an ssn", "SELECT replace(ssn,'-','')::bigint FROM customers LIMIT 3", false},
		{"digits, order by", "SELECT replace(ssn,'-','')::bigint FROM customers ORDER BY id LIMIT 3", false},
		{"digits, group by", "SELECT replace(ssn,'-','')::bigint, count(*) FROM customers GROUP BY 1 LIMIT 3", false},
		{"digits, subquery", "SELECT x FROM (SELECT replace(ssn,'-','')::bigint AS x FROM customers LIMIT 3) s", false},
		{"digits, offset 0 subquery", "SELECT x FROM (SELECT replace(ssn,'-','')::bigint AS x FROM customers OFFSET 0) s LIMIT 3", false},
		{"digits, cte", "WITH c AS MATERIALIZED (SELECT replace(ssn,'-','')::bigint AS x FROM customers) SELECT x FROM c LIMIT 3", false},
		{"digits, union", "SELECT replace(ssn,'-','')::bigint FROM customers UNION ALL SELECT 1 LIMIT 3", false},
		{"digits, lateral", "SELECT l.x FROM products p, LATERAL (SELECT replace(ssn,'-','')::bigint AS x FROM customers c WHERE c.id = p.id) l LIMIT 3", false},
		{"filter aggregate", "SELECT count(*) FILTER (WHERE ssn LIKE '1%') FROM customers", false},
		{"sum of digits", "SELECT sum(replace(ssn,'-','')::bigint) FROM customers", false},
		{"date arithmetic", "SELECT dob - date '1970-01-01', extract(epoch from dob) FROM customers LIMIT 3", false},
		{"domain column", "SELECT code + 0 FROM dom", false},
		{"array element", "SELECT scores[1] FROM dom", false},
		{"json with paths", "SELECT metadata->>'ip', metadata::text FROM support_tickets LIMIT 3", false},
		{"view of a base col", "SELECT amount FROM emp_view LIMIT 3", false},
	}
	for _, tc := range cases {
		body := e.query(t, e.agent, e.ro, tc.sql)
		if strings.Contains(body, ssn) || strings.Contains(body, digits) || ssnShape.MatchString(body) ||
			strings.Contains(body, "424242") || strings.Contains(body, "777777") || strings.Contains(body, `"10.`) {
			t.Errorf("%s leaked: %.400s", tc.label, body)
		}
		if !strings.Contains(body, `"rows"`) {
			if !tc.refused {
				t.Errorf("%s: did not run: %.200s", tc.label, body)
			}
			continue
		}
		t.Logf("%-26s ran, masked", tc.label)
		everyCellMasked(t, tc.label, body)
	}

	e.super(t, `CREATE TABLE late (id int, ssn text)`, `INSERT INTO late VALUES (1, '123-45-6789')`, `GRANT SELECT ON late TO shop_eval_ro`)
	body := e.query(t, e.agent, e.ro, "SELECT ssn || '' FROM late")
	if strings.Contains(body, "123-45-6789") {
		t.Errorf("a table created after the catalog opened leaked: %s", body)
	}
	everyCellMasked(t, "late table", body)

	hosts, err := e.op.ListHosts(t.Context())
	if err != nil || len(hosts) == 0 {
		t.Fatalf("hosts: %v", err)
	}
	c, err := e.op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: "cust_only", HostID: hosts[0].ID, Database: "shop",
		Credential: client.Credential{User: "cust_only_ro", Password: "x"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	e.setReads(t, c.ID, types.ReadsNever)
	body = e.query(t, e.agent, c.ID, "SELECT ssn || '' FROM cust_only")
	if strings.Contains(body, ssn) || ssnShape.MatchString(body) {
		t.Errorf("a role with a grant on a view only leaked: %.400s", body)
	}
	everyCellMasked(t, "view-only role", body)
}

func TestAggregatesStayUsableWhileOtherColumnsAreMasked(t *testing.T) {
	e := newFlowEnv(t)
	e.declare(t, e.ro, map[string]types.ColumnPolicy{
		"public.orders.status": {Policy: types.PolicyAllow},
	})
	for label, sql := range map[string]string{
		"count star":          "SELECT count(*) FROM orders",
		"count with filter":   "SELECT count(*) FROM customers WHERE email = 'nobody@example.com'",
		"sum of an allowed":   "SELECT sum(total) FROM orders",
		"join":                "SELECT count(*) FROM orders o JOIN customers c ON c.id = o.customer_id",
		"group by allowed":    "SELECT customer_id, sum(total) FROM orders GROUP BY customer_id ORDER BY 2 DESC LIMIT 3",
		"upper of an allowed": "SELECT upper(status) FROM orders LIMIT 3",
		"avg of an allowed":   "SELECT avg(total) FROM orders",
	} {
		body := e.query(t, e.agent, e.ro, sql)
		q := parseBody(t, body)
		masked := strings.Contains(body, "redacted")
		t.Logf("%-20s rows=%d masked=%v %.160s", label, len(q.Rows), masked, body)
		if len(q.Rows) == 0 || masked {
			t.Errorf("%s: want usable values, got %.300s", label, body)
		}
	}
}

func TestNonFiniteValuesAndHugeCellsDoNotBreakAResult(t *testing.T) {
	e := newFlowEnv(t)
	body := e.query(t, e.agent, e.ro, "SELECT id, 'NaN'::float8 AS f, 'infinity'::timestamptz AS ts, 'infinity'::date AS d, ARRAY['-infinity'::float8] AS a FROM customers WHERE id <= 2")
	q := parseBody(t, body)
	if len(q.Rows) != 2 {
		t.Fatalf("rows = %d, want 2: %.300s", len(q.Rows), body)
	}
	for _, want := range []string{"NaN", "infinity", "-Infinity"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q missing from %.300s", want, body)
		}
	}
	if strings.Contains(body, `"truncated":true`) {
		t.Errorf("a result that fits was cut: %.300s", body)
	}

	start := time.Now()
	body = e.query(t, e.agent, e.ro, "SELECT repeat('x',500000000) FROM generate_series(1,1000)")
	t.Logf("a 500 MB cell took %s", time.Since(start))
	if !strings.Contains(body, `"truncated":true`) || strings.Contains(body, `"rows":[["`) {
		t.Errorf("a result over the byte limit was not cut by size: %.300s", body)
	}
}

func TestARuleThatWritesElsewhereIsRefused(t *testing.T) {
	e := newFlowEnv(t,
		`CREATE TABLE audit_a (id int, v int)`, `CREATE TABLE audit_b (id int)`,
		`INSERT INTO audit_a VALUES (1, 1)`, `INSERT INTO audit_b VALUES (1)`,
		`CREATE RULE also_delete AS ON UPDATE TO audit_a DO ALSO DELETE FROM audit_b WHERE id = old.id`,
		`GRANT UPDATE ON audit_a TO shop_eval_rw`, `GRANT DELETE ON audit_b TO shop_eval_rw`,
	)
	body := e.query(t, e.agent, e.rw, "UPDATE audit_a SET v = 2")
	if !strings.Contains(body, "multi_statement") {
		t.Errorf("a statement that writes two tables was not refused: %.300s", body)
	}
	if got := e.superValue(t, "SELECT count(*)::text FROM audit_b"); got != "1" {
		t.Errorf("audit_b rows = %s, want 1", got)
	}
}

func TestATokenFromOneConnectionDoesNotResolveOnAnother(t *testing.T) {
	e := newFlowEnv(t)
	entries := allowAll("public.customers.id")
	entries["public.customers.email"] = token("email")
	e.declare(t, e.ro, entries)
	tok := e.mintEmailToken(t, e.agent, e.ro)

	other := e.registerWithCatalog(t, "shop_other", "")
	e.setReads(t, other, types.ReadsNever)
	body := e.query(t, e.agent, other, "SELECT id FROM customers WHERE email = $1", map[string]any{"token": tok})
	if strings.Contains(body, `"rows"`) || !strings.Contains(strings.ToLower(body), "token") {
		t.Errorf("the token resolved on another connection: %.300s", body)
	}
	body = e.query(t, e.agent, e.ro, "SELECT id FROM customers WHERE email = $1", map[string]any{"token": tok})
	if len(parseBody(t, body).Rows) != 1 {
		t.Errorf("the token stopped working on its own connection: %.300s", body)
	}
}

var _ = fmt.Sprint
