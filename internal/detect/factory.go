package detect

import (
	"context"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

// Source is what the factory reads a connection's detection setting from.
type Source interface {
	Connection(ctx context.Context, id string) (*types.Connection, error)
}

// Factory hands each connection the patterns engine, or nothing when its
// detection is off.
type Factory struct {
	patterns *Patterns
	src      Source
}

// NewFactory shares one patterns engine across every connection.
func NewFactory(patterns *Patterns, src Source) *Factory {
	return &Factory{patterns: patterns, src: src}
}

// For is a ports.DetectorFor. A connection with detection off gets no detector.
func (f *Factory) For(ctx context.Context, connID string) (ports.Detector, error) {
	c, err := f.src.Connection(ctx, connID)
	if err != nil {
		return nil, err
	}
	if c.Detection.Effective() == types.DetectionOff {
		return nil, nil
	}
	return f.patterns, nil
}
