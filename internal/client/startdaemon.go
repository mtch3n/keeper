package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// DefaultSocketPath returns $XDG_RUNTIME_DIR/keeper.sock, or a per-user
// fallback under the OS temp dir when XDG_RUNTIME_DIR is unset (SPEC §3.1,
// R3.4a). KEEPER_SOCKET overrides both, for tests and unusual setups.
func DefaultSocketPath() string {
	if v := os.Getenv("KEEPER_SOCKET"); v != "" {
		return v
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), fmt.Sprintf("keeper-%d", os.Getuid()))
	}
	return filepath.Join(dir, "keeper.sock")
}

// StartDaemon spawns keeperd if socketPath is absent or refuses connections,
// then waits for it to come up, retrying with backoff — the same pattern
// gpg-agent and Docker Desktop use (SPEC §3.1). It never restarts an already
// running daemon: a live socket is left alone.
//
// keeperd itself is responsible for the start-race election (flock on
// keeper.lock, SPEC R3.4c); StartDaemon only needs to know whether *some*
// daemon is serving the socket by the time it returns.
func StartDaemon(ctx context.Context, socketPath string) error {
	if reachable(ctx, socketPath) {
		return nil
	}

	exe, args, err := findKeeper()
	if err != nil {
		return fmt.Errorf("keeper: cannot start daemon: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return fmt.Errorf("keeper: create runtime dir: %w", err)
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdin = nil
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		cmd.Stdout = devnull
		cmd.Stderr = devnull
	}
	detachProcess(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("keeper: start %s: %w", exe, err)
	}
	// Detach fully: the daemon must outlive this process, so we do not wait
	// on it and do not want it turned into a zombie by our own reaping.
	_ = cmd.Process.Release()

	backoff := 50 * time.Millisecond
	const maxBackoff = time.Second
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if reachable(ctx, socketPath) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
		}
	}
	return fmt.Errorf("keeper: keeperd did not become ready on %s within 15s", socketPath)
}

// reachable reports whether something is accepting connections on
// socketPath. It never unlinks the socket on refusal (R3.4c: refusal in the
// window between a winning daemon's bind() and listen() looks identical to a
// stale socket, and deleting it would kill a daemon seconds from serving).
func reachable(ctx context.Context, socketPath string) bool {
	var d net.Dialer
	dialCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	conn, err := d.DialContext(dialCtx, "unix", socketPath)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// findKeeper locates the binary to start the daemon with, and the arguments
// that make it one.
//
// keeper ships as a single executable with subcommands, so the daemon is
// `keeper daemon serve` rather than a separate file. This process's own
// executable is tried first: a client started from a build directory should
// start that build's daemon, not whichever one happens to be on PATH, because
// the two refuse each other on a version mismatch (SPEC §3.4) and silently
// pairing with a stranger is the confusing version of that failure.
func findKeeper() (string, []string, error) {
	args := []string{"daemon", "serve"}
	if self, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		if info, err := os.Stat(self); err == nil && !info.IsDir() {
			return self, args, nil
		}
	}
	if p, err := exec.LookPath("keeper"); err == nil {
		return p, args, nil
	}
	return "", nil, errors.New("keeper not found: this process has no executable path and none is on PATH")
}

// RestartDaemon stops the running daemon and starts a fresh one.
//
// Nothing calls this automatically. A restart cancels every pending ticket and
// permanently invalidates every token every session is holding — the reverse map
// is memory and HMAC is one-way, so those tokens can never be resolved again and
// the queries that minted them have to be re-run (SPEC R3.4d). That is a cost
// one window must not impose on the others without a person choosing it.
func RestartDaemon(ctx context.Context, socketPath string) error {
	// Ask it to stop without handshaking first.
	//
	// A version mismatch is the main reason anyone runs this, and Dial refuses on
	// exactly that — so going through Dial would make the command the error
	// message recommends the one command that cannot work. Nothing here needs a
	// session: stopping is not an operation on one.
	shutdown(ctx, socketPath)

	// Wait for the socket to stop answering before starting a new one, so the
	// replacement does not race the old process's listener.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	return StartDaemon(ctx, socketPath)
}

// shutdown posts the stop request straight to the socket, with no handshake and
// no session. A daemon that is not there, or does not answer, is not an error:
// the caller is about to start a new one either way.
func shutdown(ctx context.Context, socketPath string) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 5 * time.Second,
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/v1/daemon/shutdown", nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
