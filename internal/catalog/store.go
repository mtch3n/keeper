package catalog

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Dependencies are the collaborators Store needs. Every field is optional:
// a nil dependency narrows what Store can do (Init without a Sampler skips
// row sampling, for instance) rather than causing a panic. internal/pgdb is
// expected to satisfy Introspector, ColumnSampler, PrivilegeChecker and
// Fingerprinter; internal/rules is expected to satisfy NameHeuristic and
// RuleMatcher. This package imports neither.
type Dependencies struct {
	Introspector Introspector
	Sampler      ColumnSampler
	Names        NameHeuristic
	Rules        RuleMatcher
	Privileges   PrivilegeChecker
	Fingerprints Fingerprinter
}

// Store is internal/catalog's implementation of ports.CatalogStore: the edit
// path used by the CLI and the UI, across every registered connection. The
// daemon constructs one Store and opens one Catalog per connection through
// it; the returned *Catalog is what gets wired into the pipeline as
// ports.Catalog.
type Store struct {
	deps Dependencies

	mu    sync.RWMutex
	conns map[string]*Catalog
}

// NewStore builds a Store. CONTRACT.md §2: no globals, every package exposes
// a constructor taking its dependencies.
func NewStore(deps Dependencies) *Store {
	return &Store{
		deps:  deps,
		conns: make(map[string]*Catalog),
	}
}

// Open loads a connection's catalog files, resolves identity against a fresh
// introspection when an Introspector is configured, registers the result,
// and returns it as the ports.Catalog to wire into the pipeline for this
// connection.
func (s *Store) Open(ctx context.Context, connID string, paths Paths) (*Catalog, error) {
	c, err := openCatalog(connID, paths, s.deps.Privileges, s.deps.Fingerprints)
	if err != nil {
		return nil, err
	}
	if s.deps.Introspector != nil {
		if err := s.recatalogIdentity(ctx, c); err != nil {
			c.close()
			return nil, err
		}
	}

	s.mu.Lock()
	s.conns[connID] = c
	s.mu.Unlock()
	return c, nil
}

// Close releases a connection's catalog files and identity cache and drops
// it from the registry. It is the daemon-side counterpart of Open, used on
// disable and on shutdown.
func (s *Store) Close(connID string) error {
	s.mu.Lock()
	c, ok := s.conns[connID]
	delete(s.conns, connID)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return c.close()
}

// Recatalog re-resolves a connection's identity cache from a fresh
// introspection. SPEC R5.6a: the agent can trigger this but not influence its
// outcome, which derives from pg_attribute and the committed YAML.
func (s *Store) Recatalog(ctx context.Context, connID string) error {
	c, err := s.get(connID)
	if err != nil {
		return err
	}
	if s.deps.Introspector == nil {
		return fmt.Errorf("catalog: no introspector configured")
	}
	return s.recatalogIdentity(ctx, c)
}

func (s *Store) recatalogIdentity(ctx context.Context, c *Catalog) error {
	relations, err := s.deps.Introspector.Introspect(ctx, c.connID)
	if err != nil {
		return fmt.Errorf("catalog: introspecting %s: %w", c.connID, err)
	}
	return c.resolve(ctx, relations)
}

func (s *Store) get(connID string) (*Catalog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.conns[connID]
	if !ok {
		return nil, fmt.Errorf("catalog: connection %q is not open", connID)
	}
	return c, nil
}

// Entries returns the merged committed file and daemon overlay, keyed
// "schema.table.column" per CONTRACT.md §3's wire format.
func (s *Store) Entries(ctx context.Context, connID string) (map[string]types.ColumnPolicy, error) {
	c, err := s.get(connID)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]types.ColumnPolicy, len(c.merged))
	for key, p := range c.merged {
		out[key.String()] = p
	}
	return out, nil
}

// Put writes to the committed file and promotes any matching overlay entries
// out of the overlay, since a promoted automatic raise is no longer pending
// review. SPEC R5.2c.
func (s *Store) Put(ctx context.Context, connID string, entries map[string]types.ColumnPolicy) error {
	c, err := s.get(connID)
	if err != nil {
		return err
	}

	keyed := make(map[columnKey]types.ColumnPolicy, len(entries))
	for keyStr, p := range entries {
		key, err := parseKey(keyStr)
		if err != nil {
			return err
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("catalog: %s: %w", keyStr, err)
		}
		keyed[key] = p
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	committed := maps.Clone(c.committed)
	overlay := maps.Clone(c.overlay)
	c.mu.RUnlock()

	for key, p := range keyed {
		committed[key] = p
		delete(overlay, key)
	}

	if err := saveYAMLFile(c.paths.Committed, committed); err != nil {
		return err
	}
	if err := saveYAMLFile(c.paths.Overlay, overlay); err != nil {
		return err
	}
	return c.reload()
}

// Raise writes an automatic classification change to the overlay. It never
// lowers a policy and never overwrites a human-authored (committed) entry.
// SPEC R5.3, R5.3a, R5.3b.
func (s *Store) Raise(ctx context.Context, connID, key string, p types.ColumnPolicy) error {
	c, err := s.get(connID)
	if err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	ck, err := parseKey(key)
	if err != nil {
		return err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	_, humanAuthored := c.committed[ck]
	current, hasOverlay := c.overlay[ck]
	overlay := maps.Clone(c.overlay)
	c.mu.RUnlock()

	if humanAuthored {
		return nil // a committed entry is a human decision; never overwritten.
	}

	currentPolicy := types.PolicyAllow // the implicit default when uncatalogued.
	if hasOverlay {
		currentPolicy = current.Policy
	}
	if currentPolicy == types.PolicyDrop || p.Policy.Rank() <= currentPolicy.Rank() {
		return nil // never lowers, and drop is never touched automatically.
	}

	overlay[ck] = p
	if err := saveYAMLFile(c.paths.Overlay, overlay); err != nil {
		return err
	}
	return c.reload()
}

// Unclassified lists every introspected column with neither a committed nor
// an overlay entry: the Catalog screen's backlog.
func (s *Store) Unclassified(ctx context.Context, connID string) ([]string, error) {
	c, err := s.get(connID)
	if err != nil {
		return nil, err
	}
	if s.deps.Introspector == nil {
		return nil, fmt.Errorf("catalog: no introspector configured")
	}
	relations, err := s.deps.Introspector.Introspect(ctx, connID)
	if err != nil {
		return nil, fmt.Errorf("catalog: introspecting %s: %w", connID, err)
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	var out []string
	for _, rel := range relations {
		for _, col := range rel.Columns {
			key := columnKey{Schema: rel.Ref.Schema, Table: rel.Ref.Relation, Column: col.Name}
			if _, ok := c.merged[key]; !ok {
				out = append(out, key.String())
			}
		}
	}
	return out, nil
}

var _ ports.CatalogStore = (*Store)(nil)
