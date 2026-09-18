package daemon

import (
	"context"
	"time"
)

// RunStartupAudit is the first half of R4.1e: the privilege audit re-runs at
// every daemon start. It is a method rather than something New does, because the
// daemon starts with the vault locked (§3.4) and there is nothing to audit until
// a human unlocks it.
func (d *Daemon) RunStartupAudit(ctx context.Context) {
	if d.deps.Vault.Locked() {
		return
	}
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return
	}
	for _, c := range cs {
		if _, err := d.Audit(ctx, c.ID); err != nil {
			d.cfg.Logger.Warn("privilege re-audit failed", "connection", c.Name)
		}
	}
}

// StartSchedules starts R4.1e's 24-hour re-audit and the catalog freshness
// sweep. Both are no-ops while the vault is locked.
//
// The freshness sweep is R9.3b's mechanism, and it is coarser than R9.3b asks
// for. ports.Catalog.Fresh answers yes or no for a set of OIDs and never says
// which relation moved, and ports.Catalog is not scoped to a connection at all,
// so a daemon that only learns "something changed" suspends every grant rather
// than guessing which one a schema change touched. SuspendGrantsForRelation is
// the precise path for a caller that knows.
func (d *Daemon) StartSchedules(reaudit, freshness time.Duration) {
	if reaudit > 0 {
		d.wg.Go(func() { d.every(reaudit, func(ctx context.Context) { d.RunStartupAudit(ctx) }) })
	}
	if freshness > 0 {
		d.wg.Go(func() { d.every(freshness, d.checkFreshness) })
	}
}

func (d *Daemon) every(interval time.Duration, f func(context.Context)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-t.C:
			f(d.ctx)
		}
	}
}

func (d *Daemon) checkFreshness(ctx context.Context) {
	if d.deps.Vault.Locked() {
		return
	}
	// Every connection has its own catalog and its own answer; a single check
	// would suspend grants on connections whose schema never moved.
	stale := map[string]bool{}
	if cs, err := d.deps.Vault.Connections(ctx); err == nil {
		for _, c := range cs {
			if fresh, err := d.freshness(ctx, c.ID); err == nil && !fresh {
				stale[c.ID] = true
			}
		}
	}
	if len(stale) == 0 {
		return
	}
	d.mu.Lock()
	conns := map[string]struct{}{}
	for _, g := range d.grants {
		if !g.Suspended && stale[g.Path.ConnectionID] {
			conns[g.Path.ConnectionID] = struct{}{}
		}
	}
	d.mu.Unlock()
	for id := range conns {
		d.SuspendGrantsForConnection(id, "a schema change touched this connection; re-authorize the path")
	}
}
