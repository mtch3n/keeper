//go:build integration

package integration

import (
	"context"
	"slices"
	"testing"

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
