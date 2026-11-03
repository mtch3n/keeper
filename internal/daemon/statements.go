package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Param is one bound parameter as it arrives from an agent: {"value": ...} or
// {"token": "..."}. §6.2.
type Param struct {
	Value any
	Token string
}

// writeStatements are the statement types G2 treats as writes.
// A statement is a write because the plan says it is, not because the agent said
// so (§6.1), so this reads the plan's own statement type.
var writeStatements = []string{"INSERT", "UPDATE", "DELETE", "MERGE"}

func isWrite(statementType string) bool { return slices.Contains(writeStatements, statementType) }

// resolveParams turns token parameters into bound values against the calling
// session's reverse map. keeper never substitutes a resolved value into
// statement text (R6.2): a token becomes a bound parameter or the call fails.
func (d *Daemon) resolveParams(ctx context.Context, sessionID, connID string, params []Param) ([]ports.Param, error) {
	out := make([]ports.Param, 0, len(params))
	for _, p := range params {
		if p.Token == "" {
			out = append(out, ports.Param{Value: p.Value})
			continue
		}
		v, pol, err := d.deps.Redactor.Resolve(ctx, sessionID, connID, p.Token)
		if err != nil {
			// R3.4d: the reverse map is memory only, so an unresolvable token is
			// not "retry" but "every token you hold is dead, re-run the queries
			// that minted them".
			return nil, errStaleToken
		}
		out = append(out, ports.Param{Value: v, Policy: pol})
	}
	return out, nil
}

// usableConnection is the admission check every statement passes: the
// connection exists.
func (d *Daemon) usableConnection(ctx context.Context, id string) (*types.Connection, error) {
	c, err := d.deps.Vault.Connection(ctx, id)
	if err != nil || c == nil {
		return nil, errUnknownConnection
	}
	return c, nil
}

// Explain is §6.1's dry run: what would be masked and whether the statement
// would escalate, without spending an approval.
func (d *Daemon) Explain(ctx context.Context, s *Session, connID, sql string, params []Param) (*types.ExplainResult, error) {
	if _, err := d.usableConnection(ctx, connID); err != nil {
		return nil, err
	}
	bound, err := d.resolveParams(ctx, s.ID(), connID, params)
	if err != nil {
		return nil, err
	}
	return d.deps.Pipeline.Explain(ctx, s.Info(), connID, sql, bound)
}

// Query runs a statement or escalates it. Exactly one of the results is non-nil
// on success.
func (d *Daemon) Query(ctx context.Context, s *Session, connID, sql string, params []Param, maxRows int) (*types.QueryResult, *types.Ticket, error) {
	info := s.Info()
	conn, err := d.usableConnection(ctx, connID)
	if err != nil {
		return nil, nil, err
	}
	bound, err := d.resolveParams(ctx, info.ID, connID, params)
	if err != nil {
		return nil, nil, err
	}

	res, tmpl, err := d.deps.Pipeline.Query(ctx, info, connID, sql, bound, maxRows, "")
	switch {
	case err != nil:
		return nil, nil, d.withWriters(ctx, connID, err)
	case res != nil:
		d.results.keep(res)
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

	tk := d.issueTicket(s, connID, sql, bound, maxRows, write, conn.Name, tmpl, facts, tmpl.Preview)
	return nil, tk, nil
}

// withWriters adds to a writes-off refusal the profiles on the same host and
// database that allow writes. keeper never switches to one: the agent resubmits
// naming it, so what runs is always the profile the agent asked for.
func (d *Daemon) withWriters(ctx context.Context, connID string, err error) error {
	var kerr *types.Error
	if !errors.As(err, &kerr) || kerr.Code != types.CodeWritesOff {
		return err
	}
	c, cerr := d.deps.Vault.Connection(ctx, connID)
	if cerr != nil || c == nil {
		return err
	}
	cs, cerr := d.deps.Vault.Connections(ctx)
	if cerr != nil {
		return err
	}
	var names []string
	for _, o := range cs {
		if o.ID != c.ID && o.HostID == c.HostID && o.Database == c.Database && (o.Writes == types.WritesAsk || o.Writes == types.WritesAllow) {
			names = append(names, o.Name)
		}
	}
	if len(names) == 0 {
		return err
	}
	slices.Sort(names)
	out := *kerr
	list := strings.Join(names, ", ")
	out.Summary += "; profiles on this database that allow writes: " + list
	out.Action = "resubmit on a profile for this database that allows writes: " + list
	return &out
}
