// Command keeperd is keeper's daemon: the only process that opens the vault, the
// catalog, a database connection or the audit log (SPEC R3.1). Everything else —
// the CLI, each MCP server, the browser — is a thin client of the two listeners
// this binary serves.
//
// It opens the vault before it announces a listener and exits if it cannot, so
// a keeperd that is up is a keeperd that can serve. The alternative is a daemon
// that pretends to be ready and fails on the first query (§3.4).
package daemonapp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/mtchen/keeper/internal/build"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mtchen/keeper/internal/api"
	"github.com/mtchen/keeper/internal/daemon"
)

// version is set with -ldflags "-X main.version=...". The handshake compares it
// with each client's, and a mismatch is an error naming both versions rather
// than an automatic restart: one window restarting the daemon cancels every
// other window's pending tickets (§3.4).
// version is the daemon's build version. It comes from internal/build so that a
// daemon and a CLI from the same build always agree; SPEC §3.4 makes a mismatch
// a hard refusal, and a default that differed per binary made every fresh build
// look like version skew.
var version = build.Version

const (
	defaultUIPort     = 7773
	reauditInterval   = 24 * time.Hour
	freshnessInterval = 5 * time.Minute
	shutdownGrace     = 10 * time.Second

	// A departing daemon holds the lock until its last connection has drained,
	// so a replacement must outwait shutdownGrace before it may conclude that
	// the holder is wedged rather than simply leaving.
	lockHandover = shutdownGrace + 5*time.Second
	lockRetry    = 100 * time.Millisecond
	dialProbe    = 250 * time.Millisecond
)

// Run is the daemon. It returns the process exit code.
//
// It lives here rather than in a cmd/ of its own because keeper ships one
// binary: a daemon and a client from different builds refuse each other
// (SPEC §3.4), and one artefact is how that stops being possible to do by
// accident. It is also one download to verify, for software whose whole
// subject is holding a database credential.
func Run(args []string) int {
	fs := flag.NewFlagSet("keeper daemon serve", flag.ContinueOnError)
	var (
		socketPath = fs.String("socket", env("KEEPER_SOCKET", ""), "unix socket path (default $XDG_RUNTIME_DIR/keeper.sock)")
		uiPort     = fs.Int("ui-port", envInt("KEEPER_UI_PORT", defaultUIPort), "loopback port for the browser UI; 0 picks a free one")
		logLevel   = fs.String("log-level", env("KEEPER_LOG_LEVEL", "info"), "debug, info, warn or error")
		logFormat  = fs.String("log-format", env("KEEPER_LOG_FORMAT", "text"), "text or json")
		printVer   = fs.Bool("version", false, "print the version and exit")
		// One idle timer. A second one locked the vault and left the process up,
		// which only made sense while something could unlock it again; with the
		// key sources all resolving inside this process, a lock it could undo by
		// itself bounded nothing. Exiting still does: the pools close, the socket
		// goes, and the decrypted credentials go with the address space.
		idleExit = fs.Duration("idle-exit", envDur("KEEPER_IDLE_EXIT", 4*time.Hour),
			"stop after this long with nothing attached; negative disables")
	)
	// Its own flag set, parsed from what the subcommand was given. The global
	// one would see os.Args and stop at "daemon", so every flag after the
	// subcommand was silently ignored — which is how --idle-exit appeared to do
	// nothing at all.
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *printVer {
		fmt.Println(version)
		return 0
	}

	logger := newLogger(*logLevel, *logFormat)

	runtimeDir, err := daemon.RuntimeDir()
	if err != nil {
		logger.Error("runtime directory", "err", err)
		return 1
	}
	sock := *socketPath
	if sock == "" {
		sock = daemon.SocketPath(runtimeDir)
	}

	// §3.4's start race: flock elects one daemon and the loser connects to the
	// winner. R3.4c: the socket is unlinked only under this lock, because a unix
	// socket returns ECONNREFUSED between bind() and listen() and a client that
	// treats refusal as "stale" would delete a live one.
	lock, err := electDaemon(daemon.LockPathFor(sock), sock)
	if errors.Is(err, errIncumbentServing) {
		logger.Info("another keeperd is serving this socket; nothing to start")
		return 0
	}
	if err != nil {
		logger.Error("lock", "err", err)
		return 1
	}
	defer lock.Release()

	socketLn, err := lock.ListenSocket(sock)
	if err != nil {
		logger.Error("socket", "err", err)
		return 1
	}
	defer os.Remove(sock)

	// The preferred port first, then any free one. A fixed port is convenient —
	// the URL is the same every day — and it must not be a requirement: a second
	// instance, or anything else already on 7773, would otherwise stop the daemon
	// starting at all. `keeper ui` reads the address back from doctor, so a
	// different port costs nothing.
	uiLn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*uiPort)))
	if err != nil && *uiPort == defaultUIPort {
		logger.Info("preferred UI port is taken; taking any free one", "port", *uiPort)
		uiLn, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		logger.Error("loopback listener", "err", err)
		socketLn.Close()
		return 1
	}
	port := uiLn.Addr().(*net.TCPAddr).Port
	base := "http://127.0.0.1:" + strconv.Itoa(port)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps, auth, closeDeps, err := buildDeps(ctx, logger)
	if err != nil {
		logger.Error("dependencies", "err", err)
		socketLn.Close()
		uiLn.Close()
		return 1
	}
	defer closeDeps()

	stateDir, _ := configDir()
	d, err := daemon.New(daemon.Config{
		Version:  version,
		UIBase:   base,
		Logger:   logger,
		StateDir: stateDir,
		IdleExit: *idleExit,
	}, deps)
	if err == nil {
		// The pipeline and the daemon each need the other; this is the second
		// half of that knot, tied once the daemon exists.
		auth.Bind(d)
	}
	if err != nil {
		logger.Error("daemon", "err", err)
		socketLn.Close()
		uiLn.Close()
		return 1
	}
	defer d.Close()
	d.StartSchedules(reauditInterval, freshnessInterval)

	srv := api.New(d, api.Options{LoopbackPort: port, Logger: logger})

	// ConnContext and ConnState are what bind a session to its connection
	// (R3.4e). Without them a session outlives the client that opened it, and
	// its tickets, reverse map and session grants outlive it too.
	socketSrv := &http.Server{
		Handler:           srv.Socket(),
		ConnContext:       d.ConnContext,
		ConnState:         d.ConnState,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	uiSrv := &http.Server{
		Handler:           srv.Loopback(),
		ConnContext:       d.ConnContext,
		ConnState:         d.ConnState,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	// Open the vault before announcing the listener, and refuse to serve without
	// one.
	//
	// All of §4.3's key sources resolve inside this process — the OS keychain,
	// KEEPER_MASTER_KEY, key.age — and a first-ever start mints a master key
	// into whichever is available. So there is exactly one question here and
	// this process can always answer it, which is why there is no unlock step
	// anywhere in keeper and no locked state for anything to report.
	//
	// This grants no access that did not already exist. These sources resolve
	// with no secret from the operator, so any process running as this user
	// could already obtain the master key; the unlock step this replaces ran the
	// identical resolution and was reachable by anything that could reach the
	// socket. What is gone is a prompt that cost a person an action and an
	// attacker nothing.
	//
	// Failing here is fatal rather than degraded. A keeperd with no vault has no
	// credential for any connection, so every tool it serves would answer with
	// the same error; serving that is worse than not starting, because the
	// operator has to work out which of keeper's parts is broken instead of
	// reading it on the first line of the log.
	keySource, err := d.OpenVault(ctx)
	if err != nil {
		logger.Error("vault", "err", err)
		return 1
	}

	// R4.3: the source in use is a reported fact, and since it is chosen where
	// nobody is watching, this line is the only place it is stated at the moment
	// it is chosen.
	logger.Info("keeperd listening", "version", version, "socket", sock, "ui", base,
		"key source", keySource)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Go(func() {
		if err := socketSrv.Serve(socketLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	})
	wg.Go(func() {
		if err := uiSrv.Serve(uiLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	})

	code := 0
	select {
	case <-d.Shutdown():
		// `keeper daemon restart` asked. It is never automatic: SPEC R3.4d makes
		// a restart cancel every pending ticket and permanently invalidate every
		// token every session holds, so a person chooses it.
		logger.Info("shutdown requested")
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-errs:
		logger.Error("listener", "err", err)
		code = 1
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	_ = socketSrv.Shutdown(shutCtx)
	_ = uiSrv.Shutdown(shutCtx)
	wg.Wait()
	return code
}

// errIncumbentServing reports a start race lost to a daemon that is actually
// serving the socket. It is the expected outcome for the loser, not a failure.
var errIncumbentServing = errors.New("keeper: another daemon is serving the socket")

// electDaemon runs §3.4's start race, with the one thing that the rule "the
// loser connects to the winner" assumes and never states: that the holder of
// the lock is a winner at all.
//
// A departing daemon holds the lock after it has stopped serving. Shutdown
// closes the listeners first and drains open connections second, and the lock
// outlives both, released only when Run returns. Between the first moment and
// the last, the socket refuses while the lock is still held — for the whole of
// shutdownGrace whenever some client is holding a connection open, which an
// attached MCP session or the dashboard's event stream always is.
//
// `keeper daemon restart` lands in that window every time. It waits for the
// socket to stop answering, which takes about a millisecond, and then spawns
// the replacement, which asks for a lock the outgoing process will not let go
// of for another ten seconds. Reading a held lock as proof of a live daemon
// left no daemon running at all: the restart killed the one it was asked to
// replace and then reported that keeperd never became ready.
//
// So a held lock settles the race only while someone is serving behind it.
// When nobody is, the holder is on its way out and the replacement waits for
// it. The wait is bounded — a holder that neither serves nor exits is a bug
// that deserves a message, not a hang.
func electDaemon(lockPath, socketPath string) (*daemon.Lock, error) {
	deadline := time.Now().Add(lockHandover)
	for {
		lock, err := daemon.AcquireLock(lockPath)
		if !errors.Is(err, daemon.ErrDaemonRunning) {
			return lock, err // won the race, or lost to something retrying cannot fix
		}
		if serving(socketPath) {
			return nil, errIncumbentServing
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("keeper: %s is held by a daemon that has not served %s in %s",
				lockPath, socketPath, lockHandover)
		}
		time.Sleep(lockRetry)
	}
}

// serving reports whether anything answers on the socket. It only ever dials:
// R3.4c forbids treating a refusal as evidence that a socket is stale, because
// a winner between bind() and listen() refuses exactly as a corpse does.
func serving(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, dialProbe)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// envDur reads a duration from the environment, falling back to a default.
func envDur(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
