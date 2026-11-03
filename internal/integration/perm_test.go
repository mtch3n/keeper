//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestPerm_PERM_C1_newProfileDefaults(t *testing.T) {
	e := newLeanEnv(t)
	host, err := e.op.ListConnections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(host) == 0 {
		t.Fatal("no connections")
	}
	d, err := e.op.DescribeConnection(t.Context(), e.ro)
	if err != nil {
		t.Fatal(err)
	}
	if d.Profile.Reads != types.ReadsUncertain {
		t.Errorf("reads = %q, want uncertain", d.Profile.Reads)
	}
	if d.Profile.Writes != types.WritesOff {
		t.Errorf("writes = %q, want off", d.Profile.Writes)
	}
}

func TestPerm_PERM_C2_readsAlwaysAsks(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.ro, types.ReadsAlways)
	body := e.query(t, e.agent, e.ro, "SELECT id FROM products LIMIT 1")
	if queryOutcome(body) != "ask" {
		t.Errorf("reads=always ran without asking: %s", body)
	}
}

func TestPerm_PERM_C4_writesAllowCommitsWithoutAsking(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t, "CREATE SEQUENCE perm_c4", "GRANT USAGE, SELECT ON SEQUENCE perm_c4 TO shop_eval_rw")
	e.setWrites(t, e.rw, types.WritesAllow)
	body := e.query(t, e.agent, e.rw, "UPDATE orders SET status = 'perm-c4', total = total + 0 * nextval('perm_c4') WHERE id = 2")
	if queryOutcome(body) != "run" {
		t.Fatalf("writes=allow did not run: %s", body)
	}
	if got := e.superValue(t, "SELECT status FROM orders WHERE id = 2"); got != "perm-c4" {
		t.Errorf("status = %q, the write did not commit", got)
	}
	if got := e.superValue(t, "SELECT last_value::text FROM perm_c4"); got != "1" {
		t.Errorf("the statement executed %s times, want once with no preview", got)
	}
	recs, err := e.activity(t)
	if err != nil {
		t.Fatal(err)
	}
	if !activityHasWrite(recs, "UPDATE") {
		t.Error("the activity log has no record of the write")
	}
}

func TestPerm_PERM_C5_writesAskWaitsForAPerson(t *testing.T) {
	e := newLeanEnv(t)
	e.setWrites(t, e.rw, types.WritesAsk)
	waiting := e.query(t, e.agent, e.rw, "UPDATE orders SET status = 'perm-c5' WHERE id = 3")
	if queryOutcome(waiting) != "ask" {
		t.Fatalf("writes=ask did not wait: %s", waiting)
	}
	if got := e.superValue(t, "SELECT status FROM orders WHERE id = 3"); got == "perm-c5" {
		t.Fatal("the write committed before anyone accepted")
	}
	if got := e.approveAndWait(t, e.agent, waiting); !strings.Contains(got, `"ready"`) {
		t.Fatalf("not finished: %s", got)
	}
	if got := e.superValue(t, "SELECT status FROM orders WHERE id = 3"); got != "perm-c5" {
		t.Errorf("status = %q after acceptance", got)
	}
}

func TestPerm_PERM_C7_transactionStopsAFunctionThatInserts(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t,
		"CREATE FUNCTION perm_c7() RETURNS int LANGUAGE sql AS $$ UPDATE orders SET status = 'perm-c7' WHERE id = 4 RETURNING 1 $$",
		"GRANT EXECUTE ON FUNCTION perm_c7() TO shop_eval_rw")
	e.setReads(t, e.rw, types.ReadsNever)
	e.setWrites(t, e.rw, types.WritesOff)
	body := e.query(t, e.agent, e.rw, "SELECT perm_c7()")
	if got := e.superValue(t, "SELECT status FROM orders WHERE id = 4"); got == "perm-c7" {
		t.Errorf("a function wrote through a read: %s", body)
	}
	if queryOutcome(body) != "refuse" {
		t.Errorf("want a refusal: %s", body)
	}
}

func TestPerm_PERM_C8_setConfigCannotTurnReadOnlyOff(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.rw, types.ReadsNever)
	body := e.query(t, e.agent, e.rw, "SELECT set_config('transaction_read_only', 'off', true)")
	if queryOutcome(body) != "refuse" {
		t.Errorf("read-only was turned off: %s", body)
	}
}

func TestPerm_PERM_C9_volatileCallsAsk(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t,
		"CREATE VIEW perm_c9_inner AS SELECT id, pg_notify('c', 'x')::text AS n FROM products",
		"CREATE VIEW perm_c9_outer AS SELECT * FROM perm_c9_inner",
		"GRANT SELECT ON perm_c9_inner, perm_c9_outer TO shop_eval_ro")
	for _, sql := range []string{
		"SELECT pg_notify('c', 'x')",
		"SELECT id FROM products WHERE pg_notify('c', 'x') IS NULL",
		"SELECT id FROM perm_c9_outer",
	} {
		if body := e.query(t, e.agent, e.ro, sql); queryOutcome(body) != "ask" {
			t.Errorf("%s did not ask: %s", sql, body)
		}
	}
}

func TestPerm_PERM_C10_ordinaryReadsDoNotAsk(t *testing.T) {
	e := newLeanEnv(t)
	for _, sql := range []string{
		"SELECT o.id, c.id FROM orders o JOIN customers c ON c.id = o.customer_id LIMIT 2",
		"SELECT count(*) FROM products",
		"SELECT upper(sku), now(), coalesce(name, 'x'), price::text FROM products LIMIT 2",
	} {
		if body := e.query(t, e.agent, e.ro, sql); queryOutcome(body) != "run" {
			t.Errorf("%s asked: %s", sql, body)
		}
	}
}

func TestPerm_PERM_C11_aColumnNamedLikeAFunctionDoesNotAsk(t *testing.T) {
	e := newLeanEnv(t)
	e.super(t, "ALTER TABLE products ADD COLUMN random int", "GRANT SELECT ON products TO shop_eval_ro")
	if body := e.query(t, e.agent, e.ro, "SELECT random FROM products LIMIT 1"); queryOutcome(body) != "run" {
		t.Errorf("a column named random asked: %s", body)
	}
}

func TestPerm_PERM_C12_oldPermissionSurfaceIsGone(t *testing.T) {
	e := newLeanEnv(t)
	tools := e.toolNames(t)
	for _, gone := range []string{"request_authorization", "get_authorization_result"} {
		if slices.Contains(tools, gone) {
			t.Errorf("tool %s still exists", gone)
		}
	}
	if got := e.status(t, http.MethodGet, "/v1/grants"); got != http.StatusNotFound {
		t.Errorf("GET /v1/grants = %d, want 404", got)
	}
	if got := e.status(t, http.MethodPut, "/v1/connections/"+e.ro+"/denylist"); got != http.StatusNotFound {
		t.Errorf("PUT denylist = %d, want 404", got)
	}
}

func TestPerm_PERM_C14_ddlAndMultiStatementAreRefusedUnderEverySetting(t *testing.T) {
	e := newLeanEnv(t)
	e.setReads(t, e.rw, types.ReadsNever)
	e.setWrites(t, e.rw, types.WritesAllow)
	for _, sql := range []string{"CREATE TABLE perm_c14 (id int)", "SELECT 1; SELECT 2"} {
		if body := e.query(t, e.agent, e.rw, sql); queryOutcome(body) != "refuse" {
			t.Errorf("%s was not refused: %s", sql, body)
		}
	}
}

func TestPerm_PERM_C15_writesOffNamesTheWriter(t *testing.T) {
	e := newLeanEnv(t)
	e.setWrites(t, e.rw, types.WritesOff)
	e.setWrites(t, e.ro, types.WritesAsk)
	body := e.query(t, e.agent, e.rw, "UPDATE orders SET status = 'x' WHERE id = 5")
	if queryOutcome(body) != "refuse" || !strings.Contains(body, "shop_ro") {
		t.Errorf("want a refusal that names shop_ro: %s", body)
	}
}

func TestPerm_PERM_C16_explainAndQueryAgree(t *testing.T) {
	e := newLeanEnv(t)
	for _, reads := range []types.Reads{types.ReadsAlways, types.ReadsUncertain, types.ReadsNever} {
		for _, writes := range []types.Writes{types.WritesOff, types.WritesAsk, types.WritesAllow} {
			e.setReads(t, e.rw, reads)
			e.setWrites(t, e.rw, writes)
			for _, sql := range []string{
				"SELECT id FROM products LIMIT 1",
				"SELECT random() > 2",
				"UPDATE orders SET status = status WHERE id = 6",
				"CREATE TABLE perm_c16 (id int)",
			} {
				predicted := e.explainOutcome(t, e.rw, sql)
				body := e.query(t, e.agent, e.rw, sql)
				if got := queryOutcome(body); got != predicted {
					t.Errorf("reads=%s writes=%s %s: explain %s, query %s: %s", reads, writes, sql, predicted, got, body)
				}
			}
		}
	}
}

func TestPerm_PERM_C17_samePlanErrorFromExplainAndQuery(t *testing.T) {
	e := newLeanEnv(t)
	sql := "SELECT nope FROM products"
	fromExplain := e.agent.call(t, "explain", map[string]any{"connection_id": e.ro, "sql": sql})
	fromQuery := e.query(t, e.agent, e.ro, sql)
	if fromExplain != fromQuery && !sameErrorCode(fromExplain, fromQuery) {
		t.Errorf("explain: %s\nquery: %s", fromExplain, fromQuery)
	}
	if strings.Contains(fromQuery, "nope") {
		t.Errorf("the server's error text reached the agent: %s", fromQuery)
	}
}

func TestPerm_PERM_C18_masterAccountIsNotAudited(t *testing.T) {
	e := newLeanEnv(t)
	id := e.registerSuper(t)
	if body := e.query(t, e.agent, id, "SELECT count(*) FROM products"); queryOutcome(body) != "run" {
		t.Errorf("a master account did not run: %s", body)
	}
	raw := e.rawGet(t, "/v1/connections/"+id)
	if strings.Contains(raw, "finding") || strings.Contains(raw, "rolsuper") || strings.Contains(raw, "audited") {
		t.Errorf("a finding appears on the connection: %s", raw)
	}
	if got := e.status(t, http.MethodGet, "/v1/audit"); got != http.StatusNotFound {
		t.Errorf("GET /v1/audit = %d, want 404", got)
	}
}
