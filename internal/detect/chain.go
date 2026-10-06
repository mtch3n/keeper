package detect

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/mtchen/keeper/internal/ports"
)

// Chain runs a connection's passes in order over the same texts. Passes add up
// and never vote: a pass finding nothing says nothing about what another finds.
type Chain struct {
	passes []ports.Detector
	allow  []string
}

var _ ports.Detector = (*Chain)(nil)

// NewChain builds a chain over passes, in the order the operator listed them.
// A span whose text equals an allow term, ignoring case, is never reported.
func NewChain(passes []ports.Detector, allow []string) *Chain {
	return &Chain{passes: passes, allow: allow}
}

type ranked struct {
	ports.Span
	pass int
}

// Detect runs every pass over every text. A text any pass did not examine is
// left unexamined, because the other passes' answers alone cannot release it.
func (c *Chain) Detect(ctx context.Context, texts []string) ([][]ports.Span, error) {
	found := make([][]ranked, len(texts))
	examined := make([]bool, len(texts))
	for i := range examined {
		examined[i] = true
	}
	for p, pass := range c.passes {
		res, err := pass.Detect(ctx, texts)
		if err != nil {
			return nil, err
		}
		for i := range texts {
			if i >= len(res) || res[i] == nil {
				examined[i] = false
				continue
			}
			for _, sp := range res[i] {
				found[i] = append(found[i], ranked{sp, p})
			}
		}
	}
	out := make([][]ports.Span, len(texts))
	for i, t := range texts {
		if examined[i] {
			out[i] = merge(c.allowed(t, found[i]))
		}
	}
	return out, nil
}

// allowed drops the spans whose text is an allow term. A span that is out of
// range is kept, so the caller sees it and fails closed.
func (c *Chain) allowed(text string, spans []ranked) []ranked {
	return slices.DeleteFunc(spans, func(sp ranked) bool {
		if sp.Start < 0 || sp.End > len(text) || sp.Start >= sp.End {
			return false
		}
		return slices.ContainsFunc(c.allow, func(a string) bool { return strings.EqualFold(a, text[sp.Start:sp.End]) })
	})
}

// merge joins overlapping spans into one covering span, labelled by the
// earliest pass among them.
func merge(spans []ranked) []ports.Span {
	out := []ports.Span{}
	if len(spans) == 0 {
		return out
	}
	slices.SortFunc(spans, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.pass, b.pass))
	})
	cur := spans[0]
	for _, sp := range spans[1:] {
		if sp.Start < cur.End {
			cur.End = max(cur.End, sp.End)
			if sp.pass < cur.pass {
				cur.pass, cur.Type, cur.Score = sp.pass, sp.Type, sp.Score
			}
			continue
		}
		out = append(out, cur.Span)
		cur = sp
	}
	return append(out, cur.Span)
}

// Identity names the passes in order, and reports a network posture of none
// only when every pass has it.
func (c *Chain) Identity() ports.DetectorIdentity {
	names := make([]string, len(c.passes))
	posture := "none"
	for i, p := range c.passes {
		id := p.Identity()
		names[i] = id.Name
		if id.NetworkPosture != "none" {
			posture = id.NetworkPosture
		}
	}
	return ports.DetectorIdentity{Name: strings.Join(names, "+"), NetworkPosture: posture}
}
