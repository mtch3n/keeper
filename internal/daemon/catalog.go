package daemon

import (
	"context"
	"slices"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// CatalogView is GET /v1/catalog/{connection}: the merged committed file and
// daemon overlay, with the unclassified backlog that the Catalog screen exists
// to drain.
type CatalogView struct {
	ConnectionID      string                        `json:"connection_id"`
	Entries           map[string]types.ColumnPolicy `json:"entries"`
	Unclassified      []string                      `json:"unclassified,omitzero"`
	UnclassifiedCount int                           `json:"unclassified_count"`
	PolicySummary     map[types.Policy]int          `json:"policy_summary,omitzero"`
}

// Catalog reads the merged catalog.
func (d *Daemon) Catalog(ctx context.Context, connID string) (*CatalogView, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	entries, err := d.deps.CatalogStore.Entries(ctx, connID)
	if err != nil {
		return nil, err
	}
	un, err := d.deps.CatalogStore.Unclassified(ctx, connID)
	if err != nil {
		return nil, err
	}
	summary := map[types.Policy]int{}
	for _, p := range entries {
		summary[p.Policy]++
	}
	slices.Sort(un)
	return &CatalogView{
		ConnectionID: connID, Entries: entries, Unclassified: un,
		UnclassifiedCount: len(un), PolicySummary: summary,
	}, nil
}

// PutColumns promotes entries into the committed file. Every entry is validated
// before it is written: R5.2b's missing namespace and R5.2d's missing form are
// validation errors, never defaults.
func (d *Daemon) PutColumns(ctx context.Context, connID string, entries map[string]types.ColumnPolicy) error {
	if d.deps.Vault.Locked() {
		return errVaultLocked
	}
	if len(entries) == 0 {
		return errValidation("entries", "must name at least one column")
	}
	for key, p := range entries {
		if err := p.Validate(); err != nil {
			return errValidation(key, err.Error())
		}
	}
	if err := d.deps.CatalogStore.Put(ctx, connID, entries); err != nil {
		return err
	}
	d.hub.Publish(Event{Type: EventCatalog, Data: map[string]any{"action": "columns", "connection_id": connID, "count": len(entries)}})
	return nil
}

// InitCatalog is catalog init: a proposal for every column, grouped by how safe
// the proposal is to accept without reading it (R5.3).
func (d *Daemon) InitCatalog(ctx context.Context, connID string, sample int) (*ports.InitProposal, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	if sample < 0 {
		return nil, errValidation("sample", "must not be negative")
	}
	p, err := d.deps.CatalogStore.Init(ctx, connID, sample)
	if err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventCatalog, Data: map[string]any{"action": "init", "connection_id": connID}})
	return p, nil
}

// SuggestGrants is §5.5's GRANT SELECT (...) per table. §2.4: use column grants
// first, where you can.
func (d *Daemon) SuggestGrants(ctx context.Context, connID string) ([]string, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	return d.deps.CatalogStore.SuggestGrants(ctx, connID)
}

// Activity is the query log (§10) behind the Activity screen.
func (d *Daemon) Activity(ctx context.Context, f ports.AuditFilter) ([]types.AuditRecord, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	return d.deps.Audit.Query(ctx, f)
}

// ActivityRecord is one audit record in full.
func (d *Daemon) ActivityRecord(ctx context.Context, id string) (*types.AuditRecord, error) {
	r, err := d.deps.Audit.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errTicketUnknown
	}
	return r, nil
}
