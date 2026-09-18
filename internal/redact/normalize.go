package redact

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NamespaceRule is how one namespace normalizes a value before it is hashed.
//
// R8.3b puts NFC and trim on the floor for every namespace and makes case
// folding opt-in: the database may compare case-sensitively, and folding would
// merge records it treats as distinct. Where the database's own comparison
// semantics make folding correct — a citext column, a domain the application
// lowercases on write — the namespace declares it.
//
// The declaration is per namespace and never per column, for the same reason the
// namespace itself is: two columns sharing a namespace must normalize
// identically or they do not join, which is the one thing tokens exist to
// preserve. users.email and orders.user_email are one namespace, and one rule
// governs both.
type NamespaceRule struct {
	// CaseFold lowercases the value after NFC and trim.
	CaseFold bool `json:"case_fold,omitzero" yaml:"case_fold"`
}

// Normalize applies a namespace's rule to a value: NFC, then trim, then the
// declared case folding if any.
//
// The order matters. NFC first, because trimming before composition can leave a
// combining mark stranded; trim second, because a human typing a value through
// §8.7's input form is the least consistent source there is and a trailing space
// must not produce a second token for the same person.
func Normalize(value string, rule NamespaceRule) string {
	v := norm.NFC.String(value)
	v = strings.TrimFunc(v, unicode.IsSpace)
	if rule.CaseFold {
		v = strings.ToLower(v)
	}
	return v
}
