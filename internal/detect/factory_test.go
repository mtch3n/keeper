package detect_test

import (
	"context"
	"testing"

	"github.com/mtchen/keeper/internal/detect"
	"github.com/mtchen/keeper/internal/types"
)

type source struct{ conn types.Connection }

func (s source) Connection(context.Context, string) (*types.Connection, error) { return &s.conn, nil }

func TestFactoryRunsPatternsUnlessDetectionIsOff(t *testing.T) {
	for _, c := range []struct {
		d    types.Detection
		want bool
	}{{types.DetectionOff, false}, {types.DetectionScan, true}, {types.DetectionAll, true}, {"", true}, {"banana", true}} {
		f := detect.NewFactory(detect.NewPatterns(), source{conn: types.Connection{ID: "c1", Detection: c.d}})
		det, err := f.For(t.Context(), "c1")
		if err != nil || (det != nil) != c.want {
			t.Errorf("detection %q: For = %v, %v; want a detector: %v", c.d, det, err, c.want)
		}
	}
}
