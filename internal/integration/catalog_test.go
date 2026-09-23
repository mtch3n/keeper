//go:build integration

package integration

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mtchen/keeper/internal/catalog"
	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
)

// Opening a catalog introspects the database and fingerprints every relation
// it can read, which is the first thing any query needs. The fingerprint SQL
// concatenated a "char" column onto text, which the server rejects as an
// ambiguous operator, so on a real server no catalog opened and every query was
// refused for having none. No fake can show that: it is the server's operator
// resolution. If this fails, keeper cannot run a statement against PostgreSQL.
func TestCatalogOpensAgainstARealServer(t *testing.T) {
	f := New(t)
	exec, err := pgdb.New(pgdb.Config{
		DSN: func(context.Context, string, ports.Role) (string, error) { return f.ReadDSN, nil },
	})
	if err != nil {
		t.Fatalf("pgdb.New: %v", err)
	}
	t.Cleanup(exec.Shutdown)

	store := catalog.NewStore(catalog.Dependencies{
		Introspector: exec,
		Sampler:      exec,
		Privileges:   exec,
		Fingerprints: exec,
	})
	dir := t.TempDir()
	cat, err := store.Open(t.Context(), "conn", catalog.Paths{
		Committed: filepath.Join(dir, "catalog.yaml"),
		Overlay:   filepath.Join(dir, "catalog.local.yaml"),
		Cache:     filepath.Join(dir, "cache.db"),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The fixture's views are fingerprinted with their definition, the tables
	// with their columns; freshness compares both against the server again.
	if fresh, err := cat.Fresh(t.Context(), nil); err != nil || !fresh {
		t.Fatalf("a catalog just opened is not fresh: fresh=%v err=%v", fresh, err)
	}
}
