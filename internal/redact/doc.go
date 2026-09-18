// Package redact is G7: everything keeper emits passes through it.
//
// It owns four things that SPEC §8 keeps deliberately separate.
//
// # Policies (§8.2)
//
// allow passes; token replaces the value with a keyed HMAC so equality — joins,
// GROUP BY, COUNT DISTINCT — survives and the value does not; partial keeps
// exactly one declared component and nothing else; redact keeps the cell and
// nothing in it; drop removes the column; scan runs the rules pass and redacts
// the matched spans only, never the whole column (R8.5a).
//
// # Tokens (§8.3)
//
//	token = HMAC-SHA256(key, namespace ‖ normalize(value))   → first 16 hex
//
// The key comes from the vault, is per connection, never leaves it, and is never
// sent to any model (R8.3a). Unkeyed hashing is broken for low-entropy PII:
// US phone numbers and SSNs are exhaustively precomputable. 16 hex is 64 bits,
// where collision probability at 10⁶ distinct values is four orders of magnitude
// below the 48-bit figure of 0.18%.
//
// # The reverse map (§8.4)
//
// Per session, in memory, TTL'd, never persisted (CONTRACT §4 rule 9). A
// persistent map is a growing local PII store and a target.
//
// # Emission scanning — the bypass this package exists to close (R8.4a)
//
// A reverse map without an emission scan is a de-tokenization oracle:
//
//	SELECT $1::text FROM clean_ids LIMIT 1;   -- params: [{token: "⟨e1:…⟩"}]
//
// keeper resolves the token to the real value and binds it. The output column is
// computed and clean_ids is entirely allow, so nothing in the catalog objects and
// the value returns in cleartext. Binding prevents injection; it does not
// preserve confidentiality. Every string leaving keeper is therefore checked
// against the session map's resolved values and re-tokenized on a match, and the
// match is substring and case-insensitive (R8.4c) because exact matching fails on
// every ordinary derivation: 'Hi ' || $1, format('Hi %s',$1), upper($1),
// to_json($1), ARRAY[$1], ROW($1).
//
// # What this package does not catch
//
// encode(convert_to($1,'UTF8'),'base64') and substr($1,1,4) survive the scan, and
// so do the numeric-typed probes of §11.5 — length($1), position($1 in …),
// CASE … THEN 1 ELSE 0. R8.4b's type-family policy floor bounds the first class
// and does not eliminate it. These are stated limitations, not a basis for
// promising complete confidentiality (§8.1).
package redact
