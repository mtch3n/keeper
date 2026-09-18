package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// CatalogResult is the merged committed catalog and daemon overlay for one
// connection, keyed "schema.table.column" (SPEC §5.2), with the backlog of
// columns no policy has been chosen for yet.
type CatalogResult struct {
	Entries      map[string]types.ColumnPolicy `json:"entries"`
	Unclassified []string                      `json:"unclassified,omitzero"`
}

// GetCatalog returns the merged catalog and overlay for a connection.
func (c *Client) GetCatalog(ctx context.Context, connID string) (*CatalogResult, error) {
	var out CatalogResult
	if err := c.do(ctx, http.MethodGet, "/v1/catalog/"+url.PathEscape(connID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutCatalogColumns promotes overlay entries into the committed file
// (`keeper catalog edit`). Keys are "schema.table.column" JSON paths, SPEC §5.2.
func (c *Client) PutCatalogColumns(ctx context.Context, connID string, entries map[string]types.ColumnPolicy) (*CatalogResult, error) {
	body := struct {
		Entries map[string]types.ColumnPolicy `json:"entries"`
	}{Entries: entries}
	var out CatalogResult
	if err := c.do(ctx, http.MethodPut, "/v1/catalog/"+url.PathEscape(connID)+"/columns", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CatalogInit proposes a policy for every column, grouped by review risk
// (`keeper catalog init`). sample is 0 for no row sampling.
func (c *Client) CatalogInit(ctx context.Context, connID string, sample int) (*ports.InitProposal, error) {
	body := struct {
		Sample int `json:"sample,omitzero"`
	}{Sample: sample}
	var out ports.InitProposal
	if err := c.do(ctx, http.MethodPost, "/v1/catalog/"+url.PathEscape(connID)+"/init", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CatalogGrants returns the suggested `GRANT SELECT (...)` statements for a
// connection's catalog (SPEC §5.5, §2.4).
func (c *Client) CatalogGrants(ctx context.Context, connID string) ([]string, error) {
	var out []string
	if err := c.do(ctx, http.MethodPost, "/v1/catalog/"+url.PathEscape(connID)+"/grants", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
