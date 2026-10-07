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
	Grants           int             `json:"grants"`
	SuspendedGrants  int             `json:"suspended_grants"`

	Connections []ConnectionHealth `json:"connections,omitzero"`

	Detector *ports.DetectorIdentity `json:"detector,omitzero"`
	// LogRetentionDays is how long the activity log keeps a record.
	LogRetentionDays int `json:"log_retention_days"`
	// ConnectionIdleMinutes is how long a database's connections stay open
	// with nobody using them.
	ConnectionIdleMinutes int `json:"connection_idle_minutes"`
	// LegacyAuditLog is a plaintext audit.log an earlier keeper left, which
	// the encrypted log never reads; the operator can delete it.
	LegacyAuditLog string `json:"legacy_audit_log,omitzero"`
}

// ConnectionHealth is one connection's line in doctor: what the vault holds,
// and nothing that needs the database. doctor never connects to one — an
// operator who wants to know whether an account connects presses Test
// connection.
type ConnectionHealth struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Findings is how many the last privilege audit reported. It is a pointer
	// at `keeper audit`, not a state: none of them stop this connection.
	Findings int          `json:"findings"`
	Mode     types.Mode   `json:"mode"`
	Limits   types.Limits `json:"limits"`
}

// Doctor assembles the report.
func (d *Daemon) Doctor(ctx context.Context) *DoctorReport {
	rep := &DoctorReport{
		Version:   d.cfg.Version,
		UIBase:    d.cfg.UIBase,
		KeySource: d.deps.Vault.KeySource(),
		Sessions:  d.Sessions(),
	}
	d.mu.Lock()
	rep.PendingApprovals = len(d.queue)
	for _, tk := range d.tickets {
		if !tk.t.State.Terminal() {
			rep.OpenTickets++
		}
	}
	for _, r := range d.requests {
		if r.r.State == statePending {
			rep.OpenRequests++
		}
	}
	rep.Grants = len(d.grants)
	for _, g := range d.grants {
		if g.Suspended {
			rep.SuspendedGrants++
		}
	}
	d.mu.Unlock()

	if d.deps.Detector != nil {
		id := d.deps.Detector.Identity()
		rep.Detector = &id
	}

	rep.LegacyAuditLog = d.deps.Audit.LegacyPlaintext()
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
		rep.Connections[i] = ConnectionHealth{ID: c.ID, Name: c.Name, Findings: len(c.Findings), Mode: c.Mode, Limits: c.Limits}
	}
	return rep
}
