package redact

import (
	"context"
	"time"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// scanState is one statement's free-text examination. Apply collects every
// string its scan columns and scan paths emit, examines them in one call to the
// connection's passes, and then redacts from the answers.
type scanState struct {
	collecting bool
	texts      []string
	seen       map[string]bool
	// results holds an answer for each text a pass examined. A text with no
	// entry was not examined.
	results map[string][]ports.Span
	// cellFailed is set while one cell is being redacted, when any of its text
	// was not examined or carried a span that could not be applied.
	cellFailed bool
}

func (sc *scanState) add(s string) {
	if sc.seen == nil {
		sc.seen = map[string]bool{}
	}
	if !sc.seen[s] {
		sc.seen[s] = true
		sc.texts = append(sc.texts, s)
	}
}

// collect walks every cell that carries free text to examine and gathers its
// strings, without changing a value or minting a token. It reports whether
// anything was gathered.
func (r *Redactor) collect(ctx context.Context, sess *session, plans []columnPlan, rows [][]any, now time.Time) bool {
	if len(plans) == 0 {
		return false
	}
	sc := plans[0].scan
	sc.collecting = true
	defer func() { sc.collecting = false }()
	for _, row := range rows {
		for j := range plans {
			if j >= len(row) {
				break
			}
			pl := &plans[j]
			v := row[j]
			if v == nil || pl.policy == types.PolicyDrop {
				continue
			}
			if len(pl.paths) > 0 {
				if _, ok := r.applyPaths(ctx, sess, pl, v, now); ok {
					continue
				}
			}
			if pl.policy == types.PolicyScan {
				rewriteStrings(v, func(s string) string {
					r.redactSpans(pl, s)
					return s
				})
			}
		}
	}
	return len(sc.texts) > 0
}

// examine runs the connection's passes over every collected text. A connection
// with no passes, a detector that cannot be built and a detector that fails all
// leave the results empty, so every scan cell is redacted whole.
func (r *Redactor) examine(ctx context.Context, connID string, sc *scanState) {
	sc.results = map[string][]ports.Span{}
	if r.detectors == nil || connID == "" {
		return
	}
	det, err := r.detectors(ctx, connID)
	if err != nil || det == nil {
		return
	}
	answers, err := det.Detect(ctx, sc.texts)
	if err != nil {
		return
	}
	for i, t := range sc.texts {
		if i < len(answers) && answers[i] != nil {
			sc.results[t] = answers[i]
		}
	}
}
