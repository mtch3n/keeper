package detect

import (
	"context"
	"strings"

	"github.com/hoophq/alcatraz"

	"github.com/mtchen/keeper/internal/ports"
)

// alcatrazVersion is the pinned module version, reported as the pass's identity.
const alcatrazVersion = "v0.21.0"

// Patterns is the patterns pass: Alcatraz's pattern recognizers, checksum
// validators and context words, in process, with no model and no network.
type Patterns struct {
	engine *alcatraz.Engine
}

var _ ports.Detector = (*Patterns)(nil)

// NewPatterns builds the patterns pass. The engine is immutable once built and
// shared by every connection.
func NewPatterns() *Patterns {
	return &Patterns{engine: alcatraz.NewEngine()}
}

// Detect reports every hit at any score: there is no threshold, because for
// masking a missed identifier costs more than an over-redacted one.
func (p *Patterns) Detect(ctx context.Context, texts []string) ([][]ports.Span, error) {
	out := make([][]ports.Span, len(texts))
	for i, t := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spans := []ports.Span{}
		for _, r := range p.engine.Analyze(t, alcatraz.Options{}) {
			spans = append(spans, ports.Span{Start: r.Start, End: r.End, Type: strings.ToLower(r.EntityType), Score: r.Score})
		}
		out[i] = spans
	}
	return out, nil
}

// Identity names the library and its version.
func (p *Patterns) Identity() ports.DetectorIdentity {
	return ports.DetectorIdentity{Name: "patterns/alcatraz", Version: alcatrazVersion, NetworkPosture: "none"}
}
