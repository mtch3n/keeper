//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package daemon

import (
	"errors"
	"os"
)

// errWouldBlock keeps AcquireLock's error handling uniform across platforms.
var errWouldBlock = errors.New("keeper: lock held")

// lockFile has no implementation here. Windows needs LockFileEx and a named pipe
// rather than a unix socket, and neither belongs in a stub that would silently
// elect two daemons.
func lockFile(*os.File) error {
	return errors.New("keeper: single-daemon election is not implemented on this platform")
}

func unlockFile(*os.File) error { return nil }
