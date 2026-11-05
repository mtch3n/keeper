package daemonapp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/mtchen/keeper/internal/api"
	"github.com/mtchen/keeper/internal/daemon"
)

// uiControl binds and releases the loopback listener as the dashboard setting
// changes.
//
// The preferred port comes first, then the one used last, then any free one. A
// fixed port is convenient, so the address is the same every day, and it must
// not be a requirement: a second instance, or anything else on 7773, would
// otherwise stop the daemon starting. `keeper ui` reads the address back from
// the daemon, so a different port costs nothing.
type uiControl struct {
	want   int
	errs   chan<- error
	logger *slog.Logger
	api    *api.Server
	d      *daemon.Daemon

	mu      sync.Mutex
	srv     *http.Server
	ln      net.Listener
	retired map[*http.Server]bool
}

func (c *uiControl) Start() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.want)))
	if err != nil && c.want != 0 {
		c.logger.Info("preferred UI port is taken; taking any free one", "port", c.want)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return "", err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	c.want = port
	c.api.SetLoopbackPort(port)
	c.ln = ln
	c.srv = &http.Server{
		Handler:           c.api.Loopback(),
		ConnContext:       c.d.ConnContext,
		ConnState:         c.d.ConnState,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(c.logger.Handler(), slog.LevelWarn),
	}
	go func(srv *http.Server) {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !c.isRetired(srv) {
			c.errs <- err
		}
	}(c.srv)
	return "http://127.0.0.1:" + strconv.Itoa(port), nil
}

func (c *uiControl) isRetired(srv *http.Server) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retired[srv]
}

// Stop frees the port at once and gives a request in flight a moment to get its
// answer before every open page and event stream is cut.
func (c *uiControl) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.srv == nil {
		return
	}
	srv := c.srv
	if c.retired == nil {
		c.retired = map[*http.Server]bool{}
	}
	c.retired[srv] = true
	_ = c.ln.Close()
	c.srv, c.ln = nil, nil
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = srv.Close()
	}()
}
