package catalog

import (
	"cmp"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"

	_ "modernc.org/sqlite" // pure-Go sqlite driver, registers as "sqlite"
)

// cachedColumn is one row of the identity cache's columns table, joined with
// enough shape to answer Lookup and RelationPolicies without a second trip.
type cachedColumn struct {
	TableOID uint32
	AttNum   uint16
	Column   columnKey
	TypeOID  uint32
}

// identityCache is catalog.db: the resolved-identity store of SPEC §5.1 and
// §5.6. Names are storage; (tableOID, attnum) is the runtime matching key,
// and OIDs do not survive a DROP/CREATE or a pg_dump restore, so this is
// rebuilt from a fresh Introspect whenever the daemon resolves or
// recatalogs, never treated as durable identity on its own.
type identityCache struct {
	db *sql.DB
}

func openIdentityCache(path string) (*identityCache, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("catalog: opening %s: %w", path, err)
	}
	// modernc.org/sqlite is a single-process, single-writer cache here (the
	// daemon is the only writer, per CONTRACT.md's "no globals" and the
	// single-writer note in DECISIONS.md); one connection avoids
	// SQLITE_BUSY entirely rather than tuning around it.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("catalog: %s: %w", pragma, err)
		}
	}

	const schema = `
CREATE TABLE IF NOT EXISTS relations (
	table_oid   INTEGER PRIMARY KEY,
	schema_name TEXT NOT NULL,
	table_name  TEXT NOT NULL,
	relkind     TEXT NOT NULL,
	fingerprint TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS columns (
	table_oid   INTEGER NOT NULL,
	att_num     INTEGER NOT NULL,
	column_name TEXT NOT NULL,
	type_oid    INTEGER NOT NULL,
	PRIMARY KEY (table_oid, att_num)
);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("catalog: creating schema in %s: %w", path, err)
	}
	return &identityCache{db: db}, nil
}

func (c *identityCache) close() error { return c.db.Close() }

// attributeFingerprint hashes a relation's own pg_attribute shape: its kind
// and its columns' (attnum, name, type OID), in attnum order. It is the part
// of SPEC R5.6's fingerprint this package can compute without a live
// connection; the view-definition half comes from Fingerprinter when one is
// configured (SPEC R5.6b).
func attributeFingerprint(rel ports.Relation) string {
	cols := slices.Clone(rel.Columns)
	slices.SortFunc(cols, func(a, b ports.Column) int { return cmp.Compare(a.AttNum, b.AttNum) })

	h := sha256.New()
	fmt.Fprintf(h, "kind:%c", rel.Kind)
	for _, col := range cols {
		fmt.Fprintf(h, "|%d:%s:%d", col.AttNum, col.Name, col.TypeOID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// resolve replaces the cache's contents with a fresh Introspect snapshot.
// fingerprintOf computes each relation's stored fingerprint. The same
// function must be used later to compute the live side of a Fresh check, or
// the two will never compare equal.
func (c *identityCache) resolve(relations []ports.Relation, fingerprintOf func(ports.Relation) (string, error)) error {
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("catalog: beginning resolve: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	if _, err := tx.Exec(`DELETE FROM relations`); err != nil {
		return fmt.Errorf("catalog: clearing relations: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM columns`); err != nil {
		return fmt.Errorf("catalog: clearing columns: %w", err)
	}

	insertRelation, err := tx.Prepare(`INSERT INTO relations (table_oid, schema_name, table_name, relkind, fingerprint) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertRelation.Close()

	insertColumn, err := tx.Prepare(`INSERT INTO columns (table_oid, att_num, column_name, type_oid) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insertColumn.Close()

	for _, rel := range relations {
		fp, err := fingerprintOf(rel)
		if err != nil {
			return fmt.Errorf("catalog: fingerprinting relation %s: %w", rel.Ref, err)
		}
		if _, err := insertRelation.Exec(rel.OID, rel.Ref.Schema, rel.Ref.Relation, string(rel.Kind), fp); err != nil {
			return fmt.Errorf("catalog: caching relation %s: %w", rel.Ref, err)
		}
		for _, col := range rel.Columns {
			if _, err := insertColumn.Exec(rel.OID, col.AttNum, col.Name, col.TypeOID); err != nil {
				return fmt.Errorf("catalog: caching column %s.%s: %w", rel.Ref, col.Name, err)
			}
		}
	}
	return tx.Commit()
}

// lookupColumn resolves (tableOID, attnum) to the column's name and type OID.
func (c *identityCache) lookupColumn(tableOID uint32, attNum uint16) (cachedColumn, bool) {
	row := c.db.QueryRow(`
		SELECT r.schema_name, r.table_name, c.column_name, c.type_oid
		FROM columns c JOIN relations r ON r.table_oid = c.table_oid
		WHERE c.table_oid = ? AND c.att_num = ?`, tableOID, attNum)
	var schema, table, column string
	var typeOID uint32
	if err := row.Scan(&schema, &table, &column, &typeOID); err != nil {
		return cachedColumn{}, false
	}
	return cachedColumn{
		TableOID: tableOID,
		AttNum:   attNum,
		Column:   columnKey{Schema: schema, Table: table, Column: column},
		TypeOID:  typeOID,
	}, true
}

// relation resolves a table OID to its name and relkind.
func (c *identityCache) relation(tableOID uint32) (types.RelationRef, byte, bool) {
	row := c.db.QueryRow(`SELECT schema_name, table_name, relkind FROM relations WHERE table_oid = ?`, tableOID)
	var schema, table, kind string
	if err := row.Scan(&schema, &table, &kind); err != nil {
		return types.RelationRef{}, 0, false
	}
	if kind == "" {
		return types.RelationRef{Schema: schema, Relation: table}, 0, true
	}
	return types.RelationRef{Schema: schema, Relation: table}, kind[0], true
}

// relationColumns lists every column the cache knows for tableOID.
func (c *identityCache) relationColumns(tableOID uint32) ([]cachedColumn, error) {
	rows, err := c.db.Query(`
		SELECT r.schema_name, r.table_name, c.att_num, c.column_name, c.type_oid
		FROM columns c JOIN relations r ON r.table_oid = c.table_oid
		WHERE c.table_oid = ?`, tableOID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []cachedColumn
	for rows.Next() {
		var schema, table, column string
		var attNum uint16
		var typeOID uint32
		if err := rows.Scan(&schema, &table, &attNum, &column, &typeOID); err != nil {
			return nil, err
		}
		out = append(out, cachedColumn{
			TableOID: tableOID,
			AttNum:   attNum,
			Column:   columnKey{Schema: schema, Table: table, Column: column},
			TypeOID:  typeOID,
		})
	}
	return out, rows.Err()
}

// storedFingerprint returns the fingerprint recorded at the last resolve.
func (c *identityCache) storedFingerprint(tableOID uint32) (string, bool) {
	row := c.db.QueryRow(`SELECT fingerprint FROM relations WHERE table_oid = ?`, tableOID)
	var fp string
	if err := row.Scan(&fp); err != nil {
		return "", false
	}
	return fp, true
}
