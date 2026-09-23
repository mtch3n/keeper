// Package pipeline orchestrates one statement through G1–G9.
//
// Every decision it makes comes from the extended protocol, the plan and the
// catalog. Nothing in this package reads statement text: admission is Parse over
// the extended protocol, routing is the plan's ModifyTable node, the denylist
// and the grants are evaluated against the plan's relation list, and output
// columns are matched by (TableOID, AttNum). SPEC R7.2, R7.4c and R7.5a are the
// same rule stated three times, and together they are why this package holds no
// SQL lexer.
//
// It has no globals and touches no database: every dependency arrives through
// New, which is what lets the whole of G1–G9 be tested with fakes.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Authority is the daemon state the pipeline reads on every statement: what an
// operator configured for a connection, and what a human has already agreed to.
// internal/daemon satisfies it. It is declared here because it is consumed here.
type Authority interface {
	// Connection returns the registered connection. Mode, Limits, Denylist and
	// WriteScope all come from it, so R4.5's denylist and R4.2b's write scope
	// have exactly one source and cannot drift apart.
	Connection(ctx context.Context, connID string) (*types.Connection, error)

	// Grant returns the live grant covering exactly path, or false. SPEC R9.3c:
	// a grant covers exactly the path it names — no prefix matching, no
	// wildcards, a view is its own path. The pipeline therefore asks once per
	// relation in the plan, and one miss sends the statement back to its normal
	// tier. A suspended or expired grant must report false.
	Grant(ctx context.Context, sessionID string, path types.PathRef) (*types.Grant, bool)
}

// Deps are the pipeline's collaborators. Judge may be nil, which means no judge
// is configured; every other field is required.
type Deps struct {
	Catalogs  ports.CatalogFor
	Redactor  ports.Redactor
	Executor  ports.Executor
	Audit     ports.AuditLog
	Judge     ports.Judge
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
	judge     ports.Judge
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
		judge:     d.Judge,
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

// Delegation is an explicit permissive-mode scope a human granted (SPEC §9.4).
// Without one, a judge verdict never releases anything: model absence and model
// presence are both short of authorization.
type Delegation struct {
	ID        string
	Relations []types.RelationRef
	ExpiresAt time.Time
}

func (d *Delegation) covers(now time.Time, rels []types.RelationRef) bool {
	if d == nil || len(rels) == 0 {
		return false
	}
	if !d.ExpiresAt.IsZero() && now.After(d.ExpiresAt) {
		return false
	}
	for _, r := range rels {
		found := false
		for _, s := range d.Relations {
			if s == r {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Request is one statement, with everything the pipeline cannot discover.
type Request struct {
	Session types.Session
	ConnID  string
	SQL     string
	Params  []ports.Param
	// ParamPolicies is the source policy of each parameter resolved from a token
	// (SPEC R6.2), parallel to Params, PolicyAllow for a plain value. The
	// Redactor resolves a token before the pipeline runs — the Executor never
	// sees one — so the policy travels here. R7.6's no-relation case and R8.4b
	// both read it.
	ParamPolicies []types.Policy
	// MaxRows is the agent's request. The operator ceiling and any grant ceiling
	// bound it; the agent cannot raise it (SPEC §4.5).
	MaxRows int
	// Approval is set when a human has approved this statement.
	Approval *Approval
	// Delegation is an explicit permissive-mode scope.
	Delegation *Delegation
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
	Tier       types.Tier
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

	tier          types.Tier
	reasons       []string
	degradations  []types.Degradation
	transforms    map[string]types.Transform
	outputColumns []types.ColumnMeta
	rowCount      int
	collisions    int
	authorization string
	approver      string
	errCode       types.Code

	// release records that a judge verdict authorized releasing unpinned
	// uncertain output inside an explicit permissive delegation. It is kept on
	// the state because G5 is redone against the executed RowDescription, and
	// the decision has to survive that.
	release bool
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

func (s *state) raise(t types.Tier) {
	if t > s.tier {
		s.tier = t
	}
}

// Query runs a statement through G1–G9. The returned error is non-nil only when
// the pipeline itself could not complete — a refusal, an escalation and a result
// all arrive in the Decision. G9 is never skipped: if the audit record cannot be
// written, nothing is returned to the caller.
func (p *Pipeline) Query(ctx context.Context, req Request) (*Decision, error) {
	st := &state{auditID: newAuditID(), started: p.now(), transforms: map[string]types.Transform{}}

	dec := p.query(ctx, req, st)
	dec.AuditID = st.auditID
	dec.Tier = st.tier
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
		return refuse(st, types.Tier4Refuse, keeperError(types.CodeInternal, "this connection is not registered", "register the connection, or check its id"))
	}
	st.conn = conn
	// The catalog is per connection: a (tableOID, attnum) pair means nothing
	// without knowing which database produced it, and two databases reuse OIDs
	// freely. Resolving here rather than holding one is what keeps a statement
	// against staging from being masked by production's classifications.
	cat, err := p.catalogs(req.ConnID)
	if err != nil || cat == nil {
		return refuse(st, types.Tier4Refuse,
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
		return refuse(st, types.Tier4Refuse, ke)
	}

	// --- G4 plan ---------------------------------------------------------
	plan, err := p.exec.Plan(ctx, req.ConnID, req.SQL, req.Params)
	if err != nil {
		ke := asKeeperError(err)
		if ke.Code == types.CodeTimeout {
			// §7.7: an EXPLAIN timeout is tier 4, refused with a safe error.
			st.reason(ReasonPlanTimeout)
			return refuse(st, types.Tier4Refuse, ke)
		}
		if ke.Code == types.CodePermissionDenied {
			// G3 — Invariant A working. Composed by keeper from the catalog, so
			// the agent learns what it may name (SPEC R5.5).
			st.reason(ReasonPermission)
			return refuse(st, types.Tier4Refuse, p.permissionError(ctx, st.cat, nil))
		}
		return refuse(st, types.Tier4Refuse, ke)
	}
	st.plan = plan

	// DDL is refused at every tier, in every mode, with any approval (R4.2c).
	if plan.StatementType == pgdb.StmtDDL {
		st.reason(ReasonDDL)
		return refuse(st, types.Tier4Refuse, keeperError(types.CodeDDLRefused,
			"keeper does not run schema changes",
			"apply schema changes through the project's migration tooling, then run keeper catalog edit"))
	}

	// The denylist is evaluated against the plan's relation list, never
	// statement text, because EXPLAIN expands views (R4.5, R7.4c). No approval
	// overrides it and no mode relaxes it.
	if rel, ok := denylisted(conn, plan); ok {
		st.reason(ReasonDenylisted)
		return refuse(st, types.Tier4Refuse, keeperError(types.CodeDenylisted,
			"this statement reads "+rel.String()+", which an operator placed on this connection's denylist",
			"ask an operator to remove the entry if the relation should be readable"))
	}

	// --- G2 routing ------------------------------------------------------
	// A statement is a write because the plan says so, not because the agent
	// said so.
	if plan.Writes {
		if dec := p.checkWrite(st, conn, plan); dec != nil {
			return dec
		}
	}

	// --- G5 output identity ----------------------------------------------
	st.cols = p.resolveColumns(st.cat, described, plan, req)

	// --- G6 tiers --------------------------------------------------------
	p.assignTier(ctx, st)
	grants := p.applyGrants(ctx, req, st)

	if st.tier == types.Tier2Judge {
		p.consultJudge(ctx, req, st)
	}

	if st.tier >= types.Tier3Approve && req.Approval == nil {
		return p.escalate(ctx, req, st)
	}
	if req.Approval != nil {
		st.authorization = "ticket:" + req.Approval.TicketID
		st.approver = req.Approval.Approver
	} else if st.authorization == "" {
		st.authorization = "tier" + fmt.Sprint(int(st.tier))
	}

	// --- G8 execution ----------------------------------------------------
	maxRows := p.rowCeiling(req, conn, grants)
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
			return refuse(st, st.tier, p.permissionError(ctx, st.cat, plan))
		}
		return refuse(st, st.tier, ke)
	}

	// The executed RowDescription is the authoritative answer for which column
	// each output came from, so G5 is redone against it and the denylist is
	// rechecked against the relations that actually produced output.
	st.cols = p.resolveColumns(st.cat, raw.Columns, plan, req)
	if st.release {
		p.releaseUnpinned(st)
	}
	if rel, ok := p.denylistedOutput(st.cat, conn, st.cols); ok {
		st.reason(ReasonDenylisted)
		return refuse(st, types.Tier4Refuse, keeperError(types.CodeDenylisted,
			"this statement reads "+rel.String()+", which an operator placed on this connection's denylist",
			"ask an operator to remove the entry if the relation should be readable"))
	}

	// --- G7 redaction ----------------------------------------------------
	cols, rows, err := p.applyRedaction(ctx, req, st, raw.Rows)
	if err != nil {
		return refuse(st, st.tier, asKeeperError(err))
	}

	st.rowCount = len(rows)
	st.outputColumns = cols

	result := &types.QueryResult{
		Rows:          rows,
		Columns:       cols,
		RowCount:      len(rows),
		Truncated:     raw.Truncated,
		Transforms:    st.transforms,
		Tier:          st.tier,
		AuditID:       st.auditID,
		Mode:          conn.Mode,
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

// checkWrite is R4.2a–f. It returns a Decision when the write is refused.
func (p *Pipeline) checkWrite(st *state, conn *types.Connection, plan *ports.PlanFacts) *Decision {
	st.reason(ReasonWrite)

	if !conn.HasWriteCredential {
		return refuse(st, types.Tier4Refuse, keeperError(types.CodeNoWriteCredential,
			"this connection has no write credential, so it cannot modify data",
			"ask an operator to register a write credential for this connection"))
	}

	// internal/pgdb puts the plan's ModifyTable target first and refuses a plan
	// with more than one, so index 0 is the relation R4.2b names.
	if len(plan.RelationNames) == 0 {
		return refuse(st, types.Tier4Refuse, keeperError(types.CodeOutOfWriteScope,
			"keeper could not identify the relation this statement writes",
			"rewrite the statement so it names one relation to modify"))
	}
	target := plan.RelationNames[0]
	op := types.WriteOp(plan.StatementType)

	if !inWriteScope(conn.WriteScope, target, op) {
		// Refused before G3 would refuse it, so the agent receives a keeper code
		// naming the relation rather than a sanitised permission error it cannot
		// act on (SPEC R4.2b, the trap R5.5 describes).
		st.reason(ReasonOutOfScope)
		return refuse(st, types.Tier4Refuse, keeperError(types.CodeOutOfWriteScope,
			string(op)+" on "+target.String()+" is outside the scope recorded for this connection's write credential",
			"ask an operator to re-audit the connection if the credential's privileges changed"))
	}

	// R4.2f: no mode and no allow rule authorizes a write. Tier 3, always.
	st.raise(types.Tier3Approve)
	return nil
}

// escalate produces the approval facts, and for a write the preview of R4.2d.
func (p *Pipeline) escalate(ctx context.Context, req Request, st *state) *Decision {
	facts := types.ApprovalFacts{
		Intent:        req.Session.Intent,
		StatementType: st.plan.StatementType,
		Relations:     st.plan.RelationNames,
		EstimatedRows: st.plan.EstimatedRows,
		EstimatedCost: st.plan.EstimatedCost,
		Egress:        egressSummary(st.cols),
		Reasons:       st.reasons,
	}
	esc := &Escalation{Facts: facts, SQL: req.SQL}
	st.outputColumns = publicColumns(st.cols)

	if st.plan.Writes {
		// R4.2d step 1: the statement runs and the transaction rolls back. The
		// count is exact as of now and is not a lock.
		raw, err := p.exec.PreviewWrite(ctx, req.ConnID, req.SQL, req.Params)
		if err != nil {
			ke := asKeeperError(err)
			if ke.Code == types.CodePermissionDenied {
				st.reason(ReasonPermission)
				return refuse(st, st.tier, p.permissionError(ctx, st.cat, st.plan))
			}
			return refuse(st, st.tier, ke)
		}
		// R4.2e: RETURNING rows are output in exactly the sense §8 means, so
		// they pass through G7. The approval screen shows the transform summary
		// rather than the rows, and the rows stop here.
		st.cols = p.resolveColumns(st.cat, raw.Columns, st.plan, req)
		if _, _, err := p.applyRedaction(ctx, req, st, raw.Rows); err != nil {
			return refuse(st, st.tier, asKeeperError(err))
		}
		esc.Write = &types.WritePreview{
			Operation:   st.plan.StatementType,
			RowCount:    raw.CommandTag,
			PreviewedAt: p.now(),
			Scope:       st.plan.RelationNames,
			Returning:   st.transforms,
		}
	}

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
		st.tier = types.Tier4Refuse
		return nil, asKeeperError(err)
	}
	plan, err := p.exec.Plan(ctx, req.ConnID, req.SQL, req.Params)
	if err != nil {
		st.tier = types.Tier4Refuse
		return nil, asKeeperError(err)
	}
	st.plan = plan

	out := &types.ExplainResult{
		StatementType: plan.StatementType,
		Relations:     plan.RelationNames,
		EstimatedRows: plan.EstimatedRows,
		EstimatedCost: plan.EstimatedCost,
	}

	if plan.StatementType == pgdb.StmtDDL {
		st.tier = types.Tier4Refuse
		st.reason(ReasonDDL)
		out.PredictedTier = types.Tier4Refuse
		out.Reasons = st.reasons
		return out, nil
	}
	if _, ok := denylisted(conn, plan); ok {
		st.tier = types.Tier4Refuse
		st.reason(ReasonDenylisted)
		out.PredictedTier = types.Tier4Refuse
		out.Reasons = st.reasons
		return out, nil
	}
	if plan.Writes {
		st.reason(ReasonWrite)
		st.raise(types.Tier3Approve)
		if len(plan.RelationNames) > 0 && !inWriteScope(conn.WriteScope, plan.RelationNames[0], types.WriteOp(plan.StatementType)) {
			st.tier = types.Tier4Refuse
			st.reason(ReasonOutOfScope)
		}
	}

	st.cols = p.resolveColumns(st.cat, described, plan, req)
	if st.tier < types.Tier4Refuse {
		p.assignTier(ctx, st)
		p.applyGrants(ctx, req, st)
	}

	out.OutputColumns = publicColumns(st.cols)
	out.PredictedTier = st.tier
	out.Reasons = st.reasons
	st.outputColumns = out.OutputColumns
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
		Intent:        req.Session.Intent,
		Connection:    req.ConnID,
		Statement:     p.audit.Normalize(req.SQL, p.knownIdentifier(st)),
		OutputColumns: st.outputColumns,
		Tier:          st.tier,
		Transforms:    st.transforms,
		RowCount:      st.rowCount,
		Authorization: st.authorization,
		Approver:      st.approver,
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

func refuse(st *state, tier types.Tier, ke *types.Error) *Decision {
	st.raise(tier)
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
