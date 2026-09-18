package catalog

import (
	"fmt"
	"strings"
)

// columnKey identifies one catalogued column by name: schema, table (or view)
// and column. It is keeper's internal representation of SPEC §5.1's "keyed by
// name" storage, kept as a struct rather than a delimited string so that a
// dot inside any of the three components is never ambiguous.
type columnKey struct {
	Schema, Table, Column string
}

// String renders the key in the "schema.table.column" form used on the wire
// by CONTRACT.md §3's PUT /v1/catalog/{connection}/columns and by
// ports.CatalogStore's map[string]types.ColumnPolicy. This is a concession to
// that frozen, string-keyed API; the committed and overlay YAML files
// themselves are always nested, never dotted (SPEC §5.2).
func (k columnKey) String() string {
	return k.Schema + "." + k.Table + "." + k.Column
}

// parseKey parses the wire form back into a columnKey. Schema and table names
// are assumed not to contain a literal dot — the overwhelmingly common case —
// so the split takes the first two dot-separated components as schema and
// table and treats everything after the second dot as the column name,
// letting a dotted column name round-trip. A schema or table name containing
// a literal dot cannot be represented in this wire format; that limitation is
// inherent to ports.CatalogStore's frozen string-keyed signature, not to the
// YAML storage, which never has this problem.
func parseKey(key string) (columnKey, error) {
	parts := strings.SplitN(key, ".", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return columnKey{}, fmt.Errorf("catalog: %q is not a schema.table.column key", key)
	}
	return columnKey{Schema: parts[0], Table: parts[1], Column: parts[2]}, nil
}
