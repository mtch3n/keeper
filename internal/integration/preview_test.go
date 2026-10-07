//go:build integration

package integration

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/pgdb"
)

// TestAWritePreviewReportsTheRowsItChanges establishes that the approval
// preview of WRITE-D4 comes from the server: the rows a write changes, after
// the change, and before it on PostgreSQL 18. If it fails, the Inbox shows a
// count and no values, and an approver decides blind again.
func TestAWritePreviewReportsTheRowsItChanges(t *testing.T) {
	for _, tc := range []struct {
		image   string
		wantOld bool
	}{{"postgres:16-alpine", false}, {"postgres:18-alpine", true}} {
		t.Run(tc.image, func(t *testing.T) {
			f := NewOn(t, tc.image)
			exec, err := pgdb.New(pgdb.Config{DSN: func(context.Context, string) (string, error) { return f.WriteDSN, nil }})
			if err != nil {
				t.Fatalf("pgdb.New: %v", err)
			}
			t.Cleanup(exec.Shutdown)
			ctx := t.Context()

			res, err := exec.PreviewWrite(ctx, "c", "UPDATE orders SET status = 'cancelled' -- close both", nil)
			if err != nil {
				t.Fatalf("preview: %v", err)
			}
			if res.CommandTag != 2 || res.Changes == nil || len(res.Changes.Rows) != 2 {
				t.Fatalf("preview = tag %d, changes %+v; want 2 changed rows", res.CommandTag, res.Changes)
			}
			status := slices.Index(res.Changes.Columns, "status")
			for _, row := range res.Changes.Rows {
				if row.New[status] != "cancelled" {
					t.Errorf("new values = %v, want status cancelled", row.New)
				}
				if tc.wantOld != (row.Old != nil) {
					t.Errorf("old values = %v, want them only on 18", row.Old)
				} else if tc.wantOld && row.Old[status] == "cancelled" {
					t.Errorf("old values = %v, want the status before the change", row.Old)
				}
			}
			if len(res.Rows) != 0 {
				t.Errorf("the appended RETURNING leaked into the agent's rows: %v", res.Rows)
			}

			// A statement the clause cannot follow is previewed by count alone.
			res, err = exec.PreviewWrite(ctx, "c", "DELETE FROM orders WHERE status = 'pending';", nil)
			if err != nil || res.CommandTag != 1 || res.Changes != nil {
				t.Fatalf("fallback preview = %+v, %v; want a count of 1 and no rows", res, err)
			}

			// And the previews rolled back.
			got, err := exec.Run(ctx, "c", "SELECT count(*) FROM orders WHERE status <> 'cancelled'", nil, 1)
			if err != nil || got.Rows[0][0].(int64) != 2 {
				t.Fatalf("a preview committed: %v %v", got, err)
			}
		})
	}
}

// TestThePlanNamesTheColumnsAStatementFiltersOn establishes WRITE-D6's input:
// the columns a statement filters and joins on, resolved to catalog identities
// from the server's own plan. If it fails, the Inbox stops flagging a filter on
// an SSN, which discloses as much as selecting it.
func TestThePlanNamesTheColumnsAStatementFiltersOn(t *testing.T) {
	f := New(t)
	exec, err := pgdb.New(pgdb.Config{DSN: func(context.Context, string) (string, error) { return f.ReadDSN, nil }})
	if err != nil {
		t.Fatalf("pgdb.New: %v", err)
	}
	t.Cleanup(exec.Shutdown)

	plan, err := exec.Plan(t.Context(), "c", `SELECT u.id FROM users u JOIN orders o ON o.user_email = u.email WHERE u.ssn = 'users.notes' AND lower(o.status) = 'x'`, nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var got []string
	for _, c := range plan.Filters {
		if c.TableOID == 0 || c.AttNum == 0 {
			t.Errorf("unresolved filter column %+v", c)
		}
		got = append(got, c.Relation.Relation+"."+c.Column)
	}
	slices.Sort(got)
	want := []string{"orders.status", "orders.user_email", "users.email", "users.ssn"}
	if !slices.Equal(got, want) {
		t.Errorf("filter columns = %v, want %v", got, want)
	}
}

// TestLAZY_C7 is in the integration suite because it needs a server: a pool
// closed for being idle must reconnect on the next statement, or closing idle
// connections would break the first query after every quiet spell.
func Test_LAZY_C7_AStatementAfterAnIdleCloseReconnects(t *testing.T) {
	f := New(t)
	now := time.Now()
	exec, err := pgdb.New(pgdb.Config{
		DSN:  func(context.Context, string) (string, error) { return f.ReadDSN, nil },
		Idle: func() time.Duration { return time.Minute },
		Now:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("pgdb.New: %v", err)
	}
	t.Cleanup(exec.Shutdown)

	if _, err := exec.Run(t.Context(), "c", "SELECT 1", nil, 1); err != nil {
		t.Fatalf("first statement: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if n := exec.CloseIdle(); n == 0 {
		t.Fatal("the idle pool was not closed")
	}
	res, err := exec.Run(t.Context(), "c", "SELECT 2", nil, 1)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("statement after the idle close: %v %v", res, err)
	}
}
