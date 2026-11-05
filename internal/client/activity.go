package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// ActivityFilter narrows `keeper activity`. Zero values mean "no filter" for
// that field, matching [ports.AuditFilter] which this mirrors over the wire.
type ActivityFilter struct {
	SessionID    string
	ConnectionID string
	Since        time.Time
	Decision     types.Decision
	Limit        int
	Offset       int
}

// ListActivity queries the append-only audit log (GET /v1/activity).
func (c *Client) ListActivity(ctx context.Context, f ActivityFilter) (ports.AuditPage, error) {
	q := url.Values{}
	if f.SessionID != "" {
		q.Set("session", f.SessionID)
	}
	if f.ConnectionID != "" {
		q.Set("connection", f.ConnectionID)
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.Format(time.RFC3339))
	}
	if f.Decision != "" {
		q.Set("decision", string(f.Decision))
	}
	if f.Offset > 0 {
		q.Set("offset", fmt.Sprint(f.Offset))
	}
	if f.Limit > 0 {
		q.Set("limit", fmt.Sprint(f.Limit))
	}
	path := "/v1/activity"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out ports.AuditPage
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return ports.AuditPage{}, err
	}
	return out, nil
}

// GetActivityRecord returns one audit record in full.
func (c *Client) GetActivityRecord(ctx context.Context, auditID string) (*types.AuditRecord, error) {
	var out types.AuditRecord
	if err := c.do(ctx, http.MethodGet, "/v1/activity/"+url.PathEscape(auditID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DoctorReport is `keeper doctor`'s answer when it can reach the daemon:
// daemon state, key source, the connections the vault holds and detector
// identity (CONTRACT §3: GET /v1/doctor).
type DoctorReport struct {
	DaemonRunning    bool                    `json:"daemon_running"`
	Version          string                  `json:"version,omitzero"`
	UIBase           string                  `json:"ui_base,omitzero"`
	KeySource        string                  `json:"key_source,omitzero"`
	Connections      []ConnectionHealth      `json:"connections,omitzero"`
	Detector         *ports.DetectorIdentity `json:"detector,omitzero"`
	Sessions         []types.Session         `json:"sessions,omitzero"`
	PendingApprovals int                     `json:"pending_approvals"`
	OpenTickets      int                     `json:"open_tickets"`
	OpenRequests     int                     `json:"open_requests"`
	LogRetentionDays int                     `json:"log_retention_days"`
	// ConnectionIdleMinutes is how long a database's connections stay open
	// with nobody using them.
	ConnectionIdleMinutes int `json:"connection_idle_minutes"`
}

// ConnectionHealth is one connection's line in a doctor report: what the vault
// holds. doctor never connects to a database; `keeper connection test` does.
type ConnectionHealth struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Reads  types.Reads  `json:"reads"`
	Writes types.Writes `json:"writes"`
}

// GetDoctor fetches the daemon-connected doctor report. cmd/keeper's
// daemonless checks (lockfile, socket, binary versions, keychain item
// presence) run independently of this and never go through the daemon
// (SPEC §3.4: doctor must work without it).
func (c *Client) GetDoctor(ctx context.Context) (*DoctorReport, error) {
	var out DoctorReport
	if err := c.do(ctx, http.MethodGet, "/v1/doctor", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
