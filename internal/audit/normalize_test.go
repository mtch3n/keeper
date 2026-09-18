package audit

import "testing"

func catalogued(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(s string) bool { return set[s] }
}

func TestNormalizeStripsLiterals(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT id FROM users WHERE email = 'jane@example.com'",
			"SELECT id FROM users WHERE email = ?"},
		{"SELECT id FROM users WHERE name = 'O''Brien'",
			"SELECT id FROM users WHERE name = ?"},
		{`SELECT E'\n jane@example.com \' still inside' FROM t`,
			"SELECT ? FROM t"},
		{`SELECT e'\\' , id FROM t`,
			"SELECT ? , id FROM t"},
		{"SELECT U&'\\0041 jane' FROM t", "SELECT ? FROM t"},
		{"SELECT B'1010', X'ff' FROM t", "SELECT ?, ? FROM t"},
		{"SELECT id FROM users WHERE ssn = 123456789", "SELECT id FROM users WHERE ssn = ?"},
		{"SELECT 1.5e3, .5, 0xFF, 1_000 FROM t", "SELECT ?, ?, ?, ? FROM t"},
		{"SELECT id FROM users WHERE email = $1 AND n > $2",
			"SELECT id FROM users WHERE email = $1 AND n > $2"},
		{"SELECT t1.c2 FROM tab1 t1", "SELECT t1.c2 FROM tab1 t1"},
		{"SELECT id::text FROM t", "SELECT id::text FROM t"},
		{"SELECT 'unterminated FROM t", "SELECT ?"},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in, nil); got != tc.want {
			t.Errorf("Normalize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeDollarQuoting(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT $$jane@example.com$$ FROM t", "SELECT ? FROM t"},
		{"SELECT $tag$ it's jane, and $$ too $tag$ FROM t", "SELECT ? FROM t"},
		{"SELECT $q$ nested $inner$ text $inner$ $q$ FROM t", "SELECT ? FROM t"},
		{"SELECT $1 FROM t", "SELECT $1 FROM t"},
		{"SELECT $12 + $3 FROM t", "SELECT $12 + $3 FROM t"},
		{"SELECT $tag$unterminated FROM t", "SELECT ?"},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in, nil); got != tc.want {
			t.Errorf("Normalize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeDiscardsComments(t *testing.T) {
	// R10c's worked example. The comment is the thing that carries the PII.
	got := Normalize("SELECT id /* patient: Jane Doe */ FROM users", nil)
	if got != "SELECT id FROM users" {
		t.Errorf("got %q", got)
	}
	cases := []struct{ in, want string }{
		{"SELECT id -- patient: Jane Doe\nFROM users", "SELECT id FROM users"},
		{"SELECT id --patient: Jane Doe", "SELECT id"},
		{"SELECT /* a /* jane@example.com */ still comment */ id FROM t", "SELECT id FROM t"},
		{"SELECT /* a /* b */ c */ id FROM t", "SELECT id FROM t"},
		{"SELECT id/*x*/FROM t", "SELECT id FROM t"},
		{"SELECT id /* unterminated", "SELECT id"},
		{"SELECT 4 - -3 FROM t", "SELECT ? - -? FROM t"},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in, nil); got != tc.want {
			t.Errorf("Normalize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeQuotedIdentifiers(t *testing.T) {
	known := catalogued("order", "Email")
	cases := []struct{ in, want string }{
		// R10c: kept only when it matches something catalogued.
		{`SELECT "Email" FROM "order"`, `SELECT "Email" FROM "order"`},
		{`SELECT "patient: Jane Doe" FROM t`, `SELECT "?" FROM t`},
		{`SELECT "Email", "ssn" FROM "order"`, `SELECT "Email", "?" FROM "order"`},
		{`SELECT "a""b" FROM t`, `SELECT "?" FROM t`},
		{`SELECT "unterminated`, `SELECT "?"`},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in, known); got != tc.want {
			t.Errorf("Normalize(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
	// With no callback nothing is catalogued, so nothing is kept.
	if got := Normalize(`SELECT "Email" FROM "order"`, nil); got != `SELECT "?" FROM "?"` {
		t.Errorf("nil known kept an identifier: %q", got)
	}
}

func TestNormalizeIsIdempotentAndCollapsesWhitespace(t *testing.T) {
	in := "SELECT\n\tid,\n\temail\nFROM   users\nWHERE id = 3"
	once := Normalize(in, nil)
	if once != "SELECT id, email FROM users WHERE id = ?" {
		t.Fatalf("got %q", once)
	}
	if twice := Normalize(once, nil); twice != once {
		t.Errorf("not idempotent: %q then %q", once, twice)
	}
}

func TestHasStrippableLiteral(t *testing.T) {
	dirty := []string{
		"SELECT 'x'", "SELECT id -- c", "SELECT /* c */ id", "SELECT $$x$$", "SELECT $t$x$t$",
	}
	for _, s := range dirty {
		if !hasStrippableLiteral(s) {
			t.Errorf("hasStrippableLiteral(%q) = false", s)
		}
	}
	clean := []string{
		"SELECT id FROM users WHERE email = ?",
		`SELECT "Email" FROM "order" WHERE id = $1`,
		"SELECT count(*) FROM t",
	}
	for _, s := range clean {
		if hasStrippableLiteral(s) {
			t.Errorf("hasStrippableLiteral(%q) = true", s)
		}
	}
}
