package detect

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/redact"
)

// TypeCustom labels a list-pass hit.
const TypeCustom = "custom"

// List is the list pass: the operator's deny terms, matched case-insensitively
// and exactly wherever they occur in a text.
type List struct {
	m *redact.Matcher
}

var _ ports.Detector = (*List)(nil)

// NewList builds the list pass over deny terms.
func NewList(deny []string) *List {
	return &List{m: redact.NewMatcher(deny, redact.MatcherOptions{Fold: true})}
}

// Detect reports every occurrence of a deny term.
func (l *List) Detect(ctx context.Context, texts []string) ([][]ports.Span, error) {
	out := make([][]ports.Span, len(texts))
	for i, t := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spans := []ports.Span{}
		for _, m := range l.m.FindAll(t) {
			spans = append(spans, ports.Span{Start: m.Start, End: m.End, Type: TypeCustom, Score: 1})
		}
		out[i] = spans
	}
	return out, nil
}

// Identity reports the pass; its terms are never part of it.
func (l *List) Identity() ports.DetectorIdentity {
	return ports.DetectorIdentity{Name: "list", NetworkPosture: "none"}
}
