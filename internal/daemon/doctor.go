package daemon

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// DoctorReport is GET /v1/doctor: daemon state, key source, the connections the
// vault holds, detector identity and network posture. It reads only this
// process and the vault, so it is cheap enough to poll. R4.3 makes the key
// source a reported fact, because silent degradation to a weaker source is a
// defect rather than a fallback.
type DoctorReport struct {
	Version   string `json:"version"`
	UIBase    string `json:"ui_base,omitzero"`
	KeySource string `json:"key_source"`

	Sessions         []types.Session `json:"sessions,omitzero"`
	PendingApprovals int             `json:"pending_approvals"`
	OpenTickets      int             `json:"open_tickets"`
	OpenRequests     int             `json:"open_requests"`

	Connections []ConnectionHealth `json:"connections,omitzero"`

	Detector *ports.DetectorIdentity `json:"detector,omitzero"`
	// LogRetentionDays is how long the activity log keeps a record.
	LogRetentionDays int `json:"log_retention_days"`
	// ConnectionIdleMinutes is how long a database's connections stay open
	// with nobody using them.
	ConnectionIdleMinutes int `json:"connection_idle_minutes"`
}

// ConnectionHealth is one connection's line in doctor: what the vault holds,
// and nothing that needs the database. doctor never connects to one — an
// operator who wants to know whether an account connects presses Test
// connection.
type ConnectionHealth struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Reads  types.Reads  `json:"reads"`
	Writes types.Writes `json:"writes"`
	Limits types.Limits `json:"limits"`
}

// Doctor assembles the report.
func (d *Daemon) Doctor(ctx context.Context) *DoctorReport {
	rep := &DoctorReport{
		Version:   d.cfg.Version,
		UIBase:    d.UIBase(),
		KeySource: d.deps.Vault.KeySource(),
		Sessions:  d.Sessions(),
	}
	rep.PendingApprovals, rep.OpenTickets, rep.OpenRequests = d.waiting()

	if d.deps.Detector != nil {
		id := d.deps.Detector.Identity()
		rep.Detector = &id
	}

	if st, err := d.deps.Vault.Settings(ctx); err == nil {
		rep.LogRetentionDays = st.LogRetentionDays
		rep.ConnectionIdleMinutes = st.ConnectionIdleMinutes
	}
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return rep
	}
	rep.Connections = make([]ConnectionHealth, len(cs))
	for i, c := range cs {
		rep.Connections[i] = connectionHealth(c)
	}
	return rep
}

func connectionHealth(c *types.Connection) ConnectionHealth {
	return ConnectionHealth{ID: c.ID, Name: c.Name, Reads: effectiveReads(c.Reads), Writes: effectiveWrites(c.Writes), Limits: c.Limits}
}

// waiting counts what a restart would cancel under someone: approvals waiting
// on a person, tickets not yet settled, and local requests not yet answered.
func (d *Daemon) waiting() (approvals, tickets, requests int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	approvals = len(d.queue)
	for _, tk := range d.tickets {
		if !tk.t.State.Terminal() {
			tickets++
		}
	}
	for _, r := range d.requests {
		if r.r.State == statePending {
			requests++
		}
	}
	return approvals, tickets, requests
}

// Waiting is the sum of waiting, which `/v1/version` reports without a
// session so that a newer client can tell whether restarting this daemon
// would cancel anything (UPG-D2).
func (d *Daemon) Waiting() int {
	a, t, r := d.waiting()
	return a + t + r
}
