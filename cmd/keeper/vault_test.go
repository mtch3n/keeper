package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mtchen/keeper/internal/client"
)

func TestMissingCatalogsNamesAnExplicitPathThatIsAbsent(t *testing.T) {
	present := filepath.Join(t.TempDir(), "shop.yaml")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	details := []*client.ConnectionDetail{
		{Name: "own"},
		{Name: "shared_here", CatalogStatus: client.CatalogStatus{Path: present}},
		{Name: "shared_gone", CatalogStatus: client.CatalogStatus{Path: "/nonexistent/shop.yaml"}},
	}
	want := []string{"shared_gone (/nonexistent/shop.yaml)"}
	if got := missingCatalogs(details); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
