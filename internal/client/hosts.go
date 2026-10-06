package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/types"
)

// HostView is one entry of GET /v1/hosts: a host and the ids of the
// connections registered on it.
type HostView struct {
	types.Host
	Connections []string `json:"connections"`
}

// ListHosts lists every registered host.
func (c *Client) ListHosts(ctx context.Context) ([]HostView, error) {
	var out []HostView
	if err := c.do(ctx, http.MethodGet, "/v1/hosts", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RegisterHostParams is `keeper host add`'s body. A zero port and an empty
// sslmode take the daemon's defaults, 5432 and prefer.
type RegisterHostParams struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port,omitzero"`
	SSLMode string `json:"sslmode,omitzero"`
}

// RegisterHost stores a server's address.
func (c *Client) RegisterHost(ctx context.Context, p RegisterHostParams) (*types.Host, error) {
	var out types.Host
	if err := c.do(ctx, http.MethodPost, "/v1/hosts", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveHost deletes a host with no connections on it.
func (c *Client) RemoveHost(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/hosts/"+url.PathEscape(id), nil, nil)
}
