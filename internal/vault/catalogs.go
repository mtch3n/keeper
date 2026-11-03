package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// CatalogFile is a profile's declared columns, CatalogOverlayFile the
	// classification keeper added to it.
	CatalogFile        = "catalog.yaml"
	CatalogOverlayFile = "catalog.local.yaml"
)

// CatalogDir is where a profile's own catalog lives in keeper's state
// directory dir. A profile with an explicit catalog path keeps its files there
// instead.
func CatalogDir(dir, connID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, connID)
	return filepath.Join(dir, "catalogs", safe)
}

// catalogFiles are one profile's catalog files, as text. An absent file is "".
type catalogFiles struct {
	Committed string `json:"committed,omitzero"`
	Overlay   string `json:"overlay,omitzero"`
}

// exportCatalogs reads the catalog of every profile that keeps it in the state
// directory. A moved vault without them would open with its declared columns
// forgotten, and a column declared as a token would come back unmasked.
func (v *Vault) exportCatalogs() (map[string]catalogFiles, error) {
	out := map[string]catalogFiles{}
	for _, rec := range v.doc.Connections {
		if rec.Conn.CatalogPath != "" {
			continue
		}
		var files catalogFiles
		for name, into := range map[string]*string{CatalogFile: &files.Committed, CatalogOverlayFile: &files.Overlay} {
			data, err := os.ReadFile(filepath.Join(CatalogDir(v.dir, rec.Conn.ID), name))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("vault: read the catalog of %s: %w", rec.Conn.Name, err)
			}
			*into = string(data)
		}
		if files != (catalogFiles{}) {
			out[rec.Conn.ID] = files
		}
	}
	return out, nil
}

// importCatalogs writes the exported catalogs of the profiles in doc into the
// state directory.
func (v *Vault) importCatalogs(doc *document) error {
	for id, files := range doc.Catalogs {
		if _, ok := doc.find(id); !ok {
			continue
		}
		dir := CatalogDir(v.dir, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("vault: make %s: %w", dir, err)
		}
		for name, data := range map[string]string{CatalogFile: files.Committed, CatalogOverlayFile: files.Overlay} {
			if data == "" {
				continue
			}
			if err := atomicWrite(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
				return fmt.Errorf("vault: write the catalog of %s: %w", id, err)
			}
		}
	}
	doc.Catalogs = nil
	return nil
}
