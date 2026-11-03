// Package build holds the one version string every keeper binary reports.
//
// It is one constant rather than one per binary because the daemon and its
// clients compare versions at the handshake (SPEC §3.4) and refuse to talk
// across a mismatch. Two declarations drift, and the first symptom is a matched
// pair of binaries from a single build refusing each other — which is exactly
// what happened the first time this was built.
package build

import (
	"cmp"
	"strconv"
	"strings"
)

// Version is the one place this number lives. `make build` sets it from
// `git describe --tags --dirty` and the release workflow from the tag, both
// through the linker. A plain `go build` reports "dev", which is older than
// every release.
//
//	go build -ldflags "-X github.com/mtchen/keeper/internal/build.Version=1.2.3"
var Version = "dev"

// Compare orders two versions: -1 when a is older than b, 1 when newer, 0 when
// equal. A version is a tag such as 0.1.0, optionally with a leading v and a
// suffix as `git describe` writes it (0.1.0-3-gabc1234-dirty), which sorts after
// the bare tag. A version that does not start with digits, such as "dev", is
// older than every release.
func Compare(a, b string) int {
	ac, aSuffix, aOK := parse(a)
	bc, bSuffix, bOK := parse(b)
	switch {
	case !aOK && !bOK:
		return 0
	case !aOK:
		return -1
	case !bOK:
		return 1
	}
	for i := range ac {
		if c := cmp.Compare(ac[i], bc[i]); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(commitsAhead(aSuffix), commitsAhead(bSuffix)); c != 0 {
		return c
	}
	return cmp.Compare(aSuffix, bSuffix)
}

func commitsAhead(suffix string) int {
	n, _, _ := strings.Cut(suffix, "-")
	c, _ := strconv.Atoi(n)
	return c
}

func parse(v string) (core [3]int, suffix string, ok bool) {
	v = strings.TrimPrefix(v, "v")
	numbers, suffix, _ := strings.Cut(v, "-")
	parts := strings.Split(numbers, ".")
	if len(parts) > 3 {
		return core, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return core, "", false
		}
		core[i] = n
	}
	return core, suffix, true
}
