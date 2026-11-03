// Package pipeline orchestrates one statement through G1–G9.
//
// Every decision it makes comes from the extended protocol, the plan, the
// profile's settings and the catalog. Nothing in this package reads statement
// text: admission is Parse over the extended protocol, routing is the plan's
// ModifyTable node, and output columns are matched by (TableOID, AttNum). That
// is why this package holds no SQL lexer.
//
// It has no globals and touches no database: every dependency arrives through
// New, which is what lets the whole of G1–G9 be tested with fakes.
package pipeline

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"time"
	"uuid"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Authority is the daemon state the pipeline reads on every statement.
// internal/daemon satisfies it. It is declared here because it is consumed here.
type Authority interface {
	// Connection returns the registered connection. Reads, Writes and Limits
	// all come from it, so each has exactly one source.
	Connection(ctx context.Context, connID string) (*types.Connection, error)
}

// Deps are the pipeline's collaborators. Every field but Now is required.
type Deps struct {
	Catalogs  ports.CatalogFor
	Redactor  ports.Redactor
	Executor  ports.Executor
	Audit     ports.AuditLog
	Authority Authority
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Pipeline runs statements.
type Pipeline struct {
	catalogs  ports.CatalogFor
	redactor  ports.Redactor
	exec      ports.Executor
	audit     ports.AuditLog
	authority Authority
	now       func() time.Time
}

// New wires a pipeline.
func New(d Deps) (*Pipeline, error) {
	switch {
	case d.Catalogs == nil:
		return nil, fmt.Errorf("pipeline: Catalog is required")
	case d.Redactor == nil:
		return nil, fmt.Errorf("pipeline: Redactor is required")
	case d.Executor == nil:
		return nil, fmt.Errorf("pipeline: Executor is required")
	case d.Audit == nil:
		return nil, fmt.Errorf("pipeline: Audit is required")
	case d.Authority == nil:
		return nil, fmt.Errorf("pipeline: Authority is required")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Pipeline{
		catalogs:  d.Catalogs,
		redactor:  d.Redactor,
		exec:      d.Executor,
		audit:     d.Audit,
		authority: d.Authority,
		now:       now,
	}, nil
}

// Approval is a decision a human already made for this exact statement. The
// daemon owns tickets; the pipeline only needs to know the decision was made.
type Approval struct {
	TicketID string
	Approver string
	// PreviewedRows is the count R4.2d's preview reported. It is returned beside
	// the executed count so a divergence is visible rather than silent.
	PreviewedRows int64
}

// Request is one statement, with everything the pipeline cannot discover.
type Request struct {
	Session types.Session
	ConnID  string
	SQL     string
	Params  []ports.Param
	// MaxRows is the agent's request. The operator ceiling bounds it; the agent
	// cannot raise it.
	MaxRows int
	// Approval is set when a human has approved this statement.
	Approval *Approval
}

// Escalation is a statement that needs a human. The daemon turns it into a
// ticket and an approval-queue row; the pipeline does not own tickets.
type Escalation struct {
	Facts types.ApprovalFacts
	// SQL is the statement as submitted. §9.2 puts it below the facts.
	SQL string
	// Write carries the preview of SPEC R4.2d, including the transform summary
	// for any RETURNING rows. The rows themselves never leave the pipeline.
	Write *types.WritePreview
}

// Decision is what the pipeline concluded. Exactly one of Result, Escalation and
// Error is set.
type Decision struct {
	AuditID    string
	Result     *types.QueryResult
	Escalation *Escalation
	Error      *types.Error
}

// state accumulates what G9 will record, whichever way the statement ends.
type state struct {
	auditID string
	started time.Time

	conn *types.Connection
	cat  ports.Catalog
	plan *ports.PlanFacts
	cols []column

	reasons       []string
	degradations  []types.Degradation
	transforms    map[string]types.Transform
	outputColumns []types.ColumnMeta
	rowCount      int
	collisions    int
	authorization string
	approver      string
	decision      types.Decision
	errCode       types.Code
}

func (s *state) reason(r string) {
	for _, have := range s.reasons {
		if have == r {
			return
		}
	}
	s.reasons = append(s.reasons, r)
}

func (s *state) degrade(layer, why string) {
	s.degradations = append(s.degradations, types.Degradation{Layer: layer, Reason: why})
}

// Query runs a statement through G1–G9. The returned error is non-nil only when
// the pipeline itself could not complete — a refusal, an escalation and a result
// all arrive in the Decision. G9 is never skipped: if the audit record cannot be
// written, nothing is returned to the caller.
func (p *Pipeline) Query(ctx context.Context, req Request) (*Decision, error) {
	st := &state{auditID: newAuditID(), started: p.now(), transforms: map[string]types.Transform{}}

	dec := p.query(ctx, req, st)
	dec.AuditID = st.auditID
	if dec.Error != nil {
		dec.Error.AuditID = st.auditID
		st.errCode = dec.Error.Code
	}

	if err := p.writeAudit(ctx, req, st); err != nil {
		return nil, err
	}
	return dec, nil
}

func (p *Pipeline) query(ctx context.Context, req Request, st *state) *Decision {
	// --- connection ------------------------------------------------------
	conn, err := p.authority.Connection(ctx, req.ConnID)
	if err != nil || conn == nil {
		return refuse(keeperError(types.CodeInternal, "this connection is not registered", "register the connection, or check its id"))
	}
	st.conn = conn
	// The catalog is per connection: a (tableOID, attnum) pair means nothing
	// without knowing which database produced it, and two databases reuse OIDs
	// freely. Resolving here rather than holding one is what keeps a statement
	// against staging from being masked by production's classifications.
	cat, err := p.catalogs(req.ConnID)
	if err != nil || cat == nil {
		return refuse(
			keeperError(types.CodeInternal, "this connection has no catalog",
				"run keeper catalog init for this connection"))
	}
	st.cat = cat

	// --- G1 admission ----------------------------------------------------
	// Parse and Describe over the extended protocol. PostgreSQL's own parser
	// validates the syntax and the protocol rejects multi-statement input
	// inherently (SPEC R7.2).
	described, err := p.exec.Describe(ctx, req.ConnID, req.SQL, req.Params)
	if err != nil {
		ke := asKeeperError(err)
		switch ke.Code {
		case types.CodeMultiStatement:
			st.reason(ReasonMultiStatement)
		default:
			st.reason(ReasonSyntax)
		}
		return refuse(ke)
	}

	// --- G4 plan ---------------------------------------------------------
	plan, err := p.exec.Plan(ctx, req.ConnID, req.SQL, req.Params)
	if err != nil {
		ke := asKeeperError(err)
		if ke.Code == types.CodeTimeout {
			// An EXPLAIN timeout is refused with a safe error.
			st.reason(ReasonPlanTimeout)
			return refuse(ke)
		}
		if ke.Code == types.CodePermissionDenied {
			// G3 — Invariant A working. Composed by keeper from the catalog, so
			// the agent learns what it may name (SPEC R5.5).
			st.reason(ReasonPermission)
			return refuse(p.permissionError(ctx, st.cat, nil))
		}
		return refuse(ke)
	}
	st.plan = plan

	// --- G2 routing ------------------------------------------------------
	// The statement is a write because the plan says so, not because the agent
	// said so. Explain reaches the same outcome through the same function.
	outcome, refusal := decide(st, conn, plan)
	if refusal != nil {
		return refuse(refusal)
	}

	// --- G5 output identity ----------------------------------------------
	st.cols = p.resolveColumns(st.cat, described, plan, req)
	p.noteColumns(ctx, st)

	if outcome == types.OutcomeAsk && req.Approval == nil {
		return p.escalate(ctx, req, st)
	}
	if req.Approval != nil {
		st.authorization = "ticket:" + req.Approval.TicketID
		st.approver = req.Approval.Approver
		st.decision = types.DecisionApproved
	} else {
		st.authorization = "profile"
	}

	// --- G8 execution ----------------------------------------------------
	maxRows := p.rowCeiling(req, conn)
	var raw *ports.RawResult
	if plan.Writes {
		raw, err = p.exec.CommitWrite(ctx, req.ConnID, req.SQL, req.Params)
	} else {
		raw, err = p.exec.Run(ctx, req.ConnID, req.SQL, req.Params, maxRows)
	}
	if err != nil {
		ke := asKeeperError(err)
		if ke.Code == types.CodePermissionDenied {
			// G3. A permission error here is the system working (R7.3).
			st.reason(ReasonPermission)
			return refuse(p.permissionError(ctx, st.cat, plan))
		}
		return refuse(ke)
	}

	// The executed RowDescription is the authoritative answer for which column
	// each output came from, so G5 is redone against it.
	st.cols = p.resolveColumns(st.cat, raw.Columns, plan, req)

	// --- G7 redaction ----------------------------------------------------
	cols, rows, err := p.applyRedaction(ctx, req, st, raw.Rows)
	if err != nil {
		return refuse(asKeeperError(err))
	}

	var cut types.Cut
	if raw.Truncated {
		cut = types.CutByRows
	}
	if kept := withinBytes(rows, conn.Limits.MaxBytes); kept < len(rows) {
		rows, cut = rows[:kept], types.CutBySize
	}

	st.rowCount = len(rows)
	st.outputColumns = cols

	result := &types.QueryResult{
		Rows:          rows,
		Columns:       cols,
		RowCount:      len(rows),
		Truncated:     cut != "",
		TruncatedBy:   cut,
		Transforms:    st.transforms,
		AuditID:       st.auditID,
		Authorization: st.authorization,
		Degradations:  st.degradations,
	}
	if plan.Writes {
		result.ExecutedRows = raw.CommandTag
		if req.Approval != nil {
			result.PreviewedRows = req.Approval.PreviewedRows
		}
	}
	return &Decision{Result: result}
}

// decide is the one rule query and explain share: what keeper does with a
// planned statement under the profile's settings. A value of either setting
// that keeper does not know fails closed.
func decide(st *state, conn *types.Connection, plan *ports.PlanFacts) (types.Outcome, *types.Error) {
	if plan.StatementType == pgdb.StmtDDL {
		st.reason(ReasonDDL)
		return types.OutcomeRefuse, keeperError(types.CodeDDLRefused,
			"keeper does not run schema changes",
			"apply schema changes through the project's migration tooling, then run keeper catalog edit")
	}
	if plan.Writes {
		st.reason(ReasonWrite)
		switch conn.Writes {
		case types.WritesAsk:
			return types.OutcomeAsk, nil
		case types.WritesAllow:
			return types.OutcomeRun, nil
		}
		st.reason(ReasonWritesOff)
		return types.OutcomeRefuse, keeperError(types.CodeWritesOff,
			"writes are off for this profile, so its sessions are read-only",
			"use a profile on this database that allows writes, or ask an operator to allow them on this one")
	}
	switch conn.Reads {
	case types.ReadsNever:
		return types.OutcomeRun, nil
	case types.ReadsUncertain:
		if !plan.Uncertain {
			return types.OutcomeRun, nil
		}
		st.reason(ReasonVolatileCall)
		return types.OutcomeAsk, nil
	}
	st.reason(ReasonReadsAsk)
	return types.OutcomeAsk, nil
}

// escalate produces the approval facts, and for a write the preview of R4.2d.
func (p *Pipeline) escalate(ctx context.Context, req Request, st *state) *Decision {
	esc := &Escalation{SQL: req.SQL}
	st.outputColumns = publicColumns(st.cols)
	facts := p.approvalFacts(req, st)

	if st.plan.Writes {
		// The statement runs and the transaction rolls back. The
		// count is exact as of now and is not a lock.
		raw, err := p.exec.PreviewWrite(ctx, req.ConnID, req.SQL, req.Params)
		if err != nil {
			ke := asKeeperError(err)
			if ke.Code == types.CodePermissionDenied {
				st.reason(ReasonPermission)
				return refuse(p.permissionError(ctx, st.cat, st.plan))
			}
			return refuse(ke)
		}
		// R4.2e: RETURNING rows are output in exactly the sense §8 means, so
		// they pass through G7. The approval screen shows the transform summary
		// rather than the rows, and the rows stop here.
		st.cols = p.resolveColumns(st.cat, raw.Columns, st.plan, req)
		if _, _, err := p.applyRedaction(ctx, req, st, raw.Rows); err != nil {
			return refuse(asKeeperError(err))
		}
		esc.Write = &types.WritePreview{
			Operation:   st.plan.StatementType,
			RowCount:    raw.CommandTag,
			PreviewedAt: p.now(),
			Scope:       st.plan.RelationNames,
			Returning:   st.transforms,
			// The changed rows are for the approver alone. They are not output
			// in §8's sense: nothing here passes them to G7, the audit record or
			// the agent.
			Changes: shownChanges(raw.Changes, raw.CommandTag),
		}
	}

	esc.Facts = facts
	return &Decision{Escalation: esc, Error: keeperError(types.CodeApprovalRequired,
		"this statement needs a human decision before it runs",
		"a keeper approval is waiting; poll the ticket for the outcome")}
}

// Explain is SPEC §6.1's dry run: what would be masked and whether the statement
// would escalate, without spending an approval and without reading a row.
//
// The three returns separate the three outcomes. The *types.Error is a refusal
// the agent should see; the error is the pipeline failing to complete, which
// includes G9 being unwritable and therefore returns nothing at all.
func (p *Pipeline) Explain(ctx context.Context, req Request) (*types.ExplainResult, *types.Error, error) {
	st := &state{auditID: newAuditID(), started: p.now(), transforms: map[string]types.Transform{}}

	res, ke := p.explain(ctx, req, st)
	if ke != nil {
		ke.AuditID = st.auditID
		st.errCode = ke.Code
	}
	if err := p.writeAudit(ctx, req, st); err != nil {
		return nil, nil, err
	}
	return res, ke, nil
}

func (p *Pipeline) explain(ctx context.Context, req Request, st *state) (*types.ExplainResult, *types.Error) {
	conn, err := p.authority.Connection(ctx, req.ConnID)
	if err != nil || conn == nil {
		return nil, keeperError(types.CodeInternal, "this connection is not registered", "register the connection, or check its id")
	}
	st.conn = conn
	// The catalog is per connection: a (tableOID, attnum) pair means nothing
	// without knowing which database produced it, and two databases reuse OIDs
	// freely. Resolving here rather than holding one is what keeps a statement
	// against staging from being masked by production's classifications.
	cat, err := p.catalogs(req.ConnID)
	if err != nil || cat == nil {
		return nil, keeperError(types.CodeInternal, "this connection has no catalog",
			"run keeper catalog init for this connection")
	}
	st.cat = cat

	described, err := p.exec.Describe(ctx, req.ConnID, req.SQL, req.Params)
	if err != nil {
		return nil, asKeeperError(err)
	}
	plan, err := p.exec.Plan(ctx, req.ConnID, req.SQL, req.Params)
	if err != nil {
		return nil, asKeeperError(err)
	}
	st.plan = plan

	out := &types.ExplainResult{
		StatementType: plan.StatementType,
		Relations:     plan.RelationNames,
		EstimatedRows: plan.EstimatedRows,
		EstimatedCost: plan.EstimatedCost,
	}
	outcome, refusal := decide(st, conn, plan)
	out.Outcome = outcome
	if refusal == nil {
		st.cols = p.resolveColumns(st.cat, described, plan, req)
		p.noteColumns(ctx, st)
		out.OutputColumns = publicColumns(st.cols)
		st.outputColumns = out.OutputColumns
	}
	out.Reasons = st.reasons
	return out, nil
}

// writeAudit is G9. It is never skipped, and it never receives a literal, a
// comment, an unmatched quoted identifier or a result row.
func (p *Pipeline) writeAudit(ctx context.Context, req Request, st *state) error {
	rec := &types.AuditRecord{
		ID:            st.auditID,
		At:            st.started,
		SessionID:     req.Session.ID,
		Client:        req.Session.Client,
		Connection:    req.ConnID,
		Statement:     p.audit.Normalize(req.SQL, p.knownIdentifier(st)),
		OutputColumns: st.outputColumns,
		Transforms:    st.transforms,
		RowCount:      st.rowCount,
		Authorization: st.authorization,
		Approver:      st.approver,
		Decision:      st.decision,
		Degradations:  st.degradations,
		Collisions:    st.collisions,
		Duration:      p.now().Sub(st.started),
		ErrorCode:     st.errCode,
	}
	if st.plan != nil {
		rec.StatementType = st.plan.StatementType
		rec.Relations = st.plan.RelationNames
	}
	if len(rec.Transforms) == 0 {
		rec.Transforms = nil
	}
	return p.audit.Write(ctx, rec)
}

// knownIdentifier tells the audit normalizer which quoted identifiers it may
// keep: the relations the plan named, and the columns the catalog knows for
// them. Everything else is stripped (SPEC R10c).
func (p *Pipeline) knownIdentifier(st *state) func(string) bool {
	return func(ident string) bool {
		if st.plan == nil {
			return false
		}
		for _, r := range st.plan.RelationNames {
			if ident == r.Relation || ident == r.Schema || ident == r.String() {
				return true
			}
			if _, ok := st.cat.LookupName(r, ident); ok {
				return true
			}
		}
		return false
	}
}

func refuse(ke *types.Error) *Decision {
	return &Decision{Error: ke}
}

func keeperError(code types.Code, summary, action string) *types.Error {
	return &types.Error{Code: code, Summary: summary, Action: action}
}

// asKeeperError keeps the *types.Error internal/pgdb already composed. Anything
// else — only reachable from a test double — becomes an internal error rather
// than travelling further.
func asKeeperError(err error) *types.Error {
	if ke, ok := errors.AsType[*types.Error](err); ok {
		return ke
	}
	return keeperError(types.CodeInternal, "keeper could not complete this statement", "retry; tell an operator if it persists")
}

func newAuditID() string { return uuid.NewV7().String() }

// shownChanges keeps the first types.PreviewRows changed rows and counts the
// rest of the affected rows as omitted.
func shownChanges(c *types.WriteChanges, affected int64) *types.WriteChanges {
	if c == nil {
		return nil
	}
	out := &types.WriteChanges{Columns: c.Columns, Rows: c.Rows[:min(len(c.Rows), types.PreviewRows)]}
	out.Omitted = max(affected-int64(len(out.Rows)), 0)
	return out
}

// Facts is §9.2's approval payload for a statement, from the dry run: nothing is
// read and no write is previewed. Like Explain, it is audited.
func (p *Pipeline) Facts(ctx context.Context, req Request) (*types.ApprovalFacts, *types.Error, error) {
	st := &state{auditID: newAuditID(), started: p.now(), transforms: map[string]types.Transform{}}
	_, ke := p.explain(ctx, req, st)
	if ke != nil {
		ke.AuditID = st.auditID
		st.errCode = ke.Code
	}
	if err := p.writeAudit(ctx, req, st); err != nil {
		return nil, nil, err
	}
	if ke != nil {
		return nil, ke, nil
	}
	f := p.approvalFacts(req, st)
	return &f, nil, nil
}

// approvalFacts describes the statement st planned.
func (p *Pipeline) approvalFacts(req Request, st *state) types.ApprovalFacts {
	f := types.ApprovalFacts{
		Egress:  egressSummary(st.cols),
		Reasons: st.reasons,
	}
	if st.plan != nil {
		f.StatementType = st.plan.StatementType
		f.Relations = st.plan.RelationNames
		f.EstimatedRows = st.plan.EstimatedRows
		f.EstimatedCost = st.plan.EstimatedCost
		if st.plan.Writes && len(st.plan.RelationNames) > 0 {
			// internal/pgdb puts the one ModifyTable target first.
			f.Targets = st.plan.RelationNames[:1]
		}
		if st.cat != nil {
			f.Filters = flaggedFilters(st.cat, st.plan.Filters)
		}
	}
	f.Risk, f.Unreadable = riskOf(f.StatementType)
	for _, c := range publicColumns(st.cols) {
		f.Columns = append(f.Columns, c.Name)
	}
	return f
}

// riskOf classes a statement type. MERGE can delete, so it is destructive; a
// type keeper does not know is destructive and unreadable.
func riskOf(stmt string) (types.Risk, bool) {
	switch stmt {
	case pgdb.StmtSelect:
		return types.RiskRead, false
	case pgdb.StmtInsert, pgdb.StmtUpdate:
		return types.RiskWrite, false
	case pgdb.StmtDelete, pgdb.StmtMerge:
		return types.RiskDestructive, false
	}
	return types.RiskDestructive, true
}

// flaggedFilters is the filter columns an approver should notice: catalogued
// ones that are not allow, and ones with no catalog entry.
func flaggedFilters(cat ports.Catalog, refs []ports.ColumnRef) []types.FilterColumn {
	var out []types.FilterColumn
	for _, r := range refs {
		fc := types.FilterColumn{Relation: r.Relation, Column: r.Column}
		entry, ok := cat.Lookup(r.TableOID, r.AttNum)
		switch {
		case !ok:
			fc.Flag = "unclassified"
		case entry.Policy != types.PolicyAllow:
			fc.Flag, fc.Policy = "sensitive", entry.Policy
		default:
			continue
		}
		if !slices.Contains(out, fc) {
			out = append(out, fc)
		}
	}
	return out
}

// withinBytes is how many leading rows fit in limit bytes of row data, measured
// as the agent receives them: after redaction, encoded as JSON.
func withinBytes(rows [][]any, limit int) int {
	total := 0
	for i, row := range rows {
		b, err := json.Marshal(row)
		if err != nil {
			return i
		}
		total += len(b) + 1
		if total > limit {
			return i
		}
	}
	return len(rows)
}
