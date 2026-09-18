package pgdb

import (
	"cmp"
	"context"
	"encoding/json/v2"
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
	// all of them at tier 4 (SPEC R4.2c).
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
	PlanRows     float64    `json:"Plan Rows"`
	TotalCost    float64    `json:"Total Cost"`
	Filter       string     `json:"Filter"`
	IndexCond    string     `json:"Index Cond"`
	RecheckCond  string     `json:"Recheck Cond"`
	JoinFilter   string     `json:"Join Filter"`
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
	rows      int64
	cost      float64
}

func walkPlan(root *planNode) walkResult {
	var w walkResult
	w.rows = int64(root.PlanRows)
	w.cost = root.TotalCost

	seenRel := map[relRef]bool{}
	seenTarget := map[relRef]bool{}

	var visit func(n *planNode, depth int)
	visit = func(n *planNode, depth int) {
		if n.filtered() {
			w.filtered = true
		}
		if n.RelationName != "" {
			r := relRef{schema: n.Schema, name: n.RelationName}
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
		if n.NodeType == "ModifyTable" && depth == 0 && w.rows == 0 {
			for i := range n.Plans {
				w.rows = max(w.rows, int64(n.Plans[i].PlanRows))
			}
		}
		for i := range n.Plans {
			visit(&n.Plans[i], depth+1)
		}
	}
	visit(root, 0)
	return w
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

	w := walkPlan(&roots[0].Plan)

	facts := &ports.PlanFacts{
		EstimatedRows: w.rows,
		EstimatedCost: w.cost,
		HasFilter:     w.filtered,
		Writes:        len(w.targets) > 0,
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
		// preview, so it joins DDL at tier 4.
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

	names, oids, err := db.resolveRelations(ctx, c, ordered)
	if err != nil {
		return nil, err
	}
	facts.RelationNames = names
	facts.Relations = oids
	return facts, nil
}

// resolveRelationsSQL turns the plan's relation names into the identities the
// catalog and the denylist are keyed by, and adds every inheritance and
// partition ancestor. A denylist entry naming a partitioned parent has to catch
// a plan that only ever names leaf partitions.
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
