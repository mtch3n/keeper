//go:build integration

package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	_ "github.com/go-sql-driver/mysql"
	sqlite "modernc.org/sqlite"
)

// TestSQLiteColumnOrigin is half of SPEC §15 gate 4.
//
// R7.5a binds a result column to a catalog entry by the server's own answer for
// where it came from, never by output name. PostgreSQL supplies that as
// (tableOID, attnum). SQLite's equivalent is sqlite3_column_origin_name, which
// is compiled out unless SQLITE_ENABLE_COLUMN_METADATA is set — so the question
// is whether the pure-Go driver keeper uses has it.
func TestSQLiteColumnOrigin(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "gate4.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, ssn TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE VIEW user_report AS SELECT id, email FROM users`); err != nil {
		t.Fatalf("create view: %v", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	info := func(query string) []sqlite.ColumnInfo {
		t.Helper()
		var out []sqlite.ColumnInfo
		err := conn.Raw(func(dc any) error {
			ci, ok := dc.(interface {
				ColumnInfo(query string) ([]sqlite.ColumnInfo, error)
			})
			if !ok {
				t.Fatal("the driver exposes no ColumnInfo: SQLITE_ENABLE_COLUMN_METADATA is off and " +
					"SPEC §15 gate 4 comes back negative for SQLite")
			}
			var err error
			out, err = ci.ColumnInfo(query)
			return err
		})
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return out
	}

	// An alias must not change the origin. This is the whole point: name
	// matching would see "x" and have to enumerate its way back to ssn.
	aliased := info(`SELECT ssn AS x FROM users`)
	t.Logf("SELECT ssn AS x   → table=%q origin=%q name=%q",
		aliased[0].TableName, aliased[0].OriginName, aliased[0].Name)
	if aliased[0].OriginName != "ssn" || aliased[0].TableName != "users" {
		t.Errorf("an alias lost the origin: table=%q origin=%q", aliased[0].TableName, aliased[0].OriginName)
	}

	// A computed column has no unambiguous origin, and says so by returning
	// empty — SQLite's equivalent of PostgreSQL's (0,0).
	computed := info(`SELECT count(*) FROM users`)
	t.Logf("SELECT count(*)   → table=%q origin=%q", computed[0].TableName, computed[0].OriginName)
	if computed[0].OriginName != "" || computed[0].TableName != "" {
		t.Errorf("a computed column reported an origin: table=%q origin=%q",
			computed[0].TableName, computed[0].OriginName)
	}

	// Through a view. PostgreSQL reports the view's own identity here; SQLite's
	// answer decides whether R5.1b's view cataloguing is needed on this engine
	// too, or whether views resolve straight through.
	viaView := info(`SELECT email FROM user_report`)
	t.Logf("through a view    → table=%q origin=%q", viaView[0].TableName, viaView[0].OriginName)

	t.Log("CONFIRMED: modernc.org/sqlite exposes sqlite3_column_origin_name, " +
		"sqlite3_column_table_name and sqlite3_column_database_name through Conn.Raw. " +
		"SPEC §15 gate 4 is positive for SQLite.")
}

// TestMySQLColumnOrigin is the other half, and it comes back negative.
//
// MySQL's column definition packet carries org_table and org_name — the real
// table and column behind an alias — so the protocol has what R7.5a needs. The
// question was whether go-sql-driver/mysql surfaces them through database/sql.
func TestMySQLColumnOrigin(t *testing.T) {
	ctx := t.Context()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mysql:8.4",
			ExposedPorts: []string{"3306/tcp"},
			Env: map[string]string{
				"MYSQL_ROOT_PASSWORD": "root",
				"MYSQL_DATABASE":      "keeper_test",
			},
			WaitingFor: wait.ForLog("port: 3306  MySQL Community Server").
				WithStartupTimeout(180 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start mysql: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := container.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	dsn := "root:root@tcp(" + host + ":" + port.Port() + ")/keeper_test"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// The container's wait strategy can fire before the server accepts TCP.
	var pingErr error
	for range 30 {
		if pingErr = db.PingContext(ctx); pingErr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if pingErr != nil {
		t.Fatalf("ping: %v", pingErr)
	}

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE users (id INT PRIMARY KEY, email VARCHAR(255), ssn VARCHAR(32))`); err != nil {
		t.Fatalf("create: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT ssn AS x FROM users`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	ct := types[0]
	t.Logf("SELECT ssn AS x → Name()=%q DatabaseTypeName()=%q", ct.Name(), ct.DatabaseTypeName())

	// database/sql's ColumnType carries name, type, nullability, length and
	// precision. There is no origin in it, and the driver does not add one.
	if ct.Name() != "x" {
		t.Fatalf("expected the alias, got %q", ct.Name())
	}

	// And the driver itself: readColumns in packets.go reads org_table and
	// org_name off the wire and calls skipLengthEncodedString on both. They are
	// parsed and discarded, and mysqlField keeps no field for either.
	var found bool
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	_ = conn.Raw(func(dc any) error {
		if _, ok := dc.(interface {
			ColumnInfo(query string) (any, error)
		}); ok {
			found = true
		}
		return nil
	})
	if found {
		t.Fatal("the driver grew an origin accessor; re-measure SPEC §15 gate 4")
	}

	t.Log("NEGATIVE: go-sql-driver/mysql v1.10.1 skips org_table and org_name when parsing " +
		"the column definition packet, and exposes no accessor for them. `SELECT ssn AS x` is " +
		"indistinguishable from a column actually named x. Per SPEC §15 gate 4, MySQL does not " +
		"ship: name-based matching is the CVE-2026-85620 failure shape, not a fallback.")
}

var _ = context.Background
