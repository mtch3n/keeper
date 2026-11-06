//go:build integration

package integration

import (
	"cmp"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mtchen/keeper/internal/client"
)

const (
	oldBuild = "0.1.0-1-gaaaaaaa"
	newBuild = "0.1.0-2-gbbbbbbb"
)

// upgradeRig is a daemon and keeper binaries at two builds, with a home, a
// socket and a key of its own, and nothing of the machine's keeper on PATH.
type upgradeRig struct {
	dir, sock string
	old, new  string
	env       []string
}

func newUpgradeRig(t *testing.T) *upgradeRig {
	t.Helper()
	dir := t.TempDir()
	r := &upgradeRig{dir: dir, sock: filepath.Join(dir, "k.sock")}
	r.old = buildKeeperAt(t, filepath.Join(dir, "old"), oldBuild)
	r.new = buildKeeperAt(t, filepath.Join(dir, "new"), newBuild)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*5 + 1)
	}
	goBin := filepath.Dir(must(exec.LookPath("go")))
	r.env = []string{
		"HOME=" + filepath.Join(dir, "home"),
		"KEEPER_HOME=" + filepath.Join(dir, "khome"),
		"KEEPER_SOCKET=" + r.sock,
		"KEEPER_UI_PORT=0",
		"KEEPER_MASTER_KEY=" + base64.StdEncoding.EncodeToString(key),
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/keeper-upgrade-bus",
		"PATH=" + goBin + ":/usr/bin:/bin",
	}
	t.Cleanup(r.stop)
	return r
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func buildKeeperAt(t *testing.T, dir, version string) string {
	t.Helper()
	bin := filepath.Join(dir, "keeper")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin,
		"-ldflags", "-X github.com/mtchen/keeper/internal/build.Version="+version,
		"github.com/mtchen/keeper/cmd/keeper")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", version, err, out)
	}
	return bin
}

// startDaemon runs bin's daemon detached, as the launcher would, and waits for
// its socket.
func (r *upgradeRig) startDaemon(t *testing.T, bin string) {
	t.Helper()
	cmd := exec.Command(bin, "daemon", "serve", "--idle-exit=-1s", "--log-level=error")
	cmd.Env = r.env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	r.waitSocket(t)
}

func (r *upgradeRig) waitSocket(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("unix", r.sock); err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("keeperd did not start")
}

func (r *upgradeRig) daemonVersion(t *testing.T) string {
	t.Helper()
	v, _, err := client.DaemonStatus(t.Context(), r.sock)
	if err != nil {
		t.Fatalf("daemon status: %v", err)
	}
	return v
}

// stop shuts the daemon down and waits until its socket stops answering.
func (r *upgradeRig) stop() {
	hc := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", r.sock)
	}}}
	if resp, err := hc.Post("http://keeper/v1/daemon/shutdown", "application/json", nil); err == nil {
		resp.Body.Close()
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("unix", r.sock)
		if err != nil {
			return
		}
		c.Close()
		time.Sleep(50 * time.Millisecond)
	}
}

// onPath puts dir first on the rig's PATH.
func (r *upgradeRig) onPath(dir string) {
	for i, kv := range r.env {
		if rest, ok := strings.CutPrefix(kv, "PATH="); ok {
			r.env[i] = "PATH=" + dir + ":" + rest
		}
	}
}

// agent connects an MCP client to `bin mcp` over stdio, the way a harness
// window does.
// protocol pins the MCP version the client offers; empty is the latest.
func (r *upgradeRig) agent(t *testing.T, bin, protocol string) (*mcp.ClientSession, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command(bin, "mcp")
	cmd.Env = r.env
	opts := &mcp.ClientSessionOptions{ProtocolVersion: protocol}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "upgrade-agent", Version: "1"}, nil).
		Connect(t.Context(), &mcp.CommandTransport{Command: cmd}, opts)
	if err != nil {
		t.Fatalf("connect %s mcp: %v", bin, err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, cmd
}

func callText(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n"), res.IsError
}

func exeOf(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	p, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "exe"))
	if err != nil {
		t.Fatalf("readlink exe: %v", err)
	}
	return strings.TrimSuffix(p, " (deleted)")
}

// Claude Code opens with initialize, the stateful protocol, so a replacement
// that did not replay it would refuse every call as made before
// initialization. The latest protocol opens with server/discover instead.
func TestUpg_UPG_C1_AnOldKeeperMcpBecomesTheDaemonsBuildAndKeepsItsSession(t *testing.T) {
	for _, protocol := range []string{"2025-06-18", ""} {
		t.Run("protocol "+cmp.Or(protocol, "latest"), func(t *testing.T) { upgradeInPlace(t, protocol) })
	}
}

func upgradeInPlace(t *testing.T, protocol string) {
	r := newUpgradeRig(t)
	r.onPath(filepath.Dir(r.new))
	r.startDaemon(t, r.old)
	cs, cmd := r.agent(t, r.old, protocol)
	if text, isErr := callText(t, cs, "list_connections", map[string]any{}); isErr {
		t.Fatalf("list_connections on matching builds: %s", text)
	}

	r.stop()
	r.startDaemon(t, r.new)
	// The first call meets the lost connection and says, once, that the
	// session is new and its tokens are gone. The next one finds the newer
	// daemon and is answered by its build.
	if text, isErr := callText(t, cs, "list_connections", map[string]any{}); isErr && !strings.Contains(text, "new session") {
		t.Fatalf("the call that met the restart = %s", text)
	}
	text, isErr := callText(t, cs, "list_connections", map[string]any{})
	if isErr || strings.Contains(text, "version mismatch") {
		t.Fatalf("after the daemon moved to %s, list_connections = %s", newBuild, text)
	}
	if got := exeOf(t, cmd); got != r.new {
		t.Errorf("keeper mcp runs %s, want the daemon's build %s", got, r.new)
	}
	if text, isErr := callText(t, cs, "list_connections", map[string]any{}); isErr {
		t.Errorf("a second call after the hand-over: %s", text)
	}
}

func TestWithNoBinaryOfTheDaemonsBuildTheMismatchIsReportedOnce(t *testing.T) {
	r := newUpgradeRig(t)
	r.startDaemon(t, r.new)
	cs, cmd := r.agent(t, r.old, "")
	for range 2 {
		text, isErr := callText(t, cs, "list_connections", map[string]any{})
		if !isErr || !strings.Contains(text, "version mismatch") {
			t.Fatalf("with no %s binary on disk, list_connections = %s, want the mismatch", newBuild, text)
		}
	}
	if got := exeOf(t, cmd); got != r.old {
		t.Errorf("keeper mcp runs %s, want it unchanged at %s", got, r.old)
	}
	if v := r.daemonVersion(t); v != newBuild {
		t.Errorf("an older keeper mcp changed the daemon to %s", v)
	}
}

func TestUpg_UPG_C3_ANewerKeeperMcpRestartsAnIdleOlderDaemon(t *testing.T) {
	r := newUpgradeRig(t)
	r.startDaemon(t, r.old)
	cs, _ := r.agent(t, r.new, "")
	text, isErr := callText(t, cs, "list_connections", map[string]any{})
	if isErr {
		t.Fatalf("list_connections against an idle older daemon: %s", text)
	}
	if v := r.daemonVersion(t); v != newBuild {
		t.Errorf("daemon is %s, want it restarted on %s", v, newBuild)
	}
}

func TestUpg_UPG_C4_AnOlderDaemonWithSomethingWaitingIsNotRestarted(t *testing.T) {
	r := newUpgradeRig(t)
	r.startDaemon(t, r.old)

	// A local request waits on a person; it needs a registered connection but
	// no database.
	prev := client.Version
	client.Version = oldBuild
	t.Cleanup(func() { client.Version = prev })
	op, err := client.DialHuman(t.Context(), r.sock)
	if err != nil {
		t.Fatalf("operator dial: %v", err)
	}
	host, err := op.RegisterHost(t.Context(), client.RegisterHostParams{Name: "nowhere", Address: "127.0.0.1", Port: 1, SSLMode: "disable"})
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	conn, err := op.RegisterConnection(t.Context(), client.RegisterConnectionParams{
		Name: "nowhere", HostID: host.ID, Database: "db", Credential: client.Credential{User: "u", Password: "p"},
	})
	op.Close()
	if err != nil {
		t.Fatalf("connection: %v", err)
	}
	waiter, _ := r.agent(t, r.old, "")
	if text, isErr := callText(t, waiter, "request_input", map[string]any{"connection_id": conn.ID, "namespace": "email"}); isErr {
		t.Fatalf("request_input: %s", text)
	}

	cs, _ := r.agent(t, r.new, "")
	text, isErr := callText(t, cs, "list_connections", map[string]any{})
	if !isErr || !strings.Contains(text, "1 approval(s), ticket(s) or local request(s) are waiting") {
		t.Errorf("a newer keeper mcp with one request waiting = %s, want the mismatch naming it", text)
	}
	if v := r.daemonVersion(t); v != oldBuild {
		t.Errorf("daemon is %s, want it left at %s", v, oldBuild)
	}
}
