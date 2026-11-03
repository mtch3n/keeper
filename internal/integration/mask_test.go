//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/mtchen/keeper/internal/redact"
	"github.com/mtchen/keeper/internal/types"
)

func customerFourEmail(t *testing.T, e *leanEnv) string {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), e.shop.superDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(t.Context())
	var email string
	if err := conn.QueryRow(t.Context(), "SELECT email FROM customers WHERE id = 4").Scan(&email); err != nil {
		t.Fatalf("read email: %v", err)
	}
	return strings.ToLower(email)
}

func TestMask_MASK_C7_tokenParameterOutputIsMasked(t *testing.T) {
	e := newLeanEnv(t)
	entries := allowAll("public.products.id", "public.products.sku", "public.products.name", "public.products.price")
	entries["public.customers.email"] = token("email")
	e.declare(t, e.ro, entries)

	tok := e.mintEmailToken(t, e.agent, e.ro)
	email := customerFourEmail(t, e)
	for _, sql := range []string{
		"SELECT upper($1::text) AS u FROM products LIMIT 1",
		"SELECT substr($1::text, 1, 6) AS s FROM products LIMIT 1",
	} {
		body := strings.ToLower(e.query(t, e.agent, e.ro, sql, map[string]any{"token": tok}))
		if strings.Contains(body, email) || strings.Contains(body, email[:6]) {
			t.Errorf("%s read a tokenized value back: %s", sql, body)
		}
	}
}

func TestMask_MASK_C8_tokenParameterMaskedAfterApproval(t *testing.T) {
	e := newLeanEnv(t)
	entries := allowAll("public.orders.id", "public.orders.customer_id", "public.orders.total", "public.orders.status", "public.orders.shipping_note")
	entries["public.customers.email"] = token("email")
	e.declare(t, e.rw, entries)

	tok := e.mintEmailToken(t, e.agent, e.rw)
	email := customerFourEmail(t, e)
	waiting := e.query(t, e.agent, e.rw, "UPDATE orders SET shipping_note = substr($1::text, 1, 6) WHERE id = 1 RETURNING shipping_note",
		map[string]any{"token": tok})
	got := strings.ToLower(e.approveAndWait(t, e.agent, waiting))
	if strings.Contains(got, email[:6]) {
		t.Errorf("an approved statement read a tokenized value back: %s", got)
	}
	if !strings.Contains(got, `"ready"`) {
		t.Errorf("the approved statement did not finish: %s", got)
	}
}

func TestMask_MASK_C9_otherSessionTokenIsStale(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.ro, map[string]types.ColumnPolicy{"public.customers.email": token("email"), "public.customers.id": {Policy: types.PolicyAllow}})
	tok := e.mintEmailToken(t, e.agent, e.ro)

	other := mcpAgent(t)
	body := e.query(t, other, e.ro, "SELECT id FROM customers WHERE email = $1", map[string]any{"token": tok})
	if !strings.Contains(body, string(types.CodeStaleToken)) || strings.Contains(body, `"rows"`) {
		t.Errorf("a token from another session: %s", body)
	}
}

func supportTicketColumnsAllowed() map[string]types.ColumnPolicy {
	entries := allowAll("public.support_tickets.id", "public.support_tickets.customer_id", "public.support_tickets.subject", "public.support_tickets.body")
	entries["public.support_tickets.metadata"] = types.ColumnPolicy{
		Policy: types.PolicyAllow,
		Paths:  map[string]types.ColumnPolicy{"$.ip": {Policy: types.PolicyRedact}},
	}
	return entries
}

func TestMask_MASK_C10_extractedJSONPathIsMasked(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.ro, supportTicketColumnsAllowed())
	body := e.query(t, e.agent, e.ro, "SELECT metadata->>'ip' AS ip FROM support_tickets ORDER BY id LIMIT 5")
	if n := e.truth.leaks(body)["ip"]; n != 0 {
		t.Errorf("->> released %d declared values: %s", n, body)
	}
}

func TestMask_MASK_C11_decodedJSONPathIsMasked(t *testing.T) {
	e := newLeanEnv(t)
	e.declare(t, e.ro, supportTicketColumnsAllowed())
	body := e.query(t, e.agent, e.ro, "SELECT metadata FROM support_tickets ORDER BY id LIMIT 5")
	if n := e.truth.leaks(body)["ip"]; n != 0 {
		t.Errorf("a decoded json column released %d declared values: %s", n, body)
	}
	if !strings.Contains(body, redact.RedactedMarker) {
		t.Errorf("the path was not masked in place: %s", body)
	}
}
