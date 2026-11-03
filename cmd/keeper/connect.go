package main

import (
	"context"
	"flag"
	"fmt"
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

	ctx := context.Background()
	cli, err := connectDaemon(ctx)
	if err != nil {
		return err
	}
	defer cli.Close()
	return connectProfile(ctx, cli, target)
}

type connTarget struct {
	address, sslmode, database, user, password, profile string
	port                                                int
}

func parseConnString(raw string) (connTarget, error) {
	raw = strings.TrimSpace(raw)
	cfg, err := pgx.ParseConfig(raw)
	if err != nil || raw == "" {
		return connTarget{}, fmt.Errorf("that is not a PostgreSQL connection string; use postgres://user:password@host:5432/database or key=value pairs")
	}
	t := connTarget{
		address: cfg.Host, port: int(cfg.Port), database: cfg.Database,
		user: cfg.User, password: cfg.Password, sslmode: "prefer",
	}
	if t.address == "" || strings.HasPrefix(t.address, "/") {
		return connTarget{}, fmt.Errorf("the connection string needs a server host name or address")
	}
	if t.database == "" || t.user == "" {
		return connTarget{}, fmt.Errorf("the connection string needs a database and a user")
	}
	if v := sslmodeOf(raw); v != "" {
		t.sslmode = v
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
		Credential: client.Credential{User: t.user, Password: t.password},
	})
	if err != nil {
		undoHost()
		return fmt.Errorf("could not register profile %q: %w", t.profile, err)
	}
	res, err := cli.TestConnection(ctx, conn.ID)
	if err != nil {
		_ = cli.RemoveConnection(ctx, conn.ID)
		undoHost()
		return fmt.Errorf("the server refused the login for user %q on database %q; check the user, the password and the database in the string, then run `keeper connect` again; nothing was registered", t.user, t.database)
	}
	fmt.Printf("profile %q registered with writes off\n", t.profile)
	fmt.Printf("connected as %s · PostgreSQL %s · %s\n", res.User, res.ServerVersion, time.Duration(res.RoundTrip).Round(time.Millisecond))
	return nil
}
