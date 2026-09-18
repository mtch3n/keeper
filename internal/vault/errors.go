package vault

import "fmt"

// NotFoundError reports that no connection matches the given id. It is a plain
// domain error, not a *types.Error: CONTRACT §2 converts to the agent-facing
// shape only at the API boundary, and vault is not that boundary.
type NotFoundError struct {
	ID string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("vault: connection %q not found", e.ID)
}

// ConflictError reports that a request could not be honoured because it named
// something the vault does not recognise, or something that has changed since
// it was last seen. Accept returns this for an unknown finding id or one whose
// Hash no longer matches the audit (SPEC R4.1, R4.1g). The API boundary is
// expected to map it to HTTP 409 via errors.AsType[*vault.ConflictError].
type ConflictError struct {
	Subject string
	Reason  string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("vault: conflict on %q: %s", e.Subject, e.Reason)
}
