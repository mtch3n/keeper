package rules

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// naiveFindAll is the O(patterns × text) reference the automaton must agree
// with. It is the whole point of the test: Aho-Corasick is an optimisation, and
// an optimisation that is not equivalent is a redaction bypass.
func naiveFindAll(text string, patterns []string, fold bool, minLen int) []Match {
	hay := text
	if fold {
		hay = string(foldBytes(text))
	}
	var out []Match
	seen := map[string]bool{}
	for i, p := range patterns {
		np := p
		if fold {
			np = string(foldBytes(p))
		}
		if np == "" || len(np) < minLen {
			continue
		}
		// Duplicates — after folding, which is where they are compared —
		// collapse onto the lowest index, as NewMatcher does.
		if seen[np] {
			continue
		}
		seen[np] = true
		for at := 0; ; {
			k := strings.Index(hay[at:], np)
			if k < 0 {
				break
			}
			s := at + k
			out = append(out, Match{Start: s, End: s + len(np), Pattern: i})
			at = s + 1
		}
	}
	slices.SortFunc(out, func(a, b Match) int {
		if a.End != b.End {
			return a.End - b.End
		}
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return a.Pattern - b.Pattern
	})
	return out
}

func normalizeMatches(in []Match) []Match {
	out := slices.Clone(in)
	slices.SortFunc(out, func(a, b Match) int {
		if a.End != b.End {
			return a.End - b.End
		}
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return a.Pattern - b.Pattern
	})
	return out
}

func TestMatcherAgreesWithNaiveOnRandomInput(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	alphabets := []string{"ab", "abc", "abcd", "abcdefgh"}

	for iter := range 400 {
		alpha := alphabets[iter%len(alphabets)]
		npat := 1 + r.IntN(8)
		patterns := make([]string, npat)
		for i := range patterns {
			n := 1 + r.IntN(5)
			var b strings.Builder
			for range n {
				b.WriteByte(alpha[r.IntN(len(alpha))])
			}
			patterns[i] = b.String()
		}
		var b strings.Builder
		for range 1 + r.IntN(120) {
			b.WriteByte(alpha[r.IntN(len(alpha))])
		}
		text := b.String()

		m := NewMatcher(patterns, MatcherOptions{})
		got := normalizeMatches(m.FindAll(text))
		want := naiveFindAll(text, patterns, false, 0)
		if !slices.Equal(got, want) {
			t.Fatalf("iter %d\npatterns %q\ntext %q\ngot  %v\nwant %v", iter, patterns, text, got, want)
		}
	}
}

func TestMatcherFoldAgreesWithNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 9))
	const alpha = "aAbBcCzZ"
	for iter := range 300 {
		npat := 1 + r.IntN(6)
		patterns := make([]string, npat)
		for i := range patterns {
			n := 2 + r.IntN(4)
			var b strings.Builder
			for range n {
				b.WriteByte(alpha[r.IntN(len(alpha))])
			}
			patterns[i] = b.String()
		}
		var b strings.Builder
		for range 1 + r.IntN(90) {
			b.WriteByte(alpha[r.IntN(len(alpha))])
		}
		text := b.String()

		m := NewMatcher(patterns, MatcherOptions{Fold: true, MinLength: 2})
		got := normalizeMatches(m.FindAll(text))
		want := naiveFindAll(text, patterns, true, 2)
		if !slices.Equal(got, want) {
			t.Fatalf("iter %d\npatterns %q\ntext %q\ngot  %v\nwant %v", iter, patterns, text, got, want)
		}
	}
}

func TestMatcherFoldReportsOriginalOffsets(t *testing.T) {
	// Lowering changes encoded length here: U+0130 is two bytes, its lowercase
	// is one. Offsets must still address the caller's own string.
	text := "xİy" + "ABC" + "z"
	m := NewMatcher([]string{"abc"}, MatcherOptions{Fold: true})
	got := m.FindAll(text)
	if len(got) != 1 {
		t.Fatalf("got %v, want one match", got)
	}
	if text[got[0].Start:got[0].End] != "ABC" {
		t.Errorf("match covers %q, want ABC", text[got[0].Start:got[0].End])
	}
}

func TestMatcherNestedAndOverlapping(t *testing.T) {
	m := NewMatcher([]string{"he", "she", "his", "hers"}, MatcherOptions{})
	got := m.FindAll("ushers")
	want := []Match{
		{Start: 1, End: 4, Pattern: 1}, // she
		{Start: 2, End: 4, Pattern: 0}, // he
		{Start: 2, End: 6, Pattern: 3}, // hers
	}
	if !slices.Equal(normalizeMatches(got), normalizeMatches(want)) {
		t.Fatalf("got %v, want %v", got, want)
	}
	res := ResolveOverlaps(got)
	if len(res) != 1 || res[0].Start != 1 || res[0].End != 4 {
		t.Fatalf("ResolveOverlaps = %v, want leftmost-longest [1,4)", res)
	}
}

func TestMatcherEdgeCases(t *testing.T) {
	var nilM *Matcher
	if nilM.FindAll("abc") != nil || nilM.Len() != 0 {
		t.Error("nil matcher should be inert")
	}
	m := NewMatcher(nil, MatcherOptions{})
	if m.FindAll("abc") != nil || m.Len() != 0 {
		t.Error("empty matcher should be inert")
	}
	m = NewMatcher([]string{"", "ab", "ab"}, MatcherOptions{MinLength: 2})
	if m.Len() != 1 {
		t.Errorf("Len = %d, want 1 (empty dropped, duplicate collapsed)", m.Len())
	}
	got := m.FindAll("abab")
	for _, g := range got {
		if g.Pattern != 1 {
			t.Errorf("duplicate did not collapse onto the lowest index: %v", got)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %v, want two matches", got)
	}
	m = NewMatcher([]string{"abc"}, MatcherOptions{MinLength: 4})
	if m.FindAll("xxabcxx") != nil {
		t.Error("MinLength should drop the pattern entirely")
	}
}
