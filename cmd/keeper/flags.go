package main

import "strings"

// stringList collects a repeated flag (e.g. --finding ID --finding ID) into
// a slice, in the order given.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
