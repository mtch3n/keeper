package client

import (
	"context"
	"net/http"
)

// UnlockVault unlocks the vault for the interactive headless path
// (POST /v1/vault/unlock, SPEC §4.3). Only keeperd ever opens the vault
// (R3.1); this just carries the passphrase to it over the socket.
func (c *Client) UnlockVault(ctx context.Context, passphrase string) error {
	body := struct {
		Passphrase string `json:"passphrase"`
	}{Passphrase: passphrase}
	return c.do(ctx, http.MethodPost, "/v1/vault/unlock", body, nil)
}

// ExportResult is `keeper vault export`'s response: an encrypted export blob
// the operator is responsible for storing safely. keeperd is the only thing
// that ever has the material to produce it (R3.1).
type ExportResult struct {
	Data []byte `json:"data"`
}

// ExportVault asks keeperd to export the vault (POST /v1/vault/export).
// This endpoint is not enumerated in CONTRACT §3's table; it exists because
// the CLI surface (`keeper vault export`) requires it and R3.1 forbids any
// other component from reading the vault to produce it.
func (c *Client) ExportVault(ctx context.Context) (*ExportResult, error) {
	var out ExportResult
	if err := c.do(ctx, http.MethodPost, "/v1/vault/export", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RotateMaster asks keeperd to rotate the vault's master key
// (POST /v1/vault/rotate-master). Like ExportVault, this endpoint is implied
// by the CLI surface rather than spelled out in CONTRACT §3's table.
func (c *Client) RotateMaster(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/vault/rotate-master", nil, nil)
}
