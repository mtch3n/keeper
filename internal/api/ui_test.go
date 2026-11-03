package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestUIWithoutABuiltBundleSaysSo(t *testing.T) {
	rec := httptest.NewRecorder()
	serveIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil), fstest.MapFS{".gitkeep": {}})
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "was not built") || !strings.Contains(body, "make build") {
		t.Errorf("want a page saying the UI was not built and how to build it, got %d %q", rec.Code, body)
	}
}

func TestUIWithABuiltBundleServesIt(t *testing.T) {
	rec := httptest.NewRecorder()
	serveIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil), fstest.MapFS{"index.html": {Data: []byte("<p>real</p>")}})
	if !strings.Contains(rec.Body.String(), "real") {
		t.Errorf("got %q", rec.Body.String())
	}
}
