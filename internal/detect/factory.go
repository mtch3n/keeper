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

// Factory builds each connection's chain from its stored passes and terms.
type Factory struct {
	patterns *Patterns
	src      Source
}

// NewFactory shares one patterns engine across every connection.
func NewFactory(patterns *Patterns, src Source) *Factory {
	return &Factory{patterns: patterns, src: src}
}

// For is a ports.DetectorFor. A connection with no passes gets no detector.
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
	passes := make([]ports.Detector, 0, len(c.Detection))
	for _, p := range c.Detection {
		switch p {
		case types.PassPatterns:
			passes = append(passes, f.patterns)
		case types.PassList:
			passes = append(passes, NewList(terms.Deny))
		default:
			return nil, fmt.Errorf("detect: connection %s names unknown pass %q", connID, p)
		}
	}
	return NewChain(passes, terms.Allow), nil
}
