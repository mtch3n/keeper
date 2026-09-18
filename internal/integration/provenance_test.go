//go:build integration

package integration

import (
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestViewProvenance is SPEC §15 gate 1, the one measurement the identity
// binding through views depends on.
//
// The question: when a statement selects from a view, does RowDescription report
// the view's own OID and attnum, or the underlying base column's? R5.1b says the
// former, and every view column therefore resolves as "not found" and masks
// unless views are catalogued — which on a schema that reports through views
// means masking nearly everything. The third revision reported this measured and
// left no artifact behind. This is the artifact.
func TestViewProvenance(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	conn, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	var usersOID, reportOID uint32
	if err := conn.QueryRow(ctx, `SELECT 'public.users'::regclass::oid, 'public.user_report'::regclass::oid`).
		Scan(&usersOID, &reportOID); err != nil {
		t.Fatalf("resolve oids: %v", err)
	}

	// A column selected straight from the table, for the control.
	base, err := conn.Query(ctx, `SELECT id, email FROM users LIMIT 0`)
	if err != nil {
		t.Fatalf("describe table select: %v", err)
	}
	baseFields := base.FieldDescriptions()
	baseTable, baseAttnum := baseFields[1].TableOID, baseFields[1].TableAttributeNumber
	base.Close()

	if baseTable != usersOID {
		t.Fatalf("a table select did not report the table's own OID: got %d want %d", baseTable, usersOID)
	}

	// The same column, reached through a view.
	viaView, err := conn.Query(ctx, `SELECT id, email FROM user_report LIMIT 0`)
	if err != nil {
		t.Fatalf("describe view select: %v", err)
	}
	viewFields := viaView.FieldDescriptions()
	viewTable, viewAttnum := viewFields[1].TableOID, viewFields[1].TableAttributeNumber
	viaView.Close()

	t.Logf("through the table: (oid=%d, attnum=%d) — users is %d", baseTable, baseAttnum, usersOID)
	t.Logf("through the view:  (oid=%d, attnum=%d) — user_report is %d", viewTable, viewAttnum, reportOID)

	switch viewTable {
	case reportOID:
		// The reported result. R5.1b stands: views must be catalogued, because a
		// view column carries the view's identity and nothing resolves it to the
		// base column.
		t.Logf("CONFIRMED: RowDescription reports the view's own identity. " +
			"Cataloguing views (R5.1b) is required, and §5.6's viewdef fingerprint has entries to invalidate.")
	case usersOID:
		t.Errorf("MEASUREMENT DIFFERS FROM SPEC: the view reported the base table's OID (%d). "+
			"R5.1b, §11.3 and §15 gate 1 all assume otherwise and need revising.", usersOID)
	default:
		t.Errorf("view column reported an unexpected OID %d (users=%d, user_report=%d)",
			viewTable, usersOID, reportOID)
	}

	// The second half of §15 gate 1: the plan names base tables even when the
	// statement names only the view, which is why R7.4c evaluates denylists
	// against the plan rather than statement text.
	var plan string
	if err := conn.QueryRow(ctx, `EXPLAIN (FORMAT JSON) SELECT id FROM user_report`).Scan(&plan); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if !contains(plan, "users") {
		t.Errorf("the plan for a view query did not name the base table: %s", plan)
	}
	if contains(plan, "user_report") {
		t.Logf("note: the plan also names the view itself")
	} else {
		t.Logf("CONFIRMED: the plan names only the base table, never the view whose identity " +
			"RowDescription reports. Matching output columns by name across that gap is the " +
			"failure shape R7.5a forbids.")
	}
}

// TestComputedColumnsReportZero is R7.6's premise: a computed column carries no
// origin, so there is nothing to look up and the type-family rule is what
// decides its policy.
func TestComputedColumnsReportZero(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	conn, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	for _, sql := range []string{
		`SELECT count(*) FROM users`,
		`SELECT upper(status) FROM users`,
		`SELECT substr(ssn, 1, 3) FROM users`,
		`SELECT 1`,
	} {
		rows, err := conn.Query(ctx, sql+" LIMIT 0")
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		fd := rows.FieldDescriptions()[0]
		rows.Close()
		if fd.TableOID != 0 || fd.TableAttributeNumber != 0 {
			t.Errorf("%s: expected (0,0) for a computed column, got (%d,%d)",
				sql, fd.TableOID, fd.TableAttributeNumber)
		}
	}
}

// TestAliasesAndCTEsKeepIdentity is R7.5a: matching by identity survives every
// renaming a parser-based approach would have to enumerate.
func TestAliasesAndCTEsKeepIdentity(t *testing.T) {
	f := New(t)
	ctx := t.Context()

	conn, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	var usersOID uint32
	if err := conn.QueryRow(ctx, `SELECT 'public.users'::regclass::oid`).Scan(&usersOID); err != nil {
		t.Fatalf("resolve oid: %v", err)
	}
	var ssnAttnum uint16
	if err := conn.QueryRow(ctx,
		`SELECT attnum FROM pg_attribute WHERE attrelid = $1 AND attname = 'ssn'`, usersOID).Scan(&ssnAttnum); err != nil {
		t.Fatalf("resolve attnum: %v", err)
	}

	for name, sql := range map[string]string{
		"alias":    `SELECT ssn AS x FROM users`,
		"cte":      `WITH c AS (SELECT ssn FROM users) SELECT ssn FROM c`,
		"subquery": `SELECT s FROM (SELECT ssn AS s FROM users) q`,
		"union":    `SELECT ssn FROM users UNION ALL SELECT ssn FROM users`,
	} {
		rows, err := conn.Query(ctx, sql+" LIMIT 0")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fd := rows.FieldDescriptions()[0]
		rows.Close()
		t.Logf("%-9s → (oid=%d, attnum=%d)", name, fd.TableOID, fd.TableAttributeNumber)
		if name == "union" {
			// A set operation has no single origin; (0,0) is correct and the
			// type-family rule takes over.
			continue
		}
		if fd.TableOID != usersOID || fd.TableAttributeNumber != ssnAttnum {
			t.Errorf("%s: identity lost — got (%d,%d) want (%d,%d)",
				name, fd.TableOID, fd.TableAttributeNumber, usersOID, ssnAttnum)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
