//go:build !keeper_short_token

package redact

// TokenHexLen is how many hex characters of the HMAC tag a token carries.
//
// 16, per R8.3a: 64 bits. At 48 bits the collision probability is 0.18% at 10⁶
// distinct values and 16.3% at 10⁷; 64 bits moves both by four orders of
// magnitude. The SPEC's inline examples abbreviate the hex; 16 is the
// requirement.
const TokenHexLen = 16

// ShortTokenBuild reports whether this binary was built with the
// keeper_short_token tag, which truncates the tag far enough to make R8.3c's
// collision path reachable. It is false here and must be surfaced by doctor
// wherever it is true: a build with short tokens is a test build.
const ShortTokenBuild = false
