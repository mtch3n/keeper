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
}

func TestPerm_PERM_C13_oldModeDenylistAndGrantsOpenAndAreIgnored(t *testing.T) {
	old := []byte(`{"connections":[{"conn":{"id":"c1","name":"prod","mode":"assisted",` +
		`"denylist":[{"schema":"public","relation":"salaries"}],` +
		`"grants":[{"id":"g1","path":{"connection_id":"c1"}}],` +
		`"findings":[{"id":"rolsuper","kind":"attribute"}],"audited_at":"2026-01-01T00:00:00Z",` +
		`"writes":"approve","limits":{"max_rows_ceiling":1000}},"read_dsn":"postgres://x"}]}`)
	var doc document
	if err := jsonUnmarshalForTest(old, &doc); err != nil {
		t.Fatalf("a vault with mode, denylist and grants does not open: %v", err)
	}
	if len(doc.Connections) != 1 || doc.Connections[0].Conn.Name != "prod" {
		t.Fatalf("connection did not survive: %+v", doc.Connections)
	}
	c := doc.Connections[0].Conn
	if c.Reads.Valid() {
		t.Errorf("reads = %q, an old vault must hold no setting that keeper reads as its own", c.Reads)
	}
	if c.Writes.Valid() {
		t.Errorf("writes = %q: the retired value approve must not count as a setting", c.Writes)
	}
}
