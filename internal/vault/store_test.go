package vault

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
)

// newTestVault returns an open Vault rooted at a temp directory, using
// KEEPER_MASTER_KEY so the test never touches a real OS keychain (already
// blocked package-wide, see keysource_test.go's init).
func newTestVault(t *testing.T) (*Vault, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(envMasterKey, validKeyB64(0x42))
	v := New(dir)
	if err := v.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	return v, dir
}

func TestEncryptDecryptDocumentRoundTrip(t *testing.T) {
	key := bytes32(0x01)
	doc := &document{Connections: []connectionRecord{{
		Conn:       testConnection("c1"),
		Credential: credential{User: "ro", Password: "pw"},
		TokenKeys: []tokenKeyEntry{
			{Version: 1, Key: bytes32(0xaa)},
		},
	}}}

	ciphertext, err := encryptDocument(key, doc)
	if err != nil {
		t.Fatalf("encryptDocument: %v", err)
	}

	got, err := decryptDocument(key, ciphertext)
	if err != nil {
		t.Fatalf("decryptDocument: %v", err)
	}
	if len(got.Connections) != 1 || got.Connections[0].Conn.ID != "c1" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got.Connections[0].Credential != doc.Connections[0].Credential {
		t.Errorf("read credential mismatch")
	}
	if string(got.Connections[0].TokenKeys[0].Key) != string(bytes32(0xaa)) {
		t.Errorf("token key mismatch")
	}

	if _, err := decryptDocument(bytes32(0x02), ciphertext); err == nil {
		t.Error("decrypting with the wrong key should fail")
	}
}

func TestAtomicWriteThenReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example")

	if err := atomicWrite(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first" {
		t.Fatalf("content = %q, want %q", got, "first")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %#o, want 0600", info.Mode().Perm())
	}

	// A second write fully replaces the content, and no temp file is left
	// behind.
	if err := atomicWrite(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("content = %q, want %q", got, "second")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want 1 (no leftover temp files): %v", len(entries), entries)
	}
}

func TestVaultRegisterPersistsAcrossInstances(t *testing.T) {
	v, dir := newTestVault(t)
	ctx := context.Background()

	conn := testConnection("")
	conn.HostID = testHost(t, v)
	if err := v.Register(ctx, &conn, ports.Credential{User: "ro", Password: "p@ss/word"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if conn.ID == "" {
		t.Fatal("Register did not assign an id")
	}

	// A second Vault instance pointed at the same directory, with the same
	// key source, must see the same data: this is the "reload" half of
	// atomic write and reload.
	t.Setenv(envMasterKey, validKeyB64(0x42))
	v2 := New(dir)
	if err := v2.Open(ctx); err != nil {
		t.Fatalf("Open (second instance): %v", err)
	}
	got, err := v2.Connection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("Connection: %v", err)
	}
	if got.Name != conn.Name {
		t.Errorf("Name = %q, want %q", got.Name, conn.Name)
	}
	dsn, err := v2.DSN(ctx, conn.ID)
	if err != nil {
		t.Fatalf("DSN: %v", err)
	}
	// The password's '@' and '/' are escaped, so they cannot move the host.
	if dsn != "postgres://ro:p%40ss%2Fword@host:5432/app?sslmode=require" {
		t.Errorf("DSN = %q", dsn)
	}
}

func TestRotateMasterKeyFileSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	v := New(dir)
	if err := v.Open(ctx); err != nil { // no env/keychain: bootstraps into key.age
		t.Fatalf("Open: %v", err)
	}
	if v.KeySource() != sourceKeyFile {
		t.Fatalf("KeySource() = %q, want %q", v.KeySource(), sourceKeyFile)
	}

	conn := testConnection("")
	conn.HostID = testHost(t, v)
	if err := v.Register(ctx, &conn, ports.Credential{User: "ro"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	oldKeyFileContents, err := os.ReadFile(keyFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}

	if err := v.RotateMaster(ctx); err != nil {
		t.Fatalf("RotateMaster: %v", err)
	}

	newKeyFileContents, err := os.ReadFile(keyFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(newKeyFileContents) == string(oldKeyFileContents) {
		t.Error("key.age was not rewritten with a new key")
	}

	// A fresh instance must unlock with the new key.age and still see the
	// connection registered before rotation.
	v2 := New(dir)
	if err := v2.Open(ctx); err != nil {
		t.Fatalf("Open after rotation: %v", err)
	}
	got, err := v2.Connection(ctx, conn.ID)
	if err != nil {
		t.Fatalf("Connection after rotation: %v", err)
	}
	if got.ID != conn.ID {
		t.Errorf("ID = %q, want %q", got.ID, conn.ID)
	}
}

// A key keeper did not write cannot be rewritten by keeper: rotating under
// KEEPER_MASTER_KEY would re-encrypt the vault under a key nothing persists,
// and the next start would find the old value in the environment.
func TestRotateMasterRefusesTheEnvSource(t *testing.T) {
	v, _ := newTestVault(t) // envMasterKey source
	if err := v.RotateMaster(context.Background()); err == nil {
		t.Error("expected RotateMaster to refuse the env source")
	}
}

func TestExportReturnsPortableJSON(t *testing.T) {
	v, _ := newTestVault(t)
	ctx := context.Background()
	conn := testConnection("")
	conn.HostID = testHost(t, v)
	if err := v.Register(ctx, &conn, ports.Credential{User: "rw"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	data, err := v.Export(ctx)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Export returned no data")
	}
	var doc document
	if err := jsonUnmarshalForTest(data, &doc); err != nil {
		t.Fatalf("exported data does not parse as the vault document: %v", err)
	}
	if len(doc.Connections) != 1 || doc.Connections[0].Conn.ID != conn.ID {
		t.Fatalf("exported document missing the registered connection: %+v", doc)
	}
	if c := doc.Connections[0].Credential; c.User != "rw" {
		t.Errorf("exported credential = %+v", c)
	}
	if len(doc.Hosts) != 1 || doc.Hosts[0].ID != conn.HostID {
		t.Errorf("exported hosts = %+v", doc.Hosts)
	}
}

// A connection cannot be registered on a host the vault does not hold, and a
// host cannot be removed while a connection is registered on it: either would
// leave credentials with nowhere to connect.
func TestHostsOwnTheirConnections(t *testing.T) {
	v, _ := newTestVault(t)
	ctx := context.Background()

	orphan := testConnection("")
	orphan.HostID = "missing"
	if err := v.Register(ctx, &orphan, ports.Credential{User: "ro"}); err == nil {
		t.Fatal("registered a connection on a host that does not exist")
	}

	host := testHost(t, v)
	a, b := testConnection(""), testConnection("")
	a.HostID, b.HostID = host, host
	b.Database = "other"
	if err := v.Register(ctx, &a, ports.Credential{User: "ro"}); err != nil {
		t.Fatal(err)
	}
	if err := v.Register(ctx, &b, ports.Credential{User: "analyst"}); err != nil {
		t.Fatal(err)
	}
	// Two databases on one host each reach their own database as their own role.
	if d, _ := v.DSN(ctx, b.ID); d != "postgres://analyst@host:5432/other?sslmode=require" {
		t.Errorf("second connection DSN = %q", d)
	}

	if err := v.RemoveHost(ctx, host); err == nil {
		t.Fatal("removed a host with connections still on it")
	}
	for _, id := range []string{a.ID, b.ID} {
		if err := v.Remove(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.RemoveHost(ctx, host); err != nil {
		t.Fatalf("RemoveHost on an empty host: %v", err)
	}
	if hs, _ := v.Hosts(ctx); len(hs) != 0 {
		t.Errorf("hosts after removal = %+v", hs)
	}
}

func Test_DET_C44_TheExportKeepsTermsAndExpressions(t *testing.T) {
	v, _ := newTestVault(t)
	ctx := context.Background()
	conn := testConnection("")
	conn.HostID = testHost(t, v)
	if err := v.Register(ctx, &conn, ports.Credential{User: "ro"}); err != nil {
		t.Fatal(err)
	}
	terms := ports.Terms{Deny: []string{"Acme Corp"}, Allow: []string{"support@yourco.com"}, Patterns: []ports.Pattern{{Label: "employee_id", Expr: `EMP-\d{6}`}}}
	if err := v.SetTerms(ctx, conn.ID, terms); err != nil {
		t.Fatal(err)
	}
	data, err := v.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var doc document
	if err := jsonUnmarshalForTest(data, &doc); err != nil {
		t.Fatal(err)
	}
	rec := doc.Connections[0]
	if !slices.Equal(rec.Deny, terms.Deny) || !slices.Equal(rec.Allow, terms.Allow) || !slices.Equal(rec.Patterns, terms.Patterns) {
		t.Errorf("export lost terms: %+v", rec)
	}
}
