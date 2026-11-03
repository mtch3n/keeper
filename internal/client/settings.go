package client

import (
	"context"
	"net/http"

	"github.com/mtchen/keeper/internal/types"
)

// UISession is the dashboard's address and the secret that opens it.
type UISession struct {
	Base   string `json:"base"`
	Secret string `json:"secret"`
}

// GetUISession is `keeper ui`. The daemon answers on the socket only.
func (c *Client) GetUISession(ctx context.Context) (*UISession, error) {
	var out UISession
	if err := c.do(ctx, http.MethodGet, "/v1/ui-session", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSettings reads the daemon-wide settings.
func (c *Client) GetSettings(ctx context.Context) (types.Settings, error) {
	var out types.Settings
	err := c.do(ctx, http.MethodGet, "/v1/settings", nil, &out)
	return out, err
}

// SetUIOff turns the dashboard off or on.
func (c *Client) SetUIOff(ctx context.Context, off bool) error {
	return c.do(ctx, http.MethodPut, "/v1/settings", map[string]bool{"ui_off": off}, nil)
}
