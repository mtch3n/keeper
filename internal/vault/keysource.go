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
	"golang.org/x/crypto/argon2"
)

// Key sources, in resolution order: SPEC §4.3, R4.3.
const (
	sourceKeychain   = "keychain"
	sourceEnv        = "env"
	sourceKeyFile    = "key.age"
	sourcePassphrase = "passphrase"
)

const (
	keyringService = "keeper"
	keyringUser    = "master"
	envMasterKey   = "KEEPER_MASTER_KEY"
	keyFileName    = "key.age"
	saltFileName   = "passphrase.salt"

	masterKeyLen = 32

	// Argon2id parameters for the interactive passphrase path. These follow the
	// OWASP baseline recommendation (m=64MiB, t=3, p=4) for an install where the
	// derived key protects everything else at rest.
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonSaltLen = 16
)

// errNoKeySource means none of the four sources resolved and no passphrase
// was supplied. It is distinguished from every other resolution error so
// Unlock can tell "nothing configured yet" (bootstrap candidate) from "a
// configured source is broken" (hard failure, never silently degraded).
var errNoKeySource = errors.New("vault: no key source available")

// keychainGet and keychainSet seam the OS keychain for tests: a test process
// must never touch the real system keyring. Production code never reassigns
// them.
var (
	keychainGet = keyring.Get
	keychainSet = keyring.Set
)

// resolveMasterKey tries the key source chain in order and returns the first
// one that resolves, or errNoKeySource if none does and no passphrase was
// given. A source that is present but malformed is a hard error: falling
// through in that case would be exactly the silent degradation R4.3 forbids.
func resolveMasterKey(dir, passphrase string) (key []byte, source string, err error) {
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

	if passphrase != "" {
		key, ok, err := keyFromPassphrase(dir, passphrase)
		if err != nil {
			return nil, "", err
		}
		if ok {
			return key, sourcePassphrase, nil
		}
		// Passphrase mode has never been set up on this install (no salt file
		// yet). That is "no source resolved", the same as every other absent
		// source, not a hard error: on a fresh install Unlock's bootstrap path
		// will set it up; against an existing vault.age it surfaces as the
		// same "no key source available" error every other absence would.
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

// keyFromPassphrase derives the master key from an interactive passphrase and
// this install's persisted salt. The salt is not secret; it only needs to be
// stable so the same passphrase always derives the same key. ok is false when
// passphrase mode has never been set up on this install (no salt file yet),
// which is absence, not a hard error: see resolveMasterKey.
func keyFromPassphrase(dir, passphrase string) (key []byte, ok bool, err error) {
	salt, err := os.ReadFile(saltPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("vault: read %s: %w", saltFileName, err)
	}
	return deriveArgon2(passphrase, salt), true, nil
}

func deriveArgon2(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, masterKeyLen)
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

func saltPath(dir string) string    { return filepath.Join(dir, saltFileName) }
func keyFilePath(dir string) string { return filepath.Join(dir, keyFileName) }

// bootstrap runs exactly once, the first time a vault is unlocked and no
// vault.age yet exists: nothing has resolved because nothing has ever been
// configured. It never runs against an existing vault.age, so it can never
// silently replace the key that file was encrypted with.
//
// A supplied passphrase is honoured directly (the operator asked for
// interactive headless mode). Otherwise the new key is stored in the
// keychain when available, falling back to key.age: the file key.age exists
// precisely to be that headless fallback.
func bootstrap(dir, passphrase string) (key []byte, source string, err error) {
	if passphrase != "" {
		salt := make([]byte, argonSaltLen)
		if _, err := rand.Read(salt); err != nil {
			return nil, "", fmt.Errorf("vault: generate salt: %w", err)
		}
		if err := atomicWrite(saltPath(dir), salt, 0o600); err != nil {
			return nil, "", fmt.Errorf("vault: write %s: %w", saltFileName, err)
		}
		return deriveArgon2(passphrase, salt), sourcePassphrase, nil
	}

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
