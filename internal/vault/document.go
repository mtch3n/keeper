package vault

import (
	"slices"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// document is the single JSON value stored, age-encrypted, in vault.age. It
// holds every host and connection this daemon knows about: each connection's
// record, its one credential, and its per-connection HMAC token
// keys.
type document struct {
	// Settings is nil until an operator first saves one; reads fall back to
	// types.DefaultSettings.
	Settings    *types.Settings    `json:"settings,omitzero"`
	Hosts       []types.Host       `json:"hosts,omitzero"`
	Connections []connectionRecord `json:"connections,omitzero"`
}

// credential is one role's login at rest.
type credential struct {
	User     string `json:"user"`
	Password string `json:"password,omitzero"`
}

// connectionRecord is one connection's full state at rest. types.Connection
// never carries a credential or a token key, so both live here beside it.
type connectionRecord struct {
	Conn types.Connection `json:"conn"`
	// Credential is the profile's one login; its writes setting is on Conn.
	Credential credential `json:"credential"`
	// Deny, Allow and Patterns are the list stage's terms and expressions. They are as sensitive as the
	// data they describe and never leave the vault except through Export.
	Deny     []string        `json:"deny,omitzero"`
	Allow    []string        `json:"allow,omitzero"`
	Patterns []ports.Pattern `json:"patterns,omitzero"`
	// TokenKeys holds every HMAC key ever minted for this connection, oldest
	// first. Old keys are never deleted so historical tokens stay interpretable;
	// the highest Version is current.
	TokenKeys []tokenKeyEntry `json:"token_keys,omitzero"`
}

// tokenKeyEntry is one version of a connection's HMAC token key. Keys are
// per-connection, never global, so a token cannot link identities across
// unrelated databases: SPEC R8.3a.
type tokenKeyEntry struct {
	Version int    `json:"version"`
	Key     []byte `json:"key"`
}

// find returns the connection record with the given id, or false.
func (d *document) find(id string) (*connectionRecord, bool) {
	for i := range d.Connections {
		if d.Connections[i].Conn.ID == id {
			return &d.Connections[i], true
		}
	}
	return nil, false
}

// host returns the host with the given id, or false.
func (d *document) host(id string) (*types.Host, bool) {
	for i := range d.Hosts {
		if d.Hosts[i].ID == id {
			return &d.Hosts[i], true
		}
	}
	return nil, false
}

// remove deletes the connection record with the given id, reporting whether
// one was found.
func (d *document) remove(id string) bool {
	for i := range d.Connections {
		if d.Connections[i].Conn.ID == id {
			d.Connections = slices.Delete(d.Connections, i, i+1)
			return true
		}
	}
	return false
}

// currentTokenKey returns the highest-versioned key on rec, or false when none
// has ever been minted.
func (rec *connectionRecord) currentTokenKey() (tokenKeyEntry, bool) {
	if len(rec.TokenKeys) == 0 {
		return tokenKeyEntry{}, false
	}
	best := rec.TokenKeys[0]
	for _, k := range rec.TokenKeys[1:] {
		if k.Version > best.Version {
			best = k
		}
	}
	return best, true
}

// tokenKeyVersion returns the key stored under the given version, or false.
func (rec *connectionRecord) tokenKeyVersion(version int) (tokenKeyEntry, bool) {
	for _, k := range rec.TokenKeys {
		if k.Version == version {
			return k, true
		}
	}
	return tokenKeyEntry{}, false
}
