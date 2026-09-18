package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/types"
)

// ListGrants lists every allow rule, session-scoped grants first
// (CONTRACT §3: GET /v1/grants).
func (c *Client) ListGrants(ctx context.Context) ([]types.Grant, error) {
	var out []types.Grant
	if err := c.do(ctx, http.MethodGet, "/v1/grants", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateGrantParams is `keeper allow`'s equivalent creation body — reachable
// only from the CLI/UI, never MCP (SPEC R9.3e).
type CreateGrantParams struct {
	Path       types.PathRef       `json:"path"`
	Lifetime   types.GrantLifetime `json:"lifetime"`
	RowCeiling int                 `json:"row_ceiling"`
}

// CreateGrant creates one standing or session allow rule.
func (c *Client) CreateGrant(ctx context.Context, p CreateGrantParams) (*types.Grant, error) {
	var out types.Grant
	if err := c.do(ctx, http.MethodPost, "/v1/grants", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeGrant revokes one grant by id (`keeper allow revoke <id>`).
func (c *Client) RevokeGrant(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/grants/"+url.PathEscape(id), nil, nil)
}
