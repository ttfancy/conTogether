package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"contogether/container-api/internal/webui"
)

// These tests deliberately never assert on index.html's specific
// content: it's the checked-in placeholder in most dev environments,
// but `make frontend`/the Dockerfile overwrite it with the real React
// build — the SPA-fallback *behavior* has to hold either way.

func TestRootServesIndexHTML(t *testing.T) {
	h, err := webui.Handler()
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected a non-empty body")
	}
}

// TestUnmatchedPathFallsBackToIndexHTML is the actual point of this
// package: an unknown path (e.g. a client-side route like
// /containers/abc/logs on a hard refresh) must serve the same content
// as /, not 404, so React Router can take over and render it.
func TestUnmatchedPathFallsBackToIndexHTML(t *testing.T) {
	h, err := webui.Handler()
	if err != nil {
		t.Fatalf("Handler failed: %v", err)
	}

	rootRec := httptest.NewRecorder()
	h.ServeHTTP(rootRec, httptest.NewRequest(http.MethodGet, "/", nil))
	rootBody, err := io.ReadAll(rootRec.Result().Body)
	if err != nil {
		t.Fatalf("reading / body failed: %v", err)
	}

	unmatchedRec := httptest.NewRecorder()
	h.ServeHTTP(unmatchedRec, httptest.NewRequest(http.MethodGet, "/containers/abc-123/logs", nil))
	if unmatchedRec.Code != http.StatusOK {
		t.Fatalf("GET /containers/abc-123/logs = %d, want 200 (SPA fallback)", unmatchedRec.Code)
	}
	unmatchedBody, err := io.ReadAll(unmatchedRec.Result().Body)
	if err != nil {
		t.Fatalf("reading fallback body failed: %v", err)
	}

	if string(rootBody) != string(unmatchedBody) {
		t.Fatal("expected the unmatched path to serve the same content as /, got different bodies")
	}
}
