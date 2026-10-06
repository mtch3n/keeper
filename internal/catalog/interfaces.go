package catalog

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Introspector lists a connection's relations and columns, including the
// server identity (OID, attnum), relkind and the constraint/default facts
// SPEC R5.1 and R5.3b need. It is the subset of ports.Executor's Introspect
// method; internal/pgdb's Executor satisfies it structurally.
type Introspector interface {
	Introspect(ctx context.Context, connID string) ([]ports.Relation, error)
}

// ColumnSampler reads a handful of values from one column for catalog-time
// sampling. It is the subset of ports.Executor's SampleColumn method; local
// reading is not egress (SPEC R5.3). internal/pgdb's Executor satisfies it
// structurally.
type ColumnSampler interface {
	SampleColumn(ctx context.Context, connID string, rel types.RelationRef, column string, n int) ([]string, error)
}

// PrivilegeChecker reports which columns of a relation has_column_privilege
// allows the connection's role to read. SPEC R5.5. This is not part of
// ports.Executor; whatever wires internal/pgdb to this package must add it
// there, since only pgdb opens a connection.
type PrivilegeChecker interface {
	ReadableColumns(ctx context.Context, connID string, rel types.RelationRef) ([]string, error)
}

// Fingerprinter hashes a relation's shape for SPEC R5.6's invalidation: the
// pg_attribute-derived column list for a table, and additionally
// pg_get_viewdef() for a view or matview. Like PrivilegeChecker, this is not
// part of ports.Executor and must be added wherever pgdb is wired in here.
type Fingerprinter interface {
	Fingerprint(ctx context.Context, connID string, tableOID uint32) (string, error)
}
