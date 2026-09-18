package rules

// CoverageEntry is one identifier type this package validates, and how.
type CoverageEntry struct {
	// Type is the label a matching span carries, and therefore the token
	// namespace a sampled column is proposed under (R5.3a).
	Type string `json:"type"`
	// Country is the jurisdiction the identifier belongs to, or "" when it has
	// none.
	Country string `json:"country,omitzero"`
	// Validation says what was actually checked. "checksum" means an arithmetic
	// check the value must pass; "format" means shape and range only, which is
	// a much weaker statement.
	Validation string `json:"validation"`
	// Source names the published specification the check comes from.
	Source string `json:"source,omitzero"`
	// Note records what the check does not establish.
	Note string `json:"note,omitzero"`
}

// UndetectedNotice is the sentence §8.5.1 requires keeper to publish alongside
// [Coverage]. doctor and the audit surface should show it verbatim.
const UndetectedNotice = "keeper validates only the identifier types listed in its coverage table. " +
	"An identifier type absent from that table is undetected rather than absent: " +
	"no claim is made that a value of that type is not present in the data."

// Coverage publishes, in code, exactly which identifier types keeper validates.
//
// SPEC §8.5.1 requires this list to exist and requires [UndetectedNotice] to
// travel with it. The list is short and the gaps are large — national
// identifiers outside the United States are absent entirely, and the one US
// identifier here has no checksum to verify — and saying so is the only
// mitigation available for an Invariant B failure keeper cannot see.
func Coverage() []CoverageEntry {
	return []CoverageEntry{
		{
			Type: TypeEmail, Validation: "format",
			Source: "RFC 5321 structural limits",
			Note:   "addressable syntax only; no mailbox is proven to exist",
		},
		{
			Type: TypeCard, Validation: "checksum",
			Source: "Luhn, ISO/IEC 7812-1 Annex B, plus published issuer identifier ranges and lengths",
			Note:   "Luhn alone passes ~10% of random digit runs; the issuer range is what makes the hit meaningful",
		},
		{
			Type: TypeIBAN, Validation: "checksum",
			Source: "MOD-97-10, ISO 13616 / ISO 7064, with the ISO 13616 country length registry",
			Note:   "a country absent from the length registry is validated on MOD-97 alone",
		},
		{
			Type: TypeUSSSN, Country: "US", Validation: "format",
			Source: "SSA published never-issued ranges: area 000/666/900-999, group 00, serial 0000",
			Note: "there is no SSN checksum — the SSA randomized issuance in 2011 — so this is a shape test. " +
				"Nine unseparated digits score low precisely because an order id looks identical",
		},
		{
			Type: TypePhone, Validation: "format",
			Source: "ITU-T E.164 length bounds; NANP area and exchange prefix rules",
			Note:   "no carrier or assignment check; libphonenumber's national number plans are not implemented",
		},
		{
			Type: TypeIP, Validation: "format",
			Source: "net/netip address parsing",
			Note:   "a dotted-quad version string is a valid address and is indistinguishable from one",
		},
		{
			Type: TypeURL, Validation: "format",
			Source: "net/url parsing over an allowlisted scheme set",
		},
		{
			Type: TypeUUID, Validation: "format",
			Source: "RFC 9562 textual representation",
		},
		{
			Type: TypePersonName, Validation: "dictionary",
			Source: "the loaded name dictionary; rules.SeedDictionary by default",
			Note: "recall is bounded by the loaded list. The embedded seed is a few hundred names; " +
				"SPEC §8.5.1 names go-name-detector's 1.7M-name list as the one that should be loaded",
		},
	}
}

// Uncovered names the identifier families keeper is known to miss. It is the
// explicit half of [UndetectedNotice]: the list is not exhaustive, and cannot
// be, which is the point.
func Uncovered() []string {
	return []string{
		"national identifiers outside the United States (CPF, CNPJ, NHS, NINO, codice fiscale, and roughly 150 others carried by python-stdnum)",
		"postal addresses and any other PII without a checksum or a dictionary — SPEC R8.5d's stated gap, which the §15 local model is for",
		"bank account and routing numbers other than IBAN",
		"passport, driving licence and tax identifiers",
		"vehicle identification numbers",
		"date of birth, which is indistinguishable from any other date in free text",
		"values transformed on the way out: base64, substrings, hashes (SPEC R8.4c states this bound)",
	}
}
