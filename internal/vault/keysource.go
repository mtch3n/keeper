package vault

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

// Key sources, in resolution order: SPEC §4.3, R4.3.
//
// All three resolve inside this process with nothing from a person. That is the
// property the whole design rests on: keeperd starts, opens the vault and
// serves, and an operator never learns that a vault is a thing that opens. An
// interactive passphrase source existed here and was removed — it could not
// work for the auto-started daemon that is keeper's normal shape, so it bought
// an unlock step on every install in exchange for protecting none of them.
const (
	sourceKeychain = "keychain"
	sourceEnv      = "env"
	sourceKeyFile  = "key.age"
)

const (
	keyringService = "keeper"
	keyringUser    = "master"
	envMasterKey   = "KEEPER_MASTER_KEY"
	keyFileName    = "key.age"

	masterKeyLen = 32
)

// errNoKeySource means no source resolved. Open distinguishes it from every
// other resolution error so it can tell "nothing configured yet" (mint a key)
// from "a configured source is broken" (fail, never silently degrade).
var errNoKeySource = errors.New("vault: no key source available")

// keychainGet and keychainSet seam the OS keychain for tests: a test process
// must never touch the real system keyring. Production code never reassigns
// them.
var (
	keychainGet = keyring.Get
	keychainSet = keyring.Set
)

// resolveMasterKey tries the key source chain in order and returns the first
// one that resolves, or errNoKeySource if none does. A source that is present
// but malformed is a hard error: falling through in that case would be exactly
// the silent degradation R4.3 forbids.
func resolveMasterKey(dir string) (key []byte, source string, err error) {
	key, ok, err := keyFromKeychain()
	if err != nil {
		return nil, "", err
	}
	if ok {
		return key, sourceKeychain, nil
	}

	if raw, present := os.LookupEnv(envMasterKey); present {
		key, err := decodeMasterKeyB64(raw)
		if err != nil {
			return nil, "", fmt.Errorf("vault: %s: %w", envMasterKey, err)
		}
		return key, sourceEnv, nil
	}

	key, ok, err = keyFromFile(dir)
	if err != nil {
		return nil, "", err
	}
	if ok {
		return key, sourceKeyFile, nil
	}

	return nil, "", errNoKeySource
}

// keyFromKeychain reads the master key from the OS keychain. Any retrieval
// failure (no item, unsupported platform, backend unreachable) is treated as
// absence rather than a hard error, since none of those states holds a value
// that could be silently wrong; a value that IS present but fails to decode is
// a hard error.
func keyFromKeychain() (key []byte, ok bool, err error) {
	v, err := keychainGet(keyringService, keyringUser)
	if err != nil {
		return nil, false, nil
	}
	key, decodeErr := decodeMasterKeyB64(v)
	if decodeErr != nil {
		return nil, false, fmt.Errorf("vault: keychain holds a malformed key: %w", decodeErr)
	}
	return key, true, nil
}

// keyFromFile reads the master key from key.age. SPEC requires mode 0600 and
// refuses a looser one rather than silently trusting a world- or group-
// readable file.
func keyFromFile(dir string) (key []byte, ok bool, err error) {
	path := filepath.Join(dir, keyFileName)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("vault: stat %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, false, fmt.Errorf("vault: %s must be mode 0600, has %#o", path, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("vault: read %s: %w", path, err)
	}
	if len(raw) != masterKeyLen {
		return nil, false, fmt.Errorf("vault: %s must hold %d raw bytes, has %d", path, masterKeyLen, len(raw))
	}
	return raw, true, nil
}

func decodeMasterKeyB64(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("not valid base64: %w", err)
	}
	if len(key) != masterKeyLen {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", masterKeyLen, len(key))
	}
	return key, nil
}

func keyFilePath(dir string) string { return filepath.Join(dir, keyFileName) }

// bootstrap runs exactly once, the first time a vault is opened and no
// vault.age yet exists: nothing has resolved because nothing has ever been
// configured. It never runs against an existing vault.age, so it can never
// silently replace the key that file was encrypted with.
//
// The new key is stored in the keychain when available, falling back to
// key.age: the file key.age exists precisely to be that headless fallback. One
// of the two always works, which is what makes a first run need no setup step.
func bootstrap(dir string) (key []byte, source string, err error) {
	newKey := make([]byte, masterKeyLen)
	if _, err := rand.Read(newKey); err != nil {
		return nil, "", fmt.Errorf("vault: generate master key: %w", err)
	}
	if err := keychainSet(keyringService, keyringUser, base64.StdEncoding.EncodeToString(newKey)); err == nil {
		return newKey, sourceKeychain, nil
	}
	if err := atomicWrite(keyFilePath(dir), newKey, 0o600); err != nil {
		return nil, "", fmt.Errorf("vault: bootstrap %s: %w", keyFileName, err)
	}
	return newKey, sourceKeyFile, nil
}
