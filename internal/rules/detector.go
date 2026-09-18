package rules

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
)

// Version is this rule set's version. It travels in [Detector.Identity] so the
// audit log can say which rules examined a row.
const Version = "1"

// Config constructs a [Detector].
type Config struct {
	// Dictionary is the person-name list. Nil uses [SeedDictionary]; use
	// Config{DisableNames: true} to run patterns only.
	Dictionary Dictionary
	// NameThreshold is the minimum score a dictionary hit must reach to be
	// reported. Zero uses DefaultNameThreshold. A lone dictionary word scores
	// below it; a given-plus-family pair scores above.
	NameThreshold float64
	// DisableNames skips the dictionary layer entirely.
	DisableNames bool
}

// DefaultNameThreshold drops lone dictionary words and keeps name pairs. It
// mirrors the 70% threshold SPEC §8.5.1 quotes for go-name-detector.
const DefaultNameThreshold = 0.7

// Detector is the in-process pattern and dictionary pass. It satisfies
// ports.Detector, and it is what SPEC R8.5d means by "patterns and dictionaries
// are a detector": a scan column runs this over every row, redacts the spans it
// returns, and passes through at tier 1.
//
// A Detector is immutable after construction and safe for concurrent use.
type Detector struct {
	names *nameIndex
}

// New builds a Detector.
func New(cfg Config) *Detector {
	d := &Detector{}
	if !cfg.DisableNames {
		dict := cfg.Dictionary
		if dict == nil {
			dict = SeedDictionary()
		}
		th := cfg.NameThreshold
		if th <= 0 {
			th = DefaultNameThreshold
		}
		d.names = newNameIndex(dict, th)
	}
	return d
}

var _ ports.Detector = (*Detector)(nil)

// Scan returns the non-overlapping spans of text that matched, by byte offset,
// each labelled with the type that matched it.
//
// It never returns an error: there is nothing here that can fail, and a
// detector that fails closed by refusing is a detector that blocks statements,
// which R8.5h forbids. The error result exists for sidecar implementations.
func (d *Detector) Scan(ctx context.Context, text string) ([]ports.Span, error) {
	if text == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	spans := scanPatterns(text)
	if d.names != nil {
		spans = append(spans, d.names.scan(text)...)
	}
	return resolveSpans(spans), nil
}

// Identity reports what examined the data and whether it could talk to anyone.
// This detector runs in process and opens no sockets, so the posture is "none"
// (R8.5g).
func (d *Detector) Identity() ports.DetectorIdentity {
	v := Version
	if d.names != nil {
		v += "+names/" + d.names.version
	} else {
		v += "+names/off"
	}
	return ports.DetectorIdentity{
		Name:           "keeper-rules",
		Version:        v,
		NetworkPosture: "none",
	}
}

// MatchRate reports, per identifier type, the fraction of values in which that
// type was detected at least once.
//
// It is the measurement behind R5.3a's proposal bands: at or above 0.9 the
// column is proposed `token` with the matching type as its namespace, between 0
// and 0.9 it is proposed `scan` and the reviewer is shown the rate, and 0 is
// `scan`. The denominator is len(values); the caller decides what to sample and
// excludes NULLs, because "non-null" is a database fact this package cannot see.
//
// Sampling misses rare PII by construction (R8.5b). A rate is evidence for a
// proposal a human reviews, not a classification.
func (d *Detector) MatchRate(values []string) map[string]float64 {
	out := make(map[string]float64, len(Types))
	if len(values) == 0 {
		return out
	}
	counts := make(map[string]int, len(Types))
	for _, v := range values {
		spans, err := d.Scan(context.Background(), v)
		if err != nil {
			continue
		}
		seen := make(map[string]bool, len(spans))
		for _, sp := range spans {
			if !seen[sp.Type] {
				seen[sp.Type] = true
				counts[sp.Type]++
			}
		}
	}
	n := float64(len(values))
	for t, c := range counts {
		out[t] = float64(c) / n
	}
	return out
}

// NameHeuristic is [NameHeuristic] as a method, so a consumer can declare the
// narrow interface it wants and hold a *Detector in it.
func (d *Detector) NameHeuristic(column string) (namespace string, ok bool) {
	return NameHeuristic(column)
}

// Coverage is [Coverage] as a method, for the same reason.
func (d *Detector) Coverage() []CoverageEntry { return Coverage() }
