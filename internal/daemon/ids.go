package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"uuid"
)

// capability returns a fresh capability string with at least 128 bits of CSPRNG
// entropy. Ticket ids (SPEC R3.4b), local request ids (R8.7c) and the UI's CSRF
// token are all guessing targets, so none of them is a uuid: a v7 uuid encodes
// its own creation time and spends 48 of its bits saying so.
func capability() string { return rand.Text() }

// timeID returns an identifier that sorts by creation time, for the values a
// human reads in a list: sessions, grants, audit ids.
func timeID() string { return uuid.NewV7().String() }

// sameCapability compares two capability strings without leaking their
// divergence point through timing.
func sameCapability(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
