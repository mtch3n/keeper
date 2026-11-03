package daemon

import (
	"net"
	"strings"
	"testing"
	"time"
)

// bare builds a Daemon with state and no collaborators, for the rules that are
// pure state: the event hub and the wait ceiling.
func bare(t *testing.T) *Daemon {
	t.Helper()
	cfg := Config{Version: "test"}
	cfg.withDefaults()
	return &Daemon{
		cfg:      cfg,
		hub:      newHub(),
		sessions: map[string]*Session{},
		byConn:   map[net.Conn]*Session{},
		tickets:  map[string]*ticket{},
		requests: map[string]*localRequest{},
	}
}

func TestWaitCeilingIs25Seconds(t *testing.T) {
	d := bare(t)
	if got := d.MaxWait(); got != 25*time.Second {
		t.Fatalf("MaxWait = %v, want 25s", got)
	}
}

// §3.4: a version mismatch is an error naming both versions, never a restart.
func TestVersionSkewNamesBothVersions(t *testing.T) {
	d := bare(t)
	if err := d.CheckVersion("test"); err != nil {
		t.Fatalf("matching versions rejected: %v", err)
	}
	if err := d.CheckVersion(""); err != nil {
		t.Fatalf("a client that did not say was rejected: %v", err)
	}
	err := d.CheckVersion("0.9")
	if err == nil {
		t.Fatal("a mismatch was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"test", "0.9", "keeper daemon restart"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("%q does not mention %q", msg, want)
		}
	}
}

// The hub drops for a subscriber that stopped reading rather than stalling the
// daemon: an SSE client must not be able to block a query.
func TestHubDropsForASlowSubscriber(t *testing.T) {
	h := newHub()
	ch, release := h.Subscribe()
	defer release()

	for i := range subscriberBuffer * 4 {
		h.Publish(Event{Type: EventSession, Data: i})
	}
	if got := len(ch); got != subscriberBuffer {
		t.Fatalf("buffered %d events, want the cap of %d", got, subscriberBuffer)
	}

	// Releasing twice is safe and a released subscriber stops receiving.
	release()
	h.Publish(Event{Type: EventSession})
}

// R9.2: a bidirectional override renders visibly rather than reordering the
// statement a human is about to approve.
func TestDisplayCopyNeutralisesBidiOverrides(t *testing.T) {
	in := "SELECT id FROM users -- \u202e harmless"
	got := forDisplay(in)
	if strings.ContainsRune(got, 0x202E) {
		t.Fatalf("an override survived into the display copy: %q", got)
	}
	if !strings.Contains(got, "[U+202E]") {
		t.Fatalf("the override is not visible: %q", got)
	}
	if plain := "SELECT 1"; forDisplay(plain) != plain {
		t.Fatal("an ordinary statement was rewritten")
	}
}
