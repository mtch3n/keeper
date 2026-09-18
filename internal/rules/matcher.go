package rules

import (
	"slices"
	"sort"
	"unicode"
	"unicode/utf8"
)

// Match is one automaton hit, reported by byte offset into the searched string.
type Match struct {
	Start   int // inclusive
	End     int // exclusive
	Pattern int // index into the pattern slice the Matcher was built from
}

// MatcherOptions configures [NewMatcher].
type MatcherOptions struct {
	// Fold matches case-insensitively by lowering each rune of both the
	// patterns and the haystack. Byte offsets are still reported against the
	// original haystack, which is why the fold keeps a per-byte offset map
	// rather than calling strings.ToLower: lowering can change a rune's encoded
	// length, and an offset computed in the folded copy would be wrong.
	Fold bool
	// MinLength drops patterns shorter than this many bytes (measured after
	// folding). A dropped pattern never matches. Zero keeps every pattern.
	MinLength int
}

// Matcher is an Aho-Corasick automaton over a fixed set of patterns.
//
// It exists because the alternative is quadratic: SPEC R8.4c scans every
// text-like cell leaving keeper for every resolved value in the session's
// reverse map, and R8.5d runs a person-name dictionary over every row of every
// scan column. Both are "many needles, one haystack, once per cell", which is
// exactly the shape Aho-Corasick is for — one linear pass over the cell, no
// matter how many needles.
//
// The automaton is immutable once built and safe for concurrent use.
type Matcher struct {
	// Goto edges, compacted: node s owns childByte[childStart[s]:childStart[s+1]]
	// sorted ascending, with the matching target in childNext at the same index.
	// A map per node costs roughly 50 bytes of overhead per node, which a 1.7M
	// name dictionary cannot afford.
	childStart []int32
	childByte  []byte
	childNext  []int32
	// root is a dense first row: every scan touches it on most bytes.
	root [256]int32

	fail     []int32
	outPat   []int32 // pattern ending exactly at this state, or -1
	dictLink []int32 // nearest failure-chain state with outPat != -1, or -1

	patLen []int32 // folded byte length per input pattern; 0 means dropped
	fold   bool
	count  int // patterns actually in the automaton
}

// NewMatcher builds an automaton over patterns. Pattern indices in a [Match]
// refer to positions in this slice. Empty patterns, and patterns shorter than
// opts.MinLength, are dropped and never match. Duplicate patterns — compared
// after folding, when Fold is set — collapse onto the lowest index.
func NewMatcher(patterns []string, opts MatcherOptions) *Matcher {
	m := &Matcher{fold: opts.Fold, patLen: make([]int32, len(patterns))}

	type node struct{ children map[byte]int32 }
	nodes := []node{{}}
	outPat := []int32{-1}

	for i, p := range patterns {
		var b []byte
		if opts.Fold {
			b = foldBytes(p)
		} else {
			b = []byte(p)
		}
		if len(b) == 0 || len(b) < opts.MinLength {
			continue
		}
		m.patLen[i] = int32(len(b))
		cur := int32(0)
		for _, c := range b {
			nx, ok := nodes[cur].children[c]
			if !ok {
				nodes = append(nodes, node{})
				outPat = append(outPat, -1)
				nx = int32(len(nodes) - 1)
				if nodes[cur].children == nil {
					nodes[cur].children = make(map[byte]int32, 4)
				}
				nodes[cur].children[c] = nx
			}
			cur = nx
		}
		if outPat[cur] == -1 {
			outPat[cur] = int32(i)
		} else {
			// A duplicate pattern: the earlier index already owns this state,
			// and reporting one hit per distinct string is what callers want.
			m.patLen[i] = 0
			continue
		}
		m.count++
	}

	n := len(nodes)
	fail := make([]int32, n)
	dict := make([]int32, n)
	for i := range dict {
		dict[i] = -1
	}

	queue := make([]int32, 0, n)
	for c := range 256 {
		if nx, ok := nodes[0].children[byte(c)]; ok {
			fail[nx] = 0
			queue = append(queue, nx)
		}
	}
	for h := 0; h < len(queue); h++ {
		u := queue[h]
		if outPat[fail[u]] != -1 {
			dict[u] = fail[u]
		} else {
			dict[u] = dict[fail[u]]
		}
		for c, v := range nodes[u].children {
			f := fail[u]
			for {
				nx, ok := nodes[f].children[c]
				if ok && nx != v {
					fail[v] = nx
					break
				}
				if f == 0 {
					fail[v] = 0
					break
				}
				f = fail[f]
			}
			queue = append(queue, v)
		}
	}

	m.fail = fail
	m.outPat = outPat
	m.dictLink = dict
	m.childStart = make([]int32, n+1)
	total := 0
	for i := range nodes {
		total += len(nodes[i].children)
	}
	m.childByte = make([]byte, 0, total)
	m.childNext = make([]int32, 0, total)
	for i := range nodes {
		m.childStart[i] = int32(len(m.childByte))
		keys := make([]byte, 0, len(nodes[i].children))
		for c := range nodes[i].children {
			keys = append(keys, c)
		}
		slices.Sort(keys)
		for _, c := range keys {
			m.childByte = append(m.childByte, c)
			m.childNext = append(m.childNext, nodes[i].children[c])
		}
	}
	m.childStart[n] = int32(len(m.childByte))
	for c := range 256 {
		if nx, ok := nodes[0].children[byte(c)]; ok {
			m.root[c] = nx
		}
	}
	return m
}

// Len reports how many patterns are in the automaton.
func (m *Matcher) Len() int {
	if m == nil {
		return 0
	}
	return m.count
}

func (m *Matcher) child(s int32, c byte) int32 {
	lo, hi := m.childStart[s], m.childStart[s+1]
	bs := m.childByte[lo:hi]
	i := sort.Search(len(bs), func(k int) bool { return bs[k] >= c })
	if i < len(bs) && bs[i] == c {
		return m.childNext[int(lo)+i]
	}
	return -1
}

func (m *Matcher) step(s int32, c byte) int32 {
	for {
		if s == 0 {
			return m.root[c]
		}
		if nx := m.child(s, c); nx >= 0 {
			return nx
		}
		s = m.fail[s]
	}
}

// FindAll returns every occurrence of every pattern in s, including overlapping
// and nested ones, ordered by end offset. Use [ResolveOverlaps] to reduce them
// to a non-overlapping leftmost-longest set.
func (m *Matcher) FindAll(s string) []Match {
	var out []Match
	m.Iterate(s, func(mt Match) bool {
		out = append(out, mt)
		return true
	})
	return out
}

// Iterate calls fn for every occurrence, in order of end offset, until fn
// returns false.
func (m *Matcher) Iterate(s string, fn func(Match) bool) {
	if m == nil || m.count == 0 || s == "" {
		return
	}
	hay := s
	var starts, ends []int32
	if m.fold {
		var buf []byte
		buf, starts, ends = foldWithOffsets(s)
		hay = string(buf)
	}
	state := int32(0)
	for i := range len(hay) {
		state = m.step(state, hay[i])
		for t := state; t != -1; t = m.dictLink[t] {
			p := m.outPat[t]
			if p < 0 {
				continue
			}
			fs := i + 1 - int(m.patLen[p])
			mt := Match{Start: fs, End: i + 1, Pattern: int(p)}
			if starts != nil {
				mt.Start = int(starts[fs])
				mt.End = int(ends[i])
			}
			if !fn(mt) {
				return
			}
		}
	}
}

// ResolveOverlaps reduces matches to a non-overlapping set, preferring the
// leftmost match and, among matches starting at the same offset, the longest.
// The input is not required to be sorted; the result is sorted by Start.
func ResolveOverlaps(in []Match) []Match {
	if len(in) < 2 {
		return in
	}
	s := slices.Clone(in)
	slices.SortStableFunc(s, func(a, b Match) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return b.End - a.End
	})
	out := s[:0]
	last := -1
	for _, mt := range s {
		if mt.Start < last {
			continue
		}
		out = append(out, mt)
		last = mt.End
	}
	return out
}

// foldBytes lowercases s rune by rune. It is the pattern-side half of the fold
// [foldWithOffsets] performs on the haystack, and must stay identical to it.
func foldBytes(s string) []byte {
	buf := make([]byte, 0, len(s))
	var tmp [utf8.UTFMax]byte
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		n := utf8.EncodeRune(tmp[:], unicode.ToLower(r))
		buf = append(buf, tmp[:n]...)
		i += w
	}
	return buf
}

// foldWithOffsets lowercases s rune by rune and, for every byte of the result,
// records the start and end byte offsets of the source rune it came from.
// Lowering changes encoded length for some runes, so an offset taken in the
// folded copy is not an offset in the original; these tables are what make a
// case-insensitive match reportable against the caller's own string.
func foldWithOffsets(s string) (buf []byte, starts, ends []int32) {
	buf = make([]byte, 0, len(s))
	starts = make([]int32, 0, len(s))
	ends = make([]int32, 0, len(s))
	var tmp [utf8.UTFMax]byte
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		n := utf8.EncodeRune(tmp[:], unicode.ToLower(r))
		buf = append(buf, tmp[:n]...)
		for range n {
			starts = append(starts, int32(i))
			ends = append(ends, int32(i+w))
		}
		i += w
	}
	return buf, starts, ends
}
