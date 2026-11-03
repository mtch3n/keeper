//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/redact"
	"github.com/mtchen/keeper/internal/types"
)

func (e *leanEnv) setDetection(t *testing.T, conn string, d types.Detection) {
	t.Helper()
	if _, err := e.op.PatchConnection(t.Context(), conn, client.PatchConnectionParams{Detection: d}); err != nil {
		t.Fatalf("set detection=%s: %v", d, err)
	}
}

func (e *leanEnv) registerWithCatalog(t *testing.T, name, catalogYAML string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".yaml")
	if err := os.WriteFile(path, []byte(catalogYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	hosts, err := e.op.ListHosts(t.Context())
	if err != nil || len(hosts) == 0 {
		t.Fatalf("hosts: %v", err)
	}
	c, err := e.op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: name, HostID: hosts[0].ID, Database: "shop",
		Credential: client.Credential{User: "shop_eval_ro", Password: e.shop.roPass}, CatalogPath: path,
	})
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return c.ID
}

func firstCell(t *testing.T, body string) string {
	t.Helper()
	q := parseBody(t, body)
	if len(q.Rows) == 0 || len(q.Rows[0]) == 0 {
		t.Fatalf("no cell in %s", body)
	}
	s, _ := q.Rows[0][0].(string)
	return s
}

func TestMask_MASK_C1_aTokenColumnStaysATokenThroughAliasCTEAndView(t *testing.T) {
	e := newLeanEnv(t)
	entries := allowAll("public.customers.id")
	entries["public.customers.email"] = token("email")
	e.declare(t, e.ro, entries)

	email := customerFourEmail(t, e)
	direct := firstCell(t, e.query(t, e.agent, e.ro, "SELECT email FROM customers WHERE id = 4"))
	for name, sql := range map[string]string{
		"alias": "SELECT email AS contact FROM customers WHERE id = 4",
		"cte":   "WITH c AS (SELECT email FROM customers WHERE id = 4) SELECT email AS e FROM c",
		"view":  "SELECT email FROM customer_contacts WHERE id = 4",
	} {
		body := e.query(t, e.agent, e.ro, sql)
		got := firstCell(t, body)
		if strings.Contains(strings.ToLower(body), email) || strings.Contains(got, "@") {
			t.Errorf("%s: the email came back: %s", name, body)
		}
		if !strings.Contains(got, "email") || strings.Contains(got, "redacted") {
			t.Errorf("%s: cell = %q, want a token", name, got)
		}
		if got != direct {
			t.Errorf("%s: token %q differs from the direct token %q for an equal value", name, got, direct)
		}
	}
}

func TestMask_MASK_C2_aDropPartialOrUnknownEntryIsRedacted(t *testing.T) {
	e := newLeanEnv(t)
	conn := e.registerWithCatalog(t, "legacy", `version: 1
schemas:
  public:
    customers:
      id: {policy: allow}
      email: {policy: drop}
      phone: {policy: partial, form: phone_country_area}
      ssn: {policy: banana}
      full_name: {policy: redact, hide_name: true}
`)
	body := e.query(t, e.agent, conn, "SELECT id, email, phone, ssn, full_name FROM customers ORDER BY id LIMIT 3")
	q := parseBody(t, body)
	if len(q.Rows) != 3 {
		t.Fatalf("not a result: %s", body)
	}
	for i, row := range q.Rows {
		if len(row) != 5 {
			t.Fatalf("row %d has %d cells, want 5 (drop must not remove the column): %s", i, len(row), body)
		}
		for j := 1; j < 5; j++ {
			if row[j] != redact.RedactedMarker {
				t.Errorf("row %d cell %d = %v, want redacted", i, j, row[j])
			}
		}
	}
	if !strings.Contains(body, "full_name") || strings.Contains(body, "keeper_hidden") {
		t.Errorf("hide_name was not ignored: %s", body)
	}
	if leaks := e.truth.leaks(body); len(leaks) != 0 {
		t.Errorf("seeded personal data in the result: %v", leaks)
	}
}

const (
	giftNotes   = "SELECT shipping_note FROM orders WHERE shipping_note LIKE 'Gift%' ORDER BY id LIMIT 5"
	ticketBody  = "SELECT body FROM support_tickets WHERE id % 4 = 1 ORDER BY id LIMIT 5"
	ticketsJSON = "SELECT metadata FROM support_tickets WHERE id <= 5 ORDER BY id"
	emailSpan   = "⟨redacted:email_address⟩"
)

func scanBodyAllowNotes() map[string]types.ColumnPolicy {
	entries := allowAll("public.orders.id", "public.orders.shipping_note", "public.support_tickets.id", "public.support_tickets.metadata")
	entries["public.support_tickets.body"] = types.ColumnPolicy{Policy: types.PolicyScan}
	return entries
}

func TestMask_MASK_C3_scanDetectionExaminesOnlyScanColumns(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.ro, scanBodyAllowNotes())

	scanned := e.query(t, e.agent, e.ro, ticketBody)
	if !strings.Contains(scanned, emailSpan) || e.truth.leaks(scanned)["email"] != 0 {
		t.Errorf("the scan column's email span was not redacted: %s", scanned)
	}
	allowed := e.query(t, e.agent, e.ro, giftNotes)
	if e.truth.leaks(allowed)["email"] == 0 || strings.Contains(allowed, emailSpan) {
		t.Errorf("detection=scan examined an allow column: %s", allowed)
	}
}

func TestMask_MASK_C4_allDetectionAlsoExaminesAllowTextAndKeepsTokens(t *testing.T) {
	e := newLeanEnv(t)
	entries := scanBodyAllowNotes()
	entries["public.customers.email"] = token("email")
	e.declare(t, e.ro, entries)
	e.setDetection(t, e.ro, types.DetectionAll)

	for name, sql := range map[string]string{"text": giftNotes, "json": ticketsJSON} {
		body := e.query(t, e.agent, e.ro, sql)
		if !strings.Contains(body, emailSpan) || e.truth.leaks(body)["email"] != 0 {
			t.Errorf("%s: an allow column's email was not redacted: %s", name, body)
		}
	}
	tok := firstCell(t, e.query(t, e.agent, e.ro, "SELECT email FROM customers WHERE id = 4"))
	if !strings.Contains(tok, "email") || strings.Contains(tok, "redacted") {
		t.Errorf("a token column = %q, want it to stay a token", tok)
	}
}

func TestMask_MASK_C6_offDetectionRedactsScanCellsWhole(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.ro, scanBodyAllowNotes())
	e.setDetection(t, e.ro, types.DetectionOff)

	q := parseBody(t, e.query(t, e.agent, e.ro, "SELECT body FROM support_tickets WHERE id <= 8 ORDER BY id"))
	if len(q.Rows) != 8 {
		t.Fatalf("rows = %d, want 8", len(q.Rows))
	}
	for i, row := range q.Rows {
		if row[0] != redact.RedactedMarker {
			t.Errorf("row %d = %v, want the whole cell redacted", i, row[0])
		}
	}
	if allowed := e.query(t, e.agent, e.ro, giftNotes); e.truth.leaks(allowed)["email"] == 0 {
		t.Errorf("detection=off changed an allow column: %s", allowed)
	}
}
