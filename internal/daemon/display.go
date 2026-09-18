package daemon

import "strings"

// bidiControls are the Unicode characters that reorder text without appearing in
// it. R9.2 requires them to render visibly on an approval screen: a statement
// that reads as a harmless SELECT and executes as something else is the
// trojan-source shape, and keeper inherits the defence from gatekeeper.
//
// This runs on the *display* copy of a statement only. The bytes that execute
// are the ones the agent sent, kept separately on the ticket, so neutralising
// them here cannot change what a human approved into something else.
var bidiControls = []rune{
	0x061C,         // ALM
	0x200E, 0x200F, // LRM, RLM
	0x202A, 0x202B, 0x202C, 0x202D, 0x202E, // LRE, RLE, PDF, LRO, RLO
	0x2066, 0x2067, 0x2068, 0x2069, // LRI, RLI, FSI, PDI
}

const hexDigits = "0123456789ABCDEF"

// forDisplay replaces every bidirectional control with a visible name.
func forDisplay(s string) string {
	if !strings.ContainsFunc(s, isBidi) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isBidi(r) {
			b.WriteString("[U+")
			for shift := 12; shift >= 0; shift -= 4 {
				b.WriteByte(hexDigits[(r>>shift)&0xF])
			}
			b.WriteString("]")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isBidi(r rune) bool {
	for _, c := range bidiControls {
		if r == c {
			return true
		}
	}
	return false
}
