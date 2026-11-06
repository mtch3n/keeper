package catalog

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// SystemIntrospector lists the pg_catalog and information_schema relations a
// connection's role can read. internal/pgdb's DB satisfies it.
type SystemIntrospector interface {
	IntrospectSystem(ctx context.Context, connID string) ([]ports.Relation, error)
}

// systemRelation is one system catalog relation and the policy of each column.
type systemRelation struct {
	ref  types.RelationRef
	cols map[uint16]types.ColumnPolicy
}

// systemScalar are the system catalogs' own scalar types outside the families:
// identifiers, transaction ids, addresses and positions. None holds row data.
var systemScalar = map[uint32]bool{
	24: true, 26: true, 27: true, 28: true, 29: true, 5069: true, // regproc, oid, tid, xid, cid, xid8
	650: true, 869: true, 3220: true, // cidr, inet, pg_lsn
	2202: true, 2203: true, 2204: true, 2205: true, 2206: true, // regprocedure, regoper, regoperator, regclass, regtype
	3734: true, 3769: true, 4089: true, 4096: true, 4191: true, // regconfig, regdictionary, regnamespace, regrole, regcollation
}

// systemText are system columns whose text is a statement, a definition, a
// credential or a connection string. Each can hold a literal from some query or
// a password, which no catalog of this connection classifies.
var systemText = map[[2]string]bool{
	{"pg_authid", "rolpassword"}:                     true,
	{"pg_shadow", "passwd"}:                          true,
	{"pg_user", "passwd"}:                            true,
	{"pg_roles", "rolpassword"}:                      true,
	{"pg_stat_activity", "query"}:                    true,
	{"pg_prepared_statements", "statement"}:          true,
	{"pg_cursors", "statement"}:                      true,
	{"pg_proc", "prosrc"}:                            true,
	{"pg_proc", "probin"}:                            true,
	{"pg_subscription", "subconninfo"}:               true,
	{"pg_stat_wal_receiver", "conninfo"}:             true,
	{"pg_views", "definition"}:                       true,
	{"pg_matviews", "definition"}:                    true,
	{"pg_rules", "definition"}:                       true,
	{"pg_policies", "qual"}:                          true,
	{"pg_policies", "with_check"}:                    true,
	{"pg_file_settings", "setting"}:                  true,
	{"views", "view_definition"}:                     true,
	{"routines", "routine_definition"}:               true,
	{"user_mapping_options", "option_value"}:         true,
	{"foreign_server_options", "option_value"}:       true,
	{"foreign_data_wrapper_options", "option_value"}: true,
	{"foreign_table_options", "option_value"}:        true,
	{"column_options", "option_value"}:               true,
}

// systemDefault is the policy of a system catalog column. The catalogs describe
// the database, not its rows, so their names, counts and states are allowed.
// What redacts is text that can carry a value from elsewhere (systemText) and
// every array, node tree, bytea or anyarray column, which is where options,
// expressions and sampled column values (pg_statistic, pg_stats) live.
func systemDefault(rel types.RelationRef, col ports.Column) types.ColumnPolicy {
	if systemText[[2]string{rel.Relation, col.Name}] {
		return types.ColumnPolicy{Policy: types.PolicyRedact}
	}
	switch Family(col.TypeOID) {
	case ports.FamilyNumeric, ports.FamilyBoolean, ports.FamilyDateTime, ports.FamilyUUID:
		return types.ColumnPolicy{Policy: types.PolicyAllow}
	case ports.FamilyText:
		if !isJSONType(col.TypeOID) {
			return types.ColumnPolicy{Policy: types.PolicyAllow}
		}
	}
	if systemScalar[col.TypeOID] {
		return types.ColumnPolicy{Policy: types.PolicyAllow}
	}
	return types.ColumnPolicy{Policy: types.PolicyRedact}
}

// setSystem replaces the system catalog policies from an IntrospectSystem
// result.
func (c *Catalog) setSystem(relations []ports.Relation) {
	system := make(map[uint32]systemRelation, len(relations))
	for _, rel := range relations {
		sr := systemRelation{ref: rel.Ref, cols: make(map[uint16]types.ColumnPolicy, len(rel.Columns))}
		for _, col := range rel.Columns {
			sr.cols[col.AttNum] = systemDefault(rel.Ref, col)
		}
		system[rel.OID] = sr
	}
	c.mu.Lock()
	c.system = system
	c.mu.Unlock()
}
