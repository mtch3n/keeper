// Package catalog implements internal/ports' Catalog and CatalogStore: the
// per-connection classification store described in SPEC.md §5.
//
// Two files back every connection: ".keeper/catalog.yaml" in the project
// repo, which is committed, reviewed and shared (SPEC R5.2a), and
// ".keeper/catalog.local.yaml" beside it, a daemon-owned, gitignored overlay
// that automatic classification writes to instead (SPEC R5.2c). The merged
// view — overlay entries taking precedence over committed ones — is what the
// pipeline reads on every statement.
//
// A third file, catalog.db (modernc.org/sqlite, WAL), caches the identity
// resolution of SPEC §5.1: which (tableOID, attnum) currently names each
// catalogued column, and the fingerprint used to detect drift (SPEC §5.6).
// Names are storage; that cache is disposable and is rebuilt from a fresh
// introspection whenever the daemon resolves or recatalogs.
package catalog

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Paths locates one connection's catalog files. All three are supplied by
// the caller — the daemon derives Committed and Overlay from the
// connection's project repo (SPEC R5.2a) and Cache from its own state
// directory.
type Paths struct {
	// Committed is .keeper/catalog.yaml: human-edited, reviewed, shared.
	Committed string
	// Overlay is .keeper/catalog.local.yaml: daemon-owned, gitignored.
	Overlay string
	// Cache is catalog.db: the resolved-identity cache for this connection.
	Cache string
}

// Catalog is the read side of one connection's classification: SPEC's
// ports.Catalog, the interface the pipeline calls on every statement. It is
// safe for concurrent use.
type Catalog struct {
	connID string
	paths  Paths

	priv         PrivilegeChecker
	fingerprints Fingerprinter

	mu        sync.RWMutex
	committed map[columnKey]types.ColumnPolicy
	overlay   map[columnKey]types.ColumnPolicy
	merged    map[columnKey]types.ColumnPolicy
	cache     *identityCache

	// writeMu serializes the edit path (Put, Raise): read-modify-write-reload
	// against the YAML files. mu above guards only the in-memory maps, so a
	// concurrent Lookup never blocks on file I/O.
	writeMu sync.Mutex
}

// openCatalog loads a connection's committed and overlay files, opens its
// identity cache, and computes the merged view. It never talks to the
// database: identity resolution happens separately, through resolve, once an
// Introspector is available.
func openCatalog(connID string, paths Paths, priv PrivilegeChecker, fingerprints Fingerprinter) (*Catalog, error) {
	cache, err := openIdentityCache(paths.Cache)
	if err != nil {
		return nil, err
	}
	c := &Catalog{
		connID:       connID,
		paths:        paths,
		priv:         priv,
		fingerprints: fingerprints,
		cache:        cache,
	}
	if err := c.reload(); err != nil {
		cache.close()
		return nil, err
	}
	return c, nil
}

func (c *Catalog) close() error { return c.cache.close() }

// reload re-reads both YAML files from disk and recomputes the merged view.
// Callers must hold no lock; reload takes the write lock itself.
func (c *Catalog) reload() error {
	committed, err := loadYAMLFile(c.paths.Committed)
	if err != nil {
		return err
	}
	overlay, err := loadYAMLFile(c.paths.Overlay)
	if err != nil {
		return err
	}

	merged := maps.Clone(committed)
	maps.Copy(merged, overlay) // SPEC §5.2c: overlay wins over committed.

	c.mu.Lock()
	defer c.mu.Unlock()
	c.committed = committed
	c.overlay = overlay
	c.merged = merged
	return nil
}

// resolve replaces the identity cache with a fresh snapshot from relations
// (an Introspect result), so Lookup, Relation and RelationPolicies answer
// from current (tableOID, attnum) values. SPEC §5.1: resolved at connect and
// on fingerprint change.
func (c *Catalog) resolve(ctx context.Context, relations []ports.Relation) error {
	fingerprintOf := func(rel ports.Relation) (string, error) {
		if c.fingerprints == nil {
			return attributeFingerprint(rel), nil
		}
		return c.fingerprints.Fingerprint(ctx, c.connID, rel.OID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cache.resolve(relations, fingerprintOf)
}

// Lookup resolves a policy by the server's own column identity, never by
// output name. SPEC R7.5a. It reports false for a column with no catalog
// entry, which the pipeline treats as unknown per SPEC R5.4a.
func (c *Catalog) Lookup(tableOID uint32, attNum uint16) (types.ColumnPolicy, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cc, ok := c.cache.lookupColumn(tableOID, attNum)
	if !ok {
		return types.ColumnPolicy{}, false
	}
	p, ok := c.merged[cc.Column]
	return p, ok
}

// LookupName resolves a policy by name, for catalog editing and error
// composition — the paths that work from what a human typed rather than a
// RowDescription.
func (c *Catalog) LookupName(rel types.RelationRef, column string) (types.ColumnPolicy, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.merged[columnKey{Schema: rel.Schema, Table: rel.Relation, Column: column}]
	return p, ok
}

// Relation names a table OID, for error messages and audit records.
func (c *Catalog) Relation(tableOID uint32) (types.RelationRef, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ref, _, ok := c.cache.relation(tableOID)
	return ref, ok
}

// RelationPolicies returns every non-allow column of a relation, each with
// the type family SPEC R7.6's inheritance rule consumes.
func (c *Catalog) RelationPolicies(tableOID uint32) []ports.FamilyPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()

	cols, err := c.cache.relationColumns(tableOID)
	if err != nil || len(cols) == 0 {
		return nil
	}
	out := make([]ports.FamilyPolicy, 0, len(cols))
	for _, col := range cols {
		p, ok := c.merged[col.Column]
		if !ok || p.Policy == types.PolicyAllow {
			continue
		}
		out = append(out, ports.FamilyPolicy{
			Column: col.Column.Column,
			Family: Family(col.TypeOID),
			Policy: p,
		})
	}
	return out
}

// Readable reports the columns has_column_privilege allows the connection's
// role to read, so get_schema can omit the rest and an error can name what
// may be read. SPEC R5.5.
func (c *Catalog) Readable(ctx context.Context, rel types.RelationRef) ([]string, error) {
	if c.priv == nil {
		return nil, fmt.Errorf("catalog: %s: no privilege checker configured", c.connID)
	}
	return c.priv.ReadableColumns(ctx, c.connID, rel)
}

// Fresh reports whether the catalog's fingerprints still match the database.
// A false answer is uncertainty, not permission (SPEC R5.6b): with no
// Fingerprinter configured, or with a relation this cache has never resolved,
// freshness cannot be established and Fresh says so rather than guessing.
func (c *Catalog) Fresh(ctx context.Context, relations []uint32) (bool, error) {
	if c.fingerprints == nil {
		return false, nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, oid := range relations {
		stored, ok := c.cache.storedFingerprint(oid)
		if !ok {
			return false, nil
		}
		live, err := c.fingerprints.Fingerprint(ctx, c.connID, oid)
		if err != nil {
			return false, fmt.Errorf("catalog: fingerprinting relation %d: %w", oid, err)
		}
		if live != stored {
			return false, nil
		}
	}
	return true, nil
}

var _ ports.Catalog = (*Catalog)(nil)
