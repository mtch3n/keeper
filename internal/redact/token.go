package redact

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// KeySource is the slice of ports.Vault this package needs. It is declared here
// rather than taken whole because redact must not be able to reach a DSN, a
// connection record or the master key: R8.3a says the token key never leaves the
// vault and is never sent to a model, and the narrowest interface that can do
// the job is the cheapest way to keep that true.
type KeySource interface {
	// TokenKey returns the HMAC key for a connection. version <= 0 asks for the
	// current key; a specific version asks for a retained older one. current is
	// the connection's current key version either way.
	TokenKey(ctx context.Context, connID string, version int) (key []byte, current int, err error)
}

// ErrNoKeySource is returned when a token policy needs a key and none is wired.
var ErrNoKeySource = errors.New("redact: no token key source configured")

// TokenOpen and TokenClose bracket a token. The brackets are deliberately not
// ASCII: a token must be recognisable in a transcript at a glance, and must not
// collide with ordinary punctuation in a value.
const (
	TokenOpen  = "⟨" // ⟨
	TokenClose = "⟩" // ⟩
)

// FormatToken renders a token: ⟨<namespace><keyVersion>:<tag>⟩, for example
// ⟨email1:a3f21b4c9d8e7f60⟩.
//
// The key version travels in the token because old keys are retained read-only
// (§8.3): a token minted before a rotation must stay readable, and nothing but
// the token itself says which key made it.
func FormatToken(namespace string, version int, tag string) string {
	return TokenOpen + namespace + strconv.Itoa(version) + ":" + tag + TokenClose
}

// ParseToken splits a rendered token. It is the inverse of [FormatToken] and is
// deliberately strict: anything that is not exactly a token is not one.
func ParseToken(s string) (namespace string, version int, tag string, ok bool) {
	if !strings.HasPrefix(s, TokenOpen) || !strings.HasSuffix(s, TokenClose) {
		return "", 0, "", false
	}
	body := s[len(TokenOpen) : len(s)-len(TokenClose)]
	colon := strings.IndexByte(body, ':')
	if colon <= 0 || colon == len(body)-1 {
		return "", 0, "", false
	}
	head, tag := body[:colon], body[colon+1:]
	i := len(head)
	for i > 0 && head[i-1] >= '0' && head[i-1] <= '9' {
		i--
	}
	if i == 0 || i == len(head) {
		return "", 0, "", false
	}
	v, err := strconv.Atoi(head[i:])
	if err != nil {
		return "", 0, "", false
	}
	for j := range len(tag) {
		if !isHexDigit(tag[j]) {
			return "", 0, "", false
		}
	}
	return head[:i], v, tag, true
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// hmacTag computes the token tag: HMAC-SHA256(key, namespace ‖ normalized value),
// truncated to hexLen hex characters.
//
// The namespace is part of the message and not part of the key, which is what
// makes it the classification label R8.3 requires: users.email and
// orders.user_email both hash under "email" and still join. Salting with the
// column name would give them different tokens and break exactly the thing
// tokens exist to preserve.
func hmacTag(key []byte, namespace, normalized string, hexLen int) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(namespace))
	m.Write([]byte(normalized))
	sum := m.Sum(nil)
	full := hex.EncodeToString(sum)
	if hexLen <= 0 || hexLen > len(full) {
		hexLen = len(full)
	}
	return full[:hexLen]
}

// validNamespace rejects a namespace that would make a token unparseable.
// Trailing digits would be read as the key version, and the bracket characters
// and the colon are structural.
func validNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("redact: token requires a namespace (R5.2b)")
	}
	if c := ns[len(ns)-1]; c >= '0' && c <= '9' {
		return fmt.Errorf("redact: namespace %q must not end in a digit: the key version does", ns)
	}
	if strings.ContainsAny(ns, ":"+TokenOpen+TokenClose) {
		return fmt.Errorf("redact: namespace %q contains a token delimiter", ns)
	}
	return nil
}
