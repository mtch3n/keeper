package daemon_test

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/mtchen/keeper/internal/daemon"
)

// §3.4: two windows may spawn keeperd simultaneously; flock elects one and the
// loser connects to the winner rather than starting a second daemon.
func TestFlockElectsOneDaemon(t *testing.T) {
	dir := t.TempDir()
	path := daemon.LockPath(dir)

	first, err := daemon.AcquireLock(path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}

	if _, err := daemon.AcquireLock(path); !errors.Is(err, daemon.ErrDaemonRunning) {
		t.Fatalf("a second daemon was elected: %v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := daemon.AcquireLock(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	second.Release()
}

// R3.4c: unlink only under the lock. A unix socket returns ECONNREFUSED between
// bind() and listen(), so anything that unlinks without the lock can delete a
// live socket belonging to a daemon that is seconds from serving.
func TestSocketCannotBeBoundWithoutTheLock(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "keeper.sock")

	// A live socket stands in for the daemon that already won.
	live, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer live.Close()

	var unlocked daemon.Lock
	if _, err := unlocked.ListenSocket(sock); err == nil {
		t.Fatal("a caller without the lock was allowed to unlink and rebind the socket")
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("the live socket was removed: %v", err)
	}
}

// R3.4a: the socket is mode 0600 in a 0700 parent, and a stale socket is
// replaced by the holder of the lock.
func TestListenSocketReplacesAStaleSocketAndIsPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "keeper.sock")

	stale, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	// Leave the file behind, as a daemon that was killed would.
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("the stale socket was not left behind: %v", err)
	}

	lock, err := daemon.AcquireLock(daemon.LockPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	ln, err := lock.ListenSocket(sock)
	if err != nil {
		t.Fatalf("rebinding a stale socket under the lock failed: %v", err)
	}
	defer ln.Close()

	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode %o, want 600", got)
	}
}

// R3.4a: the runtime directory is mode 0700.
func TestRuntimeDirIsPrivate(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "run"))
	dir, err := daemon.RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got&0o077 != 0 {
		t.Fatalf("runtime directory mode %o is reachable by others", got)
	}
}
