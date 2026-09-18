// Package rules is keeper's 100%-of-rows detection layer: the pattern and
// dictionary pass that a `scan` column runs over every row (SPEC R8.5d), the
// pass `set_session_intent` text is screened by (R10c), and the pass catalog
// init samples with (R5.3a).
//
// # On R8.5e, honestly
//
// SPEC R8.5e says keeper does not hand-author detection patterns or identifier
// validators, and names the maintained packages it prefers: Leakspok
// (Apache-2.0) for email, phone, card, IBAN, CPF/CNPJ, IP, URL, UUID and VIN;
// go-name-detector (Apache-2.0) for person names, 727k given and 983k family
// names across 105 countries; brdoc (Unlicense) for Brazilian documents.
//
// None of them is a dependency of this build. What is implemented here instead
// are the *published* algorithms — the Luhn check (ISO/IEC 7812-1), the IBAN
// MOD-97-10 check (ISO 13616 / ISO 7064) and the SSA's published rules for
// which SSN area, group and serial values were never issued. Those are
// specifications with many independent implementations, not one author's guess
// at what a value "looks like", which is the failure mode R8.5e is about.
//
// What the maintained packages would add, and this package does not have:
//
//   - National identifiers outside the United States. Leakspok and brdoc carry
//     CPF, CNPJ and other checksummed national documents; python-stdnum carries
//     roughly 150 of them. This package validates none.
//   - A 1.7M-name person dictionary. [SeedDictionary] holds a few hundred names
//     for the smoke path; [LoadDictionary] exists so a real list can be supplied,
//     and go-name-detector's is the intended one.
//   - VIN, and the long tail of shape-only identifiers.
//   - Many eyes. A pattern set maintained by one project is an enumeration with
//     one pair of eyes on it, and §11.1 is what that produces.
//
// [Coverage] publishes, in code, exactly which identifier types are validated
// and how. SPEC §8.5.1 is explicit about what that list means: **anything absent
// from it is undetected rather than absent.** An undetected national ID is an
// Invariant B failure keeper cannot see, and saying so is the only available
// mitigation.
//
// # Network posture
//
// This detector runs in process and opens no sockets, so [Detector.Identity]
// reports a network posture of "none" (R8.5g). A sidecar detector (R8.5f) is a
// different implementation of ports.Detector and reports its own posture.
package rules
