// Package vault implements ports.Vault: the age-encrypted store of connection
// records, credentials and per-connection HMAC token keys, and the key-source
// chain that opens it. It is the only package that touches the master key,
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
	open   bool
	key    []byte
	source string
	doc    *document
}

var _ ports.Vault = (*Vault)(nil)

// New returns a Vault rooted at dir. It performs no I/O; Open does all of it.
// Callers normally pass DefaultDir(), overridden in tests.
func New(dir string) *Vault {
	return &Vault{dir: dir}
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

// Open resolves the master key through the source chain and decrypts vault.age,
// or creates an empty one on a first-ever run. SPEC §4.3.
//
// There is no locked state for an operator to resolve, and so no unlock step.
// Every source resolves inside this process — the OS keychain,
// KEEPER_MASTER_KEY, key.age — and a first-ever run mints a key into whichever
// of them is available. Either this returns an open vault or the install is
// broken in a way no prompt would have fixed: the key that encrypted vault.age
// is gone. keeperd calls it before it announces its listener and exits if it
// fails, so nothing downstream ever sees a vault that is not open.
func (v *Vault) Open(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.open {
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

	key, source, err := resolveMasterKey(v.dir)
	switch {
	case err == nil:
		// resolved; fall through
	case errors.Is(err, errNoKeySource) && !exists:
		// Nothing has ever been configured, so there is nothing to lose: mint a
		// key and write the empty vault it will encrypt.
		key, source, err = bootstrap(v.dir)
		if err != nil {
			return err
		}
	case errors.Is(err, errNoKeySource):
		// A vault exists and its key does not. This is the one unrecoverable
		// state, and it is a loss rather than a lock: no passphrase, no prompt
		// and no retry produces the key that encrypted this file.
		return fmt.Errorf("vault: %s exists but its master key is gone from the keychain, "+
			"KEEPER_MASTER_KEY and key.age; restore one of them or restore the vault from an export", vaultFileName)
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
	v.open = true
	return nil
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

// errNotOpen guards every read of the decrypted document. It is a programmer
// error and not a state an operator can be in: keeperd opens the vault before
// it serves and exits if it cannot, so reaching this means a caller used a
// Vault it never opened. Nothing maps it to a code a client could act on,
// because there is no action.
func errNotOpen() error {
	return errors.New("vault: used before Open")
}

// Connections lists every registered connection. SPEC §4.3.
func (v *Vault) Connections(ctx context.Context) ([]*types.Connection, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.open {
		return nil, errNotOpen()
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
	if !v.open {
		return nil, errNotOpen()
	}
	rec, ok := v.doc.find(id)
	if !ok {
		return nil, &NotFoundError{ID: id}
	}
	c := rec.Conn
	return &c, nil
}

// Register stores a connection and its credentials, minting a fresh
// per-connection HMAC token key (version 1). Whatever the audit found about the
// role travels with the connection as a report and enables or disables nothing:
// SPEC R4.1, R8.3a.
func (v *Vault) Register(ctx context.Context, c *types.Connection, readDSN, writeDSN string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.open {
		return errNotOpen()
	}
	if c.ID == "" {
		c.ID = uuid.NewV7().String()
	}
	if _, exists := v.doc.find(c.ID); exists {
		return &ConflictError{Subject: c.ID, Reason: "a connection with this id is already registered"}
	}

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
// denylist, write scope and findings. Findings are stored as the last audit
// reported them and gate nothing; a re-audit that turns up a new privilege
// changes what the audit report says and leaves the connection usable, which
// is what makes the audit separable from the connection at all: SPEC R4.1.
func (v *Vault) Update(ctx context.Context, c *types.Connection) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.open {
		return errNotOpen()
	}
	rec, ok := v.doc.find(c.ID)
	if !ok {
		return &NotFoundError{ID: c.ID}
	}
	rec.Conn = *c
	return v.persist()
}

// Remove deletes a connection and everything stored beside it: its
// credentials and its token keys.
func (v *Vault) Remove(ctx context.Context, id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.open {
		return errNotOpen()
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
	if !v.open {
		return "", errNotOpen()
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

// TokenKey returns the per-connection HMAC key for a version, and the current
// version number. Version 0 or negative means "current". Old keys are kept so
// historical tokens stay interpretable. SPEC R8.3a.
func (v *Vault) TokenKey(ctx context.Context, id string, version int) ([]byte, int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.open {
		return nil, 0, errNotOpen()
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
	if !v.open {
		return 0, errNotOpen()
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
	if !v.open {
		return nil, errNotOpen()
	}
	opts := append(append([]json.Options{}, jsonOptions...), jsontext.WithIndent("  "))
	return json.Marshal(v.doc, opts...)
}

// RotateMaster generates a new master key, re-encrypts the vault under it,
// and persists the new key to the same source currently in use. It supports
// the keychain and key.age sources, which it can rewrite itself. It refuses
// for the env source: KEEPER_MASTER_KEY is owned by whatever set the process
// environment, and a rotation keeper cannot persist is a vault nothing can
// open afterwards. SPEC §4.3.
func (v *Vault) RotateMaster(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.open {
		return errNotOpen()
	}
	if v.source == sourceEnv {
		return errors.New("vault: cannot rotate the master key while the key source is KEEPER_MASTER_KEY; set a new value out of band")
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
