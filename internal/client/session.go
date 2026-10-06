package client

import (
	"context"
	"net/http"

	"github.com/mtchen/keeper/internal/types"
)

// SetSessionIntent sets the session's stated intent (SPEC §6.1) and how long
// its tokens live. Required before any query; shown on approval screens and
// recorded in the audit log.
func (c *Client) SetSessionIntent(ctx context.Context, intent string, scope types.TokenScope) error {
	body := struct {
		Intent     string           `json:"intent"`
		TokenScope types.TokenScope `json:"token_scope,omitzero"`
	}{Intent: intent, TokenScope: scope}
	var resp okResponse
	return c.do(ctx, http.MethodPost, "/v1/session/intent", body, &resp)
}

type okResponse struct {
	OK bool `json:"ok"`
}
