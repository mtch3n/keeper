package main

import (
	"strings"
	"testing"
)

func TestRenderSQLShowsTerminalControlBytesAsText(t *testing.T) {
	got := renderSQL("SELECT 1\x1b[2J\x07\r\u009b\u202e\nFROM t")
	for _, bad := range []string{"\x1b", "\x07", "\r", "\u009b", "\u202e"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived in %q", bad, got)
		}
	}
	if !strings.Contains(got, "[U+001B]") || !strings.Contains(got, "\nFROM t") {
		t.Errorf("got %q", got)
	}
}
