package vault

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

// init blocks every test in this package from ever touching the real OS
// keychain. Individual tests that need to exercise the keychain path
// override these and restore them via t.Cleanup.
func init() {
	keychainGet = func(service, user string) (string, error) { return "", keyring.ErrNotFound }
	keychainSet = func(service, user, pass string) error { return keyring.ErrUnsupportedPlatform }
}

func withKeychain(t *testing.T, get func(service, user string) (string, error), set func(service, user, pass string) error) {
	t.Helper()
	prevGet, prevSet := keychainGet, keychainSet
	keychainGet, keychainSet = get, set
	t.Cleanup(func() { keychainGet, keychainSet = prevGet, prevSet })
}

func validKeyB64(b byte) string {
	key := make([]byte, masterKeyLen)
	for i := range key {
		key[i] = b
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestResolveMasterKeyChain(t *testing.T) {
	t.Run("keychain resolves first", func(t *testing.T) {
		dir := t.TempDir()
		want := validKeyB64(0x11)
		withKeychain(t, func(service, user string) (string, error) {
			return want, nil
		}, keychainSet)

		key, source, err := resolveMasterKey(dir)
		if err != nil {
			t.Fatalf("resolveMasterKey: %v", err)
		}
		if source != sourceKeychain {
			t.Errorf("source = %q, want %q", source, sourceKeychain)
		}
		wantKey, _ := base64.StdEncoding.DecodeString(want)
		if string(key) != string(wantKey) {
			t.Errorf("key mismatch")
		}
	})

	t.Run("keychain present but malformed is a hard error, no fallback", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(envMasterKey, validKeyB64(0x22))
		withKeychain(t, func(service, user string) (string, error) {
			return "not-base64!!", nil
		}, keychainSet)

		_, _, err := resolveMasterKey(dir)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("env var resolves when keychain is absent", func(t *testing.T) {
		dir := t.TempDir()
		want := validKeyB64(0x33)
		t.Setenv(envMasterKey, want)

		key, source, err := resolveMasterKey(dir)
		if err != nil {
			t.Fatalf("resolveMasterKey: %v", err)
		}
		if source != sourceEnv {
			t.Errorf("source = %q, want %q", source, sourceEnv)
		}
		wantKey, _ := base64.StdEncoding.DecodeString(want)
		if string(key) != string(wantKey) {
			t.Errorf("key mismatch")
		}
	})

	t.Run("env var present but invalid is a hard error, no fallback to key.age", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(envMasterKey, "garbage")
		writeKeyFile(t, dir, bytes32(0x44), 0o600)

		_, _, err := resolveMasterKey(dir)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("key.age resolves when keychain and env are absent", func(t *testing.T) {
		dir := t.TempDir()
		want := bytes32(0x55)
		writeKeyFile(t, dir, want, 0o600)

		key, source, err := resolveMasterKey(dir)
		if err != nil {
			t.Fatalf("resolveMasterKey: %v", err)
		}
		if source != sourceKeyFile {
			t.Errorf("source = %q, want %q", source, sourceKeyFile)
		}
		if string(key) != string(want) {
			t.Errorf("key mismatch")
		}
	})

	t.Run("key.age with a looser mode is refused", func(t *testing.T) {
		dir := t.TempDir()
		writeKeyFile(t, dir, bytes32(0x66), 0o644)

		_, _, err := resolveMasterKey(dir)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("key.age with the wrong length is refused", func(t *testing.T) {
		dir := t.TempDir()
		writeKeyFile(t, dir, []byte("too-short"), 0o600)

		_, _, err := resolveMasterKey(dir)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("nothing resolves", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := resolveMasterKey(dir)
		if !errors.Is(err, errNoKeySource) {
			t.Fatalf("err = %v, want errNoKeySource", err)
		}
	})

	t.Run("precedence: keychain beats env and key.age", func(t *testing.T) {
		dir := t.TempDir()
		want := validKeyB64(0x88)
		withKeychain(t, func(service, user string) (string, error) {
			return want, nil
		}, keychainSet)
		t.Setenv(envMasterKey, validKeyB64(0x99))
		writeKeyFile(t, dir, bytes32(0xaa), 0o600)

		_, source, err := resolveMasterKey(dir)
		if err != nil {
			t.Fatalf("resolveMasterKey: %v", err)
		}
		if source != sourceKeychain {
			t.Errorf("source = %q, want %q", source, sourceKeychain)
		}
	})

	t.Run("precedence: env beats key.age", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(envMasterKey, validKeyB64(0xbb))
		writeKeyFile(t, dir, bytes32(0xcc), 0o600)

		_, source, err := resolveMasterKey(dir)
		if err != nil {
			t.Fatalf("resolveMasterKey: %v", err)
		}
		if source != sourceEnv {
			t.Errorf("source = %q, want %q", source, sourceEnv)
		}
	})

}

func TestBootstrap(t *testing.T) {
	t.Run("uses the keychain when it accepts", func(t *testing.T) {
		dir := t.TempDir()
		var stored string
		withKeychain(t, keychainGet, func(service, user, pass string) error {
			stored = pass
			return nil
		})
		key, source, err := bootstrap(dir)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		if source != sourceKeychain {
			t.Errorf("source = %q, want %q", source, sourceKeychain)
		}
		if stored == "" {
			t.Error("keychain Set was never called")
		}
		wantKey, _ := base64.StdEncoding.DecodeString(stored)
		if string(key) != string(wantKey) {
			t.Error("key mismatch")
		}
	})

	t.Run("falls back to key.age when the keychain refuses", func(t *testing.T) {
		dir := t.TempDir()
		key, source, err := bootstrap(dir)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		if source != sourceKeyFile {
			t.Errorf("source = %q, want %q", source, sourceKeyFile)
		}
		raw, err := os.ReadFile(keyFilePath(dir))
		if err != nil {
			t.Fatalf("read key.age: %v", err)
		}
		if string(raw) != string(key) {
			t.Error("key.age contents do not match the returned key")
		}
		info, err := os.Stat(keyFilePath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("key.age mode = %#o, want 0600", info.Mode().Perm())
		}
	})
}

func writeKeyFile(t *testing.T, dir string, contents []byte, perm os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, keyFileName)
	if err := os.WriteFile(path, contents, perm); err != nil {
		t.Fatal(err)
	}
	// os.WriteFile applies perm through umask on some platforms only when
	// creating; force it explicitly so the test is deterministic.
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}
