package detect_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mtchen/keeper/internal/detect"
	"github.com/mtchen/keeper/internal/ports"
)

func Test_DET_C43_ABacktrackingExpressionCannotStall(t *testing.T) {
	l, err := detect.NewList(ports.Terms{Patterns: []ports.Pattern{{Label: "evil", Expr: `(a+)+$`}}})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("a", 100_000) + "b"
	start := time.Now()
	if _, err := l.Detect(t.Context(), []string{text}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("scanning took %v", d)
	}
}
