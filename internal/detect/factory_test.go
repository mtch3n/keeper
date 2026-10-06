package detect_test

import (
	"context"
	"testing"

	"github.com/mtchen/keeper/internal/detect"
	"github.com/mtchen/keeper/internal/ports"
	"github.com/mtchen/keeper/internal/types"
)

type source struct {
	conn  types.Connection
	terms ports.Terms
}

func (s source) Connection(context.Context, string) (*types.Connection, error) { return &s.conn, nil }
func (s source) Terms(context.Context, string) (ports.Terms, error)            { return s.terms, nil }

func Test_DET_C1_AConnectionsPassesAllExamineItsText(t *testing.T) {
	f := detect.NewFactory(detect.NewPatterns(), source{
		conn:  types.Connection{ID: "c1", Detection: []types.Stage{{Kind: types.KindPatterns}, {Kind: types.KindList}}},
		terms: ports.Terms{Deny: []string{"Acme Corp"}},
	})
	det, err := f.For(t.Context(), "c1")
	if err != nil || det == nil {
		t.Fatalf("For = %v, %v", det, err)
	}
	res, err := det.Detect(t.Context(), []string{"jane@example.com at Acme Corp"})
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, sp := range res[0] {
		types[sp.Type] = true
	}
	if !types["email_address"] || !types[detect.TypeCustom] {
		t.Errorf("spans %v: want the patterns pass and the list pass both to have found theirs", res[0])
	}
}

func Test_DET_C25_AConnectionWithNoPassesHasNoDetector(t *testing.T) {
	f := detect.NewFactory(detect.NewPatterns(), source{conn: types.Connection{ID: "c1"}})
	det, err := f.For(t.Context(), "c1")
	if err != nil || det != nil {
		t.Errorf("For = %v, %v; want no detector, so its scan cells go out unexamined", det, err)
	}
}
