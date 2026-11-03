package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestTestFailureUsesKeepersOwnWords(t *testing.T) {
	err := testFailure(fmt.Errorf("wrapped: %w", &types.Error{Summary: "the server could not be reached", Action: "check the address and port"}), connTarget{})
	for _, want := range []string{"could not be reached", "check the address and port", "nothing was registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if strings.Contains(testFailure(errors.New("boom"), connTarget{}).Error(), "boom") {
		t.Error("an unknown error text must not be repeated")
	}
}

func TestParseConnStringSSLModeDefault(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		defaulted       bool
	}{
		{"remote, none", "postgres://u:p@host/db", "require", true},
		{"remote key=value, none", "host=db.internal user=u password=p dbname=db", "require", true},
		{"localhost, none", "postgres://u:p@localhost/db", "prefer", true},
		{"127.0.0.1, none", "postgres://u:p@127.0.0.1/db", "prefer", true},
		{"127.0.0.5, none", "postgres://u:p@127.0.0.5/db", "prefer", true},
		{"::1, none", "postgres://u:p@[::1]:5432/db", "prefer", true},
		{"remote, disable", "postgres://u:p@host/db?sslmode=disable", "disable", false},
		{"remote key=value, disable", "host=db.internal user=u password=p dbname=db sslmode=disable", "disable", false},
		{"remote, verify-full", "postgres://u:p@host/db?sslmode=verify-full", "verify-full", false},
		{"localhost, require", "postgres://u:p@localhost/db?sslmode=require", "require", false},
	} {
		got, err := parseConnString(tc.raw)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.sslmode != tc.want || got.sslDefaulted != tc.defaulted {
			t.Errorf("%s: sslmode %q defaulted %v, want %q %v", tc.name, got.sslmode, got.sslDefaulted, tc.want, tc.defaulted)
		}
	}
}

func TestTLSHint(t *testing.T) {
	remote := connTarget{sslmode: "require", sslDefaulted: true}
	if h := tlsHint(remote); !strings.Contains(h, "sslmode=disable") {
		t.Errorf("hint for a defaulted require lacks sslmode=disable: %q", h)
	}
	for _, tc := range []connTarget{
		{sslmode: "prefer", sslDefaulted: true},
		{sslmode: "require"},
		{sslmode: "disable"},
	} {
		if h := tlsHint(tc); h != "" {
			t.Errorf("%+v: unexpected hint %q", tc, h)
		}
	}
}

func TestModeNotice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		t        connTarget
		hostMode string
		want     bool
	}{
		{"explicit differs", connTarget{sslmode: "disable"}, "require", true},
		{"explicit same", connTarget{sslmode: "require"}, "require", false},
		{"default, stronger host", connTarget{sslmode: "require", sslDefaulted: true}, "verify-full", false},
		{"default, same host", connTarget{sslmode: "prefer", sslDefaulted: true}, "prefer", false},
	} {
		got := modeNotice(tc.t, tc.hostMode)
		if (got != "") != tc.want || tc.want && !strings.Contains(got, tc.hostMode) {
			t.Errorf("%s: notice %q, want notice %v", tc.name, got, tc.want)
		}
	}
}

func TestParseConnStringIgnoresEnvironment(t *testing.T) {
	passfile := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(passfile, []byte("*:*:*:*:from-pgpass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGUSER", "env_user")
	t.Setenv("PGPASSWORD", "env_password")
	t.Setenv("PGHOST", "env.host")
	t.Setenv("PGPORT", "6543")
	t.Setenv("PGDATABASE", "env_db")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGPASSFILE", passfile)

	for _, tc := range []struct {
		name, raw string
		want      connTarget
		wantErr   string
	}{
		{"uri complete", "postgres://u:p@db.internal:5433/app", connTarget{address: "db.internal", port: 5433, database: "app", user: "u", password: "p", sslmode: "require", sslDefaulted: true}, ""},
		{"uri no password", "postgres://u@db.internal/app", connTarget{address: "db.internal", port: 5432, database: "app", user: "u", sslmode: "require", sslDefaulted: true}, ""},
		{"uri percent-encoded", "postgres://u:p%40ss%3Aw%2Fd%20x@db.internal/app", connTarget{address: "db.internal", port: 5432, database: "app", user: "u", password: "p@ss:w/d x", sslmode: "require", sslDefaulted: true}, ""},
		{"uri query sslmode", "postgresql://u:p@db.internal/app?sslmode=verify-full", connTarget{address: "db.internal", port: 5432, database: "app", user: "u", password: "p", sslmode: "verify-full"}, ""},
		{"kv complete", "host=db.internal port=5433 dbname=app user=u password=p", connTarget{address: "db.internal", port: 5433, database: "app", user: "u", password: "p", sslmode: "require", sslDefaulted: true}, ""},
		{"kv quoted", `host='db.internal' dbname='my app' user=u password='it\'s a "pw" \\ x'`, connTarget{address: "db.internal", port: 5432, database: "my app", user: "u", password: `it's a "pw" \ x`, sslmode: "require", sslDefaulted: true}, ""},
		{"kv database alias", "host=db.internal database=app user=u", connTarget{address: "db.internal", port: 5432, database: "app", user: "u", sslmode: "require", sslDefaulted: true}, ""},
		{"kv no user", "host=db.internal dbname=app", connTarget{}, "a user"},
		{"kv no database", "host=db.internal user=u", connTarget{}, "a database"},
		{"kv no host", "dbname=app user=u", connTarget{}, "host"},
		{"uri no database", "postgres://u:p@db.internal", connTarget{}, "a database"},
		{"uri no user", "postgres://db.internal/app", connTarget{}, "a user"},
		{"unknown sslmode", "postgres://u:p@db.internal/app?sslmode=reqiure", connTarget{}, "verify-full"},
		{"bad port", "host=db.internal port=x dbname=app user=u", connTarget{}, "port"},
		{"not a string", "hello", connTarget{}, "not a PostgreSQL connection string"},
		{"empty", "  ", connTarget{}, "not a PostgreSQL connection string"},
	} {
		got, err := parseConnString(tc.raw)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%s: err %v, want it to contain %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		tc.want.profile = tc.want.database + "_" + tc.want.user
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestCheckHostMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		t          connTarget
		hostMode   string
		wantNotice bool
		wantErr    bool
	}{
		{"stronger refused", connTarget{sslmode: "require"}, "disable", false, true},
		{"stronger refused over prefer", connTarget{sslmode: "verify-full"}, "prefer", false, true},
		{"weaker noticed", connTarget{sslmode: "disable"}, "require", true, false},
		{"equal silent", connTarget{sslmode: "require"}, "require", false, false},
		{"defaulted weaker host refused", connTarget{sslmode: "require", sslDefaulted: true}, "prefer", false, true},
		{"defaulted disable host refused", connTarget{sslmode: "require", sslDefaulted: true}, "disable", false, true},
		{"defaulted stronger host silent", connTarget{sslmode: "require", sslDefaulted: true}, "verify-full", false, false},
		{"defaulted equal host silent", connTarget{sslmode: "prefer", sslDefaulted: true}, "prefer", false, false},
	} {
		notice, err := checkHostMode(tc.t, tc.hostMode)
		if (err != nil) != tc.wantErr || (notice != "") != tc.wantNotice {
			t.Errorf("%s: notice %q err %v", tc.name, notice, err)
		}
		if err != nil {
			for _, want := range []string{tc.t.sslmode, tc.hostMode, "keeper connection rm", "keeper host rm"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s: error %q lacks %q", tc.name, err, want)
				}
			}
		}
	}
	if _, err := checkHostMode(connTarget{sslmode: "require"}, "bogus"); err == nil || !strings.Contains(err.Error(), "verify-full") {
		t.Errorf("unknown host mode: %v", err)
	}
}

func TestParseConnStringRefusesAParameterItWouldDrop(t *testing.T) {
	for _, raw := range []string{
		"postgres://u:p@db.internal/app?sslrootcert=/etc/ca.pem",
		"postgres://u:p@db.internal/app?options=-c%20role%3Dro",
		"host=db.internal dbname=app user=u sslrootcert=/etc/ca.pem",
		"host=db.internal dbname=app user=u options='-c role=ro'",
	} {
		_, err := parseConnString(raw)
		if err == nil || !strings.Contains(err.Error(), "sslrootcert") && !strings.Contains(err.Error(), "options") {
			t.Errorf("%q: want a refusal naming the parameter, got %v", raw, err)
		}
		if err != nil && strings.Contains(err.Error(), "p@") {
			t.Errorf("the refusal repeats the string: %v", err)
		}
	}
	for _, raw := range []string{
		"postgres://u:p@db.internal/app?sslmode=require&database=app",
		"host=db.internal dbname=app user=u password=p port=5433 sslmode=verify-full",
	} {
		if _, err := parseConnString(raw); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
}
