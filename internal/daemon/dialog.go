package daemon

import (
	"fmt"
	"strings"

	"github.com/mtchen/keeper/internal/types"
)

// dialogLineCap is the longest SQL line the dialog shows whole. Claude Code 2.1.317
// shows only the first 3 lines of an elicitation, one row each, and cuts a line
// at the terminal width minus 6; 72 survives an 80-column terminal.
const dialogLineCap = 72

// dialogMessage is the text of the approval dialog the agent's client shows.
// It passes through the client and its hooks, so it holds facts and the SQL as
// the agent sent it, and never a changed row or a resolved parameter value: a
// write links to its rows on the local page instead. What a person must see
// goes in the first 3 lines: the SQL, the profile and reason, then the link or
// the relations and rows.
func dialogMessage(item types.ApprovalItem, profile, link string) string {
	f := item.Facts
	sql := strings.Join(strings.Fields(forDisplay(item.SQL)), " ")
	cut := false
	if r := []rune(sql); len(r) > dialogLineCap {
		sql, cut = string(r[:dialogLineCap-1])+"…", true
	}
	head := "profile: " + profile
	if len(f.Reasons) > 0 {
		head += "  why: " + strings.Join(f.Reasons, ", ")
	}
	rels := make([]string, len(f.Relations))
	for i, r := range f.Relations {
		rels[i] = r.String()
	}
	relations := "relations: " + strings.Join(rels, ", ")
	var rows string
	if item.Write != nil {
		rows = fmt.Sprintf("%s: %d rows affected", item.Write.Operation, item.Write.RowCount)
	} else {
		rows = fmt.Sprintf("rows: about %d", f.EstimatedRows)
	}
	lines := []string{sql, head}
	if link == "" {
		link = "run `keeper approve` in a terminal"
	}
	switch {
	case item.Write != nil && cut:
		lines = append(lines, "SQL cut, changed rows: "+link, relations, rows)
	case item.Write != nil:
		lines = append(lines, "changed rows: "+link, relations, rows)
	case cut:
		lines = append(lines, "SQL cut, full text: "+link, relations, rows)
	default:
		lines = append(lines, relations+"  "+rows)
	}
	if len(f.Egress) > 0 {
		lines = append(lines, "masked: "+strings.Join(f.Egress, "; "))
	}
	return strings.Join(lines, "\n")
}
