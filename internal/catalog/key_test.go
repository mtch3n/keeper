package catalog

import "testing"

func TestColumnKeyRoundTrip(t *testing.T) {
	cases := []columnKey{
		{Schema: "public", Table: "users", Column: "email"},
		{Schema: "public", Table: "users", Column: "customer.ref"}, // dotted column name
	}
	for _, want := range cases {
		got, err := parseKey(want.String())
		if err != nil {
			t.Fatalf("parseKey(%q): %v", want.String(), err)
		}
		if got != want {
			t.Errorf("round trip: got %+v, want %+v", got, want)
		}
	}
}

func TestParseKeyRejectsMalformed(t *testing.T) {
	for _, key := range []string{"", "onlytable", "schema.table", "schema..column", ".table.column"} {
		if _, err := parseKey(key); err == nil {
			t.Errorf("parseKey(%q): expected an error, got none", key)
		}
	}
}
