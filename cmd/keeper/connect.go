package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mtchen/keeper/internal/client"
	"github.com/mtchen/keeper/internal/types"
)

// runConnect registers a host and a read-only profile from one connection
// string. The string carries the password, so it is read from the terminal
// with echo off or from stdin, never from argv, and no message here repeats it.
func runConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	name := fs.String("name", "", "profile name (default <database>_<user>)")
	catalog := fs.String("catalog", "", "a shared project catalog file (default: this profile's own, in keeper's state directory)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("connect takes no argument: a connection string in argv is visible to every process; run `keeper connect` and enter it at the prompt, or pipe it on stdin")
	}
	raw, err := newSecretReader().read("connection string")
	if err != nil {
		return err
	}
	target, err := parseConnString(raw)
	if err != nil {
		return err
	}
	if *name != "" {
		target.profile = *name
	}
	target.catalogPath = *catalog

	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	return connectProfile(ctx, cli, target)
}

type connTarget struct {
	address, sslmode, database, user, password, profile, catalogPath string
	port                                                             int
	sslDefaulted                                                     bool
}

// tlsHint names the way out when keeper itself chose require: pgx reports a
// server that refuses TLS and an unreachable server as the same safe error, so
// the hint is attached to every failure on a defaulted require.
func tlsHint(t connTarget) string {
	if !t.sslDefaulted || t.sslmode != "require" {
		return ""
	}
	return "no sslmode was given, so keeper required TLS for this remote host; if the server does not offer TLS and plaintext is really intended, add sslmode=disable to the connection string"
}

var sslStrength = map[string]int{"disable": 0, "allow": 1, "prefer": 2, "require": 3, "verify-ca": 4, "verify-full": 5}

// modeNotice reports when a reused host's sslmode, not the string's, decides
// the login: the string named a different mode, or named none and the host is
// weaker than keeper's default.
func modeNotice(t connTarget, hostMode string) string {
	if hostMode == t.sslmode {
		return ""
	}
	if !t.sslDefaulted || sslStrength[hostMode] < sslStrength[t.sslmode] {
		return fmt.Sprintf("this host is already registered with sslmode=%s; that mode is used, not the connection string's", hostMode)
	}
	return ""
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func parseConnString(raw string) (connTarget, error) {
	raw = strings.TrimSpace(raw)
	cfg, err := pgx.ParseConfig(raw)
	if err != nil || raw == "" {
		return connTarget{}, fmt.Errorf("that is not a PostgreSQL connection string; use postgres://user:password@host:5432/database or key=value pairs")
	}
	t := connTarget{
		address: cfg.Host, port: int(cfg.Port), database: cfg.Database,
		user: cfg.User, password: cfg.Password,
	}
	if t.address == "" || strings.HasPrefix(t.address, "/") {
		return connTarget{}, fmt.Errorf("the connection string needs a server host name or address")
	}
	if t.database == "" || t.user == "" {
		return connTarget{}, fmt.Errorf("the connection string needs a database and a user")
	}
	if v := sslmodeOf(raw); v != "" {
		t.sslmode = v
	} else {
		t.sslDefaulted = true
		t.sslmode = "require"
		if isLoopback(t.address) {
			t.sslmode = "prefer"
		}
	}
	t.profile = t.database + "_" + t.user
	return t, nil
}

func sslmodeOf(raw string) string {
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		if u, err := url.Parse(raw); err == nil {
			return u.Query().Get("sslmode")
		}
		return ""
	}
	for f := range strings.FieldsSeq(raw) {
		if v, ok := strings.CutPrefix(f, "sslmode="); ok {
			return v
		}
	}
	return ""
}

// connectProfile registers first and tests second, because the daemon has no
// way to test a login before it stores one. A failed login removes what this
// call created, so a refused string leaves nothing behind. The daemon accepts
// duplicate profile names, so the name check is made here, before anything is created.
func connectProfile(ctx context.Context, cli *client.Client, t connTarget) error {
	conns, err := cli.ListConnections(ctx)
	if err != nil {
		return err
	}
	for _, c := range conns {
		if c.Name == t.profile {
			return fmt.Errorf("a profile named %q already exists; pick another name with --name, or remove the old one with `keeper connection rm`", t.profile)
		}
	}
	hosts, err := cli.ListHosts(ctx)
	if err != nil {
		return err
	}
	var hostID string
	for _, h := range hosts {
		if h.Address == t.address && h.Port == t.port {
			hostID = h.ID
			if n := modeNotice(t, h.SSLMode); n != "" {
				fmt.Println(n)
			}
		}
	}
	newHost := hostID == ""
	if newHost {
		hostName := t.address
		for _, h := range hosts {
			if h.Name == hostName {
				hostName = t.address + ":" + strconv.Itoa(t.port)
			}
		}
		h, err := cli.RegisterHost(ctx, client.RegisterHostParams{Name: hostName, Address: t.address, Port: t.port, SSLMode: t.sslmode})
		if err != nil {
			return fmt.Errorf("could not register the host: %w", err)
		}
		hostID = h.ID
	}
	undoHost := func() {
		if newHost {
			_ = cli.RemoveHost(ctx, hostID)
		}
	}

	conn, err := cli.RegisterConnection(ctx, client.RegisterConnectionParams{
		Name: t.profile, HostID: hostID, Database: t.database, Writes: types.WritesOff,
		Credential:  client.Credential{User: t.user, Password: t.password},
		CatalogPath: t.catalogPath,
	})
	if err != nil {
		undoHost()
		return fmt.Errorf("could not register profile %q: %w", t.profile, err)
	}
	res, err := cli.TestConnection(ctx, conn.ID)
	if err != nil {
		_ = cli.RemoveConnection(ctx, conn.ID)
		undoHost()
		return testFailure(err, t)
	}
	fmt.Printf("profile %q registered with writes off\n", t.profile)
	fmt.Printf("connected as %s · PostgreSQL %s · %s\n", res.User, res.ServerVersion, time.Duration(res.RoundTrip).Round(time.Millisecond))
	return nil
}

func testFailure(err error, t connTarget) error {
	msg := "the login test failed; check the connection string and run `keeper connect` again"
	if ke, ok := errors.AsType[*types.Error](err); ok {
		msg = ke.Summary
		if ke.Action != "" {
			msg += ". " + ke.Action
		}
	}
	if h := tlsHint(t); h != "" {
		msg += ". " + h
	}
	return fmt.Errorf("%s; nothing was registered", msg)
}
