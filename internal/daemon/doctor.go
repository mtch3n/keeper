package daemon

import (
	"context"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// DoctorReport is GET /v1/doctor: daemon state, key source, connection health,
// catalog freshness, detector identity and network posture. R4.3 makes the key
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
	Judge    JudgeHealth             `json:"judge"`
}

// ConnectionHealth is one connection's line in doctor.
type ConnectionHealth struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Findings is how many the last privilege audit reported. It is a pointer
	// at `keeper audit`, not a state: none of them stop this connection.
	Findings     int          `json:"findings"`
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

// doctorProbeTimeout bounds the catalog half of the report. It is short on
// purpose: doctor is a status read, and a status read that waits on a database
// is the thing being fixed. A connection that misses it reports the facts the
// vault holds and says its catalog freshness is unknown, which is what a
// connection nobody can reach actually is.
const doctorProbeTimeout = 3 * time.Second

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
	if d.deps.Judge != nil {
		rep.Judge = JudgeHealth{Configured: true, Available: d.deps.Judge.Available(ctx), Identity: d.deps.Judge.Identity()}
	}

	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return rep
	}
	// The cheap half of every row comes from the vault and is always reported.
	// The catalog half is a database round trip, and the probes run together
	// under one deadline: serially, a report cost the sum of every connection's
	// connect and introspection — 2m31s on an install with seventeen of them,
	// and unbounded behind a host that never answers. doctor is polled by the
	// UI and was read by `keeper vault unlock`, so that sum was charged to
	// screens and commands that only wanted a session list or a key source.
	rep.Connections = make([]ConnectionHealth, len(cs))
	probes := make([]chan ConnectionHealth, len(cs))
	pctx, cancel := context.WithTimeout(ctx, doctorProbeTimeout)
	defer cancel()
	for i, c := range cs {
		rep.Connections[i] = ConnectionHealth{
			ID: c.ID, Name: c.Name, Findings: len(c.Findings),
			Mode: c.Mode, Limits: c.Limits,
		}
		ch := make(chan ConnectionHealth, 1)
		probes[i] = ch
		go func(h ConnectionHealth) {
			// Open first: the backlog needs an open catalog too, and asking the
			// store before the catalog made an unreachable connection pay its
			// connect timeout twice. Opening takes no context, so an abandoned
			// probe runs on until pgx's connect timeout fires; the buffered
			// channel means it never blocks on a reader that has gone.
			if cat, err := d.deps.Catalogs(h.ID); err == nil {
				if un, err := d.deps.CatalogStore.Unclassified(pctx, h.ID); err == nil {
					h.Unclassified = len(un)
				}
				if fresh, err := cat.Fresh(pctx, nil); err == nil {
					h.CatalogFresh, h.FreshKnown = fresh, true
				}
			}
			ch <- h
		}(rep.Connections[i])
	}
	for i := range cs {
		select {
		case h := <-probes[i]:
			rep.Connections[i] = h
		case <-pctx.Done():
			// R5.6b: no answer is uncertainty, not permission. FreshKnown stays
			// false, which every reader already renders as "freshness unknown",
			// and the row keeps the facts the vault could supply.
		}
	}
	return rep
}
