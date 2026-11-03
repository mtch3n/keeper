package main

import (
	"errors"
	"fmt"
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
		{"default, weaker host", connTarget{sslmode: "require", sslDefaulted: true}, "prefer", true},
		{"default, stronger host", connTarget{sslmode: "require", sslDefaulted: true}, "verify-full", false},
		{"default, same host", connTarget{sslmode: "prefer", sslDefaulted: true}, "prefer", false},
	} {
		got := modeNotice(tc.t, tc.hostMode)
		if (got != "") != tc.want || tc.want && !strings.Contains(got, tc.hostMode) {
			t.Errorf("%s: notice %q, want notice %v", tc.name, got, tc.want)
		}
	}
}
