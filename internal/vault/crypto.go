package vault

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"encoding/json/v2"

	"filippo.io/age"
)

// jsonOptions carries the codecs encoding/json/v2 needs for time.Duration,
// which has no default JSON representation in v2 (unlike v1's implicit
// int64). types.Limits.StatementTimeout is a time.Duration and types is
// frozen, so the codec is supplied here instead. Nanoseconds round-trip
// exactly and match time.ParseDuration-free tooling elsewhere in the module.
var jsonOptions = []json.Options{
	json.WithMarshalers(json.JoinMarshalers(json.MarshalFunc(func(d time.Duration) ([]byte, error) {
		return strconv.AppendInt(nil, int64(d), 10), nil
	}))),
	json.WithUnmarshalers(json.JoinUnmarshalers(json.UnmarshalFunc(func(data []byte, d *time.Duration) error {
		n, err := strconv.ParseInt(string(data), 10, 64)
		if err != nil {
			return fmt.Errorf("duration: %w", err)
		}
		*d = time.Duration(n)
		return nil
	}))),
}

// scryptWorkFactor is deliberately minimal. The "password" age wraps the file
// key to is not a human passphrase: it is the master key, which already has
// 256 bits of entropy from whichever source resolved it. Scrypt's stretching
// cost defends a low-entropy password against brute force; here it only pays
// for a well-tested wrapping format, so the lowest work factor age allows is
// the right choice.
const scryptWorkFactor = 1

// masterKeyPassword renders a 32-byte master key as the "password" fed to
// age's scrypt recipient/identity. Hex avoids format-specific escaping.
func masterKeyPassword(key []byte) string { return hex.EncodeToString(key) }

// encryptDocument serialises doc to JSON and encrypts it with the master key,
// producing the bytes stored in vault.age.
func encryptDocument(masterKey []byte, doc *document) ([]byte, error) {
	plaintext, err := json.Marshal(doc, jsonOptions...)
	if err != nil {
		return nil, fmt.Errorf("marshal vault document: %w", err)
	}
	recipient, err := age.NewScryptRecipient(masterKeyPassword(masterKey))
	if err != nil {
		return nil, fmt.Errorf("build recipient: %w", err)
	}
	recipient.SetWorkFactor(scryptWorkFactor)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	if err != nil {
		return nil, fmt.Errorf("open age writer: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("write plaintext: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close age writer: %w", err)
	}
	return buf.Bytes(), nil
}

// decryptDocument reverses encryptDocument. A wrong master key surfaces as an
// error wrapping age.ErrIncorrectIdentity.
func decryptDocument(masterKey []byte, ciphertext []byte) (*document, error) {
	identity, err := age.NewScryptIdentity(masterKeyPassword(masterKey))
	if err != nil {
		return nil, fmt.Errorf("build identity: %w", err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	var doc document
	if err := json.UnmarshalRead(r, &doc, jsonOptions...); err != nil {
		return nil, fmt.Errorf("unmarshal vault document: %w", err)
	}
	return &doc, nil
}

// atomicWrite writes data to path by writing a temp file in the same
// directory, fsyncing it, and renaming it into place, then fsyncing the
// directory so the rename itself is durable. CONTRACT §"internal/vault":
// only one process ever opens this file, so no cross-process locking is
// needed here.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	renamed = true

	if df, err := os.Open(dir); err == nil {
		_ = df.Sync()
		df.Close()
	}
	return nil
}
