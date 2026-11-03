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

// checkHostMode decides whether a registered host may carry this string's
// login. A string that names a stronger sslmode than the host has would go out
// weaker than the person asked for, so it is refused; a weaker one cannot
// weaken a stronger host and only gets the notice.
func checkHostMode(t connTarget, hostMode string) (string, error) {
	want, ok := sslStrength[hostMode]
	if !ok {
		return "", fmt.Errorf("the registered host has sslmode %q, which is not one of %s", hostMode, strings.Join(types.SSLModes, ", "))
	}
	if !t.sslDefaulted && sslStrength[t.sslmode] > want {
		return "", fmt.Errorf("the connection string asks for sslmode=%s, but this host is already registered with sslmode=%s, so the login would be weaker than requested; remove the host with `keeper host rm <name>` and connect again, or lower the string's sslmode", t.sslmode, hostMode)
	}
	return modeNotice(t, hostMode), nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

var errNotConnString = errors.New("that is not a PostgreSQL connection string; use postgres://user:password@host:5432/database or key=value pairs")

// parseConnString reads only what the person typed. pgx.ParseConfig fills the
// gaps from PGUSER, PGPASSWORD, PGHOST, the service file and ~/.pgpass and has
// no option to stop, so a string missing a user would register a credential
// nobody typed.
func parseConnString(raw string) (connTarget, error) {
	raw = strings.TrimSpace(raw)
	var kv map[string]string
	var err error
	switch {
	case raw == "":
		return connTarget{}, errNotConnString
	case strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://"):
		kv, err = parseURI(raw)
	default:
		kv, err = parseKeyValue(raw)
	}
	if err != nil {
		return connTarget{}, errNotConnString
	}
	t := connTarget{address: kv["host"], port: 5432, database: kv["dbname"], user: kv["user"], password: kv["password"]}
	if p := kv["port"]; p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil || n == 0 {
			return connTarget{}, fmt.Errorf("the port in the connection string must be a number from 1 to 65535")
		}
		t.port = int(n)
	}
	if strings.HasPrefix(t.address, "/") {
		return connTarget{}, fmt.Errorf("the connection string needs a server host name or address, not a socket directory")
	}
	var missing []string
	if t.address == "" {
		missing = append(missing, "a server host name or address")
	}
	if t.database == "" {
		missing = append(missing, "a database")
	}
	if t.user == "" {
		missing = append(missing, "a user")
	}
	if len(missing) > 0 {
		return connTarget{}, fmt.Errorf("the connection string needs %s", strings.Join(missing, ", "))
	}
	if v := kv["sslmode"]; v != "" {
		if _, ok := sslStrength[v]; !ok {
			return connTarget{}, fmt.Errorf("sslmode %q is not valid; use one of %s", v, strings.Join(types.SSLModes, ", "))
		}
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

func parseURI(raw string) (map[string]string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	kv := map[string]string{"host": u.Hostname(), "port": u.Port(), "dbname": strings.TrimPrefix(u.Path, "/")}
	if u.User != nil {
		kv["user"] = u.User.Username()
		kv["password"], _ = u.User.Password()
	}
	for k, v := range u.Query() {
		if k == "database" {
			k = "dbname"
		}
		if kv[k] == "" && len(v) > 0 {
			kv[k] = v[0]
		}
	}
	if strings.Contains(kv["host"], ",") {
		return nil, errors.New("one host only")
	}
	return kv, nil
}

// parseKeyValue reads libpq's key=value form: values end at whitespace, or are
// single-quoted, and a backslash escapes the next character in both.
func parseKeyValue(s string) (map[string]string, error) {
	kv := map[string]string{}
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		key, rest, ok := strings.Cut(s, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t\n\r") {
			return nil, errors.New("invalid key=value")
		}
		rest = strings.TrimLeft(rest, " \t\n\r")
		quoted := strings.HasPrefix(rest, "'")
		if quoted {
			rest = rest[1:]
		}
		var val strings.Builder
		i, closed := 0, false
		for ; i < len(rest); i++ {
			c := rest[i]
			if c == '\\' && i+1 < len(rest) {
				i++
				val.WriteByte(rest[i])
			} else if quoted && c == '\'' {
				i++
				closed = true
				break
			} else if !quoted && strings.IndexByte(" \t\n\r", c) >= 0 {
				break
			} else {
				val.WriteByte(c)
			}
		}
		if quoted && !closed {
			return nil, errors.New("unterminated quoted value")
		}
		if key == "database" {
			key = "dbname"
		}
		kv[key] = val.String()
		s = rest[i:]
	}
	return kv, nil
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
			n, err := checkHostMode(t, h.SSLMode)
			if err != nil {
				return err
			}
			if n != "" {
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
