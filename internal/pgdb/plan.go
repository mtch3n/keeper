package pgdb

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"regexp"
	"slices"
	"strings"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Statement types keeper distinguishes. They are the values internal/pgdb puts
// in ports.PlanFacts.StatementType and the values internal/pipeline routes on.
const (
	StmtSelect = "SELECT"
	StmtInsert = "INSERT"
	StmtUpdate = "UPDATE"
	StmtDelete = "DELETE"
	StmtMerge  = "MERGE"
	// StmtDDL is every statement PostgreSQL's own grammar will not plan —
	// CREATE, ALTER, DROP, GRANT, TRUNCATE, DECLARE, SET and the rest — plus the
	// plannable ones that produce neither rows nor a ModifyTable node, which is
	// what CREATE TABLE AS and SELECT INTO look like from here. keeper refuses
	// all of them (SPEC R4.2c).
	StmtDDL = "DDL"
)

// explainPrefix is prepended to the agent's statement. Prefixing is the whole
// technique: nothing reads, splits or rewrites the statement text (R7.2), and
// FORMAT JSON with VERBOSE is what supplies schema-qualified relation names.
// ANALYZE appears nowhere in this package — CONTRACT §4 rule 3, SPEC R7.4b.
const explainPrefix = "EXPLAIN (VERBOSE, COSTS, FORMAT JSON) "

type planNode struct {
	NodeType     string     `json:"Node Type"`
	Operation    string     `json:"Operation"`
	RelationName string     `json:"Relation Name"`
	Schema       string     `json:"Schema"`
	Alias        string     `json:"Alias"`
	PlanRows     float64    `json:"Plan Rows"`
	TotalCost    float64    `json:"Total Cost"`
	Filter       string     `json:"Filter"`
	IndexCond    string     `json:"Index Cond"`
	RecheckCond  string     `json:"Recheck Cond"`
	JoinFilter   string     `json:"Join Filter"`
	Output       []string   `json:"Output"`
	ParentRel    string     `json:"Parent Relationship"`
	FunctionName string     `json:"Function Name"`
	FunctionCall string     `json:"Function Call"`
	HashCond     string     `json:"Hash Cond"`
	MergeCond    string     `json:"Merge Cond"`
	Plans        []planNode `json:"Plans"`
}

type explainRoot struct {
	Plan planNode `json:"Plan"`
}

func (n *planNode) filtered() bool {
	return n.Filter != "" || n.IndexCond != "" || n.RecheckCond != "" ||
		n.JoinFilter != "" || n.HashCond != "" || n.MergeCond != ""
}

func (n *planNode) conditions() []string {
	return []string{n.Filter, n.IndexCond, n.RecheckCond, n.JoinFilter, n.HashCond, n.MergeCond}
}

// relRef is one relation named by the plan, before OID resolution. Schema is
// empty only when the server omitted it, in which case resolution falls back to
// the session search_path.
type relRef struct {
	schema string
	name   string
}

// walk collects, in one pass: every relation the plan touches, every relation a
// ModifyTable node writes, whether any node filters, and the operation.
type walkResult struct {
	relations []relRef
	targets   []relRef
	operation string
	filtered  bool
	// filters are the relation columns the plan's conditions name.
	filters []colRef
	rows    int64
	cost    float64
	// flows are the relation columns whose values can reach the result, and
	// opaque is set when the plan has a part this walk cannot read. See flow.go.
	flows     []colRef
	opaque    bool
	functions []string
}

func walkPlan(roots []explainRoot) walkResult {
	var w walkResult
	w.rows = int64(roots[0].Plan.PlanRows)
	w.cost = roots[0].Plan.TotalCost

	seenRel := map[relRef]bool{}
	seenTarget := map[relRef]bool{}
	aliases := map[string]relRef{}
	var conds []string
	var fl flowReader

	var visit func(n, parent *planNode, depth int)
	visit = func(n, parent *planNode, depth int) {
		fl.node(n, parent)
		if n.FunctionName != "" {
			w.functions = append(w.functions, n.FunctionName)
		}
		if n.filtered() {
			w.filtered = true
			conds = append(conds, n.conditions()...)
		}
		if n.RelationName != "" {
			r := relRef{schema: n.Schema, name: n.RelationName}
			aliases[cmp.Or(n.Alias, n.RelationName)] = r
			if !seenRel[r] {
				seenRel[r] = true
				w.relations = append(w.relations, r)
			}
			if n.NodeType == "ModifyTable" && !seenTarget[r] {
				seenTarget[r] = true
				w.targets = append(w.targets, r)
			}
		}
		if n.NodeType == "ModifyTable" && n.Operation != "" && w.operation == "" {
			w.operation = strings.ToUpper(n.Operation)
		}
		// A ModifyTable with no RETURNING estimates zero rows; the count a human
		// needs on the approval screen is the rows feeding it.
		if n.NodeType == "ModifyTable" && depth == 0 && w.rows == 0 && n == &roots[0].Plan {
			for i := range n.Plans {
				w.rows = max(w.rows, int64(n.Plans[i].PlanRows))
			}
		}
		for i := range n.Plans {
			visit(&n.Plans[i], n, depth+1)
		}
	}
	for i := range roots {
		visit(&roots[i].Plan, nil, 0)
	}
	w.flows, w.opaque = fl.result()
	for _, cond := range conds {
		for _, q := range qualifiedNames(cond) {
			if r, ok := aliases[q[0]]; ok {
				if c := (colRef{rel: r, column: q[1]}); !slices.Contains(w.filters, c) {
					w.filters = append(w.filters, c)
				}
			}
		}
	}
	return w
}

// colRef is one column a plan condition names, before resolution.
type colRef struct {
	rel    relRef
	column string
}

// qualifiedName is alias.column in a VERBOSE plan condition, either part
// quoted. A name followed by "(" is a schema-qualified function, not a column.
var qualifiedName = regexp.MustCompile(`(?:"((?:[^"]|"")+)"|([A-Za-z_][A-Za-z0-9_$]*))\.(?:"((?:[^"]|"")+)"|([A-Za-z_][A-Za-z0-9_$]*))(\s*\()?`)

// literal is a quoted string constant, removed before names are read so a
// value like 'users.ssn' is not taken for a column.
var literal = regexp.MustCompile(`'(?:[^']|'')*'`)

// qualifiedNames returns the [qualifier, column] pairs a condition names.
func qualifiedNames(cond string) [][2]string {
	var out [][2]string
	for _, m := range qualifiedName.FindAllStringSubmatch(literal.ReplaceAllString(cond, "''"), -1) {
		if m[5] != "" {
			continue
		}
		unquote := func(quoted, bare string) string {
			if quoted != "" {
				return strings.ReplaceAll(quoted, `""`, `"`)
			}
			return bare
		}
		out = append(out, [2]string{unquote(m[1], m[2]), unquote(m[3], m[4])})
	}
	return out
}

// explain runs EXPLAIN inside the caller's transaction — the same BEGIN READ
// ONLY and the same SET LOCAL statement_timeout as execution (SPEC R7.4a).
// Constant folding evaluates IMMUTABLE functions at plan time, so a mislabelled
// function executes during what looks like a dry run; the envelope is what
// bounds it.
func (db *DB) explain(ctx context.Context, c *conn, sql string, params []ports.Param, hasOutput bool) (*ports.PlanFacts, error) {
	var raw []byte
	row := c.tx.QueryRow(ctx, explainPrefix+sql, args(params)...)
	if err := row.Scan(&raw); err != nil {
		if notExplainable(err) {
			// PostgreSQL's grammar refused to plan it. That is the server's own
			// answer to "is this a utility statement", and it is the only one
			// keeper uses. The failed statement aborted the transaction, so the
			// envelope has to roll back rather than commit.
			c.aborted = true
			return &ports.PlanFacts{StatementType: StmtDDL}, nil
		}
		return nil, convert(err)
	}

	var roots []explainRoot
	if err := json.Unmarshal(raw, &roots); err != nil || len(roots) == 0 {
		return nil, keeperError(types.CodeInternal)
	}

	w := walkPlan(roots)
	volatile, foreign := callsUnsafe(ctx, c, raw, w.functions)

	facts := &ports.PlanFacts{
		EstimatedRows: w.rows,
		EstimatedCost: w.cost,
		HasFilter:     w.filtered,
		Writes:        len(w.targets) > 0,
		Uncertain:     volatile,
		Opaque:        w.opaque || foreign,
	}

	switch {
	case len(w.targets) > 1:
		// Two ModifyTable targets means a data-modifying CTE wrapping another
		// write. R4.2b's scope check has exactly one relation to name, and
		// ports.PlanFacts has one place to put it, so keeper does not accept the
		// shape rather than under-checking it.
		return nil, keeperError(types.CodeMultiStatement)
	case len(w.targets) == 1:
		facts.StatementType = cmp.Or(w.operation, StmtUpdate)
	case hasOutput:
		facts.StatementType = StmtSelect
	default:
		// Plannable, but it returns no rows and modifies no relation: CREATE
		// TABLE AS, SELECT INTO, DECLARE. Not a read and not a write keeper can
		// preview, so it joins DDL.
		return &ports.PlanFacts{StatementType: StmtDDL}, nil
	}

	// Write targets lead the relation list. ports.PlanFacts has no separate
	// field for them and is frozen; internal/pipeline reads RelationNames[0] as
	// the write target of a writing plan, which is sound because the case of
	// more than one target is refused above.
	ordered := slices.Clone(w.targets)
	for _, r := range w.relations {
		if !slices.Contains(ordered, r) {
			ordered = append(ordered, r)
		}
	}

	if len(ordered) == 0 && !facts.Opaque {
		// With no relation in scope the pipeline allows a computed column
		// (R7.6), so nothing may hide in a constant the planner made.
		facts.Opaque = foldable(ctx, c)
	}
	names, oids, err := db.resolveRelations(ctx, c, ordered)
	if err != nil {
		return nil, err
	}
	facts.RelationNames = names
	facts.Relations = oids
	if facts.Filters, err = resolveColumns(ctx, c, w.filters); err != nil {
		return nil, err
	}
	if facts.Flows, err = resolveColumns(ctx, c, w.flows); err != nil {
		return nil, err
	}
	return facts, nil
}

// callName is a call-shaped name in the plan's JSON text: a bare name or an
// escaped-quoted one, directly followed by an opening parenthesis. A literal
// that looks like a call matches too, which can only add a question.
var callName = regexp.MustCompile(`(?:\\"([^"\\]+)\\"|([A-Za-z_][A-Za-z0-9_$]*))\(`)

// callsSQL answers two questions about the function names a plan calls: is any
// of them volatile, and is any of them defined outside pg_catalog. A body in a
// user schema is not in the plan, so a column computed by one can carry data
// from a relation the plan never names.
// maxScannedPlan bounds the plan text the call and flow readers will scan.
const maxScannedPlan = 1 << 20

const callsSQL = `SELECT coalesce(bool_or(provolatile = 'v'), false),
       coalesce(bool_or(pronamespace <> 'pg_catalog'::regnamespace), false)
  FROM pg_proc WHERE lower(proname) = ANY($1)`

// operatorCall is a schema-qualified operator, which the server prints only for
// an operator outside pg_catalog.
var operatorCall = regexp.MustCompile(`OPERATOR\(`)

// callsUnsafe reports whether the plan calls a volatile function or one outside
// pg_catalog. keeper reads no statement text for this. A failed lookup counts as
// both.
func callsUnsafe(ctx context.Context, c *conn, planJSON []byte, functions []string) (volatile, foreign bool) {
	if len(planJSON) > maxScannedPlan {
		// A plan this large holds a folded constant. Scanning it for call names
		// costs more than the statement, and nothing can be said of it anyway.
		return true, true
	}
	foreign = operatorCall.Match(planJSON)
	names := functions
	for _, m := range callName.FindAllSubmatch(planJSON, -1) {
		names = append(names, cmp.Or(string(m[1]), string(m[2])))
	}
	for i := range names {
		names[i] = strings.ToLower(names[i])
	}
	if len(names) == 0 {
		return false, foreign
	}
	// A savepoint, because a failed lookup (a role that cannot read pg_proc)
	// would otherwise abort the statement's own transaction.
	sp, err := c.tx.Begin(ctx)
	if err != nil {
		return true, true
	}
	var v, f bool
	if err := sp.QueryRow(ctx, callsSQL, names).Scan(&v, &f); err != nil {
		_ = sp.Rollback(ctx)
		return true, true
	}
	if sp.Commit(ctx) != nil {
		return true, true
	}
	return v, foreign || f
}

// foldableSQL finds an IMMUTABLE function outside pg_catalog and
// information_schema that the session role may call. PostgreSQL runs such a
// call while it plans when its arguments are constants, so EXPLAIN shows the
// result as a constant and names neither the function nor what it read: a
// function declared IMMUTABLE that reads a table puts that table's values into
// a column the plan says came from nowhere. Extension functions are not exempt.
const foldableSQL = `SELECT EXISTS (
  SELECT 1 FROM pg_proc
   WHERE provolatile = 'i'
     AND pronamespace NOT IN ('pg_catalog'::regnamespace, 'information_schema'::regnamespace)
     AND has_function_privilege(oid, 'EXECUTE'))`

// foldable reports whether a constant in this plan could be a value a
// planner-time call read. A failed lookup counts as yes.
func foldable(ctx context.Context, c *conn) bool {
	sp, err := c.tx.Begin(ctx)
	if err != nil {
		return true
	}
	var found bool
	if err := sp.QueryRow(ctx, foldableSQL).Scan(&found); err != nil {
		_ = sp.Rollback(ctx)
		return true
	}
	if sp.Commit(ctx) != nil {
		return true
	}
	return found
}

// resolveColumnsSQL turns (schema, relation, column) names into the
// (tableOID, attnum) identities the catalog is keyed by.
const resolveColumnsSQL = `
SELECT q.s, q.r, q.col, a.attrelid::int8, a.attnum::int4
FROM unnest($1::text[], $2::text[], $3::text[]) AS q(s, r, col)
JOIN pg_attribute a
  ON a.attrelid = to_regclass(CASE WHEN q.s = '' THEN format('%I', q.r) ELSE format('%I.%I', q.s, q.r) END)
 AND a.attname = q.col AND a.attnum > 0 AND NOT a.attisdropped`

func resolveColumns(ctx context.Context, c *conn, refs []colRef) ([]ports.ColumnRef, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	schemas, rels, cols := make([]string, len(refs)), make([]string, len(refs)), make([]string, len(refs))
	for i, r := range refs {
		schemas[i], rels[i], cols[i] = r.rel.schema, r.rel.name, r.column
	}
	rows, err := c.tx.Query(ctx, resolveColumnsSQL, schemas, rels, cols)
	if err != nil {
		return nil, convert(err)
	}
	defer rows.Close()
	var out []ports.ColumnRef
	for rows.Next() {
		var schema, rel, col string
		var oid int64
		var att int32
		if err := rows.Scan(&schema, &rel, &col, &oid, &att); err != nil {
			return nil, convert(err)
		}
		out = append(out, ports.ColumnRef{
			TableOID: uint32(oid), AttNum: uint16(att),
			Relation: types.RelationRef{Schema: schema, Relation: rel}, Column: col,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, convert(err)
	}
	return out, nil
}

// resolveRelationsSQL turns the plan's relation names into the identities the
// catalog is keyed by, and adds every inheritance and
// partition ancestor, so a plan that only names leaf partitions still carries
// the parent.
const resolveRelationsSQL = `
WITH RECURSIVE q(s, r) AS (
    SELECT * FROM unnest($1::text[], $2::text[])
), base AS (
    SELECT c.oid
    FROM q
    JOIN pg_class c ON c.relname = q.r
    JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE (q.s <> '' AND n.nspname = q.s)
       OR (q.s = '' AND pg_table_is_visible(c.oid))
), anc AS (
    SELECT oid FROM base
    UNION
    SELECT i.inhparent FROM pg_inherits i JOIN anc ON i.inhrelid = anc.oid
)
SELECT c.oid::int8, n.nspname, c.relname
FROM anc
JOIN pg_class c ON c.oid = anc.oid
JOIN pg_namespace n ON n.oid = c.relnamespace`

func (db *DB) resolveRelations(ctx context.Context, c *conn, refs []relRef) ([]types.RelationRef, []uint32, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	schemas := make([]string, len(refs))
	names := make([]string, len(refs))
	for i, r := range refs {
		schemas[i] = r.schema
		names[i] = r.name
	}

	rows, err := c.tx.Query(ctx, resolveRelationsSQL, schemas, names)
	if err != nil {
		return nil, nil, convert(err)
	}
	defer rows.Close()

	type resolved struct {
		oid uint32
		ref types.RelationRef
	}
	byName := map[relRef]resolved{}
	var order []relRef
	for rows.Next() {
		var oid int64
		var schema, name string
		if err := rows.Scan(&oid, &schema, &name); err != nil {
			return nil, nil, convert(err)
		}
		k := relRef{schema: schema, name: name}
		byName[k] = resolved{oid: uint32(oid), ref: types.RelationRef{Schema: schema, Relation: name}}
		order = append(order, k)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, convert(err)
	}

	var outRefs []types.RelationRef
	var outOIDs []uint32
	emit := func(k relRef) {
		r, ok := byName[k]
		if !ok || slices.Contains(outOIDs, r.oid) {
			return
		}
		outRefs = append(outRefs, r.ref)
		outOIDs = append(outOIDs, r.oid)
	}
	// Plan order first, so a write target stays at index 0.
	for _, r := range refs {
		if r.schema != "" {
			emit(r)
			continue
		}
		for _, k := range order {
			if k.name == r.name {
				emit(k)
			}
		}
	}
	// Then the ancestors the recursive term added.
	for _, k := range order {
		emit(k)
	}
	return outRefs, outOIDs, nil
}

// args turns bound parameters into the driver's argument list. ports.Param never
// carries a token: the Redactor resolves one before it reaches the Executor.
func args(params []ports.Param) []any {
	out := make([]any, len(params))
	for i, p := range params {
		out[i] = p.Value
	}
	return out
}
