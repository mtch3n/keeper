// Package types holds every value shared across keeper's packages. Nothing here
// performs I/O, opens a connection, or depends on another internal package: it is
// the contract the daemon, the pipeline, the catalog and the clients compile against.
package types

import "slices"

// Policy is what keeper does to a column's values on the way out. SPEC §8.2.
type Policy string

const (
	PolicyAllow   Policy = "allow"   // everything survives
	PolicyScan    Policy = "scan"    // decided per result set; matched spans redacted
	PolicyPartial Policy = "partial" // one declared component survives, see PartialForm
	PolicyToken   Policy = "token"   // HMAC token; equality survives, the value does not
	PolicyRedact  Policy = "redact"  // present, nothing survives
	PolicyDrop    Policy = "drop"    // column absent from the response
)

// strictness orders policies for inheritance. SPEC R7.6b:
//
//	allow < scan < partial < token < redact
//
// drop is deliberately absent: it is never inherited, because dropping a computed
// column deletes it from the response rather than masking it.
var strictness = map[Policy]int{
	PolicyAllow:   0,
	PolicyScan:    1,
	PolicyPartial: 2,
	PolicyToken:   3,
	PolicyRedact:  4,
}

// Rank reports a policy's position in the inheritance ordering. Drop reports -1,
// which keeps it out of every comparison rather than sorting it anywhere.
func (p Policy) Rank() int {
	if r, ok := strictness[p]; ok {
		return r
	}
	return -1
}

// Valid reports whether p is a policy keeper knows.
func (p Policy) Valid() bool {
	return p == PolicyDrop || p.Rank() >= 0
}

// Masks reports whether p removes anything from the value.
func (p Policy) Masks() bool { return p != PolicyAllow }

// Strictest returns the strictest of the given policies under the inheritance
// ordering, ignoring drop and allow. It returns PolicyAllow when nothing qualifies.
func Strictest(policies ...Policy) Policy {
	best := PolicyAllow
	for _, p := range policies {
		if p == PolicyDrop || p == PolicyAllow {
			continue
		}
		if p.Rank() > best.Rank() {
			best = p
		}
	}
	return best
}

// Inherited converts a policy chosen by Strictest into the policy a computed
// column actually receives. SPEC R7.6b: token and partial both resolve to redact,
// because a computed value has no namespace to tokenize under and no declared
// component to keep. Inheritance is capped at redact and never produces drop.
func Inherited(p Policy) Policy {
	switch p {
	case PolicyToken, PolicyPartial, PolicyRedact:
		return PolicyRedact
	case PolicyScan:
		return PolicyScan
	default:
		return PolicyAllow
	}
}

// PartialForm names the component a partial policy keeps. The set is closed:
// SPEC R5.2d rejects a per-column format string, because that is where someone
// writes "keep the first nine characters" against an SSN.
type PartialForm string

const (
	FormEmailDomain      PartialForm = "email_domain"       // ████@acme.example
	FormCardBINLast4     PartialForm = "card_bin_last4"     // 453211██████3333
	FormPhoneCountryArea PartialForm = "phone_country_area" // +1-978-████
	FormIPNetwork        PartialForm = "ip_network"         // 192.168.2.███
)

// PartialForms is every form keeper implements, in declaration order.
var PartialForms = []PartialForm{FormEmailDomain, FormCardBINLast4, FormPhoneCountryArea, FormIPNetwork}

// Valid reports whether f is a form keeper implements.
func (f PartialForm) Valid() bool { return slices.Contains(PartialForms, f) }

// ColumnPolicy is one catalog entry: what happens to a column, and the arguments
// its policy needs. SPEC §5.2.
type ColumnPolicy struct {
	Policy Policy `json:"policy"`

	// Namespace is mandatory for token and is the label two columns must share to
	// join. SPEC R5.2b makes its absence a validation error, never a default.
	Namespace string `json:"namespace,omitzero"`

	// Form is mandatory for partial. SPEC R5.2d.
	Form PartialForm `json:"form,omitzero"`

	// HideName marks a column whose name is disclosive independently of its value,
	// and suppresses it on every response surface. SPEC R6.4b.
	HideName bool `json:"hide_name,omitzero"`

	// Paths classifies the key paths of a json or jsonb column, keyed by JSONPath
	// ("$.customer.email"). SPEC §5.7.
	Paths map[string]ColumnPolicy `json:"paths,omitzero"`
}

// Validate reports why a catalog entry cannot be used, or nil.
func (c ColumnPolicy) Validate() error {
	switch {
	case !c.Policy.Valid():
		return &ValidationError{Field: "policy", Reason: "unknown policy " + string(c.Policy)}
	case c.Policy == PolicyToken && c.Namespace == "":
		return &ValidationError{Field: "namespace", Reason: "token requires a namespace (R5.2b)"}
	case c.Policy == PolicyPartial && !c.Form.Valid():
		return &ValidationError{Field: "form", Reason: "partial requires a declared form (R5.2d)"}
	case c.Policy != PolicyToken && c.Namespace != "":
		return &ValidationError{Field: "namespace", Reason: "namespace applies to token only"}
	case c.Policy != PolicyPartial && c.Form != "":
		return &ValidationError{Field: "form", Reason: "form applies to partial only"}
	}
	for path, sub := range c.Paths {
		if err := sub.Validate(); err != nil {
			return &ValidationError{Field: path, Reason: err.Error()}
		}
	}
	return nil
}

// ValidationError is a catalog or configuration value keeper will not accept.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }
