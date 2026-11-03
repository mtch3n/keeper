package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

func dialogItem(write bool) types.ApprovalItem {
	item := types.ApprovalItem{
		TicketID: "t1",
		SQL:      "SELECT email FROM customers WHERE id = 4",
		Facts: types.ApprovalFacts{
			StatementType: "SELECT",
			Relations:     []types.RelationRef{{Schema: "public", Relation: "customers"}},
			EstimatedRows: 7,
			Egress:        []string{"customers.email becomes a token"},
			Reasons:       []string{"reads=always"},
		},
	}
	if write {
		item.SQL = "UPDATE orders SET shipping_note = $1 WHERE id = 1"
		item.Facts.StatementType = "UPDATE"
		item.Facts.Relations = []types.RelationRef{{Schema: "public", Relation: "orders"}}
		item.Write = &types.WritePreview{
			Operation: "UPDATE", RowCount: 1, PreviewedAt: time.Now(),
			Changes: &types.WriteChanges{Columns: []string{"shipping_note"}, Rows: []types.ChangedRow{{Old: []string{"old-note-xyz"}, New: []string{"ada@example.test"}}}},
		}
	}
	return item
}

func TestDialogMessageNamesWhatAHumanDecidesOn(t *testing.T) {
	msg := dialogMessage(dialogItem(false), "shop_ro", "http://127.0.0.1:1/r/t1")
	for _, want := range []string{"shop_ro", "reads=always", "public.customers", "7", "customers.email becomes a token", "SELECT email FROM customers WHERE id = 4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "/r/t1") {
		t.Errorf("a read links to no rows:\n%s", msg)
	}
}

func TestDialogMessageForAWriteLinksToItsRowsAndHoldsNone(t *testing.T) {
	msg := dialogMessage(dialogItem(true), "shop_rw", "http://127.0.0.1:1/r/t1")
	if !strings.Contains(msg, "http://127.0.0.1:1/r/t1") {
		t.Errorf("no link to the rows:\n%s", msg)
	}
	for _, bad := range []string{"old-note-xyz", "ada@example.test"} {
		if strings.Contains(msg, bad) {
			t.Errorf("holds %q:\n%s", bad, msg)
		}
	}
}

func TestDialogMessageShowsASQLControlCharacterVisibly(t *testing.T) {
	item := dialogItem(false)
	item.SQL = "SELECT 1\u202e"
	if msg := dialogMessage(item, "p", "l"); strings.Contains(msg, "\u202e") {
		t.Errorf("a bidirectional control reached the dialog: %q", msg)
	}
}
