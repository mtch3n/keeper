package daemon

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// ConnectionSummary is GET /v1/connections. Host, password and connection string
// are absent by construction: §6.1 returns the fields marked exposed and nothing
// assembles the others.
type ConnectionSummary struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Engine   string     `json:"engine"`
	Database string     `json:"database"`
	Role     string     `json:"role"`
	Degraded bool       `json:"degraded"`
	Enabled  bool       `json:"enabled"`
	Mode     types.Mode `json:"mode"`
}

// AuditedPrivileges is what G0 found and what a human agreed to.
type AuditedPrivileges struct {
	AuditedAt   time.Time          `json:"audited_at"`
	Findings    []types.Finding    `json:"findings,omitzero"`
	Acceptances []types.Acceptance `json:"acceptances,omitzero"`
	Unaccepted  []types.Finding    `json:"unaccepted,omitzero"`
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
	Enabled            bool                    `json:"enabled"`
	Degraded           bool                    `json:"degraded"`
	Degradations       []types.Degradation     `json:"degradations,omitzero"`
}

// Connections lists what is registered.
func (d *Daemon) Connections(ctx context.Context) ([]ConnectionSummary, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionSummary, 0, len(cs))
	for _, c := range cs {
		out = append(out, ConnectionSummary{
			ID: c.ID, Name: c.Name, Engine: c.Engine, Database: c.Database,
			Role: c.Role, Degraded: c.Degraded(), Enabled: c.Enabled, Mode: c.Mode,
		})
	}
	slices.SortStableFunc(out, func(a, b ConnectionSummary) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Describe is describe_connection. Every layer that could not be consulted is
// reported as a degradation rather than left to look like a clean answer (§7.10).
func (d *Daemon) Describe(ctx context.Context, id string) (*ConnectionDetail, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	det := &ConnectionDetail{
		ID: c.ID, Name: c.Name, Engine: c.Engine, Version: c.Version,
		Database: c.Database, Role: c.Role,
		AuditedPrivileges: AuditedPrivileges{
			AuditedAt: c.AuditedAt, Findings: c.Findings,
			Acceptances: c.Acceptances, Unaccepted: c.Unaccepted(),
		},
		CatalogStatus:      CatalogStatus{Path: c.CatalogPath},
		Mode:               c.Mode,
		Limits:             c.Limits,
		Denylist:           c.Denylist,
		WriteScope:         c.WriteScope,
		HasWriteCredential: c.HasWriteCredential,
		Enabled:            c.Enabled,
		Degraded:           c.Degraded(),
	}

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
	if fresh, err := d.freshness(ctx, id); err == nil {
		det.CatalogStatus.Fresh, det.CatalogStatus.FreshnessKnown = fresh, true
	} else {
		// R5.6b: a false answer is uncertainty, not permission. So is no answer.
		det.Degradations = append(det.Degradations, types.Degradation{Layer: "catalog", Reason: "freshness unknown"})
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

// ColumnView is one column of get_schema.
type ColumnView struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Nullable  bool              `json:"nullable"`
	IsPK      bool              `json:"is_pk,omitzero"`
	IsFK      bool              `json:"is_fk,omitzero"`
	Policy    types.Policy      `json:"policy"`
	Namespace string            `json:"namespace,omitzero"`
	Form      types.PartialForm `json:"form,omitzero"`
}

// TableView is one relation of get_schema.
type TableView struct {
	Schema string       `json:"schema"`
	Name   string       `json:"name"`
	Kind   string       `json:"kind"`
	Rows   []ColumnView `json:"columns"`
	// HiddenColumns counts the columns omitted because their *names* are
	// disclosive (R6.4b), and the ones the role may not read (R5.5). A name
	// hidden on one surface and disclosed on another is not hidden.
	HiddenColumns int `json:"hidden_columns,omitzero"`
	Unreadable    int `json:"unreadable_columns,omitzero"`
}

// SchemaView is GET /v1/connections/{id}/schema.
type SchemaView struct {
	Tables []TableView `json:"tables"`
}

// Schema is get_schema, filtered: a few hundred columns in one response is not
// usable (§6.1).
func (d *Daemon) Schema(ctx context.Context, connID, schema, table string) (*SchemaView, error) {
	if _, err := d.usableConnection(ctx, connID); err != nil {
		return nil, err
	}
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
				// surfaced in Catalog rather than raising the tier.
				p = types.ColumnPolicy{Policy: types.PolicyRedact}
			}
			if p.HideName {
				t.HiddenColumns++
				continue
			}
			t.Rows = append(t.Rows, ColumnView{
				Name: c.Name, Type: c.Type, Nullable: c.Nullable, IsPK: c.IsPK, IsFK: c.IsFK,
				Policy: p.Policy, Namespace: p.Namespace, Form: p.Form,
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

// RegisterSpec is POST /v1/connections.
type RegisterSpec struct {
	Name        string
	DSN         string
	WriteDSN    string
	CatalogPath string
}

// Register audits the role and stores the connection disabled. R4.1: audit,
// report, accept by name — never refuse, never accept silently. Nothing here
// logs or echoes the DSN.
func (d *Daemon) Register(ctx context.Context, spec RegisterSpec) (*types.Connection, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	if spec.Name == "" || spec.DSN == "" {
		return nil, errValidation("name and dsn", "are required")
	}
	findings, err := d.deps.Auditor.Audit(ctx, spec.DSN, ports.RoleRead)
	if err != nil {
		return nil, err
	}
	if spec.WriteDSN != "" {
		wf, werr := d.deps.Auditor.Audit(ctx, spec.WriteDSN, ports.RoleWrite)
		if werr != nil {
			return nil, werr
		}
		findings = append(findings, wf...)
	}
	user, database := dsnIdentity(spec.DSN)
	c := &types.Connection{
		ID:                 timeID(),
		Name:               spec.Name,
		Engine:             "postgres",
		Database:           database,
		Role:               user,
		CatalogPath:        spec.CatalogPath,
		Mode:               types.ModeAssisted,
		Limits:             types.DefaultLimits(),
		Findings:           findings,
		Enabled:            len(findings) == 0,
		AuditedAt:          d.now(),
		HasWriteCredential: spec.WriteDSN != "",
	}
	if err := d.deps.Vault.Register(ctx, c, spec.DSN, spec.WriteDSN); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "registered", "connection_id": c.ID}})
	return c, nil
}

// Audit re-runs G0. R4.1e: on an unaccepted finding the connection is disabled,
// its pooled connections are closed and its pending tickets are cancelled.
func (d *Daemon) Audit(ctx context.Context, id string) (*types.Connection, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	dsn, err := d.deps.Vault.DSN(ctx, id, ports.RoleRead)
	if err != nil {
		return nil, err
	}
	findings, err := d.deps.Auditor.Audit(ctx, dsn, ports.RoleRead)
	if err != nil {
		return nil, err
	}
	if c.HasWriteCredential {
		if wdsn, werr := d.deps.Vault.DSN(ctx, id, ports.RoleWrite); werr == nil {
			wf, aerr := d.deps.Auditor.Audit(ctx, wdsn, ports.RoleWrite)
			if aerr != nil {
				return nil, aerr
			}
			findings = append(findings, wf...)
		}
	}
	c.Findings = findings
	c.AuditedAt = d.now()
	c.Enabled = len(c.Unaccepted()) == 0
	if err := d.deps.Vault.Update(ctx, c); err != nil {
		return nil, err
	}
	if !c.Enabled {
		d.deps.Executor.Close(id)
		d.CancelTicketsForConnection(id)
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "audited", "connection_id": id, "enabled": c.Enabled}})
	return c, nil
}

// Accept records a human agreeing to named findings. R4.1f: via is "cli" or
// "ui" and never "mcp", and no mode, allow rule or model decision creates one.
func (d *Daemon) Accept(ctx context.Context, id string, findingIDs []string, actor, via string) (*types.Connection, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	if len(findingIDs) == 0 {
		return nil, errValidation("finding_ids", "must name at least one finding")
	}
	if actor == "" {
		return nil, errValidation("actor", "is required")
	}
	if via != "cli" && via != "ui" {
		return nil, errValidation("via", "must be cli or ui")
	}
	c, err := d.deps.Vault.Accept(ctx, id, findingIDs, actor, via)
	if err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "accepted", "connection_id": id}})
	return c, nil
}

// Patch is §4.5's operator limits. Unreachable from MCP (§6.3), which the api
// enforces by refusing the route to a request carrying a session.
type Patch struct {
	Mode   *types.Mode
	Limits *types.Limits
}

// Update applies a patch.
func (d *Daemon) Update(ctx context.Context, id string, p Patch) (*types.Connection, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	if p.Mode != nil {
		if !p.Mode.Valid() {
			return nil, errValidation("mode", "must be strict, assisted or permissive")
		}
		c.Mode = *p.Mode
	}
	if p.Limits != nil {
		if p.Limits.MaxRowsCeiling <= 0 {
			return nil, errValidation("limits.max_rows_ceiling", "must be positive")
		}
		if p.Limits.StatementTimeout <= 0 {
			return nil, errValidation("limits.statement_timeout", "must be positive")
		}
		c.Limits = *p.Limits
	}
	if err := d.deps.Vault.Update(ctx, c); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "updated", "connection_id": id}})
	return c, nil
}

// SetDenylist is R4.5's relation list. It is evaluated against the plan, never
// against statement text, which is the pipeline's job; the daemon only stores it.
func (d *Daemon) SetDenylist(ctx context.Context, id string, rels []types.RelationRef) (*types.Connection, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	for _, r := range rels {
		if r.Schema == "" || r.Relation == "" {
			return nil, errValidation("relations", "each entry needs a schema and a relation")
		}
	}
	c.Denylist = rels
	if err := d.deps.Vault.Update(ctx, c); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "denylist", "connection_id": id}})
	return c, nil
}

// Unlock is the interactive headless path of §4.3. The daemon starts locked
// because key source 4 cannot work for an auto-started daemon with no TTY.
func (d *Daemon) Unlock(ctx context.Context, passphrase string) error {
	if err := d.deps.Vault.Unlock(ctx, passphrase); err != nil {
		return err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "vault_unlocked"}})
	return nil
}

// dsnIdentity extracts the role and database from a DSN for display. It is
// deliberately best-effort and deliberately narrow: the role is the username and
// is disclosed on purpose (§6.1), the database name is not a credential, and
// nothing else from the DSN is ever read, stored outside the vault or logged.
func dsnIdentity(dsn string) (user, database string) {
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		if u.User != nil {
			user = u.User.Username()
		}
		return user, strings.TrimPrefix(u.Path, "/")
	}
	for f := range strings.FieldsSeq(dsn) {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "user":
			user = v
		case "dbname":
			database = v
		}
	}
	return user, database
}

// freshness asks one connection's catalog whether its fingerprints still match
// the database. R5.6b: a false answer is uncertainty rather than permission, and
// so is an error, which is why callers record a degradation for both.
func (d *Daemon) freshness(ctx context.Context, connID string) (bool, error) {
	cat, err := d.deps.Catalogs(connID)
	if err != nil {
		return false, err
	}
	return cat.Fresh(ctx, nil)
}

// Remove deletes a connection and its credentials.
//
// Everything scoped to it goes too: its pools are closed, its grants are
// revoked, and its catalog is dropped from the resolver. A grant naming a
// connection that no longer exists would be a rule nobody can read and nobody
// can revoke, which is the state R9.3e's list exists to prevent.
func (d *Daemon) Remove(ctx context.Context, id string) error {
	if d.deps.Vault.Locked() {
		return errVaultLocked
	}
	if _, err := d.deps.Vault.Connection(ctx, id); err != nil {
		return err
	}
	d.deps.Executor.Close(id)
	d.SuspendGrantsForConnection(id, "the connection was removed")

	d.mu.Lock()
	for gid, g := range d.grants {
		if g.Path.ConnectionID == id {
			delete(d.grants, gid)
		}
	}
	d.mu.Unlock()

	if err := d.deps.Vault.Remove(ctx, id); err != nil {
		return err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "removed", "connection_id": id}})
	return nil
}
