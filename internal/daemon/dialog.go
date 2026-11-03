package daemon

import (
	"fmt"
	"strings"

	"github.com/mtchen/keeper/internal/types"
)

// dialogMessage is the text of the approval dialog the agent's client shows.
// It passes through the client and its hooks, so it holds facts and the SQL as
// the agent sent it, and never a changed row or a resolved parameter value: a
// write links to its rows on the local page instead.
func dialogMessage(item types.ApprovalItem, profile, link string) string {
	f := item.Facts
	var b strings.Builder
	b.WriteString("keeper: run this statement?\n")
	fmt.Fprintf(&b, "profile: %s\n", profile)
	if len(f.Reasons) > 0 {
		fmt.Fprintf(&b, "why: %s\n", strings.Join(f.Reasons, ", "))
	}
	rels := make([]string, len(f.Relations))
	for i, r := range f.Relations {
		rels[i] = r.String()
	}
	fmt.Fprintf(&b, "relations: %s\n", strings.Join(rels, ", "))
	if item.Write != nil {
		fmt.Fprintf(&b, "%s: %d rows affected\n", item.Write.Operation, item.Write.RowCount)
	} else {
		fmt.Fprintf(&b, "rows: about %d\n", f.EstimatedRows)
	}
	if len(f.Egress) > 0 {
		fmt.Fprintf(&b, "masked: %s\n", strings.Join(f.Egress, "; "))
	}
	if item.Write != nil {
		fmt.Fprintf(&b, "changed rows: %s\n", link)
	}
	b.WriteString("SQL:\n")
	b.WriteString(forDisplay(item.SQL))
	return b.String()
}
