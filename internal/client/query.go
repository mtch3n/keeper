package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/types"
)

// Param is one bound parameter of a query or explain call. Exactly one of
// Value or Token is set — SPEC §6.2: keeper never substitutes a resolved
// value into statement text, so a caller passes either a literal value or an
// opaque token and keeperd resolves the token itself.
type Param struct {
	Value any    `json:"value,omitzero"`
	Token string `json:"token,omitzero"`
}

// Explain runs the dry run of SPEC §6.1: what a statement would touch and
// how it would be classified, without spending an approval.
func (c *Client) Explain(ctx context.Context, connID, sql string, params []Param) (*types.ExplainResult, error) {
	body := struct {
		SQL    string  `json:"sql"`
		Params []Param `json:"params,omitzero"`
	}{SQL: sql, Params: params}
	var out types.ExplainResult
	if err := c.do(ctx, http.MethodPost, "/v1/connections/"+url.PathEscape(connID)+"/explain", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QueryOutcome is what `query` returned: either a completed Result (tier 0/1,
// SPEC §3.2) or a Ticket to poll with GetResult (tier 2/3). Exactly one is
// non-nil.
type QueryOutcome struct {
	Result *types.QueryResult
	Ticket *types.Ticket
}

// Query runs a statement. maxRows of 0 means "use the connection's ceiling";
// the agent can never raise the ceiling itself (SPEC §6.1). Writes use this
// same call — they always come back as a Ticket (SPEC R4.2f).
func (c *Client) Query(ctx context.Context, connID, sql string, params []Param, maxRows int) (*QueryOutcome, error) {
	body := struct {
		SQL     string  `json:"sql"`
		Params  []Param `json:"params,omitzero"`
		MaxRows int     `json:"max_rows,omitzero"`
	}{SQL: sql, Params: params, MaxRows: maxRows}

	data, err := c.doRaw(ctx, http.MethodPost, "/v1/connections/"+url.PathEscape(connID)+"/query", body)
	if err != nil {
		return nil, err
	}

	// The response is one of two shapes; types.Ticket is the one with a
	// non-empty "ticket" field, which types.QueryResult never has.
	var probe struct {
		Ticket string `json:"ticket"`
	}
	if err := unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("keeper: decode query response: %w", err)
	}
	if probe.Ticket != "" {
		var t types.Ticket
		if err := unmarshal(data, &t); err != nil {
			return nil, fmt.Errorf("keeper: decode ticket: %w", err)
		}
		return &QueryOutcome{Ticket: &t}, nil
	}
	var r types.QueryResult
	if err := unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("keeper: decode query result: %w", err)
	}
	return &QueryOutcome{Result: &r}, nil
}

// TicketResult is get_result's response shape (CONTRACT §3: GET
// /v1/tickets/{id} → {state, result?, error?}).
type TicketResult struct {
	State  types.TicketState  `json:"state"`
	Result *types.QueryResult `json:"result,omitzero"`
	Error  *types.Error       `json:"error,omitzero"`
}

// GetResult polls a ticket. waitMs is capped at 25000 (R8.7b, §3.2); the
// agent re-calls while State is not Terminal().
func (c *Client) GetResult(ctx context.Context, ticket string, waitMillis int) (*TicketResult, error) {
	q := url.Values{}
	if waitMillis > 0 {
		q.Set("wait_ms", fmt.Sprint(waitMs(waitMillis)))
	}
	path := "/v1/tickets/" + url.PathEscape(ticket)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out TicketResult
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
