package catalog

import (
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func hitsAndMisses(hits, misses int) []string {
	out := make([]string, 0, hits+misses)
	for range hits {
		out = append(out, "HIT")
	}
	for range misses {
		out = append(out, "MISS")
	}
	return out
}

func nineDigitSamples(n int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = "123456789"
	}
	return out
}

func testRelation() ports.Relation {
	return ports.Relation{
		Ref:  types.RelationRef{Schema: "public", Relation: "users"},
		OID:  100,
		Kind: 'r',
		Columns: []ports.Column{
			{Name: "id", AttNum: 1, TypeOID: 23, IsPK: true},                // int4, PK: typed scalar
			{Name: "email", AttNum: 2, TypeOID: 25},                         // text, name heuristic match
			{Name: "contact", AttNum: 3, TypeOID: 25},                       // text, mis-named, sampled 95%
			{Name: "notes", AttNum: 4, TypeOID: 25},                         // text, sampled 10%
			{Name: "fname", AttNum: 5, TypeOID: 25},                         // text, sampled 0%
			{Name: "account_number", AttNum: 6, TypeOID: 20},                // bigint, non-key, 9-digit 100%
			{Name: "employee_id", AttNum: 7, TypeOID: 20, IsIdentity: true}, // bigint, identity, 9-digit 100%
			{Name: "metadata", AttNum: 8, TypeOID: 3802},                    // jsonb
		},
	}
}

func testInitDeps() Dependencies {
	return Dependencies{
		Introspector: &fakeIntrospector{relations: []ports.Relation{testRelation()}},
		Sampler: &fakeSampler{values: map[string][]string{
			"public.users.contact":        hitsAndMisses(19, 1), // 95%
			"public.users.notes":          hitsAndMisses(1, 9),  // 10%
			"public.users.fname":          hitsAndMisses(0, 10), // 0%
			"public.users.account_number": nineDigitSamples(10), // 100%, non-key
			"public.users.employee_id":    nineDigitSamples(10), // 100%, identity (key)
		}},
		Names: &fakeNames{matches: map[string]string{"email": "email"}},
		Rules: fakeRuleMatcher{},
	}
}

func TestInitTypeDefaults(t *testing.T) {
	s, _ := openTestStore(t, testInitDeps(), nil)
	proposal, err := s.Init(t.Context(), "conn1", 0)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	if got := proposal.SafeToBulkAccept["public.users.id"]; got.Policy != types.PolicyAllow {
		t.Errorf("typed scalar id: got %+v, want allow", got)
	}
	if got := proposal.SafeToBulkAccept["public.users.email"]; got.Policy != types.PolicyToken || got.Namespace != "email" {
		t.Errorf("name-heuristic email: got %+v, want token/email", got)
	}
	if got, ok := proposal.NeedsReview["public.users.metadata"]; !ok || got.Policy != types.PolicyScan {
		t.Errorf("jsonb metadata: got %+v (ok=%v), want scan in NeedsReview", got, ok)
	}
	// With sample == 0, no sampling runs at all: an unnamed text column is a
	// plain scan default, landing in NeedsReview. SPEC's "cannot find them"
	// review task.
	if got, ok := proposal.NeedsReview["public.users.contact"]; !ok || got.Policy != types.PolicyScan {
		t.Errorf("contact with sample=0: got %+v (ok=%v), want scan in NeedsReview", got, ok)
	}
	if _, ok := proposal.SampleRates["public.users.contact"]; ok {
		t.Errorf("sample=0 must not record a sample rate for contact")
	}
}

func TestInitSamplingThresholdBands(t *testing.T) {
	s, _ := openTestStore(t, testInitDeps(), nil)
	proposal, err := s.Init(t.Context(), "conn1", 20)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// >=90%: promoted to token, namespace from the matching rule.
	got, ok := proposal.SafeToBulkAccept["public.users.contact"]
	if !ok || got.Policy != types.PolicyToken || got.Namespace != "email" {
		t.Errorf("contact at 95%%: got %+v (ok=%v), want token/email in SafeToBulkAccept", got, ok)
	}
	if rate := proposal.SampleRates["public.users.contact"]; rate < 0.9 {
		t.Errorf("contact sample rate = %v, want >= 0.9", rate)
	}

	// 0% < rate < 90%: stays scan, rate reported. Mirrors the measured
	// notes/memo bands from SPEC R5.3a — raising these would mask whole rows.
	got, ok = proposal.NeedsReview["public.users.notes"]
	if !ok || got.Policy != types.PolicyScan {
		t.Errorf("notes at 10%%: got %+v (ok=%v), want scan in NeedsReview", got, ok)
	}
	if rate := proposal.SampleRates["public.users.notes"]; rate <= 0 || rate >= 0.9 {
		t.Errorf("notes sample rate = %v, want strictly between 0 and 0.9", rate)
	}

	// 0%: stays scan.
	got, ok = proposal.NeedsReview["public.users.fname"]
	if !ok || got.Policy != types.PolicyScan {
		t.Errorf("fname at 0%%: got %+v (ok=%v), want scan in NeedsReview", got, ok)
	}
}

func TestInitNumericKeyVsNonKeySplit(t *testing.T) {
	s, _ := openTestStore(t, testInitDeps(), nil)
	proposal, err := s.Init(t.Context(), "conn1", 20)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// account_number: 9-digit at 100%, not a PK/FK/identity/defaulted column
	// -> promoted to token. This is the accounts.ssn_num side of R5.3b.
	got, ok := proposal.SafeToBulkAccept["public.users.account_number"]
	if !ok || got.Policy != types.PolicyToken || got.Namespace != "ssn" {
		t.Errorf("account_number: got %+v (ok=%v), want token/ssn", got, ok)
	}
	if rate := proposal.SampleRates["public.users.account_number"]; rate != 1 {
		t.Errorf("account_number sample rate = %v, want 1.0", rate)
	}

	// employee_id: same 9-digit shape, but it's an identity column -> report
	// only. It must stay allow, the typed-scalar default. This is the
	// accounts.account_number side of R5.3b: identical content, different key
	// status, different outcome.
	got, ok = proposal.SafeToBulkAccept["public.users.employee_id"]
	if !ok || got.Policy != types.PolicyAllow {
		t.Errorf("employee_id: got %+v (ok=%v), want allow (report only)", got, ok)
	}
	if _, inReview := proposal.NeedsReview["public.users.employee_id"]; inReview {
		t.Errorf("employee_id must not move to NeedsReview")
	}
	if rate := proposal.SampleRates["public.users.employee_id"]; rate != 1 {
		t.Errorf("employee_id sample rate = %v, want 1.0 (reported even though report-only)", rate)
	}
}

func TestInitSkipsSamplingWithoutDependencies(t *testing.T) {
	deps := testInitDeps()
	deps.Sampler = nil
	deps.Rules = nil
	s, _ := openTestStore(t, deps, nil)

	proposal, err := s.Init(t.Context(), "conn1", 20)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got, ok := proposal.NeedsReview["public.users.contact"]; !ok || got.Policy != types.PolicyScan {
		t.Errorf("without a Sampler/Rules, contact must fall back to scan: got %+v (ok=%v)", got, ok)
	}
	if len(proposal.SampleRates) != 0 {
		t.Errorf("without a Sampler/Rules, no sample rates should be recorded: %+v", proposal.SampleRates)
	}
}

func TestInitProposalKeysUseDottedForm(t *testing.T) {
	s, _ := openTestStore(t, testInitDeps(), nil)
	proposal, err := s.Init(t.Context(), "conn1", 0)
	if err != nil {
		t.Fatal(err)
	}
	for key := range proposal.SafeToBulkAccept {
		if strings.Count(key, ".") != 2 {
			t.Errorf("SafeToBulkAccept key %q is not schema.table.column", key)
		}
	}
	for key := range proposal.NeedsReview {
		if strings.Count(key, ".") != 2 {
			t.Errorf("NeedsReview key %q is not schema.table.column", key)
		}
	}
}
