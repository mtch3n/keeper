//go:build integration

// Package integration holds the tests that need a real PostgreSQL.
//
// SPEC §12 is explicit that these cannot be faked: `pg_read_file` and
// `COPY TO PROGRAM` behaviour, column-level grants breaking `SELECT *`, view
// provenance in RowDescription, and the lock a parse analysis takes are all
// properties of the server rather than of keeper. A mock that agreed with our
// expectations would prove only that we are consistent with ourselves.
//
// Build-tagged, so `go test ./...` stays fast and runs without Docker. Run these
// with `go test -tags integration ./internal/integration/...`.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// image is pinned rather than floating: a test that silently changes server
// version is a test whose failures cannot be attributed.
const image = "postgres:16-alpine"

// Fixture is one disposable database with the schema and roles SPEC §12's table
// exercises. Every role in it exists to be audited, so several are deliberately
// over-privileged.
type Fixture struct {
	// SuperDSN is the owner. Registering it is the accepted-superuser case that
	// SPEC R4.1 now permits and marks, rather than refusing.
	SuperDSN string
	// ReadDSN is app_ro: SELECT on the catalogued relations and nothing else.
	ReadDSN string
	// WriteDSN is app_rw: SELECT everywhere plus INSERT/UPDATE/DELETE on orders.
	WriteDSN string
	// LeakyDSN is app_leaky: app_ro plus one accidental INSERT grant, which is
	// the finding R4.1a exists to report.
	LeakyDSN string
	// ColumnGrantDSN is app_cols, which holds column-level SELECT on users and
	// therefore cannot run `SELECT *` at all. SPEC R5.5.
	ColumnGrantDSN string
}

// schema is the shape the §12 tests reason about. The column policies the tests
// apply to it live in each test, not here: this is only the database.
const schema = `
CREATE TABLE users (
    id         bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    email      text NOT NULL,
    ssn        text NOT NULL,
    salary     numeric NOT NULL,
    status     text NOT NULL,
    notes      text,
    card       text,
    dob        date,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE orders (
    id         bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    user_email text NOT NULL,
    total      numeric NOT NULL,
    status     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- clean_ids carries nothing sensitive. It is the relation the de-tokenization
-- oracle of R8.4a is built on: SELECT $1::text FROM clean_ids computes an output
-- column whose relation is entirely allow, so only the emission scan stops it.
CREATE TABLE clean_ids (id bigint PRIMARY KEY);

-- accounts is R5.3b's counterexample. Both columns test 100% nine-digit and are
-- indistinguishable on content; only key status separates an SSN from an id.
CREATE TABLE accounts (
    account_number bigint PRIMARY KEY,
    ssn_num        bigint NOT NULL
);

CREATE TABLE events (id bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY, kind text);

CREATE VIEW user_stats AS
    SELECT status, count(*) AS n FROM users GROUP BY status;

CREATE VIEW user_report AS
    SELECT id, email, left(ssn, 3) AS ssn_prefix FROM users;

INSERT INTO users (email, ssn, salary, status, notes, card, dob) VALUES
    ('ada@example.com',   '123-45-6789', 100000, 'active',
     'call ada@example.com about the invoice', '4532117999283333', '1987-03-14'),
    ('grace@example.com', '987-65-4321',  87432, 'active',
     'no contact details here at all',         '4024007198765432', '1990-11-02'),
    ('alan@example.com',  '555-44-3333',  64000, 'churned',
     'left a voicemail',                       '4916338506082832', '1975-06-23');

INSERT INTO orders (user_email, total, status) VALUES
    ('ada@example.com',   42.00, 'shipped'),
    ('grace@example.com', 17.50, 'pending');

INSERT INTO clean_ids (id) SELECT generate_series(1, 10);

INSERT INTO accounts (account_number, ssn_num) VALUES
    (123456789, 987654321),
    (234567891, 876543219);
`

// roles provisions the credentials the audit tests reason about. Passwords are
// literals in a container that lives for one test run; nothing here is a secret.
const roles = `
CREATE ROLE app_ro    LOGIN PASSWORD 'ro';
CREATE ROLE app_rw    LOGIN PASSWORD 'rw';
CREATE ROLE app_leaky LOGIN PASSWORD 'leaky';
CREATE ROLE app_cols  LOGIN PASSWORD 'cols';

GRANT USAGE ON SCHEMA public TO app_ro, app_rw, app_leaky, app_cols;

GRANT SELECT ON ALL TABLES IN SCHEMA public TO app_ro, app_rw, app_leaky;

GRANT INSERT, UPDATE, DELETE ON orders TO app_rw;

-- The accident R4.1a reports: one relation, granted at column level, invisible
-- to has_table_privilege.
GRANT INSERT (kind) ON events TO app_leaky;

-- app_cols is §5.5's measured case: column grants make SELECT * fail for the
-- whole statement, not for the column.
GRANT SELECT (id, email, status, created_at) ON users TO app_cols;
GRANT SELECT ON orders, clean_ids TO app_cols;
`

// New starts a PostgreSQL container, applies the schema and roles, and returns
// DSNs for each role. The container is terminated through t.Cleanup.
func New(t *testing.T) *Fixture {
	t.Helper()
	ctx := t.Context()

	container, err := postgres.Run(ctx, image,
		postgres.WithDatabase("keeper_test"),
		postgres.WithUsername("keeper_owner"),
		postgres.WithPassword("owner"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate postgres: %v", err)
		}
	})

	super, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	conn, err := pgx.Connect(ctx, super)
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{schema, roles} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("apply fixture: %v", err)
		}
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	dsn := func(user, pass string) string {
		return "postgres://" + user + ":" + pass + "@" + host + ":" + port.Port() +
			"/keeper_test?sslmode=disable"
	}

	return &Fixture{
		SuperDSN:       super,
		ReadDSN:        dsn("app_ro", "ro"),
		WriteDSN:       dsn("app_rw", "rw"),
		LeakyDSN:       dsn("app_leaky", "leaky"),
		ColumnGrantDSN: dsn("app_cols", "cols"),
	}
}

// Exec runs a statement as the database owner, for tests that need to change the
// schema underneath a running keeper — a view redefinition, a new column, a
// grant made after an audit.
func (f *Fixture) Exec(t *testing.T, sql string) {
	t.Helper()
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, f.SuperDSN)
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

var _ = context.Background
