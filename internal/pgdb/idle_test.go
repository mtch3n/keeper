package pgdb

import (
	"context"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/ports"
)

func Test_LAZY_C6_APoolNobodyUsedIsClosedAndARecentOneStays(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	db, err := New(Config{
		// Pools are built lazily and never dial here: nothing acquires a
		// connection, so the address is never contacted.
		DSN:  func(context.Context, string) (string, error) { return "postgres://u@127.0.0.1:1/db", nil },
		Idle: func() time.Duration { return 10 * time.Minute },
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Shutdown)

	if _, err := db.pool(t.Context(), "old", ports.RoleRead); err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Minute)
	if _, err := db.pool(t.Context(), "recent", ports.RoleRead); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute) // old: 11 minutes idle, recent: 2

	if n := db.CloseIdle(); n != 1 {
		t.Errorf("closed %d pools, want 1", n)
	}
	db.mu.Lock()
	_, oldOpen := db.pools[poolKey{connID: "old", role: ports.RoleRead}]
	_, recentOpen := db.pools[poolKey{connID: "recent", role: ports.RoleRead}]
	db.mu.Unlock()
	if oldOpen || !recentOpen {
		t.Errorf("old open %v, recent open %v; want old closed and recent kept", oldOpen, recentOpen)
	}
}
