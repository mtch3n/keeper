// Command keeperd is keeper's daemon: the only process that opens the vault, the
// catalog, a database connection or the audit log (SPEC R3.1). Everything else —
// the CLI, each MCP server, the browser — is a thin client of the two listeners
// this binary serves.
//
// It starts with the vault locked. Key source 4 is a passphrase and there is no
// TTY on an auto-started daemon, so the alternative is a daemon that pretends to
// be ready and fails on the first query (§3.4).
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
		// Two idle timers, because they give up different things. Locking the
		// vault forgets a decrypted credential and keeps everything else running;
		// exiting gives the whole process back once nothing is attached at all.
		idleLock = fs.Duration("vault-idle-lock", envDur("KEEPER_VAULT_IDLE_LOCK", time.Hour),
			"lock the vault after this long with no request; negative disables")
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
	lock, err := daemon.AcquireLock(daemon.LockPathFor(sock))
	if errors.Is(err, daemon.ErrDaemonRunning) {
		logger.Info("another keeperd already holds the lock; nothing to start")
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
		Version:       version,
		UIBase:        base,
		Logger:        logger,
		StateDir:      stateDir,
		VaultIdleLock: *idleLock,
		IdleExit:      *idleExit,
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

	logger.Info("keeperd listening", "version", version, "socket", sock, "ui", base, "vault", "locked")

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
