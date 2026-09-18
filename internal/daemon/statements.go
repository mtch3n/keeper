package daemon

import (
	"context"
	"slices"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Param is one bound parameter as it arrives from an agent: {"value": ...} or
// {"token": "..."}. §6.2.
type Param struct {
	Value any
	Token string
}

// writeStatements are the statement types G2 routes to the write credential.
// A statement is a write because the plan says it is, not because the agent said
// so (§6.1), so this reads the plan's own statement type.
var writeStatements = []string{"INSERT", "UPDATE", "DELETE", "MERGE"}

func isWrite(statementType string) bool { return slices.Contains(writeStatements, statementType) }

// resolveParams turns token parameters into bound values against the calling
// session's reverse map. keeper never substitutes a resolved value into
// statement text (R6.2): a token becomes a bound parameter or the call fails.
func (d *Daemon) resolveParams(ctx context.Context, sessionID string, params []Param) ([]ports.Param, error) {
	out := make([]ports.Param, 0, len(params))
	for _, p := range params {
		if p.Token == "" {
			out = append(out, ports.Param{Value: p.Value})
			continue
		}
		v, _, err := d.deps.Redactor.Resolve(ctx, sessionID, p.Token)
		if err != nil {
			// R3.4d: the reverse map is memory only, so an unresolvable token is
			// not "retry" but "every token you hold is dead, re-run the queries
			// that minted them".
			return nil, errStaleToken
		}
		out = append(out, ports.Param{Value: v})
	}
	return out, nil
}

// usableConnection is the admission check every statement passes: the vault is
// open, the connection exists, and no privilege-audit finding is waiting for a
// human (R4.1).
func (d *Daemon) usableConnection(ctx context.Context, id string) (*types.Connection, error) {
	if d.deps.Vault.Locked() {
		return nil, errVaultLocked
	}
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	if !c.Enabled || len(c.Unaccepted()) > 0 {
		return nil, errConnectionDisabled(c.Name)
	}
	return c, nil
}

// Explain is §6.1's dry run: what would be masked and whether the statement
// would escalate, without spending an approval.
func (d *Daemon) Explain(ctx context.Context, s *Session, connID, sql string, params []Param) (*types.ExplainResult, error) {
	if _, err := d.usableConnection(ctx, connID); err != nil {
		return nil, err
	}
	bound, err := d.resolveParams(ctx, s.ID(), params)
	if err != nil {
		return nil, err
	}
	return d.deps.Pipeline.Explain(ctx, s.Info(), connID, sql, bound)
}

// Query runs a statement or escalates it. Exactly one of the results is non-nil
// on success.
//
// The escalation path is where grants act. §9.3 says an allow rule "lowers the
// tier of matching future statements", so the daemon does not pre-authorize:
// it lets the pipeline decide the tier, and only when the pipeline escalates
// does it ask whether every relation the plan touched already has its own grant
// (R9.3c). If it does, the statement runs again with the grant named as its
// authorization basis. If one relation is unlisted, the statement goes to the
// queue at its normal tier.
func (d *Daemon) Query(ctx context.Context, s *Session, connID, sql string, params []Param, maxRows int) (*types.QueryResult, *types.Ticket, error) {
	info := s.Info()
	if info.Intent == "" {
		return nil, nil, errIntentRequired
	}
	conn, err := d.usableConnection(ctx, connID)
	if err != nil {
		return nil, nil, err
	}
	bound, err := d.resolveParams(ctx, info.ID, params)
	if err != nil {
		return nil, nil, err
	}

	res, tmpl, err := d.deps.Pipeline.Query(ctx, info, connID, sql, bound, maxRows, "")
	switch {
	case err != nil:
		return nil, nil, err
	case res != nil:
		return res, nil, nil
	case tmpl == nil:
		return nil, nil, errInternal
	case tmpl.State != "" && tmpl.State != types.TicketPending:
		return nil, nil, errInternal
	}

	facts, err := d.deps.Pipeline.Facts(ctx, info, connID, sql, bound)
	if err != nil {
		return nil, nil, err
	}
	write := isWrite(facts.StatementType)

	if !write {
		if m, ok := d.matchGrants(info.ID, connID, facts.Relations, facts.EstimatedRows); ok {
			capped := m.RowCeiling
			if maxRows > 0 {
				capped = min(maxRows, m.RowCeiling)
			}
			granted, _, gerr := d.deps.Pipeline.Query(ctx, info, connID, sql, bound, capped, m.Authorization)
			// §9.4: estimated rows are advisory, so the ceiling is checked again
			// against what actually came back. A result that overruns the rule a
			// human set is not covered by it, and goes to the queue instead.
			if gerr == nil && granted != nil && granted.RowCount <= m.RowCeiling {
				d.noteGrantUse(m.IDs)
				return granted, nil, nil
			}
			// A grant that did not lower the tier is not an error. The statement
			// falls through to the queue, where a human sees it.
		}
	}

	var preview *types.WritePreview
	if write {
		// R4.2d: the preview is a second execution that rolls back, run before a
		// human is asked, so the approval screen carries a count.
		preview, err = d.deps.Pipeline.PreviewWrite(ctx, info, connID, sql, bound)
		if err != nil {
			return nil, nil, err
		}
	}
	facts.Intent = info.Intent
	tk := d.issueTicket(s, connID, sql, bound, maxRows, write, tmpl, facts, preview, conn.Degraded())
	return nil, tk, nil
}
