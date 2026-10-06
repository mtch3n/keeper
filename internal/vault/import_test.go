package vault

import (
	"context"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/redact"
	"github.com/mtchen/keeper/internal/sealed"
)

func exportOf(t *testing.T, v *Vault) ([]byte, error) {
	plain, err := v.Export(t.Context())
	if err != nil {
		return nil, err
	}
	return sealed.Seal(plain, "correct horse")
}

// machine is a vault under its own master key, as on another computer.
func machine(t *testing.T, keyByte byte) *Vault {
	t.Helper()
	t.Setenv(envMasterKey, validKeyB64(keyByte))
	v := New(t.TempDir())
	if err := v.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	return v
}

func registered(t *testing.T, v *Vault) string {
	t.Helper()
	conn := testConnection("")
	conn.HostID = testHost(t, v)
	if err := v.Register(t.Context(), &conn, ports.Credential{User: "app_ro", Password: "pw"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return conn.ID
}

func Test_TOKEN_C3_APersistentTokenResolvesOnAnotherMachine(t *testing.T) {
	a := machine(t, 0x42)
	id := registered(t, a)
	ra, _ := redact.New(redact.Config{Keys: a})
	ctx := redact.WithStatement(t.Context(), redact.Statement{ConnectionID: id, Persistent: true})
	token, err := ra.Mint(ctx, "s1", id, "email", "jane@example.com")
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	export, err := exportOf(t, a)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	b := machine(t, 0x77)
	if err := b.Import(t.Context(), export, "correct horse"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	rb, _ := redact.New(redact.Config{Keys: b})
	if v, _, err := rb.Resolve(t.Context(), "another-session", id, token); err != nil || v != "jane@example.com" {
		t.Errorf("the token did not resolve on the other machine: %q %v", v, err)
	}
}

func Test_TOKEN_C8_ABadExportIsRefusedAndTheVaultIsUnchanged(t *testing.T) {
	a := machine(t, 0x42)
	registered(t, a)
	export, err := exportOf(t, a)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	b := machine(t, 0x77)
	before := registered(t, b)

	damaged := append([]byte(nil), export...)
	damaged[len(damaged)/2] ^= 0xff
	for name, tc := range map[string]struct {
		data []byte
		pass string
	}{"damaged": {damaged, "correct horse"}, "another key": {export, "wrong horse"}, "not an export": {[]byte("{}"), "correct horse"}} {
		if err := b.Import(t.Context(), tc.data, tc.pass); err == nil {
			t.Errorf("%s: import accepted", name)
		}
		cs, _ := b.Connections(t.Context())
		if len(cs) != 1 || cs[0].ID != before {
			t.Errorf("%s: the vault changed: %+v", name, cs)
		}
	}
}
