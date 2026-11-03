package daemonapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

type pathlessConnections struct{}

func (pathlessConnections) Connection(_ context.Context, id string) (*types.Connection, error) {
	return &types.Connection{ID: id}, nil
}

func TestMask_MASK_C20_ProfilesWithNoPathKeepSeparateCatalogs(t *testing.T) {
	rel := func(_ context.Context, _ string) ([]ports.Relation, error) {
		return []ports.Relation{{
			Ref: types.RelationRef{Schema: "public", Relation: "t"}, OID: 7, Kind: 'r',
			Columns: []ports.Column{{Name: "notes", AttNum: 1, TypeOID: 25}},
		}}, nil
	}
	c := newTestCatalogs(t, rel)
	c.vault = pathlessConnections{}
	store := openingStore{c}
	allow := map[string]types.ColumnPolicy{"public.t.notes": {Policy: types.PolicyAllow}}
	if err := store.Put(t.Context(), "a", allow); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Entries(t.Context(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if got := entries["public.t.notes"].Policy; got != types.PolicyRedact {
		t.Errorf("profile b sees %q for a column only profile a edited, want its own redact", got)
	}
	if _, err := os.Stat(filepath.Join(c.dir, "catalogs", "a", "catalog.yaml")); err != nil {
		t.Errorf("profile a has no file of its own in the state directory: %v", err)
	}
}

func TestMask_MASK_C21_AnUnreadableExplicitPathIsAKeeperErrorAndNoColumnIsAllowed(t *testing.T) {
	rel := func(_ context.Context, _ string) ([]ports.Relation, error) { return nil, nil }
	c := newTestCatalogs(t, rel)
	dir := filepath.Join(c.dir, "explicit")
	if err := os.MkdirAll(filepath.Join(dir, "catalog.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	c.vault = fakeConnections{dir: c.dir}
	_, err := c.For("explicit")
	ke, ok := errors.AsType[*types.Error](err)
	if !ok || ke.Code != types.CodeInternal || !strings.Contains(ke.Summary, "catalog file") {
		t.Fatalf("got %v, want a keeper error naming the unreadable catalog file", err)
	}
	if _, ok := c.Lookup("explicit", 7, 1); ok {
		t.Error("a column was classified although the catalog could not be read")
	}
}

func TestMask_AnUnreachableDatabaseKeepsItsCode(t *testing.T) {
	c := newTestCatalogs(t, func(_ context.Context, _ string) ([]ports.Relation, error) {
		return nil, &types.Error{Code: types.CodeUnreachable}
	})
	_, err := c.For("db")
	if ke, ok := errors.AsType[*types.Error](err); !ok || ke.Code != types.CodeUnreachable {
		t.Fatalf("got %v, want the unreachable code", err)
	}
}
