// Package sealed seals a vault export under a passphrase the operator chooses,
// with age's scrypt recipient. An export carries every credential and token
// key, so it never leaves the daemon in the clear, and it opens on any machine
// given the passphrase — not the master key, which stays in that machine's
// keychain.
package sealed

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// ErrCannotOpen is an export that is damaged or sealed under another
// passphrase. age authenticates every chunk, so either is refused whole.
var ErrCannotOpen = errors.New("sealed: the export is damaged or sealed under another passphrase")

// Seal encrypts plaintext under passphrase, armored so it survives being
// copied as text.
func Seal(plaintext []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("sealed: a passphrase is required")
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	a := armor.NewWriter(&buf)
	w, err := age.Encrypt(a, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := a.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Open reverses Seal.
func Open(data []byte, passphrase string) ([]byte, error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCannotOpen, err)
	}
	r, err := age.Decrypt(armor.NewReader(bytes.NewReader(data)), id)
	if err != nil {
		return nil, ErrCannotOpen
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, ErrCannotOpen
	}
	return out, nil
}
