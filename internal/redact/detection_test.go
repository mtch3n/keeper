package redact_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mtchen/keeper/internal/detect"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/redact"
	"github.com/mtchen/keeper/internal/types"
)

type keys struct{}

func (keys) TokenKey(_ context.Context, connID string, _ int) ([]byte, int, error) {
	return []byte("key-" + connID), 1, nil
}

// scanOnce redacts one row through a redactor whose connection runs det, and
// returns each scan cell with the column's transform.
func scanOnce(t *testing.T, det ports.Detector, cells ...any) ([]any, types.Transform) {
	t.Helper()
	r, err := redact.New(redact.Config{
		Keys: keys{},
		Detectors: func(context.Context, string) (ports.Detector, error) {
			if det == nil {
				return nil, nil
			}
			return det, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := make([][]any, len(cells))
	for i, c := range cells {
		rows[i] = []any{c}
	}
	cols := []types.ColumnMeta{{Name: "notes", Type: "text", Policy: types.PolicyScan, TableOID: 1, AttNum: 1}}
	ctx := redact.WithStatement(t.Context(), redact.Statement{ConnectionID: "c1"})
	tr, err := r.Apply(ctx, "s1", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]any, len(rows))
	for i := range rows {
		out[i] = rows[i][0]
	}
	return out, tr["notes"]
}

func Test_DET_C15_PatternsRedactsAValidCardAndKeepsAnInvalidOne(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{detect.NewPatterns()}, nil)
	got, _ := scanOnce(t, chain, "card 4111 1111 1111 1111", "ref 4111 1111 1111 1112")
	if want := "card " + redact.RedactedSpan("credit_card"); got[0] != want {
		t.Errorf("valid card: got %q, want %q", got[0], want)
	}
	if got[1] != "ref 4111 1111 1111 1112" {
		t.Errorf("a number failing Luhn was changed: %q", got[1])
	}
}

func Test_DET_C30_ListRedactsADenyTermWhateverItsCase(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{detect.NewList([]string{"Acme Corp"})}, nil)
	got, _ := scanOnce(t, chain, "invoice for ACME corp")
	if want := "invoice for " + redact.RedactedSpan(detect.TypeCustom); got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func Test_DET_C32_PassesAddUpAndASharedSpanYieldsOneMarker(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{
		detect.NewPatterns(),
		detect.NewList([]string{"jane@example.com", "Jane Roe"}),
	}, nil)
	got, _ := scanOnce(t, chain, "jane@example.com met Jane Roe")
	want := redact.RedactedSpan("email_address") + " met " + redact.RedactedSpan(detect.TypeCustom)
	if got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func Test_DET_C33_OneFailingPassRedactsTheWholeCell(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{detect.NewPatterns(), failing{}}, nil)
	got, tr := scanOnce(t, chain, "jane@example.com met Jane Roe")
	if got[0] != redact.RedactedMarker {
		t.Errorf("got %q, want the whole cell redacted", got[0])
	}
	if tr.Basis != types.BasisUnexamined {
		t.Errorf("basis = %q, want unexamined", tr.Basis)
	}
}

func Test_DET_C35_AnAllowTermStaysVisible(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{detect.NewPatterns()}, []string{"support@yourco.com"})
	got, _ := scanOnce(t, chain, "write support@yourco.com or jane@example.com")
	if want := "write support@yourco.com or " + redact.RedactedSpan("email_address"); got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func Test_DET_C36_APartialAllowMatchReleasesNothing(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{detect.NewPatterns()}, []string{"support@yourco.com"})
	got, _ := scanOnce(t, chain, "mail support@yourco.com.evil.io")
	if got[0] == "mail support@yourco.com.evil.io" {
		t.Errorf("a span only partly equal to an allow term was released: %q", got[0])
	}
}

func Test_DET_C7_AFailingPassRedactsEveryScanCell(t *testing.T) {
	got, tr := scanOnce(t, failing{}, "jane@example.com", "nothing here")
	for i, g := range got {
		if g != redact.RedactedMarker {
			t.Errorf("cell %d = %q, want the whole cell redacted", i, g)
		}
	}
	if tr.Basis != types.BasisUnexamined {
		t.Errorf("basis = %q, want unexamined", tr.Basis)
	}
}

func Test_DET_C9_UnansweredTextsAreRedactedWhole(t *testing.T) {
	got, _ := scanOnce(t, firstOnly{}, "answered jane@example.com", "unanswered")
	if want := "answered " + redact.RedactedSpan("email"); got[0] != want {
		t.Errorf("answered cell = %q, want %q", got[0], want)
	}
	if got[1] != redact.RedactedMarker {
		t.Errorf("unanswered cell = %q, want the whole cell redacted", got[1])
	}
}

func Test_DET_C17_ASpanPastTheEndRedactsTheWholeCell(t *testing.T) {
	got, _ := scanOnce(t, pastEnd{}, "short text")
	if got[0] != redact.RedactedMarker {
		t.Errorf("got %q, want the whole cell redacted", got[0])
	}
}

func Test_DET_C25_NoPassesRedactsEveryScanCell(t *testing.T) {
	got, tr := scanOnce(t, nil, "nothing sensitive", "jane@example.com")
	for i, g := range got {
		if g != redact.RedactedMarker {
			t.Errorf("cell %d = %q, want the whole cell redacted", i, g)
		}
	}
	if tr.Basis != types.BasisUnexamined {
		t.Errorf("basis = %q, want unexamined", tr.Basis)
	}
}

func Test_DET_C29_NoScanColumnRunsNoPass(t *testing.T) {
	calls := &counting{}
	r, err := redact.New(redact.Config{
		Keys:      keys{},
		Detectors: func(context.Context, string) (ports.Detector, error) { return calls, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]any{{"jane@example.com"}}
	cols := []types.ColumnMeta{{Name: "email", Type: "text", Policy: types.PolicyAllow, TableOID: 1, AttNum: 1}}
	ctx := redact.WithStatement(t.Context(), redact.Statement{ConnectionID: "c1"})
	if _, err := r.Apply(ctx, "s1", cols, rows); err != nil {
		t.Fatal(err)
	}
	if calls.n != 0 {
		t.Errorf("a pass ran %d time(s) on a statement with no scan column", calls.n)
	}
	if rows[0][0] != "jane@example.com" {
		t.Errorf("an allow column changed: %q", rows[0][0])
	}
}

// --- fake passes --------------------------------------------------------------

type failing struct{}

func (failing) Detect(context.Context, []string) ([][]ports.Span, error) {
	return nil, errors.New("service unavailable")
}
func (failing) Identity() ports.DetectorIdentity { return ports.DetectorIdentity{Name: "failing"} }

// firstOnly answers for the first text only, finding emails in it.
type firstOnly struct{}

func (firstOnly) Detect(_ context.Context, texts []string) ([][]ports.Span, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	t := texts[0]
	for i := range len(t) {
		if t[i:] == "jane@example.com" {
			return [][]ports.Span{{{Start: i, End: len(t), Type: "email"}}}, nil
		}
	}
	return [][]ports.Span{{}}, nil
}
func (firstOnly) Identity() ports.DetectorIdentity { return ports.DetectorIdentity{Name: "first"} }

type pastEnd struct{}

func (pastEnd) Detect(_ context.Context, texts []string) ([][]ports.Span, error) {
	out := make([][]ports.Span, len(texts))
	for i, t := range texts {
		out[i] = []ports.Span{{Start: 0, End: len(t) + 5, Type: "email"}}
	}
	return out, nil
}
func (pastEnd) Identity() ports.DetectorIdentity { return ports.DetectorIdentity{Name: "past-end"} }

type counting struct{ n int }

func (c *counting) Detect(_ context.Context, texts []string) ([][]ports.Span, error) {
	c.n++
	out := make([][]ports.Span, len(texts))
	for i := range out {
		out[i] = []ports.Span{}
	}
	return out, nil
}
func (c *counting) Identity() ports.DetectorIdentity { return ports.DetectorIdentity{Name: "counting"} }

func Test_DET_C5_DetectionNeverTouchesAnAllowColumn(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{detect.NewPatterns()}, nil)
	r, err := redact.New(redact.Config{
		Keys:      keys{},
		Detectors: func(context.Context, string) (ports.Detector, error) { return chain, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]any{{"mail jane@example.com"}}
	cols := []types.ColumnMeta{{Name: "body", Type: "text", Policy: types.PolicyAllow, TableOID: 1, AttNum: 1}}
	ctx := redact.WithStatement(t.Context(), redact.Statement{ConnectionID: "c1"})
	tr, err := r.Apply(ctx, "s1", cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != "mail jane@example.com" {
		t.Errorf("an allow column was changed: %q", rows[0][0])
	}
	if tr["body"].SpansRedacted != 0 {
		t.Errorf("spans redacted in an allow column: %d", tr["body"].SpansRedacted)
	}
}

func Test_DET_C22_AValueTokenizedEarlierIsReTokenizedInOutput(t *testing.T) {
	r, err := redact.New(redact.Config{Keys: keys{}})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := r.Mint(t.Context(), "s1", "c1", "email", "jane@example.com")
	if err != nil {
		t.Fatal(err)
	}
	rows := [][]any{{"forwarded to jane@example.com today"}}
	cols := []types.ColumnMeta{{Name: "body", Type: "text", Policy: types.PolicyAllow}}
	ctx := redact.WithStatement(t.Context(), redact.Statement{ConnectionID: "c1"})
	if _, err := r.Apply(ctx, "s1", cols, rows); err != nil {
		t.Fatal(err)
	}
	if want := "forwarded to " + tok + " today"; rows[0][0] != want {
		t.Errorf("got %q, want %q", rows[0][0], want)
	}
}

func Test_DET_C38_ABareLowScoreHitIsStillRedacted(t *testing.T) {
	chain := detect.NewChain([]ports.Detector{detect.NewPatterns()}, nil)
	got, _ := scanOnce(t, chain, "jane@example.com")
	if got[0] != redact.RedactedSpan("email_address") {
		t.Errorf("got %q: a hit below any score threshold was released", got[0])
	}
}
