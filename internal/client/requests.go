package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/types"
)

// RequestInput opens a local input request: a human enters a value at the
// loopback page and it is stored in this session's reverse map, never
// returned to the caller directly .
func (c *Client) RequestInput(ctx context.Context, connID, namespace, purpose string) (*types.LocalRequest, error) {
	return c.createRequest(ctx, types.RequestInput, connID, namespace, purpose)
}

func (c *Client) createRequest(ctx context.Context, kind types.RequestKind, connID, namespace, purpose string) (*types.LocalRequest, error) {
	body := struct {
		Kind         types.RequestKind `json:"kind"`
		ConnectionID string            `json:"connection_id"`
		Namespace    string            `json:"namespace,omitzero"`
		Purpose      string            `json:"purpose,omitzero"`
	}{Kind: kind, ConnectionID: connID, Namespace: namespace, Purpose: purpose}
	var out types.LocalRequest
	if err := c.do(ctx, http.MethodPost, "/v1/requests", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestResult is get_input_result's response shape (GET /v1/requests/{id}
// returns {state, token?, namespace?}).
type RequestResult struct {
	State     string `json:"state"` // pending_input, ready, cancelled, expired
	Token     string `json:"token,omitzero"`
	Namespace string `json:"namespace,omitzero"`
}

// GetInputResult polls an input request. waitMs is capped at 25000.
func (c *Client) GetInputResult(ctx context.Context, requestID string, waitMillis int) (*RequestResult, error) {
	var out RequestResult
	err := c.poll(ctx, "/v1/requests/"+url.PathEscape(requestID), waitMillis, &out, func() bool { return out.State != "pending_input" })
	if err != nil {
		return nil, err
	}
	return &out, nil
}
