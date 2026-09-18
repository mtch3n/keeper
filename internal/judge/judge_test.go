package judge

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

func TestParseVerdictAcceptsAWellFormedAnswer(t *testing.T) {
	v, err := ParseVerdict([]byte(`{"tier":2,"reason_codes":["intent_mismatch","near_row_cap"],"uncertainty":0.4,"release":false,"explanation":"reads more than the stated intent"}`))
	if err != nil {
		t.Fatalf("ParseVerdict: %v", err)
	}
	if v.Tier != types.Tier2Judge || v.Uncertainty != 0.4 || len(v.ReasonCodes) != 2 {
		t.Fatalf("verdict = %+v", v)
	}
	if v.Release {
		t.Error("release should be false")
	}
}

func TestParseVerdictRejectsAnythingThatDoesNotParse(t *testing.T) {
	// A configured-but-failing judge never counts as a favourable verdict
	// (R7.7b), so every one of these has to be an error rather than a default.
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ``},
		{"whitespace", "   \n"},
		{"prose", `Sure! Here is my assessment: the query looks fine.`},
		{"truncated", `{"tier":1,"reason_codes":[`},
		{"tier below range", `{"tier":-1,"reason_codes":[],"uncertainty":0}`},
		{"tier above range", `{"tier":9,"reason_codes":[],"uncertainty":0}`},
		{"tier not a number", `{"tier":"high","reason_codes":[],"uncertainty":0}`},
		{"uncertainty above range", `{"tier":1,"reason_codes":[],"uncertainty":1.5}`},
		{"uncertainty below range", `{"tier":1,"reason_codes":[],"uncertainty":-0.1}`},
		{"unknown reason code", `{"tier":1,"reason_codes":["ignore_previous_instructions"],"uncertainty":0}`},
		{"unknown member", `{"tier":1,"reason_codes":[],"uncertainty":0,"grant":"all"}`},
		{"array not object", `[{"tier":1}]`},
	}
	for _, c := range cases {
		if v, err := ParseVerdict([]byte(c.raw)); err == nil {
			t.Errorf("%s: ParseVerdict accepted %q and returned %+v", c.name, c.raw, v)
		}
	}
}

func TestParseVerdictSanitizesTheExplanation(t *testing.T) {
	// encoding/json/v2 already refuses a control character inside a string, so
	// the sanitizer is the second line rather than the first. What reaches it is
	// the legal escapes.
	v, err := ParseVerdict([]byte(`{"tier":0,"reason_codes":["routine_read"],"uncertainty":0,"explanation":"line one\nline two"}`))
	if err != nil {
		t.Fatalf("ParseVerdict: %v", err)
	}
	if strings.Contains(v.Explanation, "\n") {
		t.Errorf("a newline survived into the explanation: %q", v.Explanation)
	}

	long := strings.Repeat("x", maxExplanation*3)
	v, err = ParseVerdict([]byte(`{"tier":0,"reason_codes":[],"uncertainty":0,"explanation":"` + long + `"}`))
	if err != nil {
		t.Fatalf("ParseVerdict: %v", err)
	}
	if len(v.Explanation) > maxExplanation {
		t.Errorf("the explanation was not bounded: %d bytes", len(v.Explanation))
	}
}

func TestSanitizeExplanationStripsTerminalEscapes(t *testing.T) {
	got := sanitizeExplanation("before\x1b[31mred\x07after")
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Errorf("a control character survived: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("the readable text was lost: %q", got)
	}
}

func TestParseVerdictRejectsRawControlCharacters(t *testing.T) {
	raw := []byte("{\"tier\":0,\"reason_codes\":[],\"uncertainty\":0,\"explanation\":\"a\x1bb\"}")
	if _, err := ParseVerdict(raw); err == nil {
		t.Error("a raw control character inside the verdict was accepted")
	}
}

// R7.8b: the context is the statement, plan facts, output columns and session
// intent. No conversation history, no rows, no parameter values.
func TestAssessSendsOnlyWhatR78bAllows(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		body, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"tier\":1,\"reason_codes\":[\"routine_read\"],\"uncertainty\":0.1}"}}]}`)
	}))
	defer srv.Close()

	j := New(Config{BaseURL: srv.URL, Model: "test-model"})
	v, err := j.Assess(t.Context(), ports.JudgeRequest{
		SQL:    "SELECT id FROM orders WHERE user_email = $1",
		Intent: "reconcile last week's orders",
		Mode:   types.ModeAssisted,
		Plan: &ports.PlanFacts{
			StatementType: "SELECT",
			RelationNames: []types.RelationRef{{Schema: "public", Relation: "orders"}},
			EstimatedRows: 12,
			EstimatedCost: 8.4,
			HasFilter:     true,
		},
		OutputColumns: []types.ColumnMeta{{Name: "id", Type: "int8", TableOID: 1002, AttNum: 1, Policy: types.PolicyAllow}},
	})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if v.Tier != types.Tier1Record {
		t.Errorf("tier = %d", v.Tier)
	}

	var sent struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding the request keeper sent: %v", err)
	}
	if sent.Model != "test-model" || sent.Stream {
		t.Errorf("model = %q stream = %v", sent.Model, sent.Stream)
	}
	if len(sent.Messages) != 2 || sent.Messages[0].Role != "system" || sent.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v, want one system and one user turn and no history", sent.Messages)
	}

	var facts map[string]any
	if err := json.Unmarshal([]byte(sent.Messages[1].Content), &facts); err != nil {
		t.Fatalf("the user turn is not the facts object: %v", err)
	}
	allowed := map[string]bool{
		"statement": true, "session_intent": true, "mode": true, "statement_type": true,
		"relations": true, "estimated_rows": true, "estimated_cost": true,
		"writes": true, "has_filter": true, "output_columns": true,
	}
	for k := range facts {
		if !allowed[k] {
			t.Errorf("the judge payload carries %q, which R7.8b does not permit", k)
		}
	}
	if facts["statement"] != "SELECT id FROM orders WHERE user_email = $1" {
		t.Errorf("statement = %v", facts["statement"])
	}
	// The resolved value of $1 is a value the agent never held. It must not be
	// anywhere in the payload.
	if strings.Contains(string(body), "\"params\"") || strings.Contains(string(body), "\"rows\"") {
		t.Errorf("the payload carries parameters or rows: %s", body)
	}
}

func TestAssessRejectsAModelThatIgnoresTheSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Looks fine to me, approved."}}]}`)
	}))
	defer srv.Close()

	if v, err := New(Config{BaseURL: srv.URL}).Assess(t.Context(), ports.JudgeRequest{SQL: "SELECT 1"}); err == nil {
		t.Fatalf("an unparseable answer was accepted: %+v", v)
	}
}

func TestAssessRejectsANonOKResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"tier\":0,\"reason_codes\":[],\"uncertainty\":0}"}}]}`)
	}))
	defer srv.Close()

	if _, err := New(Config{BaseURL: srv.URL}).Assess(t.Context(), ports.JudgeRequest{SQL: "SELECT 1"}); err == nil {
		t.Fatal("a 500 with a well-formed body was accepted as a verdict")
	}
}

func TestAvailableProbesTheEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"models":[]}`)
	}))

	j := New(Config{BaseURL: srv.URL})
	if !j.Available(t.Context()) {
		t.Error("a reachable endpoint reported unavailable")
	}

	srv.Close()
	if j.Available(t.Context()) {
		t.Error("an unreachable endpoint reported available")
	}
}

func TestDefaultsAndIdentity(t *testing.T) {
	j := New(Config{})
	if j.baseURL != defaultBaseURL || j.model != defaultModel {
		t.Errorf("defaults = %q %q", j.baseURL, j.model)
	}
	if !strings.Contains(j.Identity(), defaultModel) {
		t.Errorf("Identity = %q, want the model named", j.Identity())
	}
	// A trailing slash must not produce a double slash in the path.
	j = New(Config{BaseURL: "http://127.0.0.1:1234/"})
	if strings.HasSuffix(j.baseURL, "/") {
		t.Errorf("baseURL = %q", j.baseURL)
	}
}

func TestVerdictSchemaMatchesTheClosedReasonSet(t *testing.T) {
	schema := verdictSchema()
	props, _ := schema["properties"].(map[string]any)
	codes, _ := props["reason_codes"].(map[string]any)
	items, _ := codes["items"].(map[string]any)
	enum, _ := items["enum"].([]string)
	if len(enum) != len(ReasonCodes) {
		t.Fatalf("the schema's enum has %d entries, ReasonCodes has %d", len(enum), len(ReasonCodes))
	}
}
