//go:build keeper_short_token

package redact

// TokenHexLen is truncated to 4 hex characters — 16 bits — under the
// keeper_short_token build tag.
//
// R8.3c says what happens when two distinct values in one session hash to the
// same token: the first binding stands, the second value is emitted as redact,
// the statement completes at tier 1 and the collision is counted. That path
// cannot be reached by ordinary means against a 64-bit tag, so an end-to-end
// test of it needs a build where collisions are producible. 16 bits collide at
// roughly 300 distinct values.
//
// Unit tests inside this package do not need the tag: they set the Redactor's
// tag length directly. The tag exists for tests that run the daemon.
const TokenHexLen = 4

// ShortTokenBuild reports that this binary truncates token tags. doctor must
// show it: a build with short tokens is a test build and must never be mistaken
// for a release one.
const ShortTokenBuild = true
