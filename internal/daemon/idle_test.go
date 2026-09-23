package daemon

import (
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// idleDaemon is bare() plus the fields the idle path needs.
func idleDaemon(t *testing.T, now time.Time) *Daemon {
	t.Helper()
	d := bare(t)
	d.shutdown = make(chan struct{})
	d.queue = nil
	d.cfg.Now = func() time.Time { return now }
	d.cfg.IdleExit = time.Minute
	d.lastActivity.Store(now.UnixNano())
	return d
}

// A daemon nobody has touched is the most eligible thing there is to stop. The
// first version treated "never touched" as "not idle" and ran forever.
func TestIdleExitStopsAnUntouchedDaemon(t *testing.T) {
	start := time.Now()
	d := idleDaemon(t, start)

	d.exitIfIdle()
	select {
	case <-d.Shutdown():
		t.Fatal("stopped while still inside the idle window")
	default:
	}

	d.cfg.Now = func() time.Time { return start.Add(2 * time.Minute) }
	d.exitIfIdle()
	select {
	case <-d.Shutdown():
	default:
		t.Fatal("did not stop after the idle window with nothing attached")
	}
}

// A queued approval means a human is expected. Stopping under them would turn
// their click into an error.
func TestIdleExitWaitsForAQueuedApproval(t *testing.T) {
	start := time.Now()
	d := idleDaemon(t, start)
	d.queue = []*queueItem{{}}
	d.cfg.Now = func() time.Time { return start.Add(2 * time.Minute) }

	d.exitIfIdle()
	select {
	case <-d.Shutdown():
		t.Fatal("stopped with an approval waiting in the queue")
	default:
	}
}

// An open dashboard holds an event subscription. It is the difference between
// "nobody is here" and "nobody has clicked".
func TestIdleExitWaitsForAnOpenDashboard(t *testing.T) {
	start := time.Now()
	d := idleDaemon(t, start)
	_, cancel := d.hub.Subscribe()
	d.cfg.Now = func() time.Time { return start.Add(2 * time.Minute) }

	d.exitIfIdle()
	select {
	case <-d.Shutdown():
		t.Fatal("stopped while an event stream was open")
	default:
	}

	cancel()
	d.exitIfIdle()
	select {
	case <-d.Shutdown():
	default:
		t.Fatal("did not stop once the last subscriber went")
	}
}

// Touch is what separates the two: any request restarts the clock.
func TestTouchRestartsTheIdleClock(t *testing.T) {
	start := time.Now()
	d := idleDaemon(t, start)

	d.cfg.Now = func() time.Time { return start.Add(2 * time.Minute) }
	d.Touch()
	d.exitIfIdle()
	select {
	case <-d.Shutdown():
		t.Fatal("stopped immediately after a request")
	default:
	}
}

var _ = types.GrantStanding
