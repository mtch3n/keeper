package pgaudit

import (
	"crypto/sha256"
	"encoding/hex"
)

// securityDefinerHash covers a SECURITY DEFINER function's full signature and
// source, so a redefinition (even one that keeps the same name and argument
// types) invalidates any acceptance bound to the old hash: SPEC R4.1g.
func securityDefinerHash(signature, source string) string {
	sum := sha256.Sum256([]byte(signature + "\n" + source))
	return hex.EncodeToString(sum[:])
}
