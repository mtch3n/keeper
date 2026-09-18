package daemon

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// DoctorReport is GET /v1/doctor: daemon state, key source, connection health,
// catalog freshness, detector identity and network posture. R4.3 makes the key
// source a reported fact, because silent degradation to a weaker source is a
// defect rather than a fallback.
type DoctorReport struct {
	Version     string `json:"version"`
	UIBase      string `json:"ui_base,omitzero"`
	VaultLocked bool   `json:"vault_locked"`
	KeySource   string `json:"key_source"`

	Sessions         []types.Session `json:"sessions,omitzero"`
	PendingApprovals int             `json:"pending_approvals"`
	OpenTickets      int             `json:"open_tickets"`
	OpenRequests     int             `json:"open_requests"`
	Grants           int             `json:"grants"`
	SuspendedGrants  int             `json:"suspended_grants"`

	Connections []ConnectionHealth `json:"connections,omitzero"`

	Detector *ports.DetectorIdentity `json:"detector,omitzero"`
	Judge    JudgeHealth             `json:"judge"`
}

// ConnectionHealth is one connection's line in doctor.
type ConnectionHealth struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Enabled      bool         `json:"enabled"`
	Degraded     bool         `json:"degraded"`
	Unaccepted   int          `json:"unaccepted_findings"`
	Unclassified int          `json:"unclassified_columns"`
	CatalogFresh bool         `json:"catalog_fresh"`
	FreshKnown   bool         `json:"catalog_freshness_known"`
	Mode         types.Mode   `json:"mode"`
	Limits       types.Limits `json:"limits"`
}

// JudgeHealth reports the local model. A configured-but-failing judge never
// counts as a favourable verdict (R7.7b), so its absence is stated rather than
// inferred from silence.
type JudgeHealth struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	Identity   string `json:"identity,omitzero"`
}

// Doctor assembles the report.
func (d *Daemon) Doctor(ctx context.Context) *DoctorReport {
	rep := &DoctorReport{
		Version:     d.cfg.Version,
		UIBase:      d.cfg.UIBase,
		VaultLocked: d.deps.Vault.Locked(),
		KeySource:   d.deps.Vault.KeySource(),
		Sessions:    d.Sessions(),
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
	if d.deps.Judge != nil {
		rep.Judge = JudgeHealth{Configured: true, Available: d.deps.Judge.Available(ctx), Identity: d.deps.Judge.Identity()}
	}

	if rep.VaultLocked {
		return rep
	}
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return rep
	}
	for _, c := range cs {
		h := ConnectionHealth{
			ID: c.ID, Name: c.Name, Enabled: c.Enabled, Degraded: c.Degraded(),
			Unaccepted: len(c.Unaccepted()), Mode: c.Mode, Limits: c.Limits,
		}
		if un, err := d.deps.CatalogStore.Unclassified(ctx, c.ID); err == nil {
			h.Unclassified = len(un)
		}
		if cat, err := d.deps.Catalogs(c.ID); err == nil {
			if fresh, err := cat.Fresh(ctx, nil); err == nil {
				h.CatalogFresh, h.FreshKnown = fresh, true
			}
		}
		rep.Connections = append(rep.Connections, h)
	}
	return rep
}
