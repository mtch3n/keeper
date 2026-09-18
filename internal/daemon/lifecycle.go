package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// ErrDaemonRunning is returned when another keeperd already holds the lock. The
// loser of the start race connects to the winner (§3.4); it does not retry the
// lock and it does not touch the socket.
var ErrDaemonRunning = errors.New("keeper: another daemon holds the lock")

// RuntimeDir is where the socket and the lock live: $XDG_RUNTIME_DIR, or a
// per-user directory under the system temp directory when the environment does
// not set one. R3.4a requires the parent to be mode 0700.
func RuntimeDir() (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "keeper-"+strconv.Itoa(os.Getuid()))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("keeper: runtime directory: %w", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("keeper: runtime directory: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", fmt.Errorf("keeper: runtime directory is group- or world-accessible: %w", err)
		}
	}
	return dir, nil
}

// SocketPath and LockPath name the two files in the runtime directory.
func SocketPath(dir string) string { return filepath.Join(dir, "keeper.sock") }
func LockPath(dir string) string   { return filepath.Join(dir, "keeper.lock") }

// LockPathFor derives the lock from the socket a daemon will serve, so the
// election is scoped to that socket rather than to the machine.
//
// A fixed lock beside a movable socket means two keepers with different sockets
// fight over one election and the second silently declines to start — which is
// wrong for a test instance, wrong for someone who wants an instance per
// project, and confusing in both cases, because the symptom is a daemon that
// reports success and a socket that never appears.
func LockPathFor(socketPath string) string {
	return socketPath + ".lock"
}

// Lock is the flock that elects a single daemon (§3.4).
type Lock struct {
	f    *os.File
	path string
}

// AcquireLock takes the exclusive, non-blocking lock. Two windows may spawn
// keeperd simultaneously; this elects one, and the loser gets ErrDaemonRunning.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("keeper: lock file: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errWouldBlock) {
			return nil, ErrDaemonRunning
		}
		return nil, fmt.Errorf("keeper: flock: %w", err)
	}
	return &Lock{f: f, path: path}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlockFile(l.f)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

// ListenSocket binds the unix socket, mode 0600 in a 0700 parent (R3.4a).
//
// It is a method on Lock, and not a function, because of R3.4c: a Unix socket
// returns ECONNREFUSED in the window between the winner's bind() and listen(),
// so a client that treats refusal as "stale, unlink and start" deletes a live
// socket belonging to a daemon that is seconds from serving. Unlinking is safe
// only for the holder of the lock, and requiring the lock as a receiver is how
// that stops being a convention someone can forget.
func (l *Lock) ListenSocket(path string) (net.Listener, error) {
	if l == nil || l.f == nil {
		return nil, errors.New("keeper: refusing to bind the socket without the lock (R3.4c)")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("keeper: stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("keeper: listen: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("keeper: socket mode: %w", err)
	}
	return ln, nil
}

// VersionSkew is the error a client reports on a handshake mismatch. It names
// both versions and the command that fixes it, and keeper never restarts the
// daemon on its own: one window doing so would cancel every other window's
// pending tickets (§3.4).
type VersionSkew struct {
	Daemon string
	Client string
}

func (e *VersionSkew) Error() string {
	return "keeper daemon " + e.Daemon + " does not match client " + e.Client + "; run `keeper daemon restart`"
}

// CheckVersion compares a client's version with the daemon's.
func (d *Daemon) CheckVersion(client string) error {
	if client == "" || client == d.cfg.Version {
		return nil
	}
	return &VersionSkew{Daemon: d.cfg.Version, Client: client}
}
