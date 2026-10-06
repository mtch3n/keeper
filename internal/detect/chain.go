package detect

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mtchen/keeper/internal/ports"
)

// Chain runs a connection's stages in order. Stages add up and never vote: a
// stage finding nothing says nothing about what another finds, so no stage
// can end the chain early on what it found. The two early returns it does
// take cannot release PII: a later stage receives earlier hits masked, and is
// not sent a text that earlier hits already cover entirely.
type Chain struct {
	stages []ports.Detector
	allow  []string
}

var _ ports.Detector = (*Chain)(nil)

// NewChain builds a chain over stages, in the order the operator listed them.
// A span whose text equals an allow term, ignoring case, is never reported.
func NewChain(stages []ports.Detector, allow []string) *Chain {
	return &Chain{stages: stages, allow: allow}
}

type ranked struct {
	ports.Span
	stage int
}

// text is one input's progress through the chain.
type text struct {
	raw     string
	masked  []byte
	covered []bool
	found   []ranked
	failed  bool
}

func (t *text) fullyCovered() bool {
	for i, r := range t.raw {
		if !t.covered[i] && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// cover masks a span with '*' of the same byte length, so every later stage's
// offsets are offsets into the original text. A span that is out of range or
// splits a character masks nothing; it is still reported, and the caller
// fails the cell closed on it.
func (t *text) cover(sp ports.Span) {
	if sp.Start < 0 || sp.End > len(t.raw) || sp.Start >= sp.End ||
		!utf8.RuneStart(t.raw[sp.Start]) || (sp.End < len(t.raw) && !utf8.RuneStart(t.raw[sp.End])) {
		return
	}
	for i := sp.Start; i < sp.End; i++ {
		t.masked[i] = '*'
		t.covered[i] = true
	}
}

// Detect runs every stage over every text still worth sending it. A text a
// stage was sent and did not answer for is unexamined, and is returned nil.
func (c *Chain) Detect(ctx context.Context, texts []string) ([][]ports.Span, error) {
	ts := make([]*text, len(texts))
	for i, s := range texts {
		ts[i] = &text{raw: s, masked: []byte(s), covered: make([]bool, len(s))}
	}
	for n, stage := range c.stages {
		var send []*text
		var inputs []string
		for _, t := range ts {
			if t.failed || t.fullyCovered() {
				continue
			}
			send = append(send, t)
			if IsRaw(stage) {
				inputs = append(inputs, t.raw)
			} else {
				inputs = append(inputs, string(t.masked))
			}
		}
		if len(send) == 0 {
			continue
		}
		res, err := stage.Detect(ctx, inputs)
		for i, t := range send {
			if err != nil || i >= len(res) || res[i] == nil {
				t.failed = true
				continue
			}
			for _, sp := range res[i] {
				t.found = append(t.found, ranked{sp, n})
			}
			for _, sp := range res[i] {
				t.cover(sp)
			}
		}
	}
	out := make([][]ports.Span, len(ts))
	for i, t := range ts {
		if !t.failed {
			out[i] = merge(c.allowed(t.raw, t.found))
		}
	}
	return out, nil
}

// allowed drops the spans whose text is an allow term. A span that is out of
// range is kept, so the caller sees it and fails closed.
func (c *Chain) allowed(s string, spans []ranked) []ranked {
	return slices.DeleteFunc(spans, func(sp ranked) bool {
		if sp.Start < 0 || sp.End > len(s) || sp.Start >= sp.End {
			return false
		}
		return slices.ContainsFunc(c.allow, func(a string) bool { return strings.EqualFold(a, s[sp.Start:sp.End]) })
	})
}

// merge joins overlapping spans into one covering span, labelled by the
// earliest stage among them.
func merge(spans []ranked) []ports.Span {
	out := []ports.Span{}
	if len(spans) == 0 {
		return out
	}
	slices.SortFunc(spans, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.stage, b.stage))
	})
	cur := spans[0]
	for _, sp := range spans[1:] {
		if sp.Start < cur.End {
			cur.End = max(cur.End, sp.End)
			if sp.stage < cur.stage {
				cur.stage, cur.Type, cur.Score = sp.stage, sp.Type, sp.Score
			}
			continue
		}
		out = append(out, cur.Span)
		cur = sp
	}
	return append(out, cur.Span)
}

// Identity names the stages in order, and reports a network posture of none
// only when every stage has it.
func (c *Chain) Identity() ports.DetectorIdentity {
	names := make([]string, len(c.stages))
	posture := "none"
	for i, s := range c.stages {
		id := s.Identity()
		names[i] = id.Name
		if id.NetworkPosture != "none" {
			posture = id.NetworkPosture
		}
	}
	return ports.DetectorIdentity{Name: strings.Join(names, "+"), NetworkPosture: posture}
}
