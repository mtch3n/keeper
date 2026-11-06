//go:build integration

package integration

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/client"
)

// A login refused while connecting, and a database name the server does not
// have, are the connection failing, not the statement. Before HLT-D2 the first
// read as "the database refused this statement" and the second as "the
// statement did not parse".
func TestHlt_HLT_C4_ALoginOrADatabaseRefusedWhileConnectingNamesWhichOne(t *testing.T) {
	e := newLeanEnv(t)
	host, err := e.op.RegisterHost(t.Context(), client.RegisterHostParams{Name: "refusing", Address: e.shop.host, Port: e.shop.port, SSLMode: "disable"})
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	for _, tc := range []struct {
		name, database, password, want string
	}{
		{"badpass", "shop", "not-the-password", "login"},
		{"nodb", "no_such_db", e.shop.roPass, "database"},
	} {
		c, err := e.op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
			Name: tc.name, HostID: host.ID, Database: tc.database,
			Credential: client.Credential{User: "shop_eval_ro", Password: tc.password},
		})
		if err != nil {
			t.Fatalf("connection %s: %v", tc.name, err)
		}
		body := e.query(t, e.agent, c.ID, "SELECT 1 AS one")
		if !strings.Contains(body, "unreachable") || !strings.Contains(body, tc.want) || strings.Contains(body, "statement") {
			t.Errorf("%s: %s, want unreachable naming the %s", tc.name, body, tc.want)
		}
		if strings.Contains(body, tc.password) && tc.password != e.shop.roPass {
			t.Errorf("%s: the password came back in the error", tc.name)
		}
	}
}

// A server that takes the connection and never answers is what a filtering
// firewall looks like. describe answers after one connect timeout, not one per
// layer, and says why.
func TestHlt_HLT_C5_ShowingAConnectionThatDoesNotAnswerTakesOneTimeoutAndSaysWhy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	sock := startDaemon(t)
	op, err := client.DialHuman(t.Context(), sock)
	if err != nil {
		t.Fatalf("operator dial: %v", err)
	}
	defer op.Close()
	addr := ln.Addr().(*net.TCPAddr)
	host, err := op.RegisterHost(t.Context(), client.RegisterHostParams{Name: "silent", Address: "127.0.0.1", Port: addr.Port, SSLMode: "disable"})
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	c, err := op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: "silent", HostID: host.ID, Database: "db", Credential: client.Credential{User: "u", Password: "p"},
	})
	if err != nil {
		t.Fatalf("connection: %v", err)
	}
	start := time.Now()
	det, err := op.DescribeConnection(t.Context(), c.ID)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if took > 15*time.Second {
		t.Errorf("describe took %s, want one connect timeout", took)
	}
	var reason string
	for _, d := range det.Degradations {
		reason += d.Layer + ": " + d.Reason + "\n"
	}
	if !strings.Contains(reason, "did not answer") {
		t.Errorf("degradations = %q, want the no-answer reason", reason)
	}
}
