package detect

import (
	"context"
	"slices"

	"github.com/mtchen/keeper/internal/ports"
)

// only keeps a stage's hits of the listed entity types.
type only struct {
	ports.Detector
	entities []string
}

// Only wraps a stage so it reports only hits of the listed entity types.
func Only(d ports.Detector, entities []string) ports.Detector {
	return only{Detector: d, entities: entities}
}

func (o only) Detect(ctx context.Context, texts []string) ([][]ports.Span, error) {
	res, err := o.Detector.Detect(ctx, texts)
	if err != nil {
		return nil, err
	}
	for i, spans := range res {
		if spans != nil {
			res[i] = slices.DeleteFunc(spans, func(sp ports.Span) bool { return !slices.Contains(o.entities, sp.Type) })
		}
	}
	return res, nil
}

// raw marks a stage that receives text with earlier hits left in place.
type raw struct{ ports.Detector }

// Raw wraps a stage so the chain sends it unmasked text: for a model that
// needs the context an earlier hit removed, at the cost of seeing that hit.
func Raw(d ports.Detector) ports.Detector { return raw{d} }

// IsRaw reports whether the chain sends d unmasked text, through any wrapping.
func IsRaw(d ports.Detector) bool {
	switch v := d.(type) {
	case raw:
		return true
	case only:
		return IsRaw(v.Detector)
	}
	return false
}
