package detect

import (
	"context"
	"fmt"
	"regexp"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/redact"
)

// TypeCustom labels a list-pass hit that carries no label of its own.
const TypeCustom = "custom"

// List is the list pass: the operator's deny terms, matched case-insensitively
// and exactly wherever they occur in a text, and the operator's expressions.
type List struct {
	m     *redact.Matcher
	exprs []labelled
}

type labelled struct {
	re    *regexp.Regexp
	label string
}

var _ ports.Detector = (*List)(nil)

// NewList builds the list pass over a connection's terms. Expressions are RE2,
// which matches in linear time, so none can stall a statement.
func NewList(t ports.Terms) (*List, error) {
	l := &List{m: redact.NewMatcher(t.Deny, redact.MatcherOptions{Fold: true})}
	for _, p := range t.Patterns {
		re, err := regexp.Compile(p.Expr)
		if err != nil {
			return nil, fmt.Errorf("detect: expression %q: %w", p.Label, err)
		}
		label := p.Label
		if label == "" {
			label = TypeCustom
		}
		l.exprs = append(l.exprs, labelled{re: re, label: label})
	}
	return l, nil
}

// Detect reports every occurrence of a deny term and every expression match.
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
		for _, e := range l.exprs {
			for _, m := range e.re.FindAllStringIndex(t, -1) {
				if m[1] > m[0] {
					spans = append(spans, ports.Span{Start: m[0], End: m[1], Type: e.label, Score: 1})
				}
			}
		}
		out[i] = spans
	}
	return out, nil
}

// Identity reports the pass; its terms and expressions are never part of it.
func (l *List) Identity() ports.DetectorIdentity {
	return ports.DetectorIdentity{Name: "list", NetworkPosture: "none"}
}
