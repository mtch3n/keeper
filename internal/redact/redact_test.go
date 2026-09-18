package redact

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/rules"
	"github.com/mtchen/keeper/internal/types"
)

// --- fixtures ---------------------------------------------------------------

type fakeKeys struct{ version int }

// TokenKey returns a key that differs per connection, which is what §8.3 means
// by "the tokenization key is per connection, not global, so tokens do not link
// identities across unrelated databases".
func (f fakeKeys) TokenKey(_ context.Context, connID string, _ int) ([]byte, int, error) {
	return []byte("test-key-for-" + connID), f.version, nil
}

type colKey struct {
	oid uint32
	att uint16
}

type fakeCatalog map[colKey]types.ColumnPolicy

func (c fakeCatalog) Lookup(_ string, oid uint32, att uint16) (types.ColumnPolicy, bool) {
	p, ok := c[colKey{oid, att}]
	return p, ok
}

func newRedactor(t *testing.T, cat PolicyLookup, ns map[string]NamespaceRule) *Redactor {
	t.Helper()
	r, err := New(Config{
		Keys:       fakeKeys{version: 1},
		Detector:   rules.New(rules.Config{}),
		Policies:   cat,
		Namespaces: ns,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func stmt(ctx context.Context, conn string, params ...ResolvedParam) context.Context {
	return WithStatement(ctx, Statement{ConnectionID: conn, Params: params})
}

func col(name, typ string, p types.Policy, oid uint32, att uint16) types.ColumnMeta {
	return types.ColumnMeta{Name: name, Type: typ, Policy: p, TableOID: oid, AttNum: att}
}

// --- tokens -----------------------------------------------------------------

func TestTokenDeterminismAndShape(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyToken, Namespace: "email"}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("email", "text", types.PolicyToken, 1, 1)}

	run := func(sess string, v string) string {
		rows := [][]any{{v}}
		if _, err := r.Apply(stmt(t.Context(), "c1"), sess, cols, rows); err != nil {
			t.Fatal(err)
		}
		s, ok := rows[0][0].(string)
		if !ok {
			t.Fatalf("cell is %T, want string", rows[0][0])
		}
		return s
	}

	a := run("s1", "jane@example.com")
	b := run("s2", "jane@example.com") // a different session, same key
	if a != b {
		t.Errorf("token is not deterministic across sessions: %q vs %q", a, b)
	}
	if c := run("s1", "john@example.com"); c == a {
		t.Error("different values produced the same token")
	}

	ns, ver, tag, ok := ParseToken(a)
	if !ok {
		t.Fatalf("ParseToken(%q) failed", a)
	}
	if ns != "email" || ver != 1 {
		t.Errorf("token = ns %q ver %d, want email/1", ns, ver)
	}
	if len(tag) != TokenHexLen {
		t.Errorf("tag is %d hex chars, want %d (R8.3a: 64 bits)", len(tag), TokenHexLen)
	}
	if !strings.HasPrefix(a, TokenOpen) || !strings.HasSuffix(a, TokenClose) {
		t.Errorf("token %q is not bracketed", a)
	}
}

func TestTokenNamespaceIsolation(t *testing.T) {
	// The same value under two namespaces must produce two tokens: the
	// namespace is part of the hashed message, which is what makes it a
	// classification label rather than a salt.
	cat := fakeCatalog{
		{1, 1}: {Policy: types.PolicyToken, Namespace: "email"},
		{1, 2}: {Policy: types.PolicyToken, Namespace: "username"},
	}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{
		col("a", "text", types.PolicyToken, 1, 1),
		col("b", "text", types.PolicyToken, 1, 2),
	}
	rows := [][]any{{"same", "same"}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0][0] == rows[0][1] {
		t.Errorf("namespaces did not isolate: both %v", rows[0][0])
	}
}

func TestTokenJoinsAcrossColumnsInOneNamespace(t *testing.T) {
	// users.email and orders.user_email are different columns of different
	// relations in one namespace. They must produce the same token or the join
	// tokens exist to preserve is gone.
	cat := fakeCatalog{
		{1, 1}: {Policy: types.PolicyToken, Namespace: "email"},
		{2, 7}: {Policy: types.PolicyToken, Namespace: "email"},
	}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{
		col("email", "text", types.PolicyToken, 1, 1),
		col("user_email", "text", types.PolicyToken, 2, 7),
	}
	rows := [][]any{{"jane@example.com", "jane@example.com"}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != rows[0][1] {
		t.Errorf("one namespace produced two tokens: %v and %v", rows[0][0], rows[0][1])
	}
}

func TestTokenKeyIsPerConnection(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyToken, Namespace: "email"}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("email", "text", types.PolicyToken, 1, 1)}

	get := func(conn string) any {
		rows := [][]any{{"jane@example.com"}}
		if _, err := r.Apply(stmt(t.Context(), conn), "s-"+conn, cols, rows); err != nil {
			t.Fatal(err)
		}
		return rows[0][0]
	}
	if get("prod") == get("staging") {
		t.Error("two connections produced the same token; tokens would link identities across databases")
	}
}

func TestTokenWithoutNamespaceFallsBackToRedact(t *testing.T) {
	r := newRedactor(t, fakeCatalog{}, nil)
	cols := []types.ColumnMeta{col("x", "text", types.PolicyToken, 1, 1)}
	rows := [][]any{{"jane@example.com"}}
	tr, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != RedactedMarker {
		t.Errorf("cell = %v, want %q", rows[0][0], RedactedMarker)
	}
	if tr["x"].Policy != types.PolicyRedact {
		t.Errorf("policy = %v, want redact", tr["x"].Policy)
	}
}

// --- normalization ----------------------------------------------------------

func TestNormalizeNFCAndTrimAreTheFloor(t *testing.T) {
	// U+0065 U+0301 (e + combining acute) composes to U+00E9.
	decomposed := "  josé@example.com  "
	composed := "josé@example.com"
	if got := Normalize(decomposed, NamespaceRule{}); got != composed {
		t.Errorf("Normalize = %q, want %q", got, composed)
	}
	// Case is preserved by default: the database may compare case-sensitively,
	// and folding would merge records it treats as distinct.
	if got := Normalize("User@Example.COM", NamespaceRule{}); got != "User@Example.COM" {
		t.Errorf("Normalize folded case without being asked: %q", got)
	}
	if got := Normalize("User@Example.COM", NamespaceRule{CaseFold: true}); got != "user@example.com" {
		t.Errorf("Normalize(CaseFold) = %q", got)
	}
}

func TestNormalizationDrivesTokenEquality(t *testing.T) {
	cat := fakeCatalog{
		{1, 1}: {Policy: types.PolicyToken, Namespace: "email"},
		{1, 2}: {Policy: types.PolicyToken, Namespace: "plain"},
	}
	r := newRedactor(t, cat, map[string]NamespaceRule{"email": {CaseFold: true}})
	cols := []types.ColumnMeta{
		col("folded", "text", types.PolicyToken, 1, 1),
		col("plain", "text", types.PolicyToken, 1, 2),
	}
	rows := [][]any{
		{"User@Example.COM", "User@Example.COM"},
		{" user@example.com ", "user@example.com"},
	}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	// The opted-in namespace joins the two spellings; the default one does not.
	if rows[0][0] != rows[1][0] {
		t.Errorf("case_fold namespace failed to join: %v vs %v", rows[0][0], rows[1][0])
	}
	if rows[0][1] == rows[1][1] {
		t.Error("default namespace folded case; the database may not")
	}
	// NFC and trim are the floor for every namespace, opted in or not.
	rows2 := [][]any{{"x", "  user@example.com  "}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows2); err != nil {
		t.Fatal(err)
	}
	if rows2[0][1] != rows[1][1] {
		t.Errorf("trim is not applied under the default rule: %v vs %v", rows2[0][1], rows[1][1])
	}
}

// --- R8.4a / R8.4c ----------------------------------------------------------

// mintedSession puts a value in the session map through §8.7's local input
// path, which is how a value gets there without a statement parameter.
func mintedSession(t *testing.T, r *Redactor, sess, value string) string {
	t.Helper()
	tok, err := r.Mint(t.Context(), sess, "c1", "email", value)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestRetokenizesResolvedValuesThroughDerivations(t *testing.T) {
	// R8.4c: exact matching fails on every ordinary derivation. These are the
	// four the SPEC names, in the shapes the server would actually return them.
	const value = "jane@example.com"
	cases := []struct {
		name string
		typ  string
		cell any
		want func(tok string) any
	}{
		{"concat", "text", "Hi " + value, func(tok string) any { return "Hi " + tok }},
		{"format", "text", "Hi " + value + "!", func(tok string) any { return "Hi " + tok + "!" }},
		{"upper", "text", strings.ToUpper(value), func(tok string) any { return tok }},
		{"to_json", "json", `"` + value + `"`, func(tok string) any { return `"` + tok + `"` }},
		{"to_json_object", "jsonb", map[string]any{"e": value}, func(tok string) any {
			return map[string]any{"e": tok}
		}},
		{"array", "text[]", []any{value, "other"}, func(tok string) any { return []any{tok, "other"} }},
		{"row", "record", []any{"x", []any{value}}, func(tok string) any { return []any{"x", []any{tok}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRedactor(t, fakeCatalog{}, nil)
			tok := mintedSession(t, r, "s", value)
			cols := []types.ColumnMeta{col("out", tc.typ, types.PolicyAllow, 0, 0)}
			rows := [][]any{{tc.cell}}
			if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
				t.Fatal(err)
			}
			got := rows[0][0]
			if strings.Contains(sprint(got), value) {
				t.Fatalf("resolved value survived emission: %v", got)
			}
			if sprint(got) != sprint(tc.want(tok)) {
				t.Errorf("got %v, want %v", got, tc.want(tok))
			}
		})
	}
}

// sprint renders a cell deterministically, so nested containers can be compared
// without depending on map iteration order.
func sprint(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = sprint(e)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, 0, len(t))
		for _, k := range keys {
			parts = append(parts, k+"="+sprint(t[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	case nil:
		return "<nil>"
	}
	return fmt.Sprint(v)
}

func TestSelectParamOracleIsClosed(t *testing.T) {
	// SPEC R8.4a's worked example: SELECT $1::text FROM clean_ids with a token
	// parameter. The column is computed, clean_ids is entirely allow, and
	// without the emission scan the real value comes back in cleartext.
	const value = "Jane Doe"
	r := newRedactor(t, fakeCatalog{}, nil)
	tok, err := r.Mint(t.Context(), "s", "c1", "person", value)
	if err != nil {
		t.Fatal(err)
	}
	ctx := stmt(t.Context(), "c1", ResolvedParam{Ordinal: 1, Namespace: "person", Policy: types.PolicyToken})
	cols := []types.ColumnMeta{col("text", "text", types.PolicyAllow, 0, 0)}
	rows := [][]any{{value}}
	tr, err := r.Apply(ctx, "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if got := rows[0][0]; got == value {
		t.Fatalf("de-tokenization oracle is open: %v", got)
	}
	if got := rows[0][0]; got != RedactedMarker && got != tok {
		t.Fatalf("cell = %v, want the token or a redaction", got)
	}
	if tr["text"].Policy == types.PolicyAllow {
		t.Errorf("transform still reports allow: %+v", tr["text"])
	}
}

func TestSubstringMatchIsCaseInsensitiveAndBounded(t *testing.T) {
	r := newRedactor(t, fakeCatalog{}, nil)
	tok := mintedSession(t, r, "s", "jane@example.com")
	cols := []types.ColumnMeta{col("notes", "text", types.PolicyAllow, 0, 0)}
	rows := [][]any{
		{"wrote to JANE@Example.Com twice"},
		{"nothing sensitive here"},
	}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != "wrote to "+tok+" twice" {
		t.Errorf("row 0 = %v", rows[0][0])
	}
	if rows[1][0] != "nothing sensitive here" {
		t.Errorf("row 1 was rewritten: %v", rows[1][0])
	}
}

func TestShortValuesMatchWholeCell(t *testing.T) {
	// Below the substring threshold a value is matched against the whole cell.
	// Substring-matching "Li" would rewrite every cell containing it; dropping
	// it entirely would be a hole.
	r := newRedactor(t, fakeCatalog{}, nil)
	tok, err := r.Mint(t.Context(), "s", "c1", "person", "Li")
	if err != nil {
		t.Fatal(err)
	}
	cols := []types.ColumnMeta{col("name", "text", types.PolicyAllow, 0, 0)}
	rows := [][]any{{"Li"}, {"Lisbon"}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != tok {
		t.Errorf("short value not re-tokenized: %v", rows[0][0])
	}
	if rows[1][0] != "Lisbon" {
		t.Errorf("short value matched as a substring: %v", rows[1][0])
	}
}

// --- R8.4b ------------------------------------------------------------------

func TestParameterFloorAppliesToTextLikeColumnsOnly(t *testing.T) {
	cat := fakeCatalog{
		{1, 1}: {Policy: types.PolicyAllow},
		{1, 2}: {Policy: types.PolicyAllow},
		{1, 3}: {Policy: types.PolicyAllow},
		{1, 4}: {Policy: types.PolicyAllow},
		{1, 5}: {Policy: types.PolicyToken, Namespace: "email"},
	}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{
		col("note", "text", types.PolicyAllow, 1, 1),
		col("n", "bigint", types.PolicyAllow, 1, 2),
		col("ok", "boolean", types.PolicyAllow, 1, 3),
		col("at", "timestamptz", types.PolicyAllow, 1, 4),
		col("email", "text", types.PolicyToken, 1, 5),
		col("weird", "some_domain", types.PolicyAllow, 0, 0),
	}
	ctx := stmt(t.Context(), "c1", ResolvedParam{Ordinal: 1, Namespace: "email", Policy: types.PolicyToken})
	rows := [][]any{{"hello", int64(7), true, "2026-01-01", "a@b.com", "x"}}
	tr, err := r.Apply(ctx, "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if tr["note"].Policy != types.PolicyRedact || tr["note"].Basis != types.BasisParameter {
		t.Errorf("text column did not inherit: %+v", tr["note"])
	}
	if tr["weird"].Policy != types.PolicyRedact {
		t.Errorf("unknown-typed column did not inherit: %+v", tr["weird"])
	}
	for _, name := range []string{"n", "ok", "at"} {
		if tr[name].Policy != types.PolicyAllow {
			t.Errorf("%s inherited but should not have: %+v", name, tr[name])
		}
	}
	if rows[0][1] != int64(7) || rows[0][2] != true {
		t.Errorf("numeric/boolean cells were masked: %v", rows[0])
	}
	// The §6.2 workflow survives: a token column with its own namespace stays a
	// token, so WHERE email = $1 still returns a usable token.
	if tr["email"].Policy != types.PolicyToken || tr["email"].Namespace != "email" {
		t.Errorf("token column was collapsed by the floor: %+v", tr["email"])
	}
	if _, _, _, ok := ParseToken(rows[0][4].(string)); !ok {
		t.Errorf("token column did not emit a token: %v", rows[0][4])
	}
}

func TestParameterFloorNeverWeakensOrDrops(t *testing.T) {
	cat := fakeCatalog{
		{1, 1}: {Policy: types.PolicyDrop},
		{1, 2}: {Policy: types.PolicyRedact},
	}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{
		col("ssn", "text", types.PolicyDrop, 1, 1),
		col("dob", "text", types.PolicyRedact, 1, 2),
	}
	ctx := stmt(t.Context(), "c1", ResolvedParam{Policy: types.PolicyScan})
	rows := [][]any{{"123-45-6789", "1980-01-01"}}
	tr, err := r.Apply(ctx, "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if tr["ssn"].Policy != types.PolicyDrop || rows[0][0] != nil {
		t.Errorf("drop was weakened: %+v %v", tr["ssn"], rows[0][0])
	}
	if tr["dob"].Policy != types.PolicyRedact {
		t.Errorf("redact was weakened: %+v", tr["dob"])
	}
}

// --- R8.3c collisions -------------------------------------------------------

func TestCollisionRedactsTheCellAndKeepsTheFirstBinding(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyToken, Namespace: "email"}}
	r := newRedactor(t, cat, nil)
	// A 64-bit tag cannot be made to collide by ordinary means. Shorten it —
	// the same thing the keeper_short_token build tag does for tests that run
	// the daemon.
	r.hexLen = 1

	a, b := findCollision(t, r, "email")
	cols := []types.ColumnMeta{col("email", "text", types.PolicyToken, 1, 1)}
	rows := [][]any{{a}, {b}, {a}}
	tr, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	tokenA, ok := rows[0][0].(string)
	if !ok || !strings.HasPrefix(tokenA, TokenOpen) {
		t.Fatalf("first value did not tokenize: %v", rows[0][0])
	}
	if rows[1][0] != RedactedMarker {
		t.Errorf("colliding cell = %v, want %q", rows[1][0], RedactedMarker)
	}
	if rows[2][0] != tokenA {
		t.Errorf("the first binding moved: %v vs %v", rows[2][0], tokenA)
	}
	if tr["email"].Collisions != 1 {
		t.Errorf("collisions = %d, want 1", tr["email"].Collisions)
	}
	// The binding still resolves to the first value, not the second.
	got, _, err := r.Resolve(t.Context(), "s", tokenA)
	if err != nil {
		t.Fatal(err)
	}
	if got != a {
		t.Errorf("token resolves to %q, want the first value %q", got, a)
	}
}

func TestMintReportsCollision(t *testing.T) {
	r := newRedactor(t, fakeCatalog{}, nil)
	r.hexLen = 1
	a, b := findCollision(t, r, "email")
	if _, err := r.Mint(t.Context(), "s", "c1", "email", a); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Mint(t.Context(), "s", "c1", "email", b); err == nil {
		t.Error("Mint accepted a colliding value")
	}
}

// findCollision returns two distinct values whose truncated tags are equal.
func findCollision(t *testing.T, r *Redactor, namespace string) (string, string) {
	t.Helper()
	key, _, err := r.key(t.Context(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for i := range 1000 {
		v := "collide-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
		tg := hmacTag(key, namespace, Normalize(v, r.rule(namespace)), r.hexLen)
		if prev, ok := seen[tg]; ok && prev != v {
			return prev, v
		}
		seen[tg] = v
	}
	t.Fatal("no collision found; the truncation hook is not short enough")
	return "", ""
}

// --- policies ---------------------------------------------------------------

func TestPartialForms(t *testing.T) {
	cases := []struct {
		form types.PartialForm
		in   string
		want string
	}{
		{types.FormEmailDomain, "jane.doe@acme.example", "████@acme.example"},
		{types.FormEmailDomain, "not an email", RedactedMarker},
		{types.FormCardBINLast4, "4532114322333338", "453211██████3338"},
		{types.FormCardBINLast4, "4532 1143 2233 3338", "453211██████3338"},
		{types.FormCardBINLast4, "378282246310005", "378282█████0005"},
		{types.FormCardBINLast4, "123", RedactedMarker},
		{types.FormPhoneCountryArea, "+1-978-555-0134", "+1-978-████"},
		{types.FormPhoneCountryArea, "(978) 555-0134", "+1-978-████"},
		{types.FormPhoneCountryArea, "+44 20 7946 0958", "+44-████"},
		{types.FormPhoneCountryArea, "nope", RedactedMarker},
		{types.FormIPNetwork, "192.168.2.44", "192.168.2.███"},
		{types.FormIPNetwork, "2001:db8:1:2:3:4:5:6", "2001:db8:1::███"},
		{types.FormIPNetwork, "not-an-ip", RedactedMarker},
		{"unknown_form", "anything", RedactedMarker},
	}
	for _, tc := range cases {
		if got := Partial(tc.form, tc.in); got != tc.want {
			t.Errorf("Partial(%s, %q) = %q, want %q", tc.form, tc.in, got, tc.want)
		}
	}
	// Every declared form is implemented.
	for _, f := range types.PartialForms {
		if got := Partial(f, ""); got != RedactedMarker {
			t.Errorf("Partial(%s, \"\") = %q, want a redaction", f, got)
		}
	}
}

func TestPartialAppliedThroughApply(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyPartial, Form: types.FormEmailDomain}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("email", "text", types.PolicyPartial, 1, 1)}
	rows := [][]any{{"jane@acme.example"}}
	tr, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != "████@acme.example" {
		t.Errorf("cell = %v", rows[0][0])
	}
	if tr["email"].Form != types.FormEmailDomain {
		t.Errorf("transform does not declare the form: %+v", tr["email"])
	}
}

func TestScanRedactsSpansNotColumns(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyScan}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("notes", "text", types.PolicyScan, 1, 1)}
	rows := [][]any{
		{"call back about jane@example.com tomorrow"},
		{"no contact details in this one"},
		{"shipping delayed"},
	}
	tr, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	want0 := "call back about " + RedactedSpan("email") + " tomorrow"
	if rows[0][0] != want0 {
		t.Errorf("row 0 = %v, want %v", rows[0][0], want0)
	}
	if rows[1][0] != "no contact details in this one" {
		t.Errorf("row 1 was masked: %v", rows[1][0])
	}
	if rows[2][0] != "shipping delayed" {
		t.Errorf("row 2 was masked: %v", rows[2][0])
	}
	if tr["notes"].SpansRedacted != 1 {
		t.Errorf("spans_redacted = %d, want 1", tr["notes"].SpansRedacted)
	}
	if tr["notes"].Basis != types.BasisRules {
		t.Errorf("basis = %s, want rules", tr["notes"].Basis)
	}
}

func TestScanRecursesIntoJSONLeaves(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyScan}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{col("doc", "jsonb", types.PolicyScan, 1, 1)}
	rows := [][]any{{map[string]any{"note": "ping jane@example.com", "n": float64(3)}}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	m := rows[0][0].(map[string]any)
	if strings.Contains(m["note"].(string), "jane@example.com") {
		t.Errorf("json leaf not scanned: %v", m)
	}
	if m["n"] != float64(3) {
		t.Errorf("non-string leaf was touched: %v", m["n"])
	}
}

func TestDropRemovesTheColumn(t *testing.T) {
	cat := fakeCatalog{{1, 1}: {Policy: types.PolicyDrop}, {1, 2}: {Policy: types.PolicyAllow}}
	r := newRedactor(t, cat, nil)
	cols := []types.ColumnMeta{
		col("ssn", "text", types.PolicyDrop, 1, 1),
		col("id", "bigint", types.PolicyAllow, 1, 2),
	}
	rows := [][]any{{"123-45-6789", int64(1)}}
	tr, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != nil {
		t.Errorf("dropped cell survived Apply: %v", rows[0][0])
	}
	outCols, outRows := DropColumns(cols, rows, tr)
	if len(outCols) != 1 || outCols[0].Name != "id" {
		t.Fatalf("columns = %v", outCols)
	}
	if len(outRows[0]) != 1 || outRows[0][0] != int64(1) {
		t.Fatalf("rows = %v", outRows)
	}
	if tr["ssn"].Policy != types.PolicyDrop {
		t.Errorf("transform should still explain the absence: %+v", tr["ssn"])
	}
}

func TestUnknownColumnRedacts(t *testing.T) {
	// R5.4a: a column appearing at query time on a catalogued relation, with
	// nobody reviewing it, redacts and the statement still runs.
	r := newRedactor(t, fakeCatalog{}, nil)
	cols := []types.ColumnMeta{col("new_col", "text", types.PolicyRedact, 9, 3)}
	rows := [][]any{{"secret"}}
	tr, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != RedactedMarker {
		t.Errorf("cell = %v", rows[0][0])
	}
	if tr["new_col"].Basis != types.BasisUnknown {
		t.Errorf("basis = %s, want unknown", tr["new_col"].Basis)
	}
}

// --- sessions ---------------------------------------------------------------

func TestResolveAndDropSession(t *testing.T) {
	r := newRedactor(t, fakeCatalog{}, nil)
	tok, err := r.Mint(t.Context(), "s1", "c1", "email", "  jane@example.com ")
	if err != nil {
		t.Fatal(err)
	}
	v, p, err := r.Resolve(t.Context(), "s1", tok)
	if err != nil {
		t.Fatal(err)
	}
	if v != "jane@example.com" {
		t.Errorf("Resolve = %q, want the trimmed value", v)
	}
	if p != types.PolicyToken {
		t.Errorf("policy = %s, want token", p)
	}
	// A different session cannot resolve it: the map is per session (§8.4).
	if _, _, err := r.Resolve(t.Context(), "s2", tok); err == nil {
		t.Error("a different session resolved the token")
	}
	r.DropSession("s1")
	if _, _, err := r.Resolve(t.Context(), "s1", tok); err == nil {
		t.Error("DropSession did not clear the map")
	}
	if _, _, err := r.Resolve(t.Context(), "s1", "not a token"); err == nil {
		t.Error("a non-token resolved")
	}
}

func TestBindingsExpire(t *testing.T) {
	clock := time.Now()
	r, err := New(Config{
		Keys:     fakeKeys{version: 1},
		Detector: rules.New(rules.Config{}),
		TTL:      time.Minute,
		Now:      func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := r.Mint(t.Context(), "s", "c1", "email", "jane@example.com")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, _, err := r.Resolve(t.Context(), "s", tok); err == nil {
		t.Error("an expired binding resolved")
	}
	// And it no longer participates in the emission scan.
	cols := []types.ColumnMeta{col("x", "text", types.PolicyAllow, 0, 0)}
	rows := [][]any{{"jane@example.com"}}
	if _, err := r.Apply(stmt(t.Context(), "c1"), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != "jane@example.com" {
		t.Errorf("expired binding still rewrote a cell: %v", rows[0][0])
	}
}

func TestNamespaceValidation(t *testing.T) {
	r := newRedactor(t, fakeCatalog{}, nil)
	for _, ns := range []string{"", "email1", "a:b", "a" + TokenOpen} {
		if _, err := r.Mint(t.Context(), "s", "c1", ns, "v"); err == nil {
			t.Errorf("Mint accepted namespace %q", ns)
		}
	}
}

func TestApplyWithoutStatementContextStillScans(t *testing.T) {
	r := newRedactor(t, fakeCatalog{}, nil)
	tok := mintedSession(t, r, "s", "jane@example.com")
	cols := []types.ColumnMeta{col("x", "text", types.PolicyAllow, 0, 0)}
	rows := [][]any{{"jane@example.com"}}
	if _, err := r.Apply(t.Context(), "s", cols, rows); err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != tok {
		t.Errorf("emission scan did not run without a statement context: %v", rows[0][0])
	}
	// But a token policy without a connection is an error, not a silent pass.
	r2 := newRedactor(t, fakeCatalog{{1, 1}: {Policy: types.PolicyToken, Namespace: "email"}}, nil)
	tokenCols := []types.ColumnMeta{col("e", "text", types.PolicyToken, 1, 1)}
	if _, err := r2.Apply(t.Context(), "s", tokenCols, [][]any{{"x"}}); err == nil {
		t.Error("tokenizing without a connection should be an error")
	}
}

func TestFamily(t *testing.T) {
	cases := map[string]bool{ // type name -> is text-like
		"text": true, "varchar": true, "varchar(64)": true, "character varying": true,
		"json": true, "jsonb": true, "xml": true, "citext": true, "name": true,
		"text[]": true, "_text": true,
		"bigint": false, "int4": false, "numeric(10,2)": false, "double precision": false,
		"boolean": false, "timestamptz": false, "date": false, "interval": false, "uuid": false,
		"bigint[]": false,
		"my_enum":  true, "bytea": true, "": true, // unknown inherits: the safe direction
	}
	for typ, want := range cases {
		if got := TextLike(Family(typ)); got != want {
			t.Errorf("TextLike(Family(%q)) = %v, want %v", typ, got, want)
		}
	}
}
