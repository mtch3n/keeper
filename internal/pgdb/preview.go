package pgdb

import (
	"context"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// previewChanges runs a write with no RETURNING of its own once more with one
// appended, inside the preview transaction, so the approver sees the rows it
// changes. PostgreSQL 18 reports each row before and after the change; earlier
// servers report it after, or before for a DELETE.
//
// The clause is appended on a new line so a trailing line comment cannot
// swallow it. A statement it does not fit, one ending in a semicolon or a MERGE
// before PostgreSQL 17, fails to parse, and that is the only failure that
// falls back: the statement has not run, so the next form or the plain run
// follows. A statement that parsed and then failed has run, at least in part,
// and running it again another way could repeat its effects, so that error is
// returned, with one exception: a permission refusal comes before the statement
// starts, so it falls back to the count. The result is nil with no error when
// no form fits. It carries no
// rows: the appended columns are the approver's, not output the agent asked for.
func (c *conn) previewChanges(ctx context.Context, sql string, params []ports.Param, op string) (*ports.RawResult, error) {
	clauses := []string{"\nRETURNING *"}
	if serverMajor(c.pg.ParameterStatus("server_version")) >= 18 {
		clauses = []string{"\nRETURNING old.*, new.*", "\nRETURNING *"}
	}
	for _, clause := range clauses {
		if _, err := c.tx.Exec(ctx, "SAVEPOINT keeper_preview"); err != nil {
			return nil, convert(err)
		}
		if _, err := c.pg.Prepare(ctx, "", sql+clause, nil); err != nil {
			if _, err := c.tx.Exec(ctx, "ROLLBACK TO SAVEPOINT keeper_preview"); err != nil {
				return nil, convert(err)
			}
			continue
		}
		r, err := c.fetch(ctx, sql+clause, params, types.PreviewRows)
		if err != nil {
			// A role that cannot read every column is refused RETURNING when
			// the executor starts, before any row changes, so the count-only
			// preview is still safe. Any other error came from a statement that
			// had begun to run.
			if ke, ok := errors.AsType[*types.Error](err); ok && ke.Code == types.CodePermissionDenied {
				if _, err := c.tx.Exec(ctx, "ROLLBACK TO SAVEPOINT keeper_preview"); err != nil {
					return nil, convert(err)
				}
				c.aborted = false
				return nil, nil
			}
			return nil, err
		}
		return &ports.RawResult{
			CommandTag: r.CommandTag,
			Duration:   r.Duration,
			Changes:    changesOf(r, op, strings.Contains(clause, "old.*")),
		}, nil
	}
	return nil, nil
}

// changesOf turns RETURNING rows into changed rows. With paired, each row holds
// the old values then the new ones.
func changesOf(r *ports.RawResult, op string, paired bool) *types.WriteChanges {
	cols := make([]string, len(r.Columns))
	for i, col := range r.Columns {
		cols[i] = col.Name
	}
	n := len(cols)
	if paired {
		n /= 2
	}
	out := &types.WriteChanges{Columns: cols[:n], Rows: make([]types.ChangedRow, 0, len(r.Rows))}
	for _, row := range r.Rows {
		var cr types.ChangedRow
		switch {
		case paired:
			cr.Old, cr.New = cells(row[:n]), cells(row[n:])
			if op == StmtInsert {
				cr.Old = nil
			}
			if op == StmtDelete {
				cr.New = nil
			}
		case op == StmtDelete:
			cr.Old = cells(row)
		default:
			cr.New = cells(row)
		}
		out.Rows = append(out.Rows, cr)
	}
	return out
}

func cells(vals []any) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = cellText(v)
	}
	return out
}

// cellText renders one value for a person to read.
func cellText(v any) string {
	if val, ok := v.(driver.Valuer); ok {
		if dv, err := val.Value(); err == nil {
			v = dv
		}
	}
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return x
	case []byte:
		return `\x` + hex.EncodeToString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(x)
	}
}

// serverMajor reads the major version from server_version, "18.0 (Debian …)".
func serverMajor(version string) int {
	end := strings.IndexFunc(version, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(version)
	}
	n, _ := strconv.Atoi(version[:end])
	return n
}
