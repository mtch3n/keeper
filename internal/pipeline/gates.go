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
// the pipeline itself.
const (
	ReasonMasked         = "masked_output"
	ReasonUnresolved     = "unresolved_column"
	ReasonStaleCatalog   = "stale_catalog"
	ReasonWrite          = "write"
	ReasonDDL            = "ddl"
	ReasonWritesOff      = "writes_off"
	ReasonReadsAsk       = "reads_ask"
	ReasonVolatileCall   = "volatile_call"
	ReasonMultiStatement = "multi_statement"
	ReasonSyntax         = "unparseable"
	ReasonPlanTimeout    = "plan_timeout"
	ReasonPermission     = "permission_denied"
)

// column is one output column after G5: the server's identity for it, the policy
// keeper resolved, and where that policy came from.
type column struct {
	meta  types.ColumnMeta
	entry types.ColumnPolicy
	basis types.Basis
}

// resolveColumns is G5. Every output column is matched by the server's own
// (TableOID, AttNum) from RowDescription and never by output name — name
// matching requires enumerating aliases, expressions, function wrappers, views
// and CTEs, which is the failure shape of CVE-2026-85620 (SPEC R7.5a).
func (p *Pipeline) resolveColumns(cat ports.Catalog, raw []types.ColumnMeta, plan *ports.PlanFacts, req Request) []column {
	paramPolicy := types.Strictest(req.paramPolicies()...)
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
			pol, basis := p.inherit(cat, plan, req)
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
			pgdb.TextLike(pgdb.FamilyForName(rc.Type)) {
			if raised := types.Strictest(c.entry.Policy, types.Inherited(paramPolicy)); raised.Rank() > c.entry.Policy.Rank() {
				c.entry = types.ColumnPolicy{Policy: raised}
				c.basis = types.BasisParameter
			}
		}

		c.meta.Policy = c.entry.Policy
		c.meta.Name = uniqueName(used, displayName(rc.Name, i))
		cols[i] = c
	}
	return cols
}

// inherit gives a computed column the strictest non-allow policy among the
// columns that flow out of a scan, as read from the plan. Anything keeper cannot
// follow redacts: a relation the catalog does not know, a function whose body is
// not in the plan, a whole-row reference.
func (p *Pipeline) inherit(cat ports.Catalog, plan *ports.PlanFacts, req Request) (types.Policy, types.Basis) {
	if plan == nil {
		return types.PolicyRedact, types.BasisUnknown
	}
	if plan.Opaque || plan.Uncertain {
		return types.PolicyRedact, types.BasisInherited
	}
	if len(plan.Relations) == 0 {
		// R7.6's last clause. With no relation in scope, only a token-resolved
		// parameter can carry a protected value; constants and pg_catalog
		// functions cannot, so SELECT 1 and SELECT version() come back allow.
		policies := req.paramPolicies()
		if len(policies) == 0 {
			return types.PolicyAllow, types.BasisInherited
		}
		return types.Inherited(types.Strictest(policies...)), types.BasisParameter
	}
	for _, oid := range plan.Relations {
		if _, ok := cat.Relation(oid); !ok {
			return types.PolicyRedact, types.BasisInherited
		}
	}

	strictest := types.PolicyAllow
	for _, f := range plan.Flows {
		entry, ok := cat.Lookup(f.TableOID, f.AttNum)
		switch {
		case !ok:
			entry = types.ColumnPolicy{Policy: types.PolicyRedact}
		case entry.Policy == types.PolicyAllow && len(entry.Paths) > 0:
			entry = types.ColumnPolicy{Policy: types.PolicyRedact}
		}
		strictest = types.Strictest(strictest, entry.Policy)
	}
	// allow, with the tripwires of §5.4 still running in G7. Inherited caps at
	// redact: token under someone else's namespace produces an HMAC where a
	// month label belongs.
	return types.Inherited(strictest), types.BasisInherited
}

// noteColumns records why a result is narrower than the table, so an approver
// or an explain sees it. It does not change the outcome.
func (p *Pipeline) noteColumns(st *state) {
	for _, c := range st.cols {
		switch {
		case c.basis == types.BasisUnknown:
			st.reason(ReasonUnresolved)
		case c.entry.Policy.Masks():
			st.reason(ReasonMasked)
		}
	}
}

// checkFresh runs before G5 so that a table created or altered after the
// catalog opened is resolved and classified by the time its columns are looked
// up. What is still stale after that is noted; it does not change the outcome.
func (p *Pipeline) checkFresh(ctx context.Context, st *state) {
	if st.plan == nil {
		return
	}
	if fresh, err := st.cat.Fresh(ctx, st.plan.Relations); err != nil || !fresh {
		st.degrade("catalog", "fingerprints no longer match the database")
		st.reason(ReasonStaleCatalog)
	}
}

// paramPolicies lists the source policy of each parameter resolved from a token.
func (r Request) paramPolicies() []types.Policy {
	var out []types.Policy
	for _, p := range r.Params {
		if p.Policy != "" {
			out = append(out, p.Policy)
		}
	}
	return out
}

// applyRedaction is G7. Every value leaving keeper passes through the Redactor;
// this function only decides what it is handed.
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
	for i, param := range req.Params {
		if param.Policy != "" && param.Policy != types.PolicyAllow {
			resolved = append(resolved, ports.ResolvedParam{Ordinal: i + 1, Policy: param.Policy})
		}
	}
	detection := types.DetectionScan
	if st.conn != nil {
		detection = st.conn.Detection.Effective()
	}
	ctx = ports.WithStatement(ctx, ports.Statement{ConnectionID: req.ConnID, Params: resolved, Detection: detection})

	transforms, err := p.redactor.Apply(ctx, req.Session.ID, metas, rows)
	if err != nil {
		return nil, nil, err
	}

	st.transforms = make(map[string]types.Transform, len(st.cols))
	unexamined := false
	for _, c := range st.cols {
		t := transforms[c.meta.Name]
		if t.Basis == types.BasisUnexamined && !unexamined {
			unexamined = true
			st.degrade("detector", "free text was not examined, so its cells were redacted whole")
		}
		t.Policy = c.entry.Policy
		// The pipeline decided the policy, so it names the basis. The redactor's
		// own basis survives only where it reports what detection did.
		if t.Basis != types.BasisRules && t.Basis != types.BasisUnexamined {
			t.Basis = c.basis
		}
		if c.entry.Policy == types.PolicyToken && t.Namespace == "" {
			t.Namespace = c.entry.Namespace
		}
		st.collisions += t.Collisions
		st.transforms[c.meta.Name] = t
	}
	return publicColumns(st.cols), rows, nil
}

// rowCeiling is the operator bound of §4.5, narrowed by the agent's request.
// Estimated rows are advisory; the actual output ceiling is what is enforced.
func (p *Pipeline) rowCeiling(req Request, conn *types.Connection) int {
	ceiling := conn.Limits.MaxRowsCeiling
	if ceiling <= 0 {
		ceiling = types.DefaultLimits().MaxRowsCeiling
	}
	if req.MaxRows > 0 {
		ceiling = min(ceiling, req.MaxRows)
	}
	return ceiling
}

// permissionError is R5.5's middle rule. R6.4a discards every PostgreSQL error
// field, so without this the agent learns only that something was denied and
// retries blindly. The readable column list is composed by keeper from the
// catalog, never copied from the server message.
func (p *Pipeline) permissionError(ctx context.Context, cat ports.Catalog, plan *ports.PlanFacts) *types.Error {
	var b strings.Builder
	b.WriteString("the database refused this statement for this connection's role")

	if plan != nil {
		for _, rel := range plan.RelationNames {
			readable, err := cat.Readable(ctx, rel)
			if err != nil {
				continue
			}
			if len(readable) == 0 {
				continue
			}
			b.WriteString("; on ")
			b.WriteString(rel.String())
			b.WriteString(" it may read ")
			b.WriteString(strings.Join(readable, ", "))
		}
	}

	return keeperError(types.CodePermissionDenied, b.String(),
		"name only the columns listed, or ask an operator to widen this connection's grants")
}

// egressSummary says what will be masked, in words a human can act on. §9.2.
func egressSummary(cols []column) []string {
	counts := map[types.Policy]int{}
	for _, c := range cols {
		if c.entry.Policy.Masks() {
			counts[c.entry.Policy]++
		}
	}
	phrase := map[types.Policy]string{
		types.PolicyRedact: "redacted",
		types.PolicyToken:  "tokenized",
		types.PolicyScan:   "scanned for PII spans",
	}
	var out []string
	for _, pol := range []types.Policy{types.PolicyRedact, types.PolicyToken, types.PolicyScan} {
		if n := counts[pol]; n > 0 {
			out = append(out, strconv.Itoa(n)+" column(s) "+phrase[pol])
		}
	}
	return out
}

// publicColumns is what any response surface may show: the resolved metadata.
func publicColumns(cols []column) []types.ColumnMeta {
	out := make([]types.ColumnMeta, len(cols))
	for i, c := range cols {
		out[i] = c.meta
	}
	return out
}

// displayName names an output column that the server left unnamed.
func displayName(name string, index int) string {
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
