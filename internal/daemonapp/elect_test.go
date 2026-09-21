package daemonapp

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/daemon"
)

// A daemon that is shutting down holds the lock for the whole of shutdownGrace
// after its socket has stopped answering, and `keeper daemon restart` spawns the
// replacement inside that window every time. The replacement must wait for the
// handover instead of bowing out, or the restart leaves no daemon running at
// all — it killed the one it was asked to replace and reported that keeperd
// never became ready.
func TestElectWaitsOutADepartingDaemon(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "keeper.sock.lock")
	sockPath := filepath.Join(dir, "keeper.sock")

	// The outgoing daemon: holding the lock, serving nothing.
	departing, err := daemon.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}

	elected := make(chan *daemon.Lock, 1)
	failed := make(chan error, 1)
	go func() {
		lock, err := electDaemon(lockPath, sockPath)
		if err != nil {
			failed <- err
			return
		}
		elected <- lock
	}()

	// It must not conclude anything while the lock is still held.
	select {
	case err := <-failed:
		t.Fatalf("gave up on a lock that was about to be released: %v", err)
	case <-elected:
		t.Fatal("took a lock that was still held")
	case <-time.After(300 * time.Millisecond):
	}

	if err := departing.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	select {
	case lock := <-elected:
		lock.Release()
	case err := <-failed:
		t.Fatalf("did not take the lock after the handover: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("never took the lock after the departing daemon released it")
	}
}

// The other half of §3.4: when the lock holder really is serving, the loser
// leaves it alone rather than waiting out a daemon that is not going anywhere.
func TestElectYieldsToAServingDaemon(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "keeper.sock.lock")
	sockPath := filepath.Join(dir, "keeper.sock")

	incumbent, err := daemon.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}
	defer incumbent.Release()

	ln, err := incumbent.ListenSocket(sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	start := time.Now()
	if _, err := electDaemon(lockPath, sockPath); !errors.Is(err, errIncumbentServing) {
		t.Fatalf("started a second daemon beside a live one: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waited %s before yielding to a daemon that was already serving", elapsed)
	}
}

// An uncontested start is the common case and must not pay for the other two.
func TestElectTakesAFreeLockAtOnce(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "keeper.sock.lock")

	lock, err := electDaemon(lockPath, filepath.Join(dir, "keeper.sock"))
	if err != nil {
		t.Fatalf("free lock: %v", err)
	}
	lock.Release()
}
