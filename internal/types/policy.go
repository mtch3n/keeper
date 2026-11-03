// Package types holds every value shared across keeper's packages. Nothing here
// performs I/O, opens a connection, or depends on another internal package: it is
// the contract the daemon, the pipeline, the catalog and the clients compile against.
package types

// Policy is what keeper does to a column's values on the way out.
type Policy string

const (
	PolicyAllow  Policy = "allow"  // everything survives
	PolicyScan   Policy = "scan"   // decided per result set; matched spans redacted
	PolicyToken  Policy = "token"  // HMAC token; equality survives, the value does not
	PolicyRedact Policy = "redact" // present, nothing survives
)

// strictness orders policies for inheritance: allow < scan < token < redact.
var strictness = map[Policy]int{
	PolicyAllow:  0,
	PolicyScan:   1,
	PolicyToken:  2,
	PolicyRedact: 3,
}

// Rank reports a policy's position in the inheritance ordering. A policy keeper
// does not know reports -1, which keeps it out of every comparison.
func (p Policy) Rank() int {
	if r, ok := strictness[p]; ok {
		return r
	}
	return -1
}

// Valid reports whether p is a policy keeper knows.
func (p Policy) Valid() bool { return p.Rank() >= 0 }

// Masks reports whether p removes anything from the value.
func (p Policy) Masks() bool { return p != PolicyAllow }

// Strictest returns the strictest of the given policies under the inheritance
// ordering, ignoring allow. It returns PolicyAllow when nothing qualifies.
func Strictest(policies ...Policy) Policy {
	best := PolicyAllow
	for _, p := range policies {
		if p.Rank() > best.Rank() {
			best = p
		}
	}
	return best
}

// Inherited converts a policy chosen by Strictest into the policy a computed
// column actually receives: a computed value has no namespace to tokenize
// under, so token resolves to redact.
func Inherited(p Policy) Policy {
	switch p {
	case PolicyToken, PolicyRedact:
		return PolicyRedact
	case PolicyScan:
		return PolicyScan
	default:
		return PolicyAllow
	}
}

// ColumnPolicy is one catalog entry: what happens to a column, and the arguments
// its policy needs.
type ColumnPolicy struct {
	Policy Policy `json:"policy"`

	// Namespace is mandatory for token and is the label two columns must share to
	// join. Its absence is a validation error, never a default.
	Namespace string `json:"namespace,omitzero"`

	// Paths classifies the key paths of a json or jsonb column, keyed by JSONPath
	// ("$.customer.email").
	Paths map[string]ColumnPolicy `json:"paths,omitzero"`
}

// Validate reports why a catalog entry cannot be used, or nil.
func (c ColumnPolicy) Validate() error {
	switch {
	case !c.Policy.Valid():
		return &ValidationError{Field: "policy", Reason: "unknown policy " + string(c.Policy)}
	case c.Policy == PolicyToken && c.Namespace == "":
		return &ValidationError{Field: "namespace", Reason: "token requires a namespace"}
	case c.Policy != PolicyToken && c.Namespace != "":
		return &ValidationError{Field: "namespace", Reason: "namespace applies to token only"}
	}
	for path, sub := range c.Paths {
		if err := sub.Validate(); err != nil {
			return &ValidationError{Field: path, Reason: err.Error()}
		}
	}
	return nil
}

// Known returns the entry as keeper acts on it: a policy keeper does not know,
// such as one an earlier version stored, behaves as redact, in its paths too.
func (c ColumnPolicy) Known() ColumnPolicy {
	if !c.Policy.Valid() {
		c.Policy, c.Namespace = PolicyRedact, ""
	}
	if len(c.Paths) > 0 {
		paths := make(map[string]ColumnPolicy, len(c.Paths))
		for path, sub := range c.Paths {
			paths[path] = sub.Known()
		}
		c.Paths = paths
	}
	return c
}

// ValidationError is a catalog or configuration value keeper will not accept.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }
