package client

import (
	"context"
	"net/http"
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
