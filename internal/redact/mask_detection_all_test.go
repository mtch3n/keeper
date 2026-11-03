package redact_test

import (
	"context"
	"testing"

	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/redact"
	"github.com/mtchen/keeper/internal/types"
)

func TestMask_MASK_C5_aFailingDetectorRedactsAllowTextWhole(t *testing.T) {
	r, err := redact.New(redact.Config{
		Keys: keys{},
		Detectors: func(context.Context, string) (ports.Detector, error) {
			return failing{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cols := []types.ColumnMeta{
		{Name: "id", Type: "int8", Policy: types.PolicyAllow, TableOID: 1, AttNum: 1},
		{Name: "notes", Type: "text", Policy: types.PolicyAllow, TableOID: 1, AttNum: 2},
	}
	rows := [][]any{{int64(7), "mail jane@example.com"}, {int64(8), "nothing here"}}
	ctx := redact.WithStatement(t.Context(), redact.Statement{ConnectionID: "c1", Detection: types.DetectionAll})
	tr, err := r.Apply(ctx, "s1", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if row[1] != redact.RedactedMarker {
			t.Errorf("row %d notes = %q, want the whole cell redacted", i, row[1])
		}
		if row[0] != int64(7+i) {
			t.Errorf("row %d id = %v, a non-text value was changed", i, row[0])
		}
	}
	if tr["notes"].Basis != types.BasisUnexamined {
		t.Errorf("notes basis = %q, want unexamined, which the pipeline records as a degradation", tr["notes"].Basis)
	}
	if tr["id"].Basis == types.BasisUnexamined {
		t.Error("id was reported unexamined")
	}
}
