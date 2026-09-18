package pipeline

import (
	"context"
	"strconv"
	"strings"

	"github.com/mtchen/keeper/internal/pgdb"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Reason codes are a closed set. They appear in approval facts, in explain
// output and in the audit log, and nothing outside this list is ever emitted by
// the pipeline itself; a judge contributes its own, validated by internal/judge.
const (
	ReasonMasked            = "masked_output"
	ReasonDropped           = "dropped_column"
	ReasonUnresolved        = "unresolved_column"
	ReasonNearRowCap        = "near_row_cap"
	ReasonStaleCatalog      = "stale_catalog"
	ReasonWrite             = "write"
	ReasonDenylisted        = "denylisted_relation"
	ReasonDDL               = "ddl"
	ReasonOutOfScope        = "out_of_write_scope"
	ReasonNoGrant           = "no_grant"
	ReasonJudge             = "judge_assessed"
	ReasonStrictUncertainty = "strict_mode_uncertainty"
	ReasonMultiStatement    = "multi_statement"
	ReasonSyntax            = "unparseable"
	ReasonPlanTimeout       = "plan_timeout"
	ReasonPermission        = "permission_denied"
)

// nearCapFraction is how close to the operator row ceiling an estimate has to
// get before §7.7 calls it "near the cap". It is a tuning value, not a design
// decision (SPEC §15).
const nearCapFraction = 0.8

// column is one output column after G5: the server's identity for it, the policy
// keeper resolved, and where that policy came from.
type column struct {
	meta  types.ColumnMeta
	entry types.ColumnPolicy
	basis types.Basis
	// hidden marks a column whose name is disclosive independently of its value
	// (R6.4b). The name is replaced on every response surface; the value still
	// follows the column's own policy.
	hidden  bool
	dropped bool
}

// resolveColumns is G5. Every output column is matched by the server's own
// (TableOID, AttNum) from RowDescription and never by output name — name
// matching requires enumerating aliases, expressions, function wrappers, views
// and CTEs, which is the failure shape of CVE-2026-85620 (SPEC R7.5a).
func (p *Pipeline) resolveColumns(cat ports.Catalog, raw []types.ColumnMeta, plan *ports.PlanFacts, req Request) []column {
	paramPolicy := types.Strictest(req.ParamPolicies...)
	cols := make([]column, len(raw))
	used := map[string]bool{}

	for i, rc := range raw {
		c := column{meta: rc}

		if rc.TableOID != 0 && rc.AttNum != 0 {
			if entry, ok := cat.Lookup(rc.TableOID, rc.AttNum); ok {
				c.entry = entry
				c.basis = types.BasisCatalog
			} else {
				// R5.4a, the single rule for an unknown column appearing at
				// query time: redact, no automatic classification, the statement
				// still runs, and the column is surfaced for a catalog edit.
				c.entry = types.ColumnPolicy{Policy: types.PolicyRedact}
				c.basis = types.BasisUnknown
			}
		} else {
			// (0,0) is the server saying "computed". SPEC §7.6.
			pol, basis := p.inherit(cat, rc, plan, req)
			c.entry = types.ColumnPolicy{Policy: pol}
			c.basis = basis
		}

		// R8.4b. If any parameter was resolved from a token, every text-like or
		// unknown-typed output column inherits at least that token's source
		// policy, whatever relations the statement reads — R7.6's relation-based
		// rule offers nothing for SELECT 'Hi ' || $1::text FROM clean_ids.
		// Numeric, boolean, date/time and uuid columns cannot carry a text value
		// and do not inherit, which is what keeps count(*) usable.
		if paramPolicy != types.PolicyAllow &&
			c.entry.Policy != types.PolicyDrop &&
			pgdb.TextLike(pgdb.FamilyForName(rc.Type)) {
			if raised := types.Strictest(c.entry.Policy, types.Inherited(paramPolicy)); raised.Rank() > c.entry.Policy.Rank() {
				c.entry = types.ColumnPolicy{Policy: raised, HideName: c.entry.HideName}
				c.basis = types.BasisParameter
			}
		}

		c.hidden = c.entry.HideName
		c.dropped = c.entry.Policy == types.PolicyDrop
		c.meta.Policy = c.entry.Policy
		c.meta.Name = uniqueName(used, displayName(rc.Name, i, c.hidden))
		cols[i] = c
	}
	return cols
}

// inherit is SPEC R7.6, exactly.
func (p *Pipeline) inherit(cat ports.Catalog, col types.ColumnMeta, plan *ports.PlanFacts, req Request) (types.Policy, types.Basis) {
	family := pgdb.FamilyForName(col.Type)

	if plan == nil || len(plan.Relations) == 0 {
		// A computed column with no referenced relation inherits from its
		// parameters (§8.4), and is redact if that is indeterminable — which,
		// with no parameters at all, it is.
		if len(req.ParamPolicies) == 0 {
			return types.PolicyRedact, types.BasisUnknown
		}
		return types.Inherited(types.Strictest(req.ParamPolicies...)), types.BasisParameter
	}

	// Numeric, boolean, date/time and uuid inherit from the same family only.
	// Text-like and unknown inherit from every non-allow column of the
	// referenced relations.
	broad := pgdb.TextLike(family)

	var candidates []types.Policy
	for _, oid := range plan.Relations {
		for _, fp := range cat.RelationPolicies(oid) {
			switch fp.Policy.Policy {
			case types.PolicyAllow:
				continue
			case types.PolicyDrop:
				// R7.6b: drop is not in the ordering and is never inherited.
				// Dropping a computed column deletes it from the response —
				// measured, SELECT upper(status), count(*) ... GROUP BY 1
				// returned counts with no group key.
				continue
			}
			if broad || fp.Family == family {
				candidates = append(candidates, fp.Policy.Policy)
			}
		}
	}
	if len(candidates) == 0 {
		// allow, with the tripwires of §5.4 still running in G7.
		return types.PolicyAllow, types.BasisInherited
	}
	// Inherited caps at redact: token under someone else's namespace produces an
	// HMAC where a month label belongs, and partial has no domain to keep.
	return types.Inherited(types.Strictest(candidates...)), types.BasisInherited
}

// assignTier is G6: SPEC §7.7's table as amended by R7.7a, R7.7d and R4.2f.
func (p *Pipeline) assignTier(ctx context.Context, st *state) {
	for _, c := range st.cols {
		switch {
		case c.dropped:
			// R7.7a: a drop column in the output is not an escalation. The
			// column is removed, the statement runs at tier 1, and transforms
			// reports the removal. Escalating it would open a ticket, send a
			// human to a terminal and produce an identical result.
			st.raise(types.Tier1Record)
			st.reason(ReasonDropped)
		case c.basis == types.BasisUnknown:
			// R7.7d: an unresolved column never raises the tier by itself —
			// never tier 2, never tier 3. The judge cannot classify it either,
			// and a human cannot decide it from an approval prompt. The fix is a
			// catalog edit, which is where it is surfaced.
			st.raise(types.Tier1Record)
			st.reason(ReasonUnresolved)
		case c.entry.Policy.Masks():
			st.raise(types.Tier1Record)
			st.reason(ReasonMasked)
		}
	}

	if st.plan == nil || st.conn == nil {
		return
	}

	ceiling := st.conn.Limits.MaxRowsCeiling
	if ceiling <= 0 {
		ceiling = types.DefaultLimits().MaxRowsCeiling
	}
	if st.plan.EstimatedRows >= int64(float64(ceiling)*nearCapFraction) {
		st.raise(types.Tier2Judge)
		st.reason(ReasonNearRowCap)
	}

	// R5.6b-2: a view whose definition changed. A false answer from Fresh is
	// uncertainty, not permission.
	if fresh, err := st.cat.Fresh(ctx, st.plan.Relations); err != nil || !fresh {
		st.degrade("catalog", "fingerprints no longer match the database")
		st.reason(ReasonStaleCatalog)
		if st.conn.Mode == types.ModeStrict {
			st.raise(types.Tier3Approve)
		} else {
			st.raise(types.Tier2Judge)
		}
	}
}

// applyGrants is SPEC §9.3. A grant covers exactly the path it names, so every
// relation the plan touches must have its own entry; one unlisted relation sends
// the statement back to its normal tier.
func (p *Pipeline) applyGrants(ctx context.Context, req Request, st *state) []*types.Grant {
	if st.plan == nil || st.tier <= types.Tier1Record {
		return nil
	}
	if st.plan.Writes {
		// R4.2f: no mode and no allow rule authorizes a write, and §9.3's rules
		// cannot lower a write below tier 3.
		return nil
	}
	if len(st.plan.RelationNames) == 0 {
		return nil
	}

	grants := make([]*types.Grant, 0, len(st.plan.RelationNames))
	for _, rel := range st.plan.RelationNames {
		g, ok := p.authority.Grant(ctx, req.Session.ID, types.PathRef{ConnectionID: req.ConnID, Relation: rel})
		if !ok || g == nil || g.Suspended {
			// No prefix matching, no wildcards, a view is its own path and so is
			// every base table its plan expands to (R9.3c). A view reached
			// through an allowed base table does not launder the base table's
			// grant, and the reverse holds too.
			st.reason(ReasonNoGrant)
			return nil
		}
		grants = append(grants, g)
	}

	// A grant lowers the tier for the paths it names and can never lower below
	// tier 1.
	st.tier = types.Tier1Record
	st.authorization = "grant:" + grants[0].ID
	return grants
}

// consultJudge is §7.8. keeperd calls the judge directly, against a local
// endpoint, never through the harness (R7.8c).
func (p *Pipeline) consultJudge(ctx context.Context, req Request, st *state) {
	fallback := func(why string) {
		// R7.7b: a configured-but-failing judge never counts as a favourable
		// verdict. Assisted and permissive continue with deterministic masking —
		// blocking every uncertain read on an unreachable sidecar is the
		// approval fatigue §9.3 exists to prevent — while strict-mode
		// uncertainty is a human decision (§9.4).
		st.degrade("judge", why)
		if st.conn.Mode == types.ModeStrict {
			st.raise(types.Tier3Approve)
			st.reason(ReasonStrictUncertainty)
		}
	}

	if p.judge == nil {
		fallback("not configured")
		return
	}
	if !p.judge.Available(ctx) {
		fallback("unreachable")
		return
	}
	verdict, err := p.judge.Assess(ctx, ports.JudgeRequest{
		SQL:           req.SQL,
		Intent:        req.Session.Intent,
		Plan:          st.plan,
		OutputColumns: publicColumns(st.cols),
		Mode:          st.conn.Mode,
	})
	if err != nil || verdict == nil {
		fallback("assessment failed")
		return
	}

	st.reason(ReasonJudge)
	for _, rc := range verdict.ReasonCodes {
		st.reason(rc)
	}

	// The judge may recommend routing. It cannot refuse: tier 4 belongs to the
	// denylist, DDL and the write scope, all decided without it.
	if verdict.Tier > st.tier {
		st.raise(min(verdict.Tier, types.Tier3Approve))
	}

	// R7.8a and §9.4: release is honoured only inside an explicit permissive
	// delegation, and only for unpinned uncertain output. A pinned catalog
	// policy is never lowered by a model.
	if !verdict.Release || st.conn.Mode != types.ModePermissive ||
		!req.Delegation.covers(p.now(), st.plan.RelationNames) {
		return
	}
	st.release = true
	if p.releaseUnpinned(st) {
		st.authorization = "delegation:" + req.Delegation.ID
		st.tier = types.Tier1Record
	}
}

// releaseUnpinned lowers the uncertain columns a delegation covers to allow. A
// pinned catalog policy — anything with BasisCatalog — is never touched: §9.4
// keeps "may this run", "may this be disclosed" and "may this be written"
// separate, and a local model cannot grant itself the second.
func (p *Pipeline) releaseUnpinned(st *state) bool {
	released := false
	for i := range st.cols {
		c := &st.cols[i]
		if c.basis == types.BasisInherited || c.basis == types.BasisUnknown {
			c.entry.Policy = types.PolicyAllow
			c.meta.Policy = types.PolicyAllow
			released = true
		}
	}
	return released
}

// applyRedaction is G7. Every value leaving keeper passes through the Redactor;
// this function only decides what it is handed and what happens to a drop column
// afterwards.
func (p *Pipeline) applyRedaction(ctx context.Context, req Request, st *state, rows [][]any) ([]types.ColumnMeta, [][]any, error) {
	metas := make([]types.ColumnMeta, len(st.cols))
	for i, c := range st.cols {
		metas[i] = c.meta
	}

	// G7 needs two facts the Redactor interface does not carry: which
	// connection's token key these columns hash under, and which parameters were
	// resolved from tokens. Without the first, a token column has no key and
	// falls back to redact — masking correctly but destroying the join the token
	// exists to preserve.
	var resolved []ports.ResolvedParam
	for i, pol := range req.ParamPolicies {
		if pol != types.PolicyAllow {
			resolved = append(resolved, ports.ResolvedParam{Ordinal: i + 1, Policy: pol})
		}
	}
	ctx = ports.WithStatement(ctx, ports.Statement{ConnectionID: req.ConnID, Params: resolved})

	transforms, err := p.redactor.Apply(ctx, req.Session.ID, metas, rows)
	if err != nil {
		return nil, nil, err
	}

	st.transforms = make(map[string]types.Transform, len(st.cols))
	keep := make([]int, 0, len(st.cols))
	for i, c := range st.cols {
		t := transforms[c.meta.Name]
		t.Policy = c.entry.Policy
		if t.Basis == "" {
			t.Basis = c.basis
		}
		if c.entry.Policy == types.PolicyToken && t.Namespace == "" {
			t.Namespace = c.entry.Namespace
		}
		if c.entry.Policy == types.PolicyPartial && t.Form == "" {
			t.Form = c.entry.Form
		}
		st.collisions += t.Collisions
		st.transforms[c.meta.Name] = t
		if !c.dropped {
			keep = append(keep, i)
		}
	}

	// A drop column is absent from the response; transforms still reports the
	// removal, which is what tells the agent the column exists and why it is not
	// here (R7.7a).
	outCols := make([]types.ColumnMeta, len(keep))
	for j, i := range keep {
		outCols[j] = st.cols[i].meta
	}
	outRows := make([][]any, len(rows))
	for r, row := range rows {
		nr := make([]any, len(keep))
		for j, i := range keep {
			if i < len(row) {
				nr[j] = row[i]
			}
		}
		outRows[r] = nr
	}
	return outCols, outRows, nil
}

// rowCeiling is the operator bound of §4.5, narrowed by the agent's request and
// by any grant's own ceiling. §9.4: estimated rows are advisory; the actual
// output ceiling is what is enforced.
func (p *Pipeline) rowCeiling(req Request, conn *types.Connection, grants []*types.Grant) int {
	ceiling := conn.Limits.MaxRowsCeiling
	if ceiling <= 0 {
		ceiling = types.DefaultLimits().MaxRowsCeiling
	}
	if req.MaxRows > 0 {
		ceiling = min(ceiling, req.MaxRows)
	}
	for _, g := range grants {
		if g != nil && g.RowCeiling > 0 {
			ceiling = min(ceiling, g.RowCeiling)
		}
	}
	return ceiling
}

// denylisted evaluates R4.5 against the plan's relation list. EXPLAIN expands
// views, so a denylisted base table reached through a view is caught, and a view
// named in statement text is evidence of nothing.
func denylisted(conn *types.Connection, plan *ports.PlanFacts) (types.RelationRef, bool) {
	if plan == nil {
		return types.RelationRef{}, false
	}
	for _, rel := range plan.RelationNames {
		for _, d := range conn.Denylist {
			if d == rel {
				return rel, true
			}
		}
	}
	return types.RelationRef{}, false
}

// denylistedOutput rechecks the denylist against the relations that actually
// produced an output column, using the executed RowDescription rather than the
// plan taken in an earlier transaction.
func (p *Pipeline) denylistedOutput(cat ports.Catalog, conn *types.Connection, cols []column) (types.RelationRef, bool) {
	for _, c := range cols {
		if c.meta.TableOID == 0 {
			continue
		}
		rel, ok := cat.Relation(c.meta.TableOID)
		if !ok {
			continue
		}
		for _, d := range conn.Denylist {
			if d == rel {
				return rel, true
			}
		}
	}
	return types.RelationRef{}, false
}

// inWriteScope is R4.2b: the scope recorded at registration, relation by
// relation and operation by operation.
func inWriteScope(scope []types.WriteScopeEntry, rel types.RelationRef, op types.WriteOp) bool {
	for _, e := range scope {
		if e.Relation != rel {
			continue
		}
		for _, have := range e.Operations {
			if have == op {
				return true
			}
		}
	}
	return false
}

// permissionError is R5.5's middle rule. R6.4a discards every PostgreSQL error
// field, so without this the agent learns only that something was denied and
// retries blindly. The readable column list is composed by keeper from the
// catalog, never copied from the server message, and hidden names stay hidden
// (R6.4b).
func (p *Pipeline) permissionError(ctx context.Context, cat ports.Catalog, plan *ports.PlanFacts) *types.Error {
	var b strings.Builder
	b.WriteString("the database refused this statement for this connection's role")

	if plan != nil {
		for _, rel := range plan.RelationNames {
			readable, err := cat.Readable(ctx, rel)
			if err != nil {
				continue
			}
			visible := readable[:0:0]
			for _, name := range readable {
				if entry, ok := cat.LookupName(rel, name); ok && entry.HideName {
					continue
				}
				visible = append(visible, name)
			}
			if len(visible) == 0 {
				continue
			}
			b.WriteString("; on ")
			b.WriteString(rel.String())
			b.WriteString(" it may read ")
			b.WriteString(strings.Join(visible, ", "))
		}
	}

	return keeperError(types.CodePermissionDenied, b.String(),
		"name only the columns listed, or ask an operator to widen this connection's grants")
}

// egressSummary says what will be masked, in words a human can act on. §9.2.
func egressSummary(cols []column) []string {
	counts := map[types.Policy]int{}
	for _, c := range cols {
		if c.entry.Policy.Masks() || c.dropped {
			counts[c.entry.Policy]++
		}
	}
	var out []string
	for _, pol := range []types.Policy{types.PolicyDrop, types.PolicyRedact, types.PolicyToken, types.PolicyPartial, types.PolicyScan} {
		if n := counts[pol]; n > 0 {
			out = append(out, strconv.Itoa(n)+" "+string(pol))
		}
	}
	return out
}

// publicColumns is what any response surface may show: the resolved metadata,
// minus the columns a drop policy removes.
func publicColumns(cols []column) []types.ColumnMeta {
	out := make([]types.ColumnMeta, 0, len(cols))
	for _, c := range cols {
		if c.dropped {
			continue
		}
		out = append(out, c.meta)
	}
	return out
}

// displayName suppresses a hide_name column's name on every response surface. A
// name hidden in one surface and disclosed in another is not hidden (R6.4b). The
// column keeps its position and its value still follows its own policy, which is
// the difference between hiding a name and dropping a column.
func displayName(name string, index int, hidden bool) string {
	if hidden {
		return "keeper_hidden_" + strconv.Itoa(index+1)
	}
	if name == "" {
		return "column_" + strconv.Itoa(index+1)
	}
	return name
}

// uniqueName keeps the transforms map total. Two output columns can share a
// name — a join projecting both sides' id, say — and a map keyed by name would
// otherwise lose one of them.
func uniqueName(used map[string]bool, name string) string {
	candidate := name
	for n := 2; used[candidate]; n++ {
		candidate = name + "_" + strconv.Itoa(n)
	}
	used[candidate] = true
	return candidate
}
