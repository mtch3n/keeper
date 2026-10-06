package daemon

import (
	"context"
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
	Mode     types.Mode `json:"mode"`
}

// AuditedPrivileges is what G0 found on one connection, and when. It is a
// report and nothing else: no field here decides whether the connection runs.
type AuditedPrivileges struct {
	AuditedAt time.Time       `json:"audited_at"`
	Findings  []types.Finding `json:"findings,omitzero"`
}

// AuditReport is one connection's entry in GET /v1/audit. The privilege audit
// is its own surface rather than a step in registration, so it carries enough
// of the connection to be read on its own. SPEC R4.1.
type AuditReport struct {
	ConnectionID string          `json:"connection_id"`
	Name         string          `json:"name"`
	Database     string          `json:"database"`
	Role         string          `json:"role"`
	AuditedAt    time.Time       `json:"audited_at"`
	Findings     []types.Finding `json:"findings,omitzero"`
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
	Detection          []types.Pass            `json:"detection"`
	Limits             types.Limits            `json:"limits"`
	Denylist           []types.RelationRef     `json:"denylist,omitzero"`
	WriteScope         []types.WriteScopeEntry `json:"write_scope,omitzero"`
	HasWriteCredential bool                    `json:"has_write_credential"`
	Degradations       []types.Degradation     `json:"degradations,omitzero"`
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
			ID: c.ID, Name: c.Name, Engine: c.Engine, Database: c.Database,
			Role: c.Role, Mode: c.Mode,
		})
	}
	slices.SortStableFunc(out, func(a, b ConnectionSummary) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Describe is describe_connection. Every layer that could not be consulted is
// reported as a degradation rather than left to look like a clean answer (§7.10).
func (d *Daemon) Describe(ctx context.Context, id string) (*ConnectionDetail, error) {
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	det := &ConnectionDetail{
		ID: c.ID, Name: c.Name, Engine: c.Engine, Version: c.Version,
		Database: c.Database, Role: c.Role,
		AuditedPrivileges:  AuditedPrivileges{AuditedAt: c.AuditedAt, Findings: c.Findings},
		CatalogStatus:      CatalogStatus{Path: c.CatalogPath},
		Mode:               c.Mode,
		Detection:          c.Detection,
		Limits:             c.Limits,
		Denylist:           c.Denylist,
		WriteScope:         c.WriteScope,
		HasWriteCredential: c.HasWriteCredential,
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

// RegisterSpec is POST /v1/connections: one database and role on a host the
// operator registered before.
type RegisterSpec struct {
	Name     string
	HostID   string
	Database string
	Read     ports.Credential
	// Write is nil when no _rw credential is being registered. There is no
	// boolean that enables writes: SPEC §4.2.
	Write       *ports.Credential
	CatalogPath string
}

// Register stores the connection and runs G0 once so the audit report has
// something to show. R4.1: the audit reports, it does not gate — a registered
// connection is usable whatever the audit found, and what to do about a
// finding is a separate decision the operator makes from the audit surface.
// Nothing here logs or echoes a credential.
//
// It stores before it audits so the audit reads its DSN from the vault, the
// one place a DSN is assembled, exactly as a later re-audit does.
func (d *Daemon) Register(ctx context.Context, spec RegisterSpec) (*types.Connection, error) {
	if spec.Name == "" || spec.HostID == "" || spec.Database == "" || spec.Read.User == "" {
		return nil, errValidation("name, host, database and user", "are required")
	}
	if spec.Write != nil && spec.Write.User == "" {
		return nil, errValidation("write user", "is required with a write credential")
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
		Role:        spec.Read.User,
		CatalogPath: spec.CatalogPath,
		Mode:        types.ModeAssisted,
		Detection:   []types.Pass{types.PassPatterns},
		Limits:      types.DefaultLimits(),
	}
	if err := d.deps.Vault.Register(ctx, c, spec.Read, spec.Write); err != nil {
		return nil, err
	}
	// An audit that cannot run is not a connection keeper refuses to hold. G0
	// reads pg_authid, pg_proc and the ACLs, and a managed server that hides
	// them, a role without the introspection grants, or a host that is down at
	// registration all arrived here as "cannot register at all" — which taught
	// the operator to keep exactly the connections worth masking outside
	// keeper. R4.1 says the audit reports and does not gate; failing to produce
	// a report is the strongest form of not gating there is. A zero AuditedAt
	// is how the connection says it has no report yet.
	if findings, err := d.auditRoles(ctx, c); err == nil {
		c.Findings, c.AuditedAt = findings, d.now()
		if err := d.deps.Vault.Update(ctx, c); err != nil {
			return nil, err
		}
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "registered", "connection_id": c.ID}})
	return c, nil
}

// auditRoles runs G0 over every credential the connection holds.
//
// It is all-or-nothing on purpose: a half-collected report rendered beside a
// timestamp claims a completeness it does not have, and the question the audit
// surface answers is "what can this role do", which no partial answer answers.
func (d *Daemon) auditRoles(ctx context.Context, c *types.Connection) ([]types.Finding, error) {
	dsn, err := d.deps.Vault.DSN(ctx, c.ID, ports.RoleRead)
	if err != nil {
		return nil, err
	}
	findings, err := d.deps.Auditor.Audit(ctx, dsn, ports.RoleRead)
	if err != nil {
		return nil, err
	}
	if c.HasWriteCredential {
		wdsn, err := d.deps.Vault.DSN(ctx, c.ID, ports.RoleWrite)
		if err != nil {
			return nil, err
		}
		wf, err := d.deps.Auditor.Audit(ctx, wdsn, ports.RoleWrite)
		if err != nil {
			return nil, err
		}
		findings = append(findings, wf...)
	}
	return findings, nil
}

// Audit re-runs G0 and replaces the stored report. R4.1e: it neither disables
// the connection nor cancels anything in flight. A privilege that appears
// between runs is a line in the audit report, and the operator decides whether
// to narrow the grant — keeper noticing was never what constrained the role.
func (d *Daemon) Audit(ctx context.Context, id string) (*types.Connection, error) {
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	findings, err := d.auditRoles(ctx, c)
	if err != nil {
		return nil, err
	}
	c.Findings = findings
	c.AuditedAt = d.now()
	if err := d.deps.Vault.Update(ctx, c); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "audited", "connection_id": id, "findings": len(findings)}})
	return c, nil
}

// Audits is GET /v1/audit: the last privilege audit for every connection, in
// the same order the connection list uses.
func (d *Daemon) Audits(ctx context.Context) ([]AuditReport, error) {
	cs, err := d.deps.Vault.Connections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AuditReport, 0, len(cs))
	for _, c := range cs {
		out = append(out, AuditReport{
			ConnectionID: c.ID, Name: c.Name, Database: c.Database,
			Role: c.Role, AuditedAt: c.AuditedAt, Findings: c.Findings,
		})
	}
	slices.SortStableFunc(out, func(a, b AuditReport) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// Patch is §4.5's operator limits. Unreachable from MCP (§6.3), which the api
// enforces by refusing the route to a request carrying a session.
type Patch struct {
	Mode   *types.Mode
	Limits *types.Limits
	// Detection replaces the connection's passes; an empty list turns
	// detection off.
	Detection *[]types.Pass
}

// Update applies a patch.
func (d *Daemon) Update(ctx context.Context, id string, p Patch) (*types.Connection, error) {
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
	if p.Detection != nil {
		if err := validPasses(*p.Detection); err != nil {
			return nil, err
		}
		c.Detection = slices.Clone(*p.Detection)
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
func (d *Daemon) ExportVault(ctx context.Context) ([]byte, error) {
	return d.deps.Vault.Export(ctx)
}

// RotateMaster re-encrypts the vault under a fresh master key, in place.
func (d *Daemon) RotateMaster(ctx context.Context) error {
	return d.deps.Vault.RotateMaster(ctx)
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

// validPasses refuses a pass keeper does not have and a pass named twice.
func validPasses(ps []types.Pass) error {
	names := make([]string, len(types.Passes))
	for i, p := range types.Passes {
		names[i] = string(p)
	}
	for i, p := range ps {
		if !slices.Contains(types.Passes, p) {
			return errValidation("detection", "passes must be from: "+strings.Join(names, ", "))
		}
		if slices.Contains(ps[:i], p) {
			return errValidation("detection", "names the "+string(p)+" pass twice")
		}
	}
	return nil
}

// TermCounts is PUT /v1/connections/{id}/terms. It reports how many terms are
// stored and never the terms themselves.
type TermCounts struct {
	Deny  int `json:"deny"`
	Allow int `json:"allow"`
}

// SetTerms replaces a connection's list-pass terms. Blank terms are dropped:
// an empty deny term would match everywhere.
func (d *Daemon) SetTerms(ctx context.Context, id string, t ports.Terms) (*TermCounts, error) {
	if c, err := d.deps.Vault.Connection(ctx, id); err != nil || c == nil {
		return nil, errUnknownConnection
	}
	clean := func(in []string) []string {
		out := []string{}
		for _, s := range in {
			if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		return out
	}
	t = ports.Terms{Deny: clean(t.Deny), Allow: clean(t.Allow)}
	if err := d.deps.Vault.SetTerms(ctx, id, t); err != nil {
		return nil, err
	}
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "terms", "connection_id": id}})
	return &TermCounts{Deny: len(t.Deny), Allow: len(t.Allow)}, nil
}
