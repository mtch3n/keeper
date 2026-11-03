package daemon

import (
	"fmt"
	"strings"

	"github.com/mtchen/keeper/internal/audit"
	"github.com/mtchen/keeper/internal/types"
)

// dialogLineCap is the longest line the dialog shows whole. Claude Code 2.1.317
// shows only the first 3 lines of an elicitation, one row each, and cuts a line
// at the terminal width minus 6; 72 survives an 80-column terminal.
const dialogLineCap = 72

// dialogMessage is the text of the approval dialog the agent's client shows.
// It passes through the client and its hooks, so it holds facts and the SQL as
// the agent sent it, and never a changed row or a resolved parameter value: a
// write links to its rows on the local page instead. What a person must see
// goes in the first 3 lines: the SQL without its comments, what the statement
// is, which tables it touches and how many rows, then the link when there is
// one or else the profile and reason.
//
// The SQL line drops comments, because a comment at the start can read as a
// different statement: `/* SELECT count(*) ... */ DELETE ...` starts like a
// read. Literals stay, because the approver decides on them.
func dialogMessage(item types.ApprovalItem, profile, link string) string {
	f := item.Facts
	sql, cut := capLine(strings.Join(strings.Fields(audit.StripComments(forDisplay(item.SQL))), " "))

	targets := f.Targets
	if len(targets) == 0 {
		targets = f.Relations
	}
	names := make([]string, len(targets))
	for i, r := range targets {
		names[i] = r.String()
	}
	kind := f.StatementType
	if kind == "" {
		kind = "statement"
	}
	var rows string
	switch {
	case item.Write != nil && item.Write.Unpreviewed:
		rows = fmt.Sprintf("about %d rows, not previewed (side effects)", item.Write.RowCount)
	case item.Write != nil:
		rows = fmt.Sprintf("%d rows affected", item.Write.RowCount)
	default:
		rows = fmt.Sprintf("about %d rows", f.EstimatedRows)
	}
	what, _ := capLine(strings.TrimSpace(kind + " " + strings.Join(names, ", ") + ": " + rows))

	head := "profile: " + profile
	if len(f.Reasons) > 0 {
		head += "  why: " + strings.Join(f.Reasons, ", ")
	}
	lines := []string{sql, what}
	switch {
	case item.Write != nil && cut:
		lines = append(lines, "SQL cut, rows: "+link, head)
	case item.Write != nil:
		lines = append(lines, "changed rows: "+link, head)
	case cut:
		lines = append(lines, "SQL cut, full text: "+link, head)
	default:
		lines = append(lines, head)
	}
	if item.Write != nil && item.Write.Unpreviewed {
		lines = append(lines, "not previewed: the statement calls a function with side effects")
	}
	if len(f.Egress) > 0 {
		lines = append(lines, "masked: "+strings.Join(f.Egress, "; "))
	}
	return strings.Join(lines, "\n")
}

// capLine cuts a line to dialogLineCap characters, with a mark where it was cut.
func capLine(s string) (string, bool) {
	if r := []rune(s); len(r) > dialogLineCap {
		return string(r[:dialogLineCap-1]) + "…", true
	}
	return s, false
}
