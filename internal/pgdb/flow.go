package pgdb

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// flowReader reads which relation columns can reach a statement's result from
// the Output lists of EXPLAIN (VERBOSE). It exists because a computed column has
// no (tableOID, attnum) to match: its policy is the strictest one among the
// columns that flow into it. A false positive here only masks more.
//
// A scan directly under a node that only passes tuples on is skipped: the server
// gives such a scan every column of the relation as its Output (a physical
// tlist), while the node above lists only what it needs. Anywhere else the
// scan's own Output counts, because a node that renames columns or holds a
// sub-plan result hides the column name from the lines above it.
type flowReader struct {
	scans  []flowScan
	reads  []string
	opaque bool
}

type flowScan struct {
	rel   relRef
	alias string
}

// passThrough are node types whose Output names the child columns it keeps, so
// the child's own Output adds nothing to it.
var passThrough = map[string]bool{
	"Aggregate": true, "Sort": true, "Incremental Sort": true, "Hash Join": true,
	"Merge Join": true, "Nested Loop": true, "Hash": true, "Materialize": true,
	"Memoize": true, "Limit": true, "Gather": true, "Gather Merge": true,
	"Unique": true, "Group": true, "WindowAgg": true, "ProjectSet": true,
	"LockRows": true, "SetOp": true, "Result": true,
}

// understood are the node types without a relation that the walk can read.
var understood = map[string]bool{
	"Append": true, "Merge Append": true, "Recursive Union": true,
	"BitmapAnd": true, "BitmapOr": true, "Bitmap Index Scan": true,
	"Subquery Scan": true, "CTE Scan": true, "WorkTable Scan": true,
	"Function Scan": true,
}

func (f *flowReader) node(n, parent *planNode) {
	isScan := n.RelationName != "" && n.NodeType != "ModifyTable"
	if n.RelationName != "" {
		f.scans = append(f.scans, flowScan{
			rel:   relRef{schema: n.Schema, name: n.RelationName},
			alias: cmp.Or(n.Alias, n.RelationName),
		})
	}
	readable := n.RelationName != "" || passThrough[n.NodeType] || understood[n.NodeType] ||
		((n.NodeType == "Foreign Scan" || n.NodeType == "Custom Scan") && len(n.Output) > 0)
	if !readable {
		f.opaque = true
	}
	if isScan && parent != nil && passThrough[parent.NodeType] &&
		(n.ParentRel == "Outer" || n.ParentRel == "Inner") {
		return
	}
	for _, o := range n.Output {
		if len(o) > maxScannedPlan {
			f.opaque = true
			continue
		}
		f.reads = append(f.reads, o)
	}
	if n.FunctionCall != "" {
		f.reads = append(f.reads, n.FunctionCall)
	}
}

// identifier is a bare or quoted name; the quoted form comes first so a quote
// is never read as the start of a literal. A string literal is matched so its
// content is skipped.
var identifier = regexp.MustCompile(`'(?:[^']|'')*'|"(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_$]*`)

type nameChain struct {
	parts    []string
	wholeRow bool
}

// chains splits a deparsed expression into dotted names. A literal ends a chain
// and is dropped. wholeRow is set when the chain ends in ".*".
func chains(expr string) []nameChain {
	var out []nameChain
	end := -1
	for _, loc := range identifier.FindAllStringIndex(expr, -1) {
		tok := expr[loc[0]:loc[1]]
		if tok[0] == '\'' {
			end = -1
			continue
		}
		if tok[0] == '"' {
			tok = strings.ReplaceAll(tok[1:len(tok)-1], `""`, `"`)
		}
		if end >= 0 && end+1 == loc[0] && expr[end] == '.' {
			last := &out[len(out)-1]
			last.parts = append(last.parts, tok)
		} else {
			out = append(out, nameChain{parts: []string{tok}})
		}
		end = loc[1]
		if strings.HasPrefix(expr[end:], ".*") {
			out[len(out)-1].wholeRow = true
		}
	}
	return out
}

// result returns the columns that flow, as names for resolveColumns, and
// whether the plan has a whole-row reference or a part the walk cannot read.
func (f *flowReader) result() ([]colRef, bool) {
	opaque := f.opaque
	aliases := map[string]bool{}
	for _, s := range f.scans {
		aliases[s.alias] = true
	}
	var out []colRef
	add := func(c colRef) {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	for _, r := range f.reads {
		for _, ch := range chains(r) {
			name := ch.parts[len(ch.parts)-1]
			qualifier := ""
			if len(ch.parts) > 1 {
				qualifier = ch.parts[len(ch.parts)-2]
			}
			if ch.wholeRow || (len(ch.parts) == 1 && aliases[name]) {
				opaque = true
			}
			if ch.wholeRow {
				continue
			}
			known := aliases[qualifier]
			for _, s := range f.scans {
				if !known || s.alias == qualifier {
					add(colRef{rel: s.rel, column: name})
				}
			}
		}
	}
	return out, opaque
}
