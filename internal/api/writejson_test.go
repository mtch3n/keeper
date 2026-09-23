package api

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mtchen/keeper/internal/types"
)

// An encode that fails partway must never reach the client as a success. The
// status has to be decided before any byte of the body is written, which is why
// writeJSON marshals into memory first: a reader cannot tell a truncated body
// from a complete one, so a half-written 200 is indistinguishable from data.
func TestWriteJSONRefusesToShipAPartialBody(t *testing.T) {
	// A channel has no JSON representation, so the encode fails after the
	// surrounding object has already been opened.
	unencodable := map[string]any{"a": "value", "ch": make(chan int)}

	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, unencodable)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var e types.Error
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body is not complete JSON: %v (%s)", err, w.Body.String())
	}
	if e.Code != types.CodeInternal {
		t.Fatalf("code = %q, want %q", e.Code, types.CodeInternal)
	}
	// CONTRACT §4: nothing about what actually failed travels to the caller.
	if e.Summary == "" {
		t.Fatal("summary is empty")
	}
}
