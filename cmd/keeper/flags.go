package main

import (
	"flag"
	"strings"
)

// parseFlags parses flags wherever they appear among the positional arguments,
// and leaves the positionals in fs.Args() as flag.Parse would.
//
// The standard package stops at the first non-flag, and every usage line in
// this CLI puts the name first. So `audit NAME --rerun` saw no flags and did
// nothing, `connection show NAME --json` printed text, and
// `connection set NAME --mode strict` changed nothing while reporting success.
// A literal "--" still ends flag parsing.
func parseFlags(fs *flag.FlagSet, args []string) error {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return err
		}
		rest := fs.Args()
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			positional = append(positional, rest...)
			break
		}
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	// Parsing a terminator followed by the positionals sets fs.Args() to them
	// and leaves every flag value already parsed as it is.
	return fs.Parse(append([]string{"--"}, positional...))
}

// stringList collects a repeated flag (e.g. --finding ID --finding ID) into
// a slice, in the order given.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
