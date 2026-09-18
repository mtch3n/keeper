// Package build holds the one version string every keeper binary reports.
//
// It is one constant rather than one per binary because the daemon and its
// clients compare versions at the handshake (SPEC §3.4) and refuse to talk
// across a mismatch. Two declarations drift, and the first symptom is a matched
// pair of binaries from a single build refusing each other — which is exactly
// what happened the first time this was built.
package build

// Version is the one place this number lives. A release overrides it from the
// tag at link time; a local build reports it as written, so a binary built from
// a checkout of a tag and the binary published for that tag agree, and the
// version handshake does not fire on a difference that is not real.
//
//	go build -ldflags "-X github.com/mtchen/keeper/internal/build.Version=1.2.3"
var Version = "0.0.2"
