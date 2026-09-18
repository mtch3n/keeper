package pgdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// readableColumnsSQL asks the server which columns this role may actually read.
//
// has_column_privilege rather than has_table_privilege, for the same reason
// R4.1a gives on the write side: a grant can attach at column level, and a
// table-level answer is blind to it. SPEC R5.5 needs this twice — get_schema
// returns only readable columns, and a permission denial names them, because a
// sanitised error that leaves the agent no path forward is worse than either
// the sanitising or the denial alone.
const readableColumnsSQL = `
SELECT a.attname
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
 WHERE n.nspname = $1
   AND c.relname = $2
   AND has_column_privilege(c.oid, a.attnum, 'SELECT')
 ORDER BY a.attnum`

// ReadableColumns lists the columns of one relation this connection's role holds
// SELECT on. It satisfies the PrivilegeChecker seam internal/catalog declares;
// ports.Executor does not carry it because only the catalog needs it.
func (db *DB) ReadableColumns(ctx context.Context, connID string, rel types.RelationRef) ([]string, error) {
	var out []string
	err := db.withTx(ctx, connID, ports.RoleRead, readTx, func(ctx context.Context, c *conn) error {
		rows, err := c.tx.Query(ctx, readableColumnsSQL, rel.Schema, rel.Relation)
		if err != nil {
			c.aborted = true
			return convert(err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				c.aborted = true
				return convert(err)
			}
			out = append(out, name)
		}
		if err := rows.Err(); err != nil {
			c.aborted = true
			return convert(err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// fingerprintSQL gathers everything a classification is bound to: the column
// shape from pg_attribute, and for a view or matview its definition.
//
// A view's definition is included because SPEC R5.6b binds a view's
// classifications to it — `left(ssn,3)` and `ssn` reference the same source and
// expose different information, so an unchanged dependency set proves nothing.
// Nested views are covered by their own relations' fingerprints; the caller
// fingerprints each relation it depends on.
const fingerprintSQL = `
SELECT c.relkind::text,
       coalesce(
         (SELECT string_agg(
                    a.attnum::text || ':' || a.attname || ':' ||
                    a.atttypid::text || ':' || a.attnotnull::text ||
                    ':' || a.attidentity || ':' || a.atthasdef::text,
                    ',' ORDER BY a.attnum)
            FROM pg_attribute a
           WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped),
         ''),
       CASE WHEN c.relkind IN ('v', 'm') THEN pg_get_viewdef(c.oid, true) ELSE '' END
  FROM pg_class c
 WHERE c.oid = $1`

// Fingerprint returns a stable hash of one relation's column shape and, for a
// view or matview, its definition. It satisfies the Fingerprinter seam
// internal/catalog declares.
//
// The hash is compared, never interpreted, so its only requirements are that it
// changes when the relation does and that it does not carry the definition text
// into any structure that might be logged — a view definition can contain
// literals, and SPEC R10a keeps literals out of the audit log.
func (db *DB) Fingerprint(ctx context.Context, connID string, tableOID uint32) (string, error) {
	var kind, cols, viewdef string
	err := db.withTx(ctx, connID, ports.RoleRead, readTx, func(ctx context.Context, c *conn) error {
		row := c.tx.QueryRow(ctx, fingerprintSQL, int64(tableOID))
		if err := row.Scan(&kind, &cols, &viewdef); err != nil {
			if err == pgx.ErrNoRows {
				// The relation is gone. An empty fingerprint never matches a
				// stored one, so the caller treats it as changed, which is what
				// a dropped relation is.
				kind, cols, viewdef = "", "", ""
				return nil
			}
			c.aborted = true
			return convert(err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if kind == "" && cols == "" {
		return "", nil
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%s", kind, cols, viewdef))
	return hex.EncodeToString(sum[:]), nil
}
