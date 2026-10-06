package client

import (
	"context"
	"net/http"
)

// ExportResult is `keeper vault export`'s response: an export sealed under the
// operator's passphrase,
// the operator is responsible for storing safely. keeperd is the only thing
// that ever has the material to produce it (R3.1).
type ExportResult struct {
	Data []byte `json:"data"`
}

// ExportVault asks keeperd to export the vault (POST /v1/vault/export).
// This endpoint is not enumerated in CONTRACT §3's table; it exists because
// the CLI surface (`keeper vault export`) requires it and R3.1 forbids any
// other component from reading the vault to produce it.
func (c *Client) ExportVault(ctx context.Context, passphrase string) (*ExportResult, error) {
	var out ExportResult
	body := map[string]string{"passphrase": passphrase}
	if err := c.do(ctx, http.MethodPost, "/v1/vault/export", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ImportVault replaces the daemon's vault with an export (POST
// /v1/vault/import). An export that does not open under passphrase is refused
// and the vault is left as it was.
func (c *Client) ImportVault(ctx context.Context, data []byte, passphrase string) error {
	body := struct {
		Data       []byte `json:"data"`
		Passphrase string `json:"passphrase"`
	}{data, passphrase}
	return c.do(ctx, http.MethodPost, "/v1/vault/import", body, nil)
}

// RotateMaster asks keeperd to rotate the vault's master key
// (POST /v1/vault/rotate-master). Like ExportVault, this endpoint is implied
// by the CLI surface rather than spelled out in CONTRACT §3's table.
func (c *Client) RotateMaster(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/vault/rotate-master", nil, nil)
}
