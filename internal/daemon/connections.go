package daemon

import (
	"context"
	"slices"
	"strings"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/sealed"
	"github.com/mtchen/keeper/internal/types"
)

// Profile is what an agent may know about where a connection logs in: enough to
// find the profile a person names ("orders_rw on prod-db"), never the password,
// which nothing outside the vault assembles.
type Profile struct {
	Host     string       `json:"host"`
	Address  string       `json:"address"`
	Port     int          `json:"port"`
	Database string       `json:"database"`
	Username string       `json:"username"`
	Writes   types.Writes `json:"writes"`
	Reads    types.Reads  `json:"reads"`
}

// ConnectionSummary is GET /v1/connections.
type ConnectionSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Profile
}

// CatalogStatus is the freshness and backlog half of describe_connection.
type CatalogStatus struct {
	Path            string   `json:"path"`
	Fresh           bool     `json:"fresh"`
	FreshnessKnown  bool     `json:"freshness_known"`
	Unclassified    int      `json:"unclassified"`
	UnclassifiedTop []string `json:"unclassified_top,omitzero"`
}

// ConnectionDetail is GET /v1/connections/{id}.
type ConnectionDetail struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	Version string `json:"version,omitzero"`
	Profile
	Schemas       []string             `json:"schemas,omitzero"`
	CatalogStatus CatalogStatus        `json:"catalog_status"`
	PolicySummary map[types.Policy]int `json:"policy_summary,omitzero"`
	Detection     types.Detection      `json:"detection"`
	Limits        types.Limits         `json:"limits"`
	Degradations  []types.Degradation  `json:"degradations,omitzero"`
}

// Connections lists what is registered.
func (d *Daemon) Connections(ctx context.Context) ([]ConnectionSummary, error) {
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionSummary, 0, len(cs))
	for _, c := range cs {
		out = append(out, ConnectionSummary{
			ID: c.ID, Name: c.Name, Engine: c.Engine,
			Profile: d.profile(ctx, c),
		})
	}
	slices.SortStableFunc(out, func(a, b ConnectionSummary) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// effectiveReads is what the pipeline does with a stored value: an empty or
// unknown one asks on every read, so it shows as always.
func effectiveReads(r types.Reads) types.Reads {
	if r.Valid() {
		return r
	}
	return types.ReadsAlways
}

// effectiveWrites is what the pipeline does with a stored value: anything but
// ask or allow, such as an old approve, refuses writes, so it shows as off.
func effectiveWrites(w types.Writes) types.Writes {
	if w.Valid() {
		return w
	}
	return types.WritesOff
}

// updated is c as an operator sees it: a copy with the effective settings.
func updated(c *types.Connection) *types.Connection {
	v := *c
	v.Reads = effectiveReads(v.Reads)
	v.Writes = effectiveWrites(v.Writes)
	return &v
}

// profile is c's login without its password. A host the vault cannot find
// leaves the host fields empty rather than failing the listing.
func (d *Daemon) profile(ctx context.Context, c *types.Connection) Profile {
	p := Profile{Database: c.Database, Username: c.Role, Reads: effectiveReads(c.Reads), Writes: effectiveWrites(c.Writes)}
	if h, err := d.deps.Vault.Host(ctx, c.HostID); err == nil && h != nil {
		p.Host, p.Address, p.Port = h.Name, h.Address, h.Port
	}
	return p
}

// Describe is describe_connection. Every layer that could not be consulted is
// reported as a degradation rather than left to look like a clean answer (§7.10).
func (d *Daemon) Describe(ctx context.Context, ref string) (*ConnectionDetail, error) {
	c, err := d.usableConnection(ctx, ref)
	if err != nil {
		return nil, err
	}
	id := c.ID
	det := &ConnectionDetail{
		ID: c.ID, Name: c.Name, Engine: c.Engine, Version: c.Version,
		Profile:       d.profile(ctx, c),
		CatalogStatus: CatalogStatus{Path: c.CatalogPath},
		Detection:     c.Detection.Effective(),
		Limits:        c.Limits,
	}

	// Every catalog layer needs the catalog open, and opening is what an
	// unreachable database answers with a connect timeout. Asking once keeps
	// describe from paying that timeout again for each layer.
	if cat, err := d.deps.Catalogs(id); err != nil {
		for _, reason := range []string{"entries unavailable", "backlog unavailable", "freshness unknown"} {
			det.Degradations = append(det.Degradations, types.Degradation{Layer: "catalog", Reason: reason})
		}
	} else {
		d.describeCatalog(ctx, id, cat, det)
	}
	if rels, err := d.deps.Executor.Introspect(ctx, id); err == nil {
		for _, r := range rels {
			if !slices.Contains(det.Schemas, r.Ref.Schema) {
				det.Schemas = append(det.Schemas, r.Ref.Schema)
			}
		}
		slices.Sort(det.Schemas)
	} else {
		det.Degradations = append(det.Degradations, types.Degradation{Layer: "catalog", Reason: "introspection unavailable"})
	}
	return det, nil
}

// describeCatalog fills describe's catalog layers from an open catalog.
func (d *Daemon) describeCatalog(ctx context.Context, id string, cat ports.Catalog, det *ConnectionDetail) {
	if entries, err := d.deps.CatalogStore.Entries(ctx, id); err == nil {
		det.PolicySummary = map[types.Policy]int{}
		for _, p := range entries {
			det.PolicySummary[p.Policy]++
		}
	} else {
		det.Degradations = append(det.Degradations, types.Degradation{Layer: "catalog", Reason: "entries unavailable"})
	}
	if un, err := d.deps.CatalogStore.Unclassified(ctx, id); err == nil {
		det.CatalogStatus.Unclassified = len(un)
		det.CatalogStatus.UnclassifiedTop = un[:min(len(un), 20)]
	} else {
		det.Degradations = append(det.Degradations, types.Degradation{Layer: "catalog", Reason: "backlog unavailable"})
	}
	if fresh, err := cat.Fresh(ctx, nil); err == nil {
		det.CatalogStatus.Fresh, det.CatalogStatus.FreshnessKnown = fresh, true
	} else {
		// R5.6b: a false answer is uncertainty, not permission. So is no answer.
		det.Degradations = append(det.Degradations, types.Degradation{Layer: "catalog", Reason: "freshness unknown"})
	}
}

// ColumnView is one column of get_schema.
type ColumnView struct {
	Name      string       `json:"name"`
	Type      string       `json:"type"`
	Nullable  bool         `json:"nullable"`
	IsPK      bool         `json:"is_pk,omitzero"`
	IsFK      bool         `json:"is_fk,omitzero"`
	Policy    types.Policy `json:"policy"`
	Namespace string       `json:"namespace,omitzero"`
}

// TableView is one relation of get_schema.
type TableView struct {
	Schema string       `json:"schema"`
	Name   string       `json:"name"`
	Kind   string       `json:"kind"`
	Rows   []ColumnView `json:"columns"`
	// Unreadable counts the columns the role may not read.
	Unreadable int `json:"unreadable_columns,omitzero"`
}

// SchemaView is GET /v1/connections/{id}/schema.
type SchemaView struct {
	Tables []TableView `json:"tables"`
}

// Schema is get_schema, filtered: a few hundred columns in one response is not
// usable (§6.1).
func (d *Daemon) Schema(ctx context.Context, connID, schema, table string) (*SchemaView, error) {
	conn, err := d.usableConnection(ctx, connID)
	if err != nil {
		return nil, err
	}
	connID = conn.ID
	rels, err := d.deps.Executor.Introspect(ctx, connID)
	if err != nil {
		return nil, err
	}
	cat, err := d.deps.Catalogs(connID)
	if err != nil {
		return nil, err
	}
	out := &SchemaView{Tables: []TableView{}}
	for _, r := range rels {
		if schema != "" && !strings.EqualFold(r.Ref.Schema, schema) {
			continue
		}
		if !matchRelation(r.Ref.Relation, table) {
			continue
		}
		readable, rerr := cat.Readable(ctx, r.Ref)
		t := TableView{Schema: r.Ref.Schema, Name: r.Ref.Relation, Kind: string(r.Kind), Rows: []ColumnView{}}
		for _, c := range r.Columns {
			if rerr == nil && !slices.Contains(readable, c.Name) {
				t.Unreadable++
				continue
			}
			p, ok := cat.LookupName(r.Ref, c.Name)
			if !ok {
				// R5.4a: an unresolved column is redacted, not refused, and is
				// surfaced in Catalog.
				p = types.ColumnPolicy{Policy: types.PolicyRedact}
			}
			t.Rows = append(t.Rows, ColumnView{
				Name: c.Name, Type: c.Type, Nullable: c.Nullable, IsPK: c.IsPK, IsFK: c.IsFK,
				Policy: p.Policy, Namespace: p.Namespace,
			})
		}
		out.Tables = append(out.Tables, t)
	}
	return out, nil
}

func matchRelation(name, pattern string) bool {
	switch {
	case pattern == "":
		return true
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(strings.ToLower(name), strings.ToLower(strings.TrimSuffix(pattern, "*")))
	default:
		return strings.EqualFold(name, pattern)
	}
}

// RegisterSpec is POST /v1/connections: one database and role on a host the
// operator registered before.
type RegisterSpec struct {
	Name       string
	HostID     string
	Database   string
	Credential ports.Credential
	// Writes is off unless the operator lets this profile's sessions write.
	Writes      types.Writes
	CatalogPath string
}

// Register stores the connection. Nothing here connects to the database, and
// nothing logs or echoes a credential.
func (d *Daemon) Register(ctx context.Context, spec RegisterSpec) (*types.Connection, error) {
	if spec.Name == "" || spec.HostID == "" || spec.Database == "" || spec.Credential.User == "" {
		return nil, errValidation("name, host, database and user", "are required")
	}
	writes, err := validWrites(spec.Writes)
	if err != nil {
		return nil, err
	}
	if h, err := d.deps.Vault.Host(ctx, spec.HostID); err != nil || h == nil {
		return nil, errUnknownHost
	}
	c := &types.Connection{
		ID:          timeID(),
		HostID:      spec.HostID,
		Name:        spec.Name,
		Engine:      "postgres",
		Database:    spec.Database,
		Role:        spec.Credential.User,
		Writes:      writes,
		CatalogPath: spec.CatalogPath,
		Reads:       types.ReadsUncertain,
		Detection:   types.DetectionScan,
		Limits:      types.DefaultLimits(),
	}
	if err := d.deps.Vault.Register(ctx, c, spec.Credential); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "registered", "connection_id": c.ID}})
	return c, nil
}

// TestConnection logs in with a connection's credential and reports who the
// server says it is. It is the only operator action that connects without a
// statement, and it is asked for, never run on a schedule.
func (d *Daemon) TestConnection(ctx context.Context, id string) (*ports.PingResult, error) {
	if c, err := d.deps.Vault.Connection(ctx, id); err != nil || c == nil {
		return nil, errUnknownConnection
	}
	return d.deps.Executor.Ping(ctx, id)
}

// Patch is §4.5's operator limits. Unreachable from MCP (§6.3), which the api
// enforces by refusing the route to a request carrying a session.
type Patch struct {
	Reads     *types.Reads
	Limits    *types.Limits
	Detection *types.Detection
	Writes    *types.Writes
}

// validWrites accepts off, ask and allow; empty is off.
func validWrites(w types.Writes) (types.Writes, error) {
	if w == "" {
		return types.WritesOff, nil
	}
	if !w.Valid() {
		return "", errValidation("writes", "must be off, ask or allow")
	}
	return w, nil
}

// Update applies a patch.
func (d *Daemon) Update(ctx context.Context, id string, p Patch) (*types.Connection, error) {
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	if p.Reads != nil {
		if !p.Reads.Valid() {
			return nil, errValidation("reads", "must be always, uncertain or never")
		}
		c.Reads = *p.Reads
	}
	if p.Writes != nil {
		w, err := validWrites(*p.Writes)
		if err != nil {
			return nil, err
		}
		c.Writes = w
	}
	if p.Detection != nil {
		if !p.Detection.Valid() {
			return nil, errValidation("detection", "must be off, scan or all")
		}
		c.Detection = *p.Detection
	}
	if p.Limits != nil {
		if p.Limits.MaxRowsCeiling <= 0 {
			return nil, errValidation("limits.max_rows_ceiling", "must be positive")
		}
		if p.Limits.StatementTimeout <= 0 {
			return nil, errValidation("limits.statement_timeout", "must be positive")
		}
		if p.Limits.MaxBytes <= 0 {
			return nil, errValidation("limits.max_bytes", "must be positive")
		}
		c.Limits = *p.Limits
	}
	if err := d.deps.Vault.Update(ctx, c); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "updated", "connection_id": id}})
	return updated(c), nil
}

// OpenVault resolves the master key and decrypts the vault, returning the key
// source that answered. keeperd calls it once, before it serves.
//
// R4.3 requires the source in use to be named, and this is where it becomes
// known. Nothing else asks: `doctor` reports it from the vault, and asking
// doctor for it instead would charge one string a database connect per
// registered connection.
func (d *Daemon) OpenVault(ctx context.Context) (string, error) {
	if err := d.deps.Vault.Open(ctx); err != nil {
		return "", err
	}
	return d.deps.Vault.KeySource(), nil
}

// ExportVault is `keeper vault export`: every connection record and credential,
// encrypted, for the operator to store somewhere the keychain is not.
//
// It is the only way back. With no passphrase source there is no second way to
// derive the master key, so a keychain item lost to a reinstall or a profile
// reset takes every registered connection with it unless an export exists. That
// is the trade the automatic open makes, and this is the other half of it.
func (d *Daemon) ExportVault(ctx context.Context, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errValidation("passphrase", "is required")
	}
	plain, err := d.deps.Vault.Export(ctx)
	if err != nil {
		return nil, err
	}
	return sealed.Seal(plain, passphrase)
}

// ImportVault replaces this daemon's vault with an export, keys included.
func (d *Daemon) ImportVault(ctx context.Context, data []byte, passphrase string) error {
	if err := d.deps.Vault.Import(ctx, data, passphrase); err != nil {
		return err
	}
	// Pools and catalogs are keyed by connection id and hold the old
	// credentials and the old database's column identities. An id the import
	// reuses would otherwise keep reaching the old server.
	if d.deps.VaultReplaced != nil {
		d.deps.VaultReplaced()
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "imported"}})
	return nil
}

// Remove deletes a connection and its credentials.
//
// Its pools are closed with it.
func (d *Daemon) Remove(ctx context.Context, id string) error {
	if _, err := d.deps.Vault.Connection(ctx, id); err != nil {
		return err
	}
	d.deps.Executor.Close(id)
	if err := d.deps.Vault.Remove(ctx, id); err != nil {
		return err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "removed", "connection_id": id}})
	return nil
}
