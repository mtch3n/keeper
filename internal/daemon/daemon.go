// Package daemon owns every piece of keeper's mutable state: sessions bound to
// socket connections, escalation tickets, the shared approval queue, grants and
// local input/authorization requests.
//
// SPEC §3.1 makes keeperd the only stateful process, so this package is where
// concurrency lives. One mutex guards the whole state map. That is deliberate:
// closing a connection has to drop a session, its tickets, its queue items, its
// session-scoped grants and its pending local requests atomically, and finer
// locking buys nothing at this scale but a chance to get that wrong. No port is
// ever called while the mutex is held.
//
// Nothing here writes HTTP. internal/api is the only package that turns these
// values into responses.
package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Deps are the collaborators the daemon wires together. Every one is an
// interface so the daemon is testable without a database, a vault or a model.
type Deps struct {
	Vault        ports.Vault
	Auditor      ports.Auditor
	Catalogs     ports.CatalogFor
	CatalogStore ports.CatalogStore
	Executor     ports.Executor
	Redactor     ports.Redactor
	Audit        ports.AuditLog
	Judge        ports.Judge
	Pipeline     Pipeline
	// Detector is optional. It is the pipeline's collaborator, not the daemon's;
	// the daemon holds it only so doctor can report what examined the data and
	// whether it could reach the network (SPEC R8.5g).
	Detector ports.Detector
}

// Config is the daemon's tuning. Everything has a default; New fills the zeroes.
type Config struct {
	// Version is the daemon binary's version, compared against a client's at the
	// handshake. SPEC §3.4 version skew.
	Version string
	// UIBase is the loopback origin local request URLs are built from,
	// "http://127.0.0.1:7773".
	UIBase string
	// RequestTTL is how long a local request stays open. SPEC R8.7c: initial
	// default 10 minutes.
	RequestTTL time.Duration
	// TicketTTL bounds a ticket that nobody ever decides. Tickets also expire
	// with their session (R3.4b); this only stops a forgotten one accumulating.
	TicketTTL time.Duration
	// MaxWait caps wait_ms on every polling endpoint. SPEC §3.2: 25000 ms.
	MaxWait time.Duration
	// StandingGrantTTL and WriteGrantTTL are R9.3d's 30 days and R4.2f's 7.
	StandingGrantTTL time.Duration
	WriteGrantTTL    time.Duration
	// ExecuteTimeout bounds the background execution of an approved ticket.
	ExecuteTimeout time.Duration
	// Sweep is how often expired tickets and requests are reaped.
	Sweep time.Duration
	// StateDir is where the daemon keeps what must outlive it — currently the
	// standing grants. Empty disables persistence, which is what tests want.
	StateDir string
	// IdleExit is how long the daemon runs with nothing attached before it
	// stops. Zero uses the default; negative disables it.
	//
	// "Nothing attached" is strict: no sessions, no queued approvals, no open
	// local requests, and no live event subscribers — an open dashboard holds a
	// subscription, so a browser tab left on the approvals screen keeps the
	// daemon up. It has to, because a tab cannot start one again and a dead page
	// with no way to revive it is worse than a daemon that idled.
	//
	// An agent has no such problem: its client spawns the daemon when the socket
	// is gone, which is the same path a first run takes.
	IdleExit time.Duration
	// VaultIdleLock is how long the daemon goes without a request before it
	// locks the vault again. Zero uses the default; a negative value disables
	// it, which is a choice somebody makes rather than one they drift into.
	//
	// It locks rather than exits. Exiting would take the UI, the approval queue
	// and every client's socket with it; locking gives up only the thing worth
	// bounding, which is a decrypted credential sitting in memory for as long as
	// the machine is on.
	VaultIdleLock time.Duration

	Now    func() time.Time
	Logger *slog.Logger
}

func (c *Config) withDefaults() {
	if c.Version == "" {
		c.Version = "dev"
	}
	if c.RequestTTL <= 0 {
		c.RequestTTL = 10 * time.Minute
	}
	if c.TicketTTL <= 0 {
		c.TicketTTL = time.Hour
	}
	if c.MaxWait <= 0 {
		c.MaxWait = 25 * time.Second
	}
	if c.StandingGrantTTL <= 0 {
		c.StandingGrantTTL = 30 * 24 * time.Hour
	}
	if c.WriteGrantTTL <= 0 {
		c.WriteGrantTTL = 7 * 24 * time.Hour
	}
	if c.ExecuteTimeout <= 0 {
		c.ExecuteTimeout = 5 * time.Minute
	}
	if c.Sweep <= 0 {
		c.Sweep = 15 * time.Second
	}
	if c.VaultIdleLock == 0 {
		c.VaultIdleLock = time.Hour
	}
	if c.IdleExit == 0 {
		c.IdleExit = 4 * time.Hour
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
}

// Daemon is keeper's state. It is safe for concurrent use.
type Daemon struct {
	grantsDirty  atomic.Bool
	lastActivity atomic.Int64
	shutdown     chan struct{}
	shutdownOnce sync.Once
	cfg          Config
	deps         Deps
	hub          *Hub

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	sessions map[string]*Session
	byConn   map[net.Conn]*Session
	tickets  map[string]*ticket
	queue    []*queueItem
	grants   map[string]*types.Grant
	requests map[string]*localRequest
}

// New wires the daemon. It does not unlock the vault: SPEC §3.4 starts locked,
// because key source 4 cannot work for an auto-started daemon with no TTY.
func New(cfg Config, deps Deps) (*Daemon, error) {
	switch {
	case deps.Vault == nil:
		return nil, errors.New("daemon: Vault is required")
	case deps.Auditor == nil:
		return nil, errors.New("daemon: Auditor is required")
	case deps.Catalogs == nil:
		return nil, errors.New("daemon: Catalog is required")
	case deps.CatalogStore == nil:
		return nil, errors.New("daemon: CatalogStore is required")
	case deps.Executor == nil:
		return nil, errors.New("daemon: Executor is required")
	case deps.Redactor == nil:
		return nil, errors.New("daemon: Redactor is required")
	case deps.Audit == nil:
		return nil, errors.New("daemon: AuditLog is required")
	case deps.Pipeline == nil:
		return nil, errors.New("daemon: Pipeline is required")
	}
	cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		shutdown: make(chan struct{}),
		cfg:      cfg,
		deps:     deps,
		hub:      newHub(),
		ctx:      ctx,
		cancel:   cancel,
		sessions: map[string]*Session{},
		byConn:   map[net.Conn]*Session{},
		tickets:  map[string]*ticket{},
		grants:   map[string]*types.Grant{},
		requests: map[string]*localRequest{},
	}

	// The clock starts at startup, not at the first request. A daemon nobody ever
	// connected to is the most eligible thing there is to stop, and treating
	// "never touched" as "not idle" had it running forever.
	d.lastActivity.Store(d.now().UnixNano())

	// Standing grants outlive the process that issued them (R9.3d).
	d.loadGrants()
	d.wg.Go(d.sweep)
	return d, nil
}

// Version is the daemon's own version, for the handshake.
func (d *Daemon) Version() string { return d.cfg.Version }

// MaxWait is the ceiling every wait_ms parameter is clamped to.
func (d *Daemon) MaxWait() time.Duration { return d.cfg.MaxWait }

// Events is the SSE hub.
func (d *Daemon) Events() *Hub { return d.hub }

func (d *Daemon) now() time.Time { return d.cfg.Now() }

// Close stops the daemon's background work and drops every session. It does not
// close the listeners; internal/api owns those.
func (d *Daemon) Close() error {
	d.cancel()
	d.wg.Wait()
	d.mu.Lock()
	conns := make([]net.Conn, 0, len(d.byConn))
	for c := range d.byConn {
		conns = append(conns, c)
	}
	d.mu.Unlock()
	for _, c := range conns {
		d.CloseConn(c)
	}
	d.hub.close()
	return nil
}

// sweep expires tickets and local requests on a timer.
func (d *Daemon) sweep() {
	t := time.NewTicker(d.cfg.Sweep)
	defer t.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-t.C:
			d.expire()
			d.flushGrants()
			d.lockIfIdle()
			d.exitIfIdle()
		}
	}
}

func (d *Daemon) expire() {
	now := d.now()
	var events []Event
	d.mu.Lock()
	for id, tk := range d.tickets {
		if tk.t.State.Terminal() {
			if now.Sub(tk.settledAt) > d.cfg.TicketTTL {
				delete(d.tickets, id)
			}
			continue
		}
		if now.Sub(tk.t.CreatedAt) > d.cfg.TicketTTL {
			d.setTicketStateLocked(tk, types.TicketExpired, nil, nil)
			events = append(events, d.dequeueLocked(id, "expired")...)
		}
	}
	for id, r := range d.requests {
		if r.r.State != stateReady && r.r.State != statePending {
			continue
		}
		if r.r.State == statePending && now.After(r.r.ExpiresAt) {
			r.r.State = stateExpired
			r.notify()
			events = append(events, Event{Type: EventRequest, Data: RequestEvent{Action: "expired", Kind: r.r.Kind, State: r.r.State}})
		}
		if r.r.State != statePending && now.Sub(r.settledAt) > d.cfg.RequestTTL {
			delete(d.requests, id)
		}
	}
	for id, g := range d.grants {
		if !g.ExpiresAt.IsZero() && now.After(g.ExpiresAt) {
			delete(d.grants, id)
			events = append(events, Event{Type: EventApproval, Data: GrantEvent{Action: "expired", GrantID: id}})
		}
	}
	d.mu.Unlock()
	for _, e := range events {
		d.hub.Publish(e)
	}
}

// RequestShutdown asks the process to stop. It is what `keeper daemon restart`
// reaches, and it is deliberately explicit rather than automatic: SPEC §3.4
// forbids a client restarting the daemon on version skew, because one window
// doing so would cancel every other window's pending tickets and permanently
// invalidate every token those sessions hold (R3.4d).
func (d *Daemon) RequestShutdown() {
	d.shutdownOnce.Do(func() { close(d.shutdown) })
}

// Shutdown is closed when RequestShutdown is called. main selects on it.
func (d *Daemon) Shutdown() <-chan struct{} { return d.shutdown }

// Touch records that something happened. Every request passes through it, so
// "idle" means nobody asked keeper for anything — not that no agent is
// connected. A human reading the activity log with no agent running is using
// keeper, and locking the vault under them would be wrong.
func (d *Daemon) Touch() { d.lastActivity.Store(d.now().UnixNano()) }

// idleFor reports how long it has been since the last request.
func (d *Daemon) idleFor() time.Duration {
	return d.now().Sub(time.Unix(0, d.lastActivity.Load()))
}

// lockIfIdle is the other half of Touch, run on the sweep.
//
// Nothing pending may be dropped on the floor: a queued approval means a human
// is expected, and locking under them would turn their click into an error.
func (d *Daemon) lockIfIdle() {
	if d.cfg.VaultIdleLock < 0 || d.deps.Vault.Locked() {
		return
	}
	if d.idleFor() < d.cfg.VaultIdleLock {
		return
	}

	d.mu.Lock()
	busy := len(d.queue) > 0 || len(d.requests) > 0
	d.mu.Unlock()
	if busy {
		return
	}

	d.deps.Vault.Lock()
	d.cfg.Logger.Info("vault locked after idle", "idle", d.idleFor().Round(time.Second))
	d.hub.Publish(Event{Type: EventConnection, Data: map[string]any{"action": "vault_locked", "reason": "idle"}})
}

// attached reports whether anything is using the daemon. Callers hold no lock.
func (d *Daemon) attached() bool {
	d.mu.Lock()
	n := len(d.sessions) + len(d.queue) + len(d.requests)
	d.mu.Unlock()
	return n > 0 || d.hub.Subscribers() > 0
}

// exitIfIdle stops the daemon when nothing has been attached to it for a while.
//
// Stopping is better than idling for the case it covers: the pools close, the
// memory goes back, the socket is removed, and there is nothing left to reach.
// It is only safe because the standing grants are on disk and the vault is
// already locked by the time this fires — everything else the daemon holds is
// scoped to a session that is gone.
func (d *Daemon) exitIfIdle() {
	if d.cfg.IdleExit < 0 || d.attached() {
		return
	}
	if d.idleFor() < d.cfg.IdleExit {
		return
	}
	d.cfg.Logger.Info("stopping after idle", "idle", d.idleFor().Round(time.Second))
	d.RequestShutdown()
}
