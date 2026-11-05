package types

import (
	"encoding/json/v2"
	"testing"
)

func TestDetectionReadsStoredValues(t *testing.T) {
	for in, want := range map[string]Detection{
		`{"detection":"all"}`:    DetectionAll,
		`{"detection":"off"}`:    DetectionOff,
		`{}`:                     "",
		`{"detection":"banana"}`: "banana",
	} {
		var c Connection
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if c.Detection != want {
			t.Errorf("%s: detection = %q, want %q", in, c.Detection, want)
		}
	}
	for _, d := range []Detection{"", "banana", DetectionScan} {
		if d.Effective() != DetectionScan {
			t.Errorf("%q behaves as %q, want scan", d, d.Effective())
		}
	}
}
