package mcpapp

import (
	"fmt"
	"strings"

	"github.com/mtchen/keeper/internal/types"
)

// parseRelationPath turns "schema.table" into a types.RelationRef. It
// requires exactly one dot: a schema does not cover its relations and a
// view is its own path (SPEC R9.3c), so there is no shorthand that expands
// to more than one relation.
func parseRelationPath(path string) (types.RelationRef, error) {
	schema, table, ok := strings.Cut(path, ".")
	if !ok || schema == "" || table == "" || strings.Contains(table, ".") {
		return types.RelationRef{}, fmt.Errorf("keeper-mcp: path %q must be exactly schema.table", path)
	}
	return types.RelationRef{Schema: schema, Relation: table}, nil
}
