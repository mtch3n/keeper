package rules

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/mtchen/keeper/internal/ports"
)

//go:embed data/given_names.txt
var seedGiven string

//go:embed data/family_names.txt
var seedFamily string

// Dictionary supplies the person names the dictionary layer looks for. It is an
// interface because the list that matters is not the one embedded here: SPEC
// §8.5.1 measures go-name-detector's 727k given and 983k family names at 3-9ms
// per detection and roughly 500MB resident, and that list is loaded, not
// compiled in.
type Dictionary interface {
	// Given returns given names, lowercased. Order is irrelevant.
	Given() []string
	// Family returns family names, lowercased.
	Family() []string
	// Version names the list, so the audit log and doctor can say which one ran.
	Version() string
}

type dictionary struct {
	given   []string
	family  []string
	version string
}

func (d *dictionary) Given() []string  { return d.given }
func (d *dictionary) Family() []string { return d.family }
func (d *dictionary) Version() string  { return d.version }

var (
	seedOnce sync.Once
	seedDict *dictionary
)

// SeedDictionary returns the few hundred names embedded in this package.
//
// It exists so the dictionary layer has something to run on a fresh install,
// not because it is adequate. A name absent from it is undetected rather than
// absent, exactly as §8.5.1 says of the whole coverage list. Load a real list
// with [LoadDictionary] or [LoadDictionaryFiles].
func SeedDictionary() Dictionary {
	seedOnce.Do(func() {
		seedDict = &dictionary{
			given:   parseNameList(strings.NewReader(seedGiven)),
			family:  parseNameList(strings.NewReader(seedFamily)),
			version: "seed",
		}
	})
	return seedDict
}

// LoadDictionary reads newline-delimited given and family names. Blank lines
// and lines beginning with '#' are ignored; names are lowercased and deduped.
// Either reader may be nil.
func LoadDictionary(version string, given, family io.Reader) (Dictionary, error) {
	d := &dictionary{version: version}
	if given != nil {
		d.given = parseNameList(given)
	}
	if family != nil {
		d.family = parseNameList(family)
	}
	if len(d.given)+len(d.family) == 0 {
		return nil, fmt.Errorf("rules: dictionary %q is empty", version)
	}
	return d, nil
}

// LoadDictionaryFiles is [LoadDictionary] over two files. An empty path skips
// that half.
func LoadDictionaryFiles(version, givenPath, familyPath string) (Dictionary, error) {
	open := func(p string) (io.ReadCloser, error) {
		if p == "" {
			return nil, nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("rules: open name list: %w", err)
		}
		return f, nil
	}
	g, err := open(givenPath)
	if err != nil {
		return nil, err
	}
	if g != nil {
		defer g.Close()
	}
	f, err := open(familyPath)
	if err != nil {
		return nil, err
	}
	if f != nil {
		defer f.Close()
	}
	var gr, fr io.Reader
	if g != nil {
		gr = g
	}
	if f != nil {
		fr = f
	}
	return LoadDictionary(version, gr, fr)
}

func parseNameList(r io.Reader) []string {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.ToLower(line))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// nameIndex is the dictionary compiled for matching: one automaton over the
// union of both lists, with a per-pattern flag saying which list each came from.
type nameIndex struct {
	matcher   *Matcher
	patterns  []string
	isGiven   []bool
	isFamily  []bool
	threshold float64
	version   string
}

// minNameLength keeps two-letter dictionary entries from matching inside every
// other word. It is a floor on the dictionary layer only.
const minNameLength = 3

func newNameIndex(d Dictionary, threshold float64) *nameIndex {
	if d == nil {
		return nil
	}
	idx := map[string]int{}
	var pats []string
	var given, family []bool
	add := func(n string, g bool) {
		if len(n) < minNameLength {
			return
		}
		i, ok := idx[n]
		if !ok {
			i = len(pats)
			idx[n] = i
			pats = append(pats, n)
			given = append(given, false)
			family = append(family, false)
		}
		if g {
			given[i] = true
		} else {
			family[i] = true
		}
	}
	for _, n := range d.Given() {
		add(n, true)
	}
	for _, n := range d.Family() {
		add(n, false)
	}
	if len(pats) == 0 {
		return nil
	}
	return &nameIndex{
		matcher:   NewMatcher(pats, MatcherOptions{Fold: true, MinLength: minNameLength}),
		patterns:  pats,
		isGiven:   given,
		isFamily:  family,
		threshold: threshold,
		version:   d.Version(),
	}
}

// scan finds person names.
//
// A single dictionary word is weak evidence — "Mark", "Will", "Rose" and "May"
// are all common English words — so a lone hit scores below the default
// threshold and is dropped. A given name immediately followed by a family name
// is the pair that carries the signal, and that is what scores above it. This
// is the same trade go-name-detector makes with its 70% threshold; what this
// package lacks is its 1.7M names, not its shape.
//
// The residual false positive is a sentence that reads like a name — "rose may"
// fires. That redacts a span that did not need it, which is the direction this
// layer is allowed to be wrong in; the other direction is an Invariant B
// failure.
func (n *nameIndex) scan(text string) []ports.Span {
	if n == nil {
		return nil
	}
	raw := n.matcher.FindAll(text)
	if len(raw) == 0 {
		return nil
	}
	// Keep only whole-word hits, longest first at each start.
	var words []Match
	for _, m := range raw {
		if wordBoundary(text, m.Start, m.End) {
			words = append(words, m)
		}
	}
	words = ResolveOverlaps(words)

	var out []ports.Span
	for i := 0; i < len(words); i++ {
		m := words[i]
		score := 0.0
		end := m.End
		switch {
		case n.isGiven[m.Pattern]:
			score = 0.6
		case n.isFamily[m.Pattern]:
			score = 0.5
		}
		if n.isGiven[m.Pattern] && i+1 < len(words) {
			nxt := words[i+1]
			if n.isFamily[nxt.Pattern] && isNameSeparator(text[m.End:nxt.Start]) {
				score = 0.95
				end = nxt.End
				i++
			}
		}
		if score < n.threshold {
			continue
		}
		out = append(out, ports.Span{Start: m.Start, End: end, Type: TypePersonName, Score: score})
	}
	return out
}

// isNameSeparator reports whether the bytes between two dictionary words are
// the kind of gap a person's name spans: one space, or a hyphen or apostrophe.
func isNameSeparator(gap string) bool {
	switch gap {
	case " ", "-", "'", "\u2019", ". ", "\u00a0":
		return true
	}
	return false
}
