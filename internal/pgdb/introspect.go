package pgdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/mtchen/keeper/internal/catalog"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// introspectSQL enumerates every relation the connection's role can name, with
// the column facts catalog init reasons from. relkind is restricted to r, v, m
// and p — ordinary tables, views, materialized views and partitioned tables.
//
// HasDefault covers a nextval default as well as a literal one, and IsIdentity
// is separate, because SPEC R5.3b needs them to tell an id column apart from an
// SSN column that happens to be nine digits.
const introspectSQL = `
SELECT c.oid::int8,
       n.nspname,
       c.relname,
       c.relkind::text,
       a.attnum::int4,
       a.attname,
       a.atttypid::int8,
       t.typname,
       NOT a.attnotnull                                   AS nullable,
       (a.atthasdef OR a.attidentity <> '')               AS has_default,
       (a.attidentity <> '')                              AS is_identity,
       EXISTS (SELECT 1 FROM pg_index i
                WHERE i.indrelid = c.oid AND i.indisprimary
                  AND a.attnum = ANY (i.indkey))          AS is_pk,
       EXISTS (SELECT 1 FROM pg_constraint k
                WHERE k.conrelid = c.oid AND k.contype = 'f'
                  AND a.attnum = ANY (k.conkey))          AS is_fk
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
  JOIN pg_type t ON t.oid = a.atttypid
 WHERE c.relkind IN ('r', 'v', 'm', 'p')
   AND (n.nspname IN ('pg_catalog', 'information_schema')) = $1
   AND n.nspname NOT LIKE 'pg\_toast%'
   AND has_any_column_privilege(c.oid, 'SELECT')
 ORDER BY n.nspname, c.relname, a.attnum`

// Introspect implements ports.Executor.
func (db *DB) Introspect(ctx context.Context, connID string) ([]ports.Relation, error) {
	return db.introspect(ctx, connID, false)
}

// IntrospectSystem lists the pg_catalog and information_schema relations the
// role can read, so a statement over them is classified rather than unknown.
func (db *DB) IntrospectSystem(ctx context.Context, connID string) ([]ports.Relation, error) {
	return db.introspect(ctx, connID, true)
}

func (db *DB) introspect(ctx context.Context, connID string, system bool) ([]ports.Relation, error) {
	var out []ports.Relation
	err := db.withTx(ctx, connID, ports.RoleRead, readTx, func(ctx context.Context, c *conn) error {
		rows, err := c.tx.Query(ctx, introspectSQL, system)
		if err != nil {
			c.aborted = true
			return convert(err)
		}
		defer rows.Close()

		byOID := map[uint32]int{}
		for rows.Next() {
			var (
				oid, typeOID             int64
				schema, relname, relkind string
				attnum                   int32
				attname, typname         string
				nullable, hasDefault     bool
				isIdentity, isPK, isFK   bool
			)
			if err := rows.Scan(&oid, &schema, &relname, &relkind, &attnum, &attname,
				&typeOID, &typname, &nullable, &hasDefault, &isIdentity, &isPK, &isFK); err != nil {
				c.aborted = true
				return convert(err)
			}

			idx, ok := byOID[uint32(oid)]
			if !ok {
				idx = len(out)
				byOID[uint32(oid)] = idx
				kind := byte(0)
				if relkind != "" {
					kind = relkind[0]
				}
				out = append(out, ports.Relation{
					Ref:  types.RelationRef{Schema: schema, Relation: relname},
					OID:  uint32(oid),
					Kind: kind,
				})
			}
			out[idx].Columns = append(out[idx].Columns, ports.Column{
				Name:       attname,
				AttNum:     uint16(attnum),
				Type:       typname,
				TypeOID:    uint32(typeOID),
				Family:     FamilyForOID(uint32(typeOID)),
				Nullable:   nullable,
				IsPK:       isPK,
				IsFK:       isFK,
				HasDefault: hasDefault,
				IsIdentity: isIdentity,
			})
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

// maxSample bounds what one catalog-time sample may read, whatever the caller
// asks for. Reading locally is not egress (SPEC R5.3), but it is still a read.
const maxSample = 10000

// SampleColumn implements ports.Executor. The relation and column come from the
// catalog, never from statement text, and they are quoted with pgx's own
// identifier quoting rather than interpolated.
func (db *DB) SampleColumn(ctx context.Context, connID string, rel types.RelationRef, column string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	n = min(n, maxSample)

	table := pgx.Identifier{rel.Schema, rel.Relation}.Sanitize()
	col := pgx.Identifier{column}.Sanitize()
	sql := fmt.Sprintf("SELECT %s::text FROM %s WHERE %s IS NOT NULL LIMIT %d", col, table, col, n)

	var out []string
	err := db.withTx(ctx, connID, ports.RoleRead, readTx, func(ctx context.Context, c *conn) error {
		rows, err := c.tx.Query(ctx, sql)
		if err != nil {
			c.aborted = true
			return convert(err)
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				c.aborted = true
				return convert(err)
			}
			out = append(out, v)
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

// textShapesSQL reads the planner's statistics for text columns: the distinct
// count, made absolute where pg_stats gives a fraction of the rows, and the
// average width. pg_stats shows only columns the role can read, and nothing
// here reads a value.
const textShapesSQL = `
SELECT s.schemaname::text, s.tablename::text, s.attname::text,
       CASE WHEN s.n_distinct >= 0 THEN s.n_distinct::float8
            ELSE -s.n_distinct * greatest(c.reltuples, 0)::float8 END,
       s.avg_width
  FROM pg_stats s
  JOIN pg_namespace n ON n.nspname = s.schemaname
  JOIN pg_class c ON c.relnamespace = n.oid AND c.relname = s.tablename
 WHERE s.schemaname NOT IN ('pg_catalog', 'information_schema')
   AND NOT s.inherited`

// TextShapes implements catalog.TextStats.
func (db *DB) TextShapes(ctx context.Context, connID string) (map[types.RelationRef]map[string]catalog.TextShape, error) {
	out := map[types.RelationRef]map[string]catalog.TextShape{}
	err := db.withTx(ctx, connID, ports.RoleRead, readTx, func(ctx context.Context, c *conn) error {
		rows, err := c.tx.Query(ctx, textShapesSQL)
		if err != nil {
			c.aborted = true
			return convert(err)
		}
		defer rows.Close()
		for rows.Next() {
			var schema, table, column string
			var shape catalog.TextShape
			var width int32
			if err := rows.Scan(&schema, &table, &column, &shape.Distinct, &width); err != nil {
				c.aborted = true
				return convert(err)
			}
			shape.AvgWidth = int(width)
			ref := types.RelationRef{Schema: schema, Relation: table}
			if out[ref] == nil {
				out[ref] = map[string]catalog.TextShape{}
			}
			out[ref][column] = shape
		}
		if err := rows.Err(); err != nil {
			c.aborted = true
			return convert(err)
		}
		return nil
	})
	return out, err
}
