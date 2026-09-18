// Package judge is SPEC §7.8's local model.
//
// keeperd calls it directly, against any local endpoint speaking the
// OpenAI-compatible chat API, or a sidecar under §8.5.2's no-network rule. It is never
// reached through the harness: the only channel an MCP server has to the harness
// model is sampling/createMessage, which routes through the agent's client — the
// untrusted party — so asking the agent's own client to arrange the check on the
// agent's own statement is not an independent check (R7.8c).
//
// What is sent is the closed list of R7.8b: the statement, plan facts, output
// columns and session intent. No conversation history, no rows, no parameter
// values — a token-resolved parameter is a value the agent never held, and
// judging the resolved statement anywhere else would disclose it.
//
// What comes back is data. It is validated against ports.JudgeVerdict's schema
// and rejected if it does not parse; a configured-but-failing judge never counts
// as a favourable verdict (R7.7b), so strictness here costs availability and
// never safety.
package judge

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Config points the judge at a local model.
type Config struct {
	// BaseURL is the endpoint root. Zero means http://127.0.0.1:8080, which is
	// llama.cpp's llama-server default.
	//
	// The wire format is the OpenAI chat API rather than any one runtime's own,
	// because every local runtime speaks it — llama.cpp, LM Studio, vLLM and
	// Ollama all serve /v1/chat/completions — and keeper then has no opinion
	// about which one is running. A format tied to one runtime would make that
	// runtime a dependency, and §8.5.2 exists precisely so it is not.
	BaseURL string
	// Model is the model tag. Zero means qwen2.5-coder:7b.
	Model string
	// Timeout bounds one assessment. Zero means 20s.
	Timeout time.Duration
	// ProbeTimeout bounds Available. Zero means 2s.
	ProbeTimeout time.Duration
	// Client is the HTTP client. Zero means a client with Timeout.
	Client *http.Client
}

// Judge implements ports.Judge.
type Judge struct {
	baseURL      string
	model        string
	timeout      time.Duration
	probeTimeout time.Duration
	client       *http.Client
}

const (
	defaultBaseURL      = "http://127.0.0.1:8080"
	defaultModel        = "qwen2.5-coder:7b"
	defaultTimeout      = 20 * time.Second
	defaultProbeTimeout = 2 * time.Second
	// maxBody bounds what is read back. A local model that streams forever must
	// not become a memory problem.
	maxBody = 1 << 20
	// maxExplanation bounds the one free-text field a verdict may carry. It is
	// shown to humans labelled as a model suggestion (R7.8d), never trusted.
	maxExplanation = 600
)

// New builds a judge. It performs no I/O: an endpoint that is not running yet is
// discovered by Available, not by the constructor.
func New(cfg Config) *Judge {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	probe := cfg.ProbeTimeout
	if probe <= 0 {
		probe = defaultProbeTimeout
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel
	}
	return &Judge{
		baseURL:      strings.TrimRight(base, "/"),
		model:        model,
		timeout:      timeout,
		probeTimeout: probe,
		client:       client,
	}
}

var _ ports.Judge = (*Judge)(nil)

// Identity reports what assessed a statement, so the audit log and doctor can
// say which model produced a verdict.
func (j *Judge) Identity() string { return "ollama/" + j.model + " @ " + j.baseURL }

// Available probes the endpoint. An unreachable judge means the caller uses a
// masked fallback, never a favourable verdict.
func (j *Judge) Available(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, j.probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.baseURL+"/v1/models", nil)
	if err != nil {
		return false
	}
	resp, err := j.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	return resp.StatusCode == http.StatusOK
}

// Assess returns a validated structured verdict, or an error. It never returns a
// partially trusted answer: anything that fails validation is an error, and the
// pipeline treats an error as "no verdict".
func (j *Judge) Assess(ctx context.Context, req ports.JudgeRequest) (*ports.JudgeVerdict, error) {
	ctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()

	body, err := json.Marshal(j.chatRequest(req))
	if err != nil {
		return nil, fmt.Errorf("judge: encoding request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, j.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("judge: building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := j.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("judge: endpoint unreachable: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("judge: reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("judge: endpoint returned %d", resp.StatusCode)
	}

	var chat chatResponse
	if err := json.Unmarshal(raw, &chat); err != nil {
		return nil, fmt.Errorf("judge: response is not the expected shape: %w", err)
	}
	return ParseVerdict([]byte(chat.content()))
}

// ParseVerdict validates a model's answer against ports.JudgeVerdict's schema.
// It is exported because it is the whole trust boundary: everything the model
// says passes through here and nothing else.
func ParseVerdict(raw []byte) (*ports.JudgeVerdict, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("judge: empty verdict")
	}

	var v ports.JudgeVerdict
	if err := json.Unmarshal(trimmed, &v, json.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("judge: verdict does not parse: %w", err)
	}

	if v.Tier < types.Tier0Run || v.Tier > types.Tier4Refuse {
		return nil, fmt.Errorf("judge: verdict tier %d is out of range", int(v.Tier))
	}
	if math.IsNaN(v.Uncertainty) || v.Uncertainty < 0 || v.Uncertainty > 1 {
		return nil, fmt.Errorf("judge: verdict uncertainty is out of range")
	}
	for _, code := range v.ReasonCodes {
		if !slices.Contains(ReasonCodes, code) {
			// A reason code outside the closed set is a model that did not
			// follow the schema. Rejecting costs a masked fallback; accepting
			// would put unvalidated model text into approval facts and the audit
			// log.
			return nil, fmt.Errorf("judge: unknown reason code")
		}
	}
	v.Explanation = sanitizeExplanation(v.Explanation)
	return &v, nil
}

// ReasonCodes is the closed set a verdict may use. SPEC R7.8d: reason codes are
// structured output, and the free-text explanation beside them is a model
// suggestion rather than an approval fact.
var ReasonCodes = []string{
	"intent_mismatch",
	"unusually_broad_read",
	"near_row_cap",
	"sensitive_relation",
	"view_definition_changed",
	"computed_output_uncertain",
	"narrower_query_available",
	"routine_read",
}

// sanitizeExplanation bounds the one free-text field and strips control
// characters, so a model cannot smuggle terminal escapes onto a human's screen.
func sanitizeExplanation(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxExplanation {
		s = s[:maxExplanation]
	}
	return s
}

// --- the wire shapes -------------------------------------------------------

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	// Temperature is zero so the same statement gets the same verdict. A judge
	// that answers differently on a retry is not a gate.
	Temperature float64 `json:"temperature"`
	// ResponseFormat asks for JSON matching the verdict schema. Runtimes that
	// do not implement it ignore it, and ParseVerdict rejects whatever comes
	// back instead — strictness costs availability, never safety.
	ResponseFormat *responseFormat `json:"response_format,omitzero"`
}

type responseFormat struct {
	Type       string         `json:"type"`
	JSONSchema map[string]any `json:"json_schema,omitzero"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// content is the model's answer, or empty when the endpoint returned no choice.
func (c chatResponse) content() string {
	if len(c.Choices) == 0 {
		return ""
	}
	return c.Choices[0].Message.Content
}

// facts is the whole of what R7.8b permits, and it is built field by field
// rather than by marshalling a request struct, so nothing can be added to the
// payload by accident.
type facts struct {
	Statement     string        `json:"statement"`
	SessionIntent string        `json:"session_intent,omitzero"`
	Mode          string        `json:"mode"`
	StatementType string        `json:"statement_type,omitzero"`
	Relations     []string      `json:"relations,omitzero"`
	EstimatedRows int64         `json:"estimated_rows"`
	EstimatedCost float64       `json:"estimated_cost"`
	Writes        bool          `json:"writes"`
	HasFilter     bool          `json:"has_filter"`
	OutputColumns []factsColumn `json:"output_columns,omitzero"`
}

type factsColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Policy   string `json:"policy,omitzero"`
	Computed bool   `json:"computed,omitzero"`
}

func (j *Judge) chatRequest(req ports.JudgeRequest) chatRequest {
	f := facts{
		Statement:     req.SQL,
		SessionIntent: req.Intent,
		Mode:          string(req.Mode),
	}
	if req.Plan != nil {
		f.StatementType = req.Plan.StatementType
		f.EstimatedRows = req.Plan.EstimatedRows
		f.EstimatedCost = req.Plan.EstimatedCost
		f.Writes = req.Plan.Writes
		f.HasFilter = req.Plan.HasFilter
		for _, r := range req.Plan.RelationNames {
			f.Relations = append(f.Relations, r.String())
		}
	}
	for _, c := range req.OutputColumns {
		f.OutputColumns = append(f.OutputColumns, factsColumn{
			Name:     c.Name,
			Type:     c.Type,
			Policy:   string(c.Policy),
			Computed: c.TableOID == 0,
		})
	}

	payload, err := json.Marshal(f)
	if err != nil {
		payload = []byte("{}")
	}

	return chatRequest{
		Model:  j.model,
		Stream: false,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: string(payload)},
		},
		Temperature: 0,
		ResponseFormat: &responseFormat{
			Type:       "json_schema",
			JSONSchema: verdictSchema(),
		},
	}
}

const systemPrompt = `You route database statements for a guard called keeper. You are one input to a routing decision; you do not grant access and you cannot see any data.

You receive facts about a single statement: its text, the session's declared intent, the plan's relation list and estimates, and the output columns with the policy keeper already resolved for each. There is no conversation history and there will not be one.

Answer with one JSON object and nothing else:
  tier          0 run, 1 run and record, 2 uncertain, 3 a human should decide
  reason_codes  zero or more of: intent_mismatch, unusually_broad_read, near_row_cap,
                sensitive_relation, view_definition_changed, computed_output_uncertain,
                narrower_query_available, routine_read
  uncertainty   0.0 to 1.0
  release       true only if the uncertain output is safe to release unmasked
  explanation   one short sentence, shown to a human as your suggestion

Judge the statement against the declared intent. A statement that reads far more than the intent needs, or reads something the intent never mentions, is what tier 2 and 3 are for. An ordinary read that matches its intent is tier 0 or 1. Never answer with anything but the JSON object.`

// verdictSchema is the structured-output schema the endpoint enforces. It is a
// convenience: ParseVerdict validates the answer again regardless, because a
// sidecar that ignores the schema must not become a trusted one.
func verdictSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tier": map[string]any{"type": "integer", "minimum": 0, "maximum": 4},
			"reason_codes": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "enum": ReasonCodes},
			},
			"uncertainty": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"release":     map[string]any{"type": "boolean"},
			"explanation": map[string]any{"type": "string"},
		},
		"required": []any{"tier", "reason_codes", "uncertainty"},
	}
}
