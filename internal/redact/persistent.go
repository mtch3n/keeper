package redact

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/tink-crypto/tink-go/v2/daead/subtle"
)

// A persistent token is ⟨<namespace><keyVersion>=<sealed>⟩: the value's
// canonical form, padded and sealed with AES-SIV under a key derived from the
// connection's token key and the namespace. Equal values seal to equal tokens,
// and any machine holding the vault opens one without a stored map. The
// namespace is the associated data, so a token cannot be replayed under
// another namespace.
const persistentSep = "="

var sealEncoding = base64.RawURLEncoding

// sessionKey is the HMAC key for one session's tokens: the same value gets a
// different token in another session, so short-lived work links nothing.
func sessionKey(connKey []byte, sessionID string) []byte {
	m := hmac.New(sha256.New, connKey)
	m.Write([]byte("keeper session token v1\x00"))
	m.Write([]byte(sessionID))
	return m.Sum(nil)
}

func sealer(connKey []byte, namespace string) (*subtle.AESSIV, error) {
	key, err := hkdf.Key(sha256.New, connKey, nil, "keeper persistent token v1\x00"+namespace, subtle.AESSIVKeySize)
	if err != nil {
		return nil, err
	}
	return subtle.NewAESSIV(key)
}

// seal is the persistent token for a canonical value.
func seal(connKey []byte, namespace string, version int, canonical string) (string, error) {
	s, err := sealer(connKey, namespace)
	if err != nil {
		return "", err
	}
	ct, err := s.EncryptDeterministically(pad([]byte(canonical)), []byte(namespace))
	if err != nil {
		return "", err
	}
	return TokenOpen + namespace + strconv.Itoa(version) + persistentSep + sealEncoding.EncodeToString(ct) + TokenClose, nil
}

// parsePersistent splits a persistent token. Anything else is not one.
func parsePersistent(s string) (namespace string, version int, sealed []byte, ok bool) {
	if !strings.HasPrefix(s, TokenOpen) || !strings.HasSuffix(s, TokenClose) {
		return "", 0, nil, false
	}
	head, body, found := strings.Cut(s[len(TokenOpen):len(s)-len(TokenClose)], persistentSep)
	if !found || body == "" {
		return "", 0, nil, false
	}
	i := len(head)
	for i > 0 && head[i-1] >= '0' && head[i-1] <= '9' {
		i--
	}
	if i == 0 || i == len(head) {
		return "", 0, nil, false
	}
	v, err := strconv.Atoi(head[i:])
	if err != nil {
		return "", 0, nil, false
	}
	ct, err := sealEncoding.DecodeString(body)
	if err != nil {
		return "", 0, nil, false
	}
	return head[:i], v, ct, true
}

// open returns the canonical value a persistent token seals, or false when it
// was not sealed under this key and namespace: AES-SIV authenticates before it
// releases anything.
func open(connKey []byte, namespace string, sealed []byte) (string, bool) {
	s, err := sealer(connKey, namespace)
	if err != nil {
		return "", false
	}
	pt, err := s.DecryptDeterministically(sealed, []byte(namespace))
	if err != nil {
		return "", false
	}
	v, ok := unpad(pt)
	return string(v), ok
}

// pad brings a value to its length bucket — 16 bytes, then powers of two — with
// a 0x80 marker and zeros, so a token's length says little about its value.
func pad(b []byte) []byte {
	size := 16
	for size < len(b)+1 {
		size *= 2
	}
	out := make([]byte, size)
	copy(out, b)
	out[len(b)] = 0x80
	return out
}

func unpad(b []byte) ([]byte, bool) {
	i := bytes.LastIndexByte(b, 0x80)
	if i < 0 || bytes.ContainsFunc(b[i+1:], func(r rune) bool { return r != 0 }) {
		return nil, false
	}
	return b[:i], true
}
