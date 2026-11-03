package mcpapp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func schemaOf(t *testing.T, tk *types.Ticket) string {
	t.Helper()
	p := elicitation(tk)
	if p == nil {
		t.Fatal("no dialog")
	}
	b, err := json.Marshal(p.RequestedSchema)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestElicitationForAReadHasNoFields(t *testing.T) {
	tk := &types.Ticket{ID: "t1", Prompt: "run it?"}
	if p := elicitation(tk); p.Message != "run it?" {
		t.Errorf("message = %q", p.Message)
	}
	if s := schemaOf(t, tk); !strings.Contains(s, `"properties":{}`) || strings.Contains(s, "required") {
		t.Errorf("schema = %s", s)
	}
}

func TestElicitationForAWriteRequiresAChoice(t *testing.T) {
	s := schemaOf(t, &types.Ticket{ID: "t1", Write: true})
	for _, want := range []string{`"required":["decision"]`, `"enum":["Run once","Refuse"]`} {
		if !strings.Contains(s, want) {
			t.Errorf("schema lacks %s: %s", want, s)
		}
	}
}

func TestOnlyAcceptWithRunOnceRunsAWrite(t *testing.T) {
	for _, c := range []struct {
		action, choice string
		write, want    bool
	}{
		{"accept", "", false, true},
		{"accept", "Run once", true, true},
		{"accept", "", true, false},
		{"accept", "Refuse", true, false},
		{"decline", "Run once", true, false},
		{"cancel", "Run once", true, false},
	} {
		if got := runs(c.action, c.choice, c.write); got != c.want {
			t.Errorf("runs(%q, %q, write=%v) = %v", c.action, c.choice, c.write, got)
		}
	}
}
