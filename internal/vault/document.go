package vault

import (
	"slices"

	"github.com/mtchen/keeper/internal/types"
)

// document is the single JSON value stored, age-encrypted, in vault.age. It
// holds every connection this daemon knows about: the connection record
// itself, its read and write DSNs, and its per-connection HMAC token keys.
type document struct {
	Connections []connectionRecord `json:"connections,omitzero"`
}

// connectionRecord is one connection's full state at rest. types.Connection
// never carries a DSN or a token key, so both live here beside it.
type connectionRecord struct {
	Conn types.Connection `json:"conn"`
	// ReadDSN is always set once a connection is registered.
	ReadDSN string `json:"read_dsn,omitzero"`
	// WriteDSN is empty when the connection holds no write credential. There is
	// no boolean that enables writes: SPEC §4.2. types.Connection.HasWriteCredential
	// is derived from this field's presence, never stored independently.
	WriteDSN string `json:"write_dsn,omitzero"`
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

// findFinding returns the finding with the given id from findings, or false.
func findFinding(findings []types.Finding, id string) (types.Finding, bool) {
	for _, f := range findings {
		if f.ID == id {
			return f, true
		}
	}
	return types.Finding{}, false
}

// upsertAcceptance replaces the acceptance for a.FindingID if one exists, or
// appends a new one.
func upsertAcceptance(list []types.Acceptance, a types.Acceptance) []types.Acceptance {
	for i := range list {
		if list[i].FindingID == a.FindingID {
			list[i] = a
			return list
		}
	}
	return append(list, a)
}
