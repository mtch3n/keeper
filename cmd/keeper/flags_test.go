package main

import (
	"flag"
	"slices"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

// Every usage line puts the name first and the flags after it. The standard
// flag package stops at the first positional, so the documented form of a
// command parsed no flags at all.
func TestParseFlagsAcceptsFlagsAfterPositionals(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPos  []string
		wantMode string
		wantIDs  []string
	}{
		{"flags after the name", []string{"prod", "--mode", "strict", "--finding", "a", "--finding", "b"}, []string{"prod"}, "strict", []string{"a", "b"}},
		{"flags before the name", []string{"--mode", "strict", "prod"}, []string{"prod"}, "strict", nil},
		{"flags between positionals", []string{"prod", "--mode=strict", "public.users.email"}, []string{"prod", "public.users.email"}, "strict", nil},
		{"a terminator ends flag parsing", []string{"prod", "--", "--mode", "strict"}, []string{"prod", "--mode", "strict"}, "", nil},
		{"no arguments", nil, nil, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			mode := fs.String("mode", "", "")
			var ids stringList
			fs.Var(&ids, "finding", "")

			if err := parseFlags(fs, tc.args); err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			if got := fs.Args(); !slices.Equal(got, tc.wantPos) {
				t.Errorf("positionals = %q, want %q", got, tc.wantPos)
			}
			if *mode != tc.wantMode {
				t.Errorf("--mode = %q, want %q", *mode, tc.wantMode)
			}
			if !slices.Equal([]string(ids), tc.wantIDs) {
				t.Errorf("--finding = %q, want %q", ids, tc.wantIDs)
			}
		})
	}
}

// A connection carries its statement timeout as a time.Duration, which json/v2
// refuses to encode without a format. `connection show --json` failed on every
// connection because of it.
func TestPrintJSONEncodesAConnectionsLimits(t *testing.T) {
	var det client.ConnectionDetail
	det.Limits = types.Limits{StatementTimeout: 5 * time.Second}
	if err := printJSON(det); err != nil {
		t.Fatalf("printJSON: %v", err)
	}
}
