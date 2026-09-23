package daemonapp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/catalog"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

type introspectorFunc func(ctx context.Context, connID string) ([]ports.Relation, error)

func (f introspectorFunc) Introspect(ctx context.Context, connID string) ([]ports.Relation, error) {
	return f(ctx, connID)
}

// fakeConnections answers for any id, with its committed catalog in a
// directory of its own so two connections never share a file.
type fakeConnections struct{ dir string }

func (f fakeConnections) Connection(_ context.Context, id string) (*types.Connection, error) {
	return &types.Connection{ID: id, CatalogPath: filepath.Join(f.dir, id, "catalog.yaml")}, nil
}

func newTestCatalogs(t *testing.T, in introspectorFunc) *catalogs {
	t.Helper()
	dir := t.TempDir()
	return &catalogs{
		store: catalog.NewStore(catalog.Dependencies{Introspector: in}),
		vault: fakeConnections{dir: dir},
		dir:   dir,
		open:  map[string]*catalog.Catalog{},
	}
}

// Opening a catalog introspects its database, and an unreachable server answers
// that with a connect timeout. A lock shared across connections turned one such
// server into a daemon where every connection hung, so an open that never
// returns must not hold up any other connection's.
func TestOpeningOneCatalogDoesNotWaitOnAnother(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	c := newTestCatalogs(t, func(_ context.Context, connID string) ([]ports.Relation, error) {
		if connID == "unreachable" {
			close(entered)
			<-release
			return nil, errors.New("connect timeout")
		}
		return nil, nil
	})

	stuck := make(chan error, 1)
	go func() {
		_, err := c.For("unreachable")
		stuck <- err
	}()
	<-entered

	done := make(chan error, 1)
	go func() {
		_, err := c.For("reachable")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("For(reachable): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("For(reachable) waited on another connection's open")
	}

	close(release)
	if err := <-stuck; err == nil {
		t.Fatal("For(unreachable) succeeded after its introspection failed")
	}
}

// The daemon reads the catalog store without opening anything first. The bare
// store answers only for an open catalog, so on a fresh daemon every catalog
// command failed until some query happened to open that connection.
func TestCatalogStoreOpensOnFirstUse(t *testing.T) {
	c := newTestCatalogs(t, func(context.Context, string) ([]ports.Relation, error) { return nil, nil })

	if _, err := (openingStore{c}).Unclassified(t.Context(), "fresh"); err != nil {
		t.Fatalf("Unclassified on an unopened connection: %v", err)
	}
	if c.cached("fresh") == nil {
		t.Fatal("the store call did not leave the catalog open")
	}
}
