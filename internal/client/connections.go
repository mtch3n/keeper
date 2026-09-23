package client

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// ConnectionSummary is one row of list_connections / `keeper connection ls`
// (CONTRACT §3: GET /v1/connections). It deliberately carries none of the
// registration or credential detail full describe does — host, password and
// connection string never appear here or anywhere else (SPEC §6.1).
type ConnectionSummary struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Engine   string     `json:"engine"`
	Database string     `json:"database"`
	Role     string     `json:"role"`
	Mode     types.Mode `json:"mode"`
}

// ListConnections lists every registered connection.
func (c *Client) ListConnections(ctx context.Context) ([]ConnectionSummary, error) {
	var out []ConnectionSummary
	if err := c.do(ctx, http.MethodGet, "/v1/connections", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AuditedPrivileges is the G0 half of describe_connection: what the last
// privilege audit found, and when it ran. Nothing in it gates the connection
// (SPEC R4.1) — `keeper audit` is where these are meant to be read.
type AuditedPrivileges struct {
	AuditedAt time.Time       `json:"audited_at"`
	Findings  []types.Finding `json:"findings,omitzero"`
}

// AuditReport is one connection's entry in `keeper audit`, mirroring
// daemon.AuditReport.
type AuditReport struct {
	ConnectionID string          `json:"connection_id"`
	Name         string          `json:"name"`
	Database     string          `json:"database"`
	Role         string          `json:"role"`
	AuditedAt    time.Time       `json:"audited_at"`
	Findings     []types.Finding `json:"findings,omitzero"`
}

// CatalogStatus is the freshness and backlog half.
type CatalogStatus struct {
	Path            string   `json:"path"`
	Fresh           bool     `json:"fresh"`
	FreshnessKnown  bool     `json:"freshness_known"`
	Unclassified    int      `json:"unclassified"`
	UnclassifiedTop []string `json:"unclassified_top,omitzero"`
}

// ConnectionDetail mirrors daemon.ConnectionDetail. It is deliberately not
// types.Connection: describe_connection returns what §6.1 lists and no more, so
// host, password and connection string have nowhere to appear.
type ConnectionDetail struct {
	ID                 string                  `json:"id"`
	Name               string                  `json:"name"`
	Engine             string                  `json:"engine"`
	Version            string                  `json:"version,omitzero"`
	Database           string                  `json:"database"`
	Schemas            []string                `json:"schemas,omitzero"`
	Role               string                  `json:"role"`
	AuditedPrivileges  AuditedPrivileges       `json:"audited_privileges"`
	CatalogStatus      CatalogStatus           `json:"catalog_status"`
	PolicySummary      map[types.Policy]int    `json:"policy_summary,omitzero"`
	Mode               types.Mode              `json:"mode"`
	Limits             types.Limits            `json:"limits"`
	Denylist           []types.RelationRef     `json:"denylist,omitzero"`
	WriteScope         []types.WriteScopeEntry `json:"write_scope,omitzero"`
	HasWriteCredential bool                    `json:"has_write_credential"`
	Degradations       []types.Degradation     `json:"degradations,omitzero"`
}

// DescribeConnection returns describe_connection's field set for one
// connection.
func (c *Client) DescribeConnection(ctx context.Context, id string) (*ConnectionDetail, error) {
	var out ConnectionDetail
	if err := c.do(ctx, http.MethodGet, "/v1/connections/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RegisterConnectionParams is `keeper connection add`'s body. writeDSN is
// empty when no _rw credential is being registered (SPEC §4.2: write mode is
// a separate stored credential, not a flag).
type RegisterConnectionParams struct {
	Name        string `json:"name"`
	DSN         string `json:"dsn"`
	WriteDSN    string `json:"write_dsn,omitzero"`
	CatalogPath string `json:"catalog_path,omitzero"`
}

// RegisterConnection stores a connection and audits it once (G0). The
// returned connection carries every finding and is usable regardless of them:
// the audit advises, it does not gate (SPEC R4.1).
func (c *Client) RegisterConnection(ctx context.Context, p RegisterConnectionParams) (*types.Connection, error) {
	var out types.Connection
	if err := c.do(ctx, http.MethodPost, "/v1/connections", p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AuditConnection re-runs G0 against a registered connection.
func (c *Client) AuditConnection(ctx context.Context, id string) (*types.Connection, error) {
	var out types.Connection
	if err := c.do(ctx, http.MethodPost, "/v1/connections/"+url.PathEscape(id)+"/audit", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Audit returns the privilege-audit report for every connection.
func (c *Client) Audit(ctx context.Context) ([]AuditReport, error) {
	var out []AuditReport
	if err := c.do(ctx, http.MethodGet, "/v1/audit", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// --max-rows) never implicitly resets the others to zero; a zero value here
// means "leave this alone", not "set it to zero".
type PatchConnectionParams struct {
	Mode             types.Mode    `json:"mode,omitzero"`
	MaxRowsCeiling   int           `json:"max_rows_ceiling,omitzero"`
	StatementTimeout time.Duration `json:"statement_timeout,omitzero"`
	ScanSample       int           `json:"scan_sample,omitzero"`
}

// PatchConnection updates a connection's mode and/or limits.
func (c *Client) PatchConnection(ctx context.Context, id string, p PatchConnectionParams) (*types.Connection, error) {
	var out types.Connection
	if err := c.do(ctx, http.MethodPatch, "/v1/connections/"+url.PathEscape(id), p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetDenylist replaces a connection's denylist wholesale (`keeper connection
// denylist --add/--remove` compute the new set client-side and call this
// once). SPEC R4.5: entries are (schema, table) names resolved to OIDs the
// way catalog entries are.
func (c *Client) SetDenylist(ctx context.Context, id string, relations []types.RelationRef) (*types.Connection, error) {
	body := struct {
		Relations []types.RelationRef `json:"relations"`
	}{Relations: relations}
	var out types.Connection
	if err := c.do(ctx, http.MethodPut, "/v1/connections/"+url.PathEscape(id)+"/denylist", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SchemaColumn is one column of get_schema's response (SPEC §6.1).
type SchemaColumn struct {
	Name     string       `json:"name"`
	Type     string       `json:"type"`
	Nullable bool         `json:"nullable"`
	IsPK     bool         `json:"is_pk"`
	IsFK     bool         `json:"is_fk"`
	Policy   types.Policy `json:"policy"`
}

// SchemaTable is one table of get_schema's response. HiddenCount is how many
// hide_name columns were omitted from Columns (SPEC R6.4b).
type SchemaTable struct {
	Schema      string         `json:"schema"`
	Name        string         `json:"name"`
	Columns     []SchemaColumn `json:"columns"`
	HiddenCount int            `json:"hidden_count,omitzero"`
}

// SchemaResult is get_schema's whole response.
type SchemaResult struct {
	Tables []SchemaTable `json:"tables"`
}

// GetSchema returns the filtered, policy-annotated schema for a connection.
// schema and tablePattern narrow the result; either may be empty.
func (c *Client) GetSchema(ctx context.Context, connID, schema, tablePattern string) (*SchemaResult, error) {
	q := url.Values{}
	if schema != "" {
		q.Set("schema", schema)
	}
	if tablePattern != "" {
		q.Set("table", tablePattern)
	}
	path := "/v1/connections/" + url.PathEscape(connID) + "/schema"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out SchemaResult
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveConnection deletes a connection and its credentials.
func (c *Client) RemoveConnection(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/connections/"+url.PathEscape(id), nil, nil)
}
