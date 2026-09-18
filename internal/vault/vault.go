// Package vault implements ports.Vault: the age-encrypted store of connection
// records, credentials and per-connection HMAC token keys, and the key-source
// chain that unlocks it. It is the only package that touches the master key,
// the age file or the OS keychain, and the only place a DSN exists at rest.
package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"encoding/json/jsontext"
	"encoding/json/v2"

	"uuid"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

const vaultFileName = "vault.age"

// tokenKeySize is the width of an HMAC-SHA256 key: SPEC R8.3a.
const tokenKeySize = 32

// Vault is the concrete ports.Vault. It holds no package-level state: every
// instance owns its own directory, key and decrypted document.
type Vault struct {
	dir string

	mu     sync.Mutex
	locked bool
	key    []byte
	source string
	doc    *document
}

var _ ports.Vault = (*Vault)(nil)

// New returns a Vault rooted at dir. It performs no I/O and starts locked:
// SPEC §3.4. Callers normally pass DefaultDir(), overridden in tests.
func New(dir string) *Vault {
	return &Vault{dir: dir, locked: true}
}

// DefaultDir returns ~/.config/keeper, honouring XDG_CONFIG_HOME.
func DefaultDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "keeper"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("vault: resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "keeper"), nil
}

func (v *Vault) vaultPath() string { return filepath.Join(v.dir, vaultFileName) }

// Unlock resolves the master key through the source chain and decrypts
// vault.age, or creates an empty one on a first-ever run. SPEC §4.3.
func (v *Vault) Unlock(ctx context.Context, passphrase string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.locked {
		return nil
	}

	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return fmt.Errorf("vault: create config directory: %w", err)
	}

	_, statErr := os.Stat(v.vaultPath())
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("vault: stat %s: %w", v.vaultPath(), statErr)
	}

	key, source, err := resolveMasterKey(v.dir, passphrase)
	switch {
	case err == nil:
		// resolved; fall through
	case errors.Is(err, errNoKeySource) && !exists:
		key, source, err = bootstrap(v.dir, passphrase)
		if err != nil {
			return err
		}
	case errors.Is(err, errNoKeySource):
		return errors.New("vault: no key source available; set KEEPER_MASTER_KEY, provide key.age, or supply a passphrase")
	default:
		return err
	}

	var doc *document
	if exists {
		ciphertext, err := os.ReadFile(v.vaultPath())
		if err != nil {
			return fmt.Errorf("vault: read %s: %w", vaultFileName, err)
		}
		doc, err = decryptDocument(key, ciphertext)
		if err != nil {
			return fmt.Errorf("vault: decrypt %s (wrong master key?): %w", vaultFileName, err)
		}
	} else {
		doc = &document{}
		if err := writeDocument(v.vaultPath(), key, doc); err != nil {
			return err
		}
	}

	v.key = key
	v.source = source
	v.doc = doc
	v.locked = false
	return nil
}

// Lock forgets the master key and the decrypted document.
//
// It is the counterpart to Unlock and not a shutdown: the daemon keeps running,
// the UI keeps serving, the approval queue survives, and the next thing that
// needs a credential returns CodeVaultLocked with something to do about it. What
// goes is the only part worth bounding — the decrypted DSNs and token keys
// sitting in this process's memory.
func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return
	}
	// Overwrite rather than drop the reference: a freed []byte is still the key
	// until the allocator reuses the page.
	for i := range v.key {
		v.key[i] = 0
	}
	v.key = nil
	v.doc = nil
	v.source = ""
	v.locked = true
}

// Locked reports whether the vault still needs Unlock. SPEC §3.4.
func (v *Vault) Locked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.locked
}

// KeySource names the key source in use, for doctor. SPEC R4.3.
func (v *Vault) KeySource() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.source
}

func writeDocument(path string, key []byte, doc *document) error {
	ciphertext, err := encryptDocument(key, doc)
	if err != nil {
		return fmt.Errorf("vault: encrypt %s: %w", vaultFileName, err)
	}
	if err := atomicWrite(path, ciphertext, 0o600); err != nil {
		return fmt.Errorf("vault: write %s: %w", vaultFileName, err)
	}
	return nil
}

// persist writes v.doc under v.key. Callers must hold v.mu.
func (v *Vault) persist() error {
	return writeDocument(v.vaultPath(), v.key, v.doc)
}

func lockedError() *types.Error {
	return &types.Error{
		Code:    types.CodeVaultLocked,
		Summary: "the vault is locked",
		Action:  "unlock the vault",
	}
}

// Connections lists every registered connection. SPEC §4.3.
func (v *Vault) Connections(ctx context.Context) ([]*types.Connection, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return nil, lockedError()
	}
	out := make([]*types.Connection, 0, len(v.doc.Connections))
	for _, rec := range v.doc.Connections {
		c := rec.Conn
		out = append(out, &c)
	}
	return out, nil
}

// Connection returns one connection by id.
func (v *Vault) Connection(ctx context.Context, id string) (*types.Connection, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return nil, lockedError()
	}
	rec, ok := v.doc.find(id)
	if !ok {
		return nil, &NotFoundError{ID: id}
	}
	c := rec.Conn
	return &c, nil
}

// Register stores a connection and its credentials. The connection is forced
// disabled with no acceptances regardless of what the caller passed: only
// Accept can change that. A fresh per-connection HMAC token key (version 1)
// is minted here. SPEC R4.1, R8.3a.
func (v *Vault) Register(ctx context.Context, c *types.Connection, readDSN, writeDSN string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return lockedError()
	}
	if c.ID == "" {
		c.ID = uuid.NewV7().String()
	}
	if _, exists := v.doc.find(c.ID); exists {
		return &ConflictError{Subject: c.ID, Reason: "a connection with this id is already registered"}
	}

	c.Enabled = false
	c.Acceptances = nil
	c.HasWriteCredential = writeDSN != ""

	key := make([]byte, tokenKeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("vault: generate token key: %w", err)
	}

	rec := connectionRecord{
		Conn:      *c,
		ReadDSN:   readDSN,
		WriteDSN:  writeDSN,
		TokenKeys: []tokenKeyEntry{{Version: 1, Key: key}},
	}
	v.doc.Connections = append(v.doc.Connections, rec)
	if err := v.persist(); err != nil {
		v.doc.Connections = v.doc.Connections[:len(v.doc.Connections)-1]
		return err
	}
	return nil
}

// Update replaces the stored connection's editable state: mode, limits,
// denylist, write scope, findings and acceptances. Enabled is always
// recomputed from Findings and Acceptances, never trusted from the caller, so
// a re-audit that adds an unaccepted finding disables the connection whether
// or not the caller remembered to set Enabled: SPEC R4.1e.
func (v *Vault) Update(ctx context.Context, c *types.Connection) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return lockedError()
	}
	rec, ok := v.doc.find(c.ID)
	if !ok {
		return &NotFoundError{ID: c.ID}
	}
	updated := *c
	updated.Enabled = len(updated.Unaccepted()) == 0
	rec.Conn = updated
	return v.persist()
}

// Remove deletes a connection and everything stored beside it: its
// credentials and its token keys.
func (v *Vault) Remove(ctx context.Context, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return lockedError()
	}
	if !v.doc.remove(id) {
		return &NotFoundError{ID: id}
	}
	return v.persist()
}

// DSN returns the credential for a role. There is no boolean that enables
// writes: an absent write credential is reported as CodeNoWriteCredential,
// the code the rest of the system already expects for this case. SPEC §4.2.
func (v *Vault) DSN(ctx context.Context, id string, role ports.Role) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return "", lockedError()
	}
	rec, ok := v.doc.find(id)
	if !ok {
		return "", &NotFoundError{ID: id}
	}
	switch role {
	case ports.RoleRead:
		if rec.ReadDSN == "" {
			return "", fmt.Errorf("vault: connection %s has no read credential", id)
		}
		return rec.ReadDSN, nil
	case ports.RoleWrite:
		if rec.WriteDSN == "" {
			return "", &types.Error{
				Code:    types.CodeNoWriteCredential,
				Summary: "connection " + id + " has no write credential",
			}
		}
		return rec.WriteDSN, nil
	default:
		return "", fmt.Errorf("vault: unknown role %q", role)
	}
}

// Accept records a human agreeing to named findings: SPEC R4.1. Each entry of
// findingIDs is either a bare finding id, or "<id>@<hash>" to bind the
// acceptance to the exact hash the operator was shown. A bare id always
// succeeds against whatever the connection currently reports for that id
// (the remediation path for a stale acceptance: "disabled until
// re-accepted", SPEC R4.1g); a qualified id fails with a ConflictError if the
// finding's current Hash has moved on since the operator looked at it. An
// unknown id always fails with a ConflictError. via must be "cli" or "ui" and
// is never trusted to be "mcp": SPEC R4.1f, §6.3. The whole batch is
// validated before anything is written, so a partial failure changes
// nothing.
func (v *Vault) Accept(ctx context.Context, id string, findingIDs []string, actor, via string) (*types.Connection, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return nil, lockedError()
	}
	if via != "cli" && via != "ui" {
		return nil, fmt.Errorf("vault: accept via must be \"cli\" or \"ui\", got %q", via)
	}
	rec, ok := v.doc.find(id)
	if !ok {
		return nil, &NotFoundError{ID: id}
	}

	type resolved struct {
		finding types.Finding
	}
	batch := make([]resolved, 0, len(findingIDs))
	for _, raw := range findingIDs {
		fid, wantHash, hasHash := cutFindingID(raw)
		f, ok := findFinding(rec.Conn.Findings, fid)
		if !ok {
			return nil, &ConflictError{Subject: fid, Reason: "unknown to this connection"}
		}
		if hasHash && wantHash != f.Hash {
			return nil, &ConflictError{Subject: fid, Reason: "hash differs from what was audited"}
		}
		batch = append(batch, resolved{finding: f})
	}

	now := time.Now().UTC()
	for _, r := range batch {
		rec.Conn.Acceptances = upsertAcceptance(rec.Conn.Acceptances, types.Acceptance{
			FindingID: r.finding.ID,
			Hash:      r.finding.Hash,
			Actor:     actor,
			At:        now,
			Via:       via,
		})
	}
	rec.Conn.Enabled = len(rec.Conn.Unaccepted()) == 0

	if err := v.persist(); err != nil {
		return nil, err
	}
	out := rec.Conn
	return &out, nil
}

// cutFindingID splits "<id>@<hash>" into id and hash. A plain id (no "@")
// reports hasHash=false.
func cutFindingID(raw string) (id, hash string, hasHash bool) {
	for i := 0; i < len(raw); i++ {
		if raw[i] == '@' {
			return raw[:i], raw[i+1:], true
		}
	}
	return raw, "", false
}

// TokenKey returns the per-connection HMAC key for a version, and the current
// version number. Version 0 or negative means "current". Old keys are kept so
// historical tokens stay interpretable. SPEC R8.3a.
func (v *Vault) TokenKey(ctx context.Context, id string, version int) ([]byte, int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return nil, 0, lockedError()
	}
	rec, ok := v.doc.find(id)
	if !ok {
		return nil, 0, &NotFoundError{ID: id}
	}
	current, ok := rec.currentTokenKey()
	if !ok {
		return nil, 0, fmt.Errorf("vault: connection %s has no token key", id)
	}
	want := version
	if want <= 0 {
		want = current.Version
	}
	entry, ok := rec.tokenKeyVersion(want)
	if !ok {
		return nil, current.Version, fmt.Errorf("vault: connection %s has no token key version %d", id, want)
	}
	key := make([]byte, len(entry.Key))
	copy(key, entry.Key)
	return key, current.Version, nil
}

// RotateTokenKey mints a new, current HMAC token key for a connection,
// retaining every previous version read-only. It is not part of ports.Vault
// (that interface has no rotation trigger yet) but is exposed for whichever
// caller ends up owning that trigger, and to exercise the retention behaviour
// TokenKey depends on.
func (v *Vault) RotateTokenKey(ctx context.Context, id string) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return 0, lockedError()
	}
	rec, ok := v.doc.find(id)
	if !ok {
		return 0, &NotFoundError{ID: id}
	}
	next := 1
	if cur, ok := rec.currentTokenKey(); ok {
		next = cur.Version + 1
	}
	key := make([]byte, tokenKeySize)
	if _, err := rand.Read(key); err != nil {
		return 0, fmt.Errorf("vault: generate token key: %w", err)
	}
	rec.TokenKeys = append(rec.TokenKeys, tokenKeyEntry{Version: next, Key: key})
	if err := v.persist(); err != nil {
		return 0, err
	}
	return next, nil
}

// Export returns the whole vault document, decrypted, as portable JSON.
// Losing the keychain item without an export loses every connection: SPEC
// §4.3.
func (v *Vault) Export(ctx context.Context) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return nil, lockedError()
	}
	opts := append(append([]json.Options{}, jsonOptions...), jsontext.WithIndent("  "))
	return json.Marshal(v.doc, opts...)
}

// RotateMaster generates a new master key, re-encrypts the vault under it,
// and persists the new key to the same source currently in use. It supports
// the keychain and key.age sources, which it can rewrite itself. It refuses
// for the env and passphrase sources: KEEPER_MASTER_KEY is owned by whatever
// set the process environment, and a passphrase-derived key cannot be
// re-derived under a new salt without the passphrase, which the vault does
// not retain after Unlock. SPEC §4.3.
func (v *Vault) RotateMaster(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.locked {
		return lockedError()
	}
	switch v.source {
	case sourceEnv:
		return errors.New("vault: cannot rotate the master key while the key source is KEEPER_MASTER_KEY; set a new value out of band")
	case sourcePassphrase:
		return errors.New("vault: cannot rotate a passphrase-derived master key without the passphrase, which the vault does not retain")
	}

	newKey := make([]byte, masterKeyLen)
	if _, err := rand.Read(newKey); err != nil {
		return fmt.Errorf("vault: generate new master key: %w", err)
	}
	ciphertext, err := encryptDocument(newKey, v.doc)
	if err != nil {
		return fmt.Errorf("vault: encrypt with new master key: %w", err)
	}

	switch v.source {
	case sourceKeychain:
		if err := keychainSet(keyringService, keyringUser, base64.StdEncoding.EncodeToString(newKey)); err != nil {
			return fmt.Errorf("vault: store new key in keychain: %w", err)
		}
	case sourceKeyFile:
		if err := atomicWrite(keyFilePath(v.dir), newKey, 0o600); err != nil {
			return fmt.Errorf("vault: write %s: %w", keyFileName, err)
		}
	default:
		return fmt.Errorf("vault: unknown key source %q", v.source)
	}

	if err := atomicWrite(v.vaultPath(), ciphertext, 0o600); err != nil {
		return fmt.Errorf("vault: write %s: %w", vaultFileName, err)
	}
	v.key = newKey
	return nil
}
