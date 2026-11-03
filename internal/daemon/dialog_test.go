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

func TestDialogShowsSQLInItsFirstLines(t *testing.T) {
	item := dialogItem(false)
	item.SQL = "SELECT  email\n  FROM customers\n\tWHERE id = 4"
	msg := dialogMessage(item, "shop_ro", "http://127.0.0.1:1/r/t1")
	lines := strings.Split(msg, "\n")
	if lines[0] != "SELECT email FROM customers WHERE id = 4" {
		t.Errorf("the first line is not the collapsed SQL: %q", lines[0])
	}
	if strings.Contains(msg, "/r/t1") {
		t.Errorf("a short read SQL needs no link:\n%s", msg)
	}

	item.SQL = "SELECT " + strings.Repeat("a, ", 100) + "z FROM customers"
	msg = dialogMessage(item, "shop_ro", "http://127.0.0.1:1/r/t1")
	lines = strings.Split(msg, "\n")
	if n := len([]rune(lines[0])); n > 72 || !strings.HasSuffix(lines[0], "…") {
		t.Errorf("a long SQL is not cut with the mark within 72 characters (%d): %q", n, lines[0])
	}
	if !strings.Contains(strings.Join(lines[:3], "\n"), "http://127.0.0.1:1/r/t1") {
		t.Errorf("the link is not in the first three lines:\n%s", msg)
	}
}

func TestDialogSQLLineDropsLeadingComments(t *testing.T) {
	item := dialogItem(true)
	item.SQL = "/* SELECT count(*) FROM orders */ -- read only\nDELETE FROM orders WHERE total = 'x /* y */'"
	msg := dialogMessage(item, "shop_rw", "http://127.0.0.1:1/r/t1")
	first := strings.Split(msg, "\n")[0]
	if first != "DELETE FROM orders WHERE total = 'x /* y */'" {
		t.Errorf("the SQL line is %q", first)
	}
}

func TestDialogFirstThreeLinesNameTheStatementItsTargetsAndRows(t *testing.T) {
	for _, write := range []bool{false, true} {
		item := dialogItem(write)
		msg := dialogMessage(item, "shop_rw", "http://127.0.0.1:1/r/t1")
		visible := strings.Join(strings.Split(msg, "\n")[:3], "\n")
		kind := item.Facts.StatementType
		for _, want := range []string{kind, item.Facts.Relations[0].String(), map[bool]string{false: "7", true: "1 rows"}[write]} {
			if !strings.Contains(visible, want) {
				t.Errorf("write=%v: the first three lines lack %q:\n%s", write, want, msg)
			}
		}
		for i, l := range strings.Split(msg, "\n")[:3] {
			if n := len([]rune(l)); n > dialogLineCap {
				t.Errorf("write=%v: line %d is %d characters: %q", write, i, n, l)
			}
		}
	}
}

func TestDialogSaysAnUnpreviewedWriteWasNotPreviewed(t *testing.T) {
	item := dialogItem(true)
	item.Write.Unpreviewed = true
	item.Write.RowCount = 9
	msg := dialogMessage(item, "shop_rw", "http://127.0.0.1:1/r/t1")
	if visible := strings.Join(strings.Split(msg, "\n")[:3], "\n"); !strings.Contains(visible, "not previewed") {
		t.Errorf("the visible lines do not say so:\n%s", msg)
	}
	if !strings.Contains(msg, "calls a function with side effects") || strings.Contains(msg, "9 rows affected") {
		t.Errorf("the reason is missing, or the estimate reads as a count:\n%s", msg)
	}
}

func TestDisplayCopyShowsTerminalControlBytesAsText(t *testing.T) {
	got := forDisplay("SELECT 1\x1b[2J\x1b]0;x\x07\r\u009b")
	for _, bad := range []string{"\x1b", "\x07", "\r", "\u009b"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	if !strings.Contains(got, "[U+001B]") {
		t.Errorf("the escape is not shown as text: %q", got)
	}
	if forDisplay("SELECT 1\n\tFROM t") != "SELECT 1\n\tFROM t" {
		t.Error("a newline or a tab was rewritten")
	}
}
