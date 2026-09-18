package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/types"
)

// RequestInput opens a local input request: a human enters a value at the
// loopback page and it is stored in this session's reverse map, never
// returned to the caller directly (SPEC R8.7a–c).
func (c *Client) RequestInput(ctx context.Context, connID, namespace, purpose string) (*types.LocalRequest, error) {
	return c.createRequest(ctx, types.RequestInput, connID, namespace, purpose, nil)
}

// RequestAuthorization opens a local authorization request for one path
// (SPEC R8.7g, R9.3c). Granting it changes state the next statement
// observes; no token is ever minted for it.
func (c *Client) RequestAuthorization(ctx context.Context, connID string, path types.PathRef, purpose string) (*types.LocalRequest, error) {
	return c.createRequest(ctx, types.RequestAuthorization, connID, "", purpose, &path)
}

func (c *Client) createRequest(ctx context.Context, kind types.RequestKind, connID, namespace, purpose string, path *types.PathRef) (*types.LocalRequest, error) {
	body := struct {
		Kind         types.RequestKind `json:"kind"`
		ConnectionID string            `json:"connection_id"`
		Namespace    string            `json:"namespace,omitzero"`
		Purpose      string            `json:"purpose,omitzero"`
		Path         *types.PathRef    `json:"path,omitzero"`
	}{Kind: kind, ConnectionID: connID, Namespace: namespace, Purpose: purpose, Path: path}
	var out types.LocalRequest
	if err := c.do(ctx, http.MethodPost, "/v1/requests", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestResult is get_input_result / get_authorization_result's response
// shape (CONTRACT §3: GET /v1/requests/{id} → {state, token?, namespace?}).
// keeperd omits Token entirely for an authorization request; ForAuthorization
// scrubs it again on this side as a second guard, since an authorization
// result must never carry a token (SPEC R8.7g).
type RequestResult struct {
	State     string `json:"state"` // pending_input, ready, cancelled, expired
	Token     string `json:"token,omitzero"`
	Namespace string `json:"namespace,omitzero"`
}

// GetInputResult polls an input request. waitMs is capped at 25000.
func (c *Client) GetInputResult(ctx context.Context, requestID string, waitMillis int) (*RequestResult, error) {
	return c.getRequestResult(ctx, requestID, waitMillis)
}

// GetAuthorizationResult polls an authorization request. The returned
// RequestResult never carries a token, regardless of what keeperd sent.
func (c *Client) GetAuthorizationResult(ctx context.Context, requestID string, waitMillis int) (*RequestResult, error) {
	r, err := c.getRequestResult(ctx, requestID, waitMillis)
	if err != nil {
		return nil, err
	}
	r.Token = ""
	return r, nil
}

func (c *Client) getRequestResult(ctx context.Context, requestID string, waitMillis int) (*RequestResult, error) {
	q := url.Values{}
	if waitMillis > 0 {
		q.Set("wait_ms", fmt.Sprint(waitMs(waitMillis)))
	}
	path := "/v1/requests/" + url.PathEscape(requestID)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out RequestResult
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
