package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/mtchen/keeper/internal/types"
)

// grantRolePlaceholder stands in for the connection's read role. SPEC §5.5
// and CONTRACT.md's POST /v1/catalog/{connection}/grants describe emitting
// "GRANT SELECT (...)" statements but neither the SPEC nor
// ports.CatalogStore's frozen SuggestGrants(ctx, connID) signature carries
// the role name — only internal/vault's Connection record has it, and
// catalog does not import vault. Whatever calls SuggestGrants (the daemon,
// which does hold the Connection) is expected to substitute the real role
// before showing or running these statements.
const grantRolePlaceholder = "<role>"

// SuggestGrants emits one GRANT SELECT (...) statement per relation,
// omitting every column marked drop. SPEC §5.5: applying it moves those
// columns from Invariant B to Invariant A, and keeper then never sees them.
func (s *Store) SuggestGrants(ctx context.Context, connID string) ([]string, error) {
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

	stmts := make([]string, 0, len(relations))
	for _, rel := range relations {
		kept := make([]string, 0, len(rel.Columns))
		for _, col := range rel.Columns {
			key := columnKey{Schema: rel.Ref.Schema, Table: rel.Ref.Relation, Column: col.Name}
			if p, ok := c.merged[key]; ok && p.Policy == types.PolicyDrop {
				continue
			}
			kept = append(kept, quoteIdent(col.Name))
		}
		if len(kept) == 0 {
			continue
		}
		stmts = append(stmts, fmt.Sprintf(
			"GRANT SELECT (%s) ON %s.%s TO %s;",
			strings.Join(kept, ", "),
			quoteIdent(rel.Ref.Schema),
			quoteIdent(rel.Ref.Relation),
			grantRolePlaceholder,
		))
	}
	return stmts, nil
}

// quoteIdent double-quotes a PostgreSQL identifier, doubling any embedded
// quote, so a mixed-case or reserved-word identifier survives being copied
// into the statement.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
