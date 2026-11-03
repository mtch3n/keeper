package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mtchen/keeper/internal/types"
	"gopkg.in/yaml.v3"
)

// catalogVersion is the only version this package writes and accepts.
const catalogVersion = 1

// yamlColumnPolicy is the on-disk shape of types.ColumnPolicy, which carries
// only `json` struct tags. Keys an earlier version wrote, such as form and
// hide_name, are not read.
type yamlColumnPolicy struct {
	Policy    string                      `yaml:"policy"`
	Namespace string                      `yaml:"namespace,omitempty"`
	Paths     map[string]yamlColumnPolicy `yaml:"paths,omitempty"`
}

func toYAML(p types.ColumnPolicy) yamlColumnPolicy {
	y := yamlColumnPolicy{
		Policy:    string(p.Policy),
		Namespace: p.Namespace,
	}
	if len(p.Paths) > 0 {
		y.Paths = make(map[string]yamlColumnPolicy, len(p.Paths))
		for path, sub := range p.Paths {
			y.Paths[path] = toYAML(sub)
		}
	}
	return y
}

func fromYAML(y yamlColumnPolicy) types.ColumnPolicy {
	p := types.ColumnPolicy{
		Policy:    types.Policy(y.Policy),
		Namespace: y.Namespace,
	}
	if len(y.Paths) > 0 {
		p.Paths = make(map[string]types.ColumnPolicy, len(y.Paths))
		for path, sub := range y.Paths {
			p.Paths[path] = fromYAML(sub)
		}
	}
	return p
}

// yamlFile is the top-level shape of catalog.yaml and catalog.local.yaml:
// nested schema -> table -> column, never dotted. SPEC §5.2.
type yamlFile struct {
	Version int                                               `yaml:"version"`
	Schemas map[string]map[string]map[string]yamlColumnPolicy `yaml:"schemas,omitempty"`
}

// loadYAMLFile reads and validates one catalog file. A missing file is not an
// error: it reports an empty, valid catalog, which is the ordinary state of a
// fresh overlay or a not-yet-created committed file.
func loadYAMLFile(path string) (map[columnKey]types.ColumnPolicy, error) {
	entries := make(map[columnKey]types.ColumnPolicy)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: reading %s: %w", path, err)
	}

	var file yamlFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("catalog: parsing %s: %w", path, err)
	}
	if file.Version != 0 && file.Version != catalogVersion {
		return nil, fmt.Errorf("catalog: %s: unsupported version %d", path, file.Version)
	}

	for schema, tables := range file.Schemas {
		for table, columns := range tables {
			for column, y := range columns {
				p := fromYAML(y).Known()
				if err := p.Validate(); err != nil {
					return nil, fmt.Errorf("catalog: %s: %s.%s.%s: %w", path, schema, table, column, err)
				}
				entries[columnKey{Schema: schema, Table: table, Column: column}] = p
			}
		}
	}
	return entries, nil
}

// saveYAMLFile validates every entry, then writes the nested form atomically
// — to a temp file in the same directory, renamed over the target — so a
// crash mid-write never leaves a half-written catalog.
func saveYAMLFile(path string, entries map[columnKey]types.ColumnPolicy) error {
	file := yamlFile{
		Version: catalogVersion,
		Schemas: make(map[string]map[string]map[string]yamlColumnPolicy),
	}
	for key, p := range entries {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("catalog: %s: %w", key, err)
		}
		tables, ok := file.Schemas[key.Schema]
		if !ok {
			tables = make(map[string]map[string]yamlColumnPolicy)
			file.Schemas[key.Schema] = tables
		}
		columns, ok := tables[key.Table]
		if !ok {
			columns = make(map[string]yamlColumnPolicy)
			tables[key.Table] = columns
		}
		columns[key.Column] = toYAML(p)
	}

	data, err := yaml.Marshal(file)
	if err != nil {
		return fmt.Errorf("catalog: marshalling %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("catalog: creating directory for %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".catalog-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("catalog: creating temp file for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("catalog: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("catalog: closing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("catalog: renaming into %s: %w", path, err)
	}
	return nil
}
