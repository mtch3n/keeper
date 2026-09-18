package rules

import (
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
)

func spanTexts(text string, spans []ports.Span) map[string][]string {
	out := map[string][]string{}
	for _, s := range spans {
		out[s.Type] = append(out[s.Type], text[s.Start:s.End])
	}
	return out
}

func TestDetectorFindsValidatedIdentifiers(t *testing.T) {
	d := New(Config{})
	text := "contact jane.doe@example.com or 4111 1111 1111 1111, iban GB82WEST12345698765432, " +
		"ip 192.168.2.44, ssn 078-05-1120, tel +1 978 555 0134, id 0198f3c1-3b2a-7c4d-8e9f-0a1b2c3d4e5f, " +
		"see https://example.com/orders?id=9"
	spans, err := d.Scan(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	got := spanTexts(text, spans)
	want := map[string]string{
		TypeEmail: "jane.doe@example.com",
		TypeCard:  "4111 1111 1111 1111",
		TypeIBAN:  "GB82WEST12345698765432",
		TypeIP:    "192.168.2.44",
		TypeUSSSN: "078-05-1120",
		TypeUUID:  "0198f3c1-3b2a-7c4d-8e9f-0a1b2c3d4e5f",
	}
	for typ, w := range want {
		if len(got[typ]) == 0 {
			t.Errorf("%s: no span, want %q (all: %v)", typ, w, got)
			continue
		}
		if got[typ][0] != w {
			t.Errorf("%s: span %q, want %q", typ, got[typ][0], w)
		}
	}
	if len(got[TypePhone]) == 0 {
		t.Errorf("phone: no span (all: %v)", got)
	}
	if len(got[TypeURL]) == 0 {
		t.Errorf("url: no span (all: %v)", got)
	}
}

func TestDetectorSpansDoNotOverlap(t *testing.T) {
	d := New(Config{})
	text := "mail to jane.doe@example.com and browse https://example.com/x?e=a@b.co now"
	spans, err := d.Scan(t.Context(), text)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(spans); i++ {
		if spans[i].Start < spans[i-1].End {
			t.Fatalf("overlapping spans: %+v and %+v", spans[i-1], spans[i])
		}
	}
}

func TestDetectorRejectsNearMisses(t *testing.T) {
	d := New(Config{DisableNames: true})
	for _, s := range []string{
		"card 4111111111111112 declined",  // fails Luhn
		"iban GB82WEST12345698765433 bad", // fails MOD-97
		"ssn 000-12-3456",                 // never-issued area
		"version 1.2.3.400 released",      // not an address
	} {
		spans, err := d.Scan(t.Context(), s)
		if err != nil {
			t.Fatal(err)
		}
		for _, sp := range spans {
			if sp.Type == TypeCard || sp.Type == TypeIBAN || sp.Type == TypeUSSSN || sp.Type == TypeIP {
				t.Errorf("%q: unexpected %s span %q", s, sp.Type, s[sp.Start:sp.End])
			}
		}
	}
}

func TestDetectorNames(t *testing.T) {
	d := New(Config{})
	spans, err := d.Scan(t.Context(), "spoke to Jane Doe about the refund")
	if err != nil {
		t.Fatal(err)
	}
	got := spanTexts("spoke to Jane Doe about the refund", spans)
	if len(got[TypePersonName]) != 1 || got[TypePersonName][0] != "Jane Doe" {
		t.Fatalf("person_name spans = %v, want [Jane Doe]", got[TypePersonName])
	}

	// A lone dictionary word is below the threshold and must not fire: "rose"
	// and "mark" are ordinary English. (A *pair* of them would fire, and
	// "Rose May" is exactly why: the dictionary layer cannot tell a person from
	// a sentence that reads like one. That is a false positive, which redacts a
	// span that did not need it, and is the direction this layer is allowed to
	// be wrong in.)
	const lone = "a rose in the garden"
	spans, err = d.Scan(t.Context(), lone)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 0 {
		t.Fatalf("lone dictionary words produced spans: %v", spanTexts(lone, spans))
	}

	// A name inside an email belongs to the email span, not to two name spans.
	const s = "jane.doe@example.com"
	spans, err = d.Scan(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].Type != TypeEmail || spans[0].Start != 0 || spans[0].End != len(s) {
		t.Fatalf("spans = %v, want one email covering the whole value", spanTexts(s, spans))
	}
}

func TestDetectorIdentity(t *testing.T) {
	id := New(Config{}).Identity()
	if id.NetworkPosture != "none" {
		t.Errorf("network posture = %q, want none", id.NetworkPosture)
	}
	if id.Name == "" || id.Version == "" {
		t.Errorf("identity = %+v, want name and version", id)
	}
}

func TestMatchRate(t *testing.T) {
	d := New(Config{DisableNames: true})
	emails := []string{"a@b.com", "c@d.org", "e@f.net", "not an email", "g@h.io"}
	got := d.MatchRate(emails)
	if got[TypeEmail] != 0.8 {
		t.Errorf("email rate = %v, want 0.8", got[TypeEmail])
	}
	if got[TypeCard] != 0 {
		t.Errorf("card rate = %v, want 0", got[TypeCard])
	}
	if len(d.MatchRate(nil)) != 0 {
		t.Error("MatchRate(nil) should be empty")
	}
}

func TestNameHeuristic(t *testing.T) {
	hits := map[string]string{
		"email": TypeEmail, "user_email": TypeEmail, "emailAddress": TypeEmail,
		"first_name": TypePersonName, "surname": TypePersonName, "name": TypePersonName,
		"customer_name": TypePersonName, "fname": TypePersonName,
		"ssn": TypeUSSSN, "ssn_num": TypeUSSSN,
		"phone_number": TypePhone, "mobile": TypePhone,
		"ip_address": TypeIP, "client_ip": TypeIP,
		"card_number": TypeCard, "iban": TypeIBAN,
		"street2": TypeAddress, "postal_code": TypeAddress,
		"date_of_birth": TypeDOB,
	}
	for col, want := range hits {
		got, ok := NameHeuristic(col)
		if !ok || got != want {
			t.Errorf("NameHeuristic(%q) = %q,%v; want %q", col, got, ok, want)
		}
	}
	misses := []string{
		"filename", "file_name", "hostname", "host_name", "table_name", "column_name",
		"product_name", "company_name", "zip_file", "recipient_count", "description",
		"id", "created_at", "status", "shipment", "", "type_name",
	}
	for _, col := range misses {
		if got, ok := NameHeuristic(col); ok {
			t.Errorf("NameHeuristic(%q) = %q, want no match", col, got)
		}
	}
}

func TestCoverageIsPublished(t *testing.T) {
	cov := Coverage()
	if len(cov) < len(Types) {
		t.Fatalf("coverage lists %d types, Types has %d", len(cov), len(Types))
	}
	seen := map[string]bool{}
	for _, e := range cov {
		if e.Type == "" || e.Validation == "" {
			t.Errorf("incomplete coverage entry %+v", e)
		}
		seen[e.Type] = true
	}
	for _, typ := range Types {
		if !seen[typ] {
			t.Errorf("type %q emitted but absent from Coverage()", typ)
		}
	}
	if !strings.Contains(UndetectedNotice, "undetected rather than absent") {
		t.Error("UndetectedNotice must state the §8.5.1 sentence")
	}
	if len(Uncovered()) == 0 {
		t.Error("Uncovered() must name the known gaps")
	}
}

func TestDictionaryLoading(t *testing.T) {
	d, err := LoadDictionary("test", strings.NewReader("# comment\nAlfred\n\nbeatrix\n"), strings.NewReader("Zimmermann\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Given(); len(got) != 2 || got[0] != "alfred" || got[1] != "beatrix" {
		t.Errorf("given = %v", got)
	}
	if got := d.Family(); len(got) != 1 || got[0] != "zimmermann" {
		t.Errorf("family = %v", got)
	}
	det := New(Config{Dictionary: d})
	const s = "ref alfred zimmermann here"
	spans, err := det.Scan(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || s[spans[0].Start:spans[0].End] != "alfred zimmermann" {
		t.Fatalf("spans = %v", spanTexts(s, spans))
	}
	if _, err := LoadDictionary("empty", strings.NewReader(""), nil); err == nil {
		t.Error("an empty dictionary should be an error")
	}
}
