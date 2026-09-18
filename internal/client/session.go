package client

import (
	"context"
	"net/http"
)

// SetSessionIntent sets the session's stated intent (SPEC §6.1). Required
// before any query; shown on approval screens and recorded in the audit log.
func (c *Client) SetSessionIntent(ctx context.Context, intent string) error {
	body := struct {
		Intent string `json:"intent"`
	}{Intent: intent}
	var resp okResponse
	return c.do(ctx, http.MethodPost, "/v1/session/intent", body, &resp)
}

type okResponse struct {
	OK bool `json:"ok"`
}
