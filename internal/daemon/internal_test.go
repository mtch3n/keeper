package daemon

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/types"
)

// bare builds a Daemon with state and no collaborators, for the rules that are
// pure state: grant matching, the event hub and the wait ceiling.
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
		grants:   map[string]*types.Grant{},
		requests: map[string]*localRequest{},
	}
}

func rel(schema, name string) types.RelationRef {
	return types.RelationRef{Schema: schema, Relation: name}
}

// R9.3c: a path grants exactly itself. A column is its own path, a schema does
// not cover its relations, a view over users is its own path and so is every
// base table its plan expands to, and a different connection is a different path.
func TestGrantPathsDoNotInherit(t *testing.T) {
	d := bare(t)
	users := rel("public", "users")
	if _, err := d.CreateGrant("", types.PathRef{ConnectionID: "prod", Relation: users},
		GrantRequest{Lifetime: types.GrantStanding, RowCeiling: 100}, "ming"); err != nil {
		t.Fatalf("grant: %v", err)
	}

	cases := []struct {
		name  string
		conn  string
		rels  []types.RelationRef
		match bool
	}{
		{"the path itself", "prod", []types.RelationRef{users}, true},
		{"a view over it", "prod", []types.RelationRef{rel("public", "user_stats")}, false},
		{"a base table beside it", "prod", []types.RelationRef{users, rel("public", "orders")}, false},
		{"the schema it lives in", "prod", []types.RelationRef{rel("public", "")}, false},
		{"a wildcard relation", "prod", []types.RelationRef{rel("public", "*")}, false},
		{"another connection", "staging", []types.RelationRef{users}, false},
		{"another schema", "prod", []types.RelationRef{rel("private", "users")}, false},
		{"no relations at all", "prod", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := d.matchGrants("s1", tc.conn, tc.rels, 1)
			if ok != tc.match {
				t.Fatalf("match = %v, want %v", ok, tc.match)
			}
		})
	}
}

// R9.3b: a schema change to a relation in scope suspends the rule pending
// review, and a suspended rule stops matching immediately.
func TestSuspendedGrantStopsMatching(t *testing.T) {
	d := bare(t)
	users := rel("public", "users")
	g, err := d.CreateGrant("", types.PathRef{ConnectionID: "prod", Relation: users},
		GrantRequest{Lifetime: types.GrantStanding, RowCeiling: 100}, "ming")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.matchGrants("s1", "prod", []types.RelationRef{users}, 1); !ok {
		t.Fatal("grant did not match before suspension")
	}
	if n := d.SuspendGrantsForRelation("prod", users, "users gained a column"); n != 1 {
		t.Fatalf("suspended %d grants, want 1", n)
	}
	if _, ok := d.matchGrants("s1", "prod", []types.RelationRef{users}, 1); ok {
		t.Fatal("a suspended grant kept firing")
	}
	if list := d.Grants(); len(list) != 1 || !list[0].Suspended || list[0].Reason == "" {
		t.Fatalf("suspension not visible in the list: %+v", list)
	}
	_ = g
}

// R9.3d: a session grant belongs to its session and is invisible to any other.
func TestSessionGrantsAreScopedAndSortFirst(t *testing.T) {
	d := bare(t)
	d.sessions["s1"] = &Session{id: "s1"}
	users := rel("public", "users")
	orders := rel("public", "orders")

	if _, err := d.CreateGrant("s1", types.PathRef{ConnectionID: "prod", Relation: users},
		GrantRequest{Lifetime: types.GrantSession, RowCeiling: 100}, "ming"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateGrant("", types.PathRef{ConnectionID: "prod", Relation: orders},
		GrantRequest{Lifetime: types.GrantStanding, RowCeiling: 100}, "ming"); err != nil {
		t.Fatal(err)
	}

	if _, ok := d.matchGrants("s1", "prod", []types.RelationRef{users}, 1); !ok {
		t.Fatal("the owning session could not use its own grant")
	}
	if _, ok := d.matchGrants("s2", "prod", []types.RelationRef{users}, 1); ok {
		t.Fatal("another session used a session-scoped grant")
	}

	list := d.Grants()
	if len(list) != 2 || list[0].Lifetime != types.GrantSession {
		t.Fatalf("session grants do not sort first: %+v", list)
	}
	if list[0].ExpiresAt.IsZero() != true {
		t.Fatal("a session grant was given an expiry; it dies with the session")
	}
	if list[1].ExpiresAt.IsZero() {
		t.Fatal("a standing grant was stored without an expiry")
	}
}

// A standing grant that has expired stops matching without anyone sweeping it.
func TestExpiredGrantStopsMatching(t *testing.T) {
	d := bare(t)
	users := rel("public", "users")
	g, err := d.CreateGrant("", types.PathRef{ConnectionID: "prod", Relation: users},
		GrantRequest{Lifetime: types.GrantStanding, RowCeiling: 100}, "ming")
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.grants[g.ID].ExpiresAt = time.Now().Add(-time.Minute)
	d.mu.Unlock()
	if _, ok := d.matchGrants("s1", "prod", []types.RelationRef{users}, 1); ok {
		t.Fatal("an expired grant kept firing")
	}
}

// The narrowest ceiling among the matched grants is the one that applies.
func TestMatchUsesTheNarrowestCeiling(t *testing.T) {
	d := bare(t)
	users, orders := rel("public", "users"), rel("public", "orders")
	for r, ceiling := range map[types.RelationRef]int{users: 1000, orders: 50} {
		if _, err := d.CreateGrant("", types.PathRef{ConnectionID: "prod", Relation: r},
			GrantRequest{Lifetime: types.GrantStanding, RowCeiling: ceiling}, "ming"); err != nil {
			t.Fatal(err)
		}
	}
	m, ok := d.matchGrants("s1", "prod", []types.RelationRef{users, orders}, 10)
	if !ok {
		t.Fatal("both relations are granted and it did not match")
	}
	if m.RowCeiling != 50 {
		t.Fatalf("ceiling %d, want the narrower 50", m.RowCeiling)
	}
	if _, ok := d.matchGrants("s1", "prod", []types.RelationRef{users, orders}, 500); ok {
		t.Fatal("a plan above the narrower ceiling matched")
	}
}

// §3.2: wait_ms is capped so a harness never times out a tool call.
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
