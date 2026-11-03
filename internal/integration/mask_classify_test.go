//go:build integration

package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/client"
)

func (e *leanEnv) registerPathless(t *testing.T, name string) string {
	t.Helper()
	host, err := e.op.RegisterHost(t.Context(), client.RegisterHostParams{Name: "pathless-" + name, Address: e.shop.host, Port: e.shop.port, SSLMode: "disable"})
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	c, err := e.op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: name, HostID: host.ID, Database: "shop",
		Credential: client.Credential{User: "shop_eval_ro", Password: e.shop.roPass},
	})
	if err != nil {
		t.Fatalf("connection %s: %v", name, err)
	}
	return c.ID
}

func TestMask_MASK_C17_ANewProfileIsUsableWithNoCatalogStep(t *testing.T) {
	e := newLeanEnv(t)
	conn := e.registerPathless(t, "fresh")
	body := e.query(t, e.agent, conn, "SELECT id, created_at, email, loyalty_tier FROM customers ORDER BY id LIMIT 5")

	var out struct {
		Rows [][]any `json:"rows"`
		Hint string  `json:"hint"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Rows) != 5 {
		t.Fatalf("not a result: %s", body)
	}
	for i, row := range out.Rows {
		if id, _ := row[0].(float64); int(id) != i+1 {
			t.Errorf("row %d id = %v, want it shown", i, row[0])
		}
		if ts, _ := row[1].(string); ts == "" || strings.Contains(ts, "redacted") {
			t.Errorf("row %d created_at = %v, want it shown", i, row[1])
		}
		if mail, _ := row[2].(string); !strings.Contains(mail, "email") || strings.Contains(mail, "@") {
			t.Errorf("row %d email = %v, want a token", i, row[2])
		}
		if tier, _ := row[3].(string); !strings.Contains(tier, "redacted") {
			t.Errorf("row %d loyalty_tier = %v, want redacted", i, row[3])
		}
	}
	if !strings.Contains(out.Hint, "keeper catalog edit fresh") {
		t.Errorf("hint = %q, want it to name the command", out.Hint)
	}
	if leaks := e.truth.leaks(body); len(leaks) != 0 {
		t.Errorf("seeded personal data in the result: %v", leaks)
	}
}

func TestMask_MASK_C19_SalaryAndBirthDateAreMaskedOnARealDatabase(t *testing.T) {
	e := newLeanEnv(t)
	conn := e.registerPathless(t, "pay")
	body := e.query(t, e.agent, conn, "SELECT salary, name FROM employees LIMIT 3")
	body += e.query(t, e.agent, conn, "SELECT dob FROM customers LIMIT 3")
	if leaks := e.truth.leaks(body); len(leaks) != 0 {
		t.Errorf("seeded personal data in the result: %v", leaks)
	}
}

func TestMask_MASK_C18_APersonsAllowSurvivesAFreshOpen(t *testing.T) {
	e := newLeanEnv(t)
	conn := e.registerPathless(t, "kept")
	e.declare(t, conn, allowAll("public.customers.loyalty_tier"))
	body := e.query(t, e.agent, conn, "SELECT loyalty_tier FROM customers LIMIT 3")
	if strings.Contains(body, "redacted") {
		t.Errorf("a column a person allowed was masked: %s", body)
	}
}
