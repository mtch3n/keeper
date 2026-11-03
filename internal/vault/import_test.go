package vault

import (
	"bytes"
	"context"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
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

func TestImportIgnoresFieldsAnOlderVaultCarried(t *testing.T) {
	a := machine(t, 0x42)
	id := registered(t, a)
	plain, err := a.Export(t.Context())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	old := bytes.Replace(plain, []byte(`"id": "`+id+`"`), []byte(`"persistent_tokens": true, "intent": "x", "id": "`+id+`"`), 1)
	if bytes.Equal(old, plain) {
		t.Fatal("test did not add the old fields")
	}
	sealedOld, err := sealed.Seal(old, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	b := machine(t, 0x77)
	if err := b.Import(t.Context(), sealedOld, "correct horse"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := b.Connection(t.Context(), id); err != nil {
		t.Errorf("the connection is gone: %v", err)
	}
}
