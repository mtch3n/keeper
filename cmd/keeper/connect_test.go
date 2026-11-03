package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

func TestTestFailureUsesKeepersOwnWords(t *testing.T) {
	err := testFailure(fmt.Errorf("wrapped: %w", &types.Error{Summary: "the server could not be reached", Action: "check the address and port"}))
	for _, want := range []string{"could not be reached", "check the address and port", "nothing was registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if strings.Contains(testFailure(errors.New("boom")).Error(), "boom") {
		t.Error("an unknown error text must not be repeated")
	}
}
