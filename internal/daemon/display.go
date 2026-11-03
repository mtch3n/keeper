package daemon

import (
	"fmt"
	"strings"
	"unicode"
)

// forDisplay makes every character that acts on a terminal or reorders text
// visible as its code point. R9.2 requires the bidirectional controls to render
// visibly on an approval screen: a statement that reads as a harmless SELECT and
// executes as something else is the trojan-source shape. The other control
// characters, an escape above all, would let a statement rewrite what the
// approver's terminal shows. Newline and tab stay, because a statement is laid
// out with them.
//
// This runs on the *display* copy of a statement only. The bytes that execute
// are the ones the agent sent, kept separately on the ticket, so neutralising
// them here cannot change what a human approved into something else.
func forDisplay(s string) string {
	if !strings.ContainsFunc(s, isHidden) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isHidden(r) {
			fmt.Fprintf(&b, "[U+%04X]", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isHidden(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case unicode.IsControl(r):
		return true
	}
	switch r {
	case 0x061C, 0x200E, 0x200F, 0x202A, 0x202B, 0x202C, 0x202D, 0x202E, 0x2066, 0x2067, 0x2068, 0x2069:
		return true
	}
	return false
}
