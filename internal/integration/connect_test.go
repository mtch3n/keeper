//go:build integration

package integration

import (
	"bytes"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/client"
)

func buildKeeper(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "keeper")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "github.com/mtchen/keeper/cmd/keeper").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// runKeeper runs the built command with stdin as given and returns everything it printed.
func runKeeper(t *testing.T, bin, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func connString(host string, port int, user, pass, db string) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(user, pass), Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/" + db, RawQuery: "sslmode=disable"}
	return u.String()
}

// otherAddress names the same server by a different address, so the daemon sees a new host.
func otherAddress(host string) string {
	if host == "127.0.0.1" {
		return "localhost"
	}
	return "127.0.0.1"
}

func (e *leanEnv) counts(t *testing.T) (hosts, conns int) {
	t.Helper()
	h, err := e.op.ListHosts(t.Context())
	if err != nil {
		t.Fatalf("hosts: %v", err)
	}
	c, err := e.op.ListConnections(t.Context())
	if err != nil {
		t.Fatalf("connections: %v", err)
	}
	return len(h), len(c)
}

func TestConnect_PLG_C16_registers_a_profile_and_prints_the_login(t *testing.T) {
	e := newLeanEnv(t)
	bin := buildKeeper(t)
	hosts0, conns0 := e.counts(t)

	out, err := runKeeper(t, bin, connString(e.shop.host, e.shop.port, "shop_eval_ro", e.shop.roPass, "shop")+"\n", "connect")
	if err != nil {
		t.Fatalf("connect on a registered host: %v\n%s", err, out)
	}
	for _, want := range []string{`"shop_shop_eval_ro"`, "writes off", "connected as shop_eval_ro", "PostgreSQL 18"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if h, c := e.counts(t); h != hosts0 || c != conns0+1 {
		t.Errorf("same address and port must reuse the host: hosts %d->%d, connections %d->%d", hosts0, h, conns0, c)
	}

	alias := otherAddress(e.shop.host)
	out, err = runKeeper(t, bin, connString(alias, e.shop.port, "shop_eval_rw", e.shop.rwPass, "shop")+"\n", "connect", "--name", "shop_alias")
	if err != nil {
		t.Fatalf("connect on a new host: %v\n%s", err, out)
	}
	if !strings.Contains(out, "connected as shop_eval_rw") {
		t.Errorf("no login result:\n%s", out)
	}
	if h, c := e.counts(t); h != hosts0+1 || c != conns0+2 {
		t.Errorf("a new address must create one host: hosts %d->%d, connections %d->%d", hosts0, h, conns0, c)
	}
	hosts, _ := e.op.ListHosts(t.Context())
	found := false
	for _, h := range hosts {
		found = found || h.Name == alias && h.Address == alias
	}
	if !found {
		t.Errorf("no host named after the address %q: %+v", alias, hosts)
	}
	conns, _ := e.op.ListConnections(t.Context())
	for _, c := range conns {
		if (c.Name == "shop_alias" || c.Name == "shop_shop_eval_ro") && c.Writes != "off" {
			t.Errorf("%s writes = %q, want off", c.Name, c.Writes)
		}
	}
}

func TestConnect_PLG_C17_a_failed_connect_registers_nothing_and_hides_the_password(t *testing.T) {
	e := newLeanEnv(t)
	bin := buildKeeper(t)
	hosts0, conns0 := e.counts(t)
	alias := otherAddress(e.shop.host)

	secret := "wrong-Pa55/word"
	badLogin := connString(alias, e.shop.port, "shop_eval_ro", secret, "shop")
	taken := connString(alias, e.shop.port, "shop_eval_ro", e.shop.roPass, "shop")
	garbage := "postgres://u:" + secret + "@[bad host/db"
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"refused login": {badLogin, nil},
		"name in use":   {taken, []string{"--name", "shop_ro"}},
		"bad string":    {garbage, nil},
		"empty string":  {"", nil},
		"no database":   {"postgres://u:" + secret + "@" + alias + "/", nil},
	} {
		out, err := runKeeper(t, bin, tc.stdin+"\n", append([]string{"connect"}, tc.args...)...)
		if err == nil {
			t.Errorf("%s: want a failure, got:\n%s", name, out)
		}
		for _, leak := range []string{secret, url.QueryEscape(secret), e.shop.roPass, tc.stdin} {
			if leak != "" && strings.Contains(out, leak) {
				t.Errorf("%s: output repeats the secret or the string:\n%s", name, out)
			}
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%s: no message naming the fix", name)
		}
		if h, c := e.counts(t); h != hosts0 || c != conns0 {
			t.Errorf("%s: registered something: hosts %d->%d, connections %d->%d", name, hosts0, h, conns0, c)
		}
	}
}

func TestConnect_PLG_C18_reads_stdin_and_refuses_argv(t *testing.T) {
	e := newLeanEnv(t)
	bin := buildKeeper(t)
	hosts0, conns0 := e.counts(t)
	str := connString(e.shop.host, e.shop.port, "shop_eval_ro", e.shop.roPass, "shop")

	out, err := runKeeper(t, bin, str+"\n", "connect")
	if err != nil || !strings.Contains(out, "connected as shop_eval_ro") {
		t.Fatalf("one line on stdin must connect: %v\n%s", err, out)
	}
	if _, c := e.counts(t); c != conns0+1 {
		t.Fatalf("stdin connect registered %d profiles, want 1", c-conns0)
	}

	hosts1, conns1 := e.counts(t)
	out, err = runKeeper(t, bin, "", "connect", str)
	if err == nil {
		t.Fatalf("a string in argv must be refused:\n%s", out)
	}
	if strings.Contains(out, e.shop.roPass) || strings.Contains(out, str) {
		t.Errorf("refusal repeats the string:\n%s", out)
	}
	if !strings.Contains(out, "stdin") {
		t.Errorf("refusal does not name the fix:\n%s", out)
	}
	if h, c := e.counts(t); h != hosts1 || c != conns1 || h != hosts0 {
		t.Errorf("argv attempt registered something: hosts %d->%d, connections %d->%d", hosts1, h, conns1, c)
	}
}

func TestConnectUnreachableNamesReachability(t *testing.T) {
	e := newLeanEnv(t)
	bin := buildKeeper(t)
	hosts0, conns0 := e.counts(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	out, err := runKeeper(t, bin, connString("127.0.0.1", port, "u", "pw-closed-port", "shop")+"\n", "connect")
	if err == nil {
		t.Fatalf("want a failure:\n%s", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "reach") || strings.Contains(low, "refused the login") {
		t.Errorf("output must say the server could not be reached, not that a login was refused:\n%s", out)
	}
	if strings.Contains(out, "pw-closed-port") || !strings.Contains(out, "nothing was registered") {
		t.Errorf("bad output:\n%s", out)
	}
	if h, c := e.counts(t); h != hosts0 || c != conns0 {
		t.Errorf("registered something: hosts %d->%d, connections %d->%d", hosts0, h, conns0, c)
	}
}

// The README's first-run path: keeper connect, then the agent's first query
// returns the email as a token with no catalog step. It has no case ID of its own.
func TestConnect_ThenTheFirstQueryReturnsAnEmailToken(t *testing.T) {
	e := newLeanEnv(t)
	bin := buildKeeper(t)
	alias := otherAddress(e.shop.host)
	out, err := runKeeper(t, bin, connString(alias, e.shop.port, "shop_eval_ro", e.shop.roPass, "shop")+"\n", "connect", "--name", "first_run")
	if err != nil {
		t.Fatalf("connect: %v\n%s", err, out)
	}
	conns, err := e.op.ListConnections(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, c := range conns {
		if c.Name == "first_run" {
			id = c.ID
		}
	}
	if id == "" {
		t.Fatalf("keeper connect did not register first_run: %+v", conns)
	}
	body := e.query(t, e.agent, id, "SELECT email FROM customers WHERE id = 4")
	q := parseBody(t, body)
	if len(q.Rows) != 1 {
		t.Fatalf("not one row: %s", body)
	}
	if tok, _ := q.Rows[0][0].(string); !strings.Contains(tok, "email") || strings.Contains(tok, "@") {
		t.Errorf("email = %v, want a token", q.Rows[0][0])
	}
	if leaks := e.truth.leaks(body); len(leaks) != 0 {
		t.Errorf("seeded personal data in the result: %v", leaks)
	}
}

func TestConnectDefaultsToTLSOnARemoteHost(t *testing.T) {
	e := newLeanEnv(t)
	bin := buildKeeper(t)
	hosts0, conns0 := e.counts(t)

	addrs, _ := net.InterfaceAddrs()
	remote := ""
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
			remote = ipn.IP.String()
			break
		}
	}
	if remote == "" {
		t.Skip("no non-loopback IPv4 address on this machine")
	}
	if c, err := net.Dial("tcp", net.JoinHostPort(remote, strconv.Itoa(e.shop.port))); err != nil {
		t.Skipf("the test server is not reachable on %s: %v", remote, err)
	} else {
		c.Close()
	}

	str := connString(remote, e.shop.port, "shop_eval_ro", e.shop.roPass, "shop")
	str = strings.TrimSuffix(str, "?sslmode=disable")
	out, err := runKeeper(t, bin, str+"\n", "connect")
	if err == nil {
		t.Fatalf("a non-TLS server with no sslmode on a remote host must fail:\n%s", out)
	}
	if !strings.Contains(out, "sslmode=disable") || !strings.Contains(out, "nothing was registered") {
		t.Errorf("output does not name the fix:\n%s", out)
	}
	if strings.Contains(out, e.shop.roPass) {
		t.Errorf("output repeats the password:\n%s", out)
	}
	if h, c := e.counts(t); h != hosts0 || c != conns0 {
		t.Errorf("registered something: hosts %d->%d, connections %d->%d", hosts0, h, conns0, c)
	}
}

func TestHostDefaultsToRequireUnlessLoopback(t *testing.T) {
	sock := startDaemon(t)
	op, err := client.DialHuman(t.Context(), sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { op.Close() })
	for address, want := range map[string]string{"db.example.com": "require", "10.1.2.3": "require", "localhost": "prefer", "127.0.0.1": "prefer", "::1": "prefer"} {
		h, err := op.RegisterHost(t.Context(), client.RegisterHostParams{Name: address, Address: address})
		if err != nil {
			t.Fatalf("%s: %v", address, err)
		}
		if h.SSLMode != want {
			t.Errorf("%s: sslmode %q, want %q", address, h.SSLMode, want)
		}
	}
}
