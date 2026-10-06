package detect

import (
	"context"
	"fmt"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Source is what the factory reads a connection's passes and terms from.
type Source interface {
	Connection(ctx context.Context, id string) (*types.Connection, error)
	Terms(ctx context.Context, id string) (ports.Terms, error)
}

// Factory builds each connection's chain from its stored stages and terms.
type Factory struct {
	patterns *Patterns
	src      Source
}

// NewFactory shares one patterns engine across every connection.
func NewFactory(patterns *Patterns, src Source) *Factory {
	return &Factory{patterns: patterns, src: src}
}

// For is a ports.DetectorFor. A connection with no stages gets no detector.
func (f *Factory) For(ctx context.Context, connID string) (ports.Detector, error) {
	c, err := f.src.Connection(ctx, connID)
	if err != nil {
		return nil, err
	}
	if len(c.Detection) == 0 {
		return nil, nil
	}
	terms, err := f.src.Terms(ctx, connID)
	if err != nil {
		return nil, err
	}
	stages := make([]ports.Detector, 0, len(c.Detection))
	for _, st := range c.Detection {
		d, err := f.build(st, terms)
		if err != nil {
			return nil, fmt.Errorf("detect: connection %s: %w", connID, err)
		}
		if len(st.Entities) > 0 {
			d = Only(d, st.Entities)
		}
		if st.Raw {
			d = Raw(d)
		}
		stages = append(stages, d)
	}
	return NewChain(stages, terms.Allow), nil
}

// build is the registry of stage kinds. A new provider is one more case here
// and an adapter beside it; nothing outside this package changes.
func (f *Factory) build(st types.Stage, terms ports.Terms) (ports.Detector, error) {
	switch st.Kind {
	case types.KindPatterns:
		return f.patterns, nil
	case types.KindList:
		return NewList(terms)
	default:
		return nil, fmt.Errorf("unknown stage kind %q", st.Kind)
	}
}
