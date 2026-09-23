package vault

import "testing"

// An existing vault on disk still carries "enabled" and "acceptances" on every
// connection. Decoding must ignore them rather than fail, or the upgrade
// bricks the vault.
func TestOldVaultFieldsAreIgnored(t *testing.T) {
	old := []byte(`{"connections":[{"conn":{"id":"c1","name":"prod","enabled":true,` +
		`"acceptances":[{"finding_id":"rolsuper","actor":"ming","via":"cli"}],` +
		`"findings":[{"id":"rolsuper","kind":"attribute","subject":"rolsuper","hash":"deadbeef"}],` +
		`"limits":{"max_rows_ceiling":1000,"statement_timeout":30000000000,"scan_sample":300}},` +
		`"read_dsn":"postgres://x"}]}`)
	var doc document
	if err := jsonUnmarshalForTest(old, &doc); err != nil {
		t.Fatalf("a vault written before the acceptance removal no longer loads: %v", err)
	}
	if len(doc.Connections) != 1 || doc.Connections[0].Conn.Name != "prod" {
		t.Fatalf("connection did not survive: %+v", doc.Connections)
	}
	if n := len(doc.Connections[0].Conn.Findings); n != 1 {
		t.Fatalf("findings = %d, want 1", n)
	}
}
