package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fixturePath resolves a fixture file name to its path in the shared
// testdata/fixtures/handmade corpus - see cmd/pdfpreview/main_test.go's
// identical helper for why this is reimplemented per-command rather
// than imported (a separate `main` package with its own module-relative
// path back to the repository root).
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fixtures", "handmade", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture %s not found at %s: %v", name, path, err)
	}
	return path
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t, name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

// newTestServer starts an httptest.Server backed by a fresh
// browserServer, cleaned up automatically at the end of the test.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := newBrowserServer(defaultPageScale, false)
	ts := httptest.NewServer(newMux(srv, func() {}))
	t.Cleanup(ts.Close)
	return ts
}

func postPDF(t *testing.T, ts *httptest.Server, data []byte) documentResponse {
	t.Helper()
	resp, err := http.Post(ts.URL+"/api/open", "application/pdf", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST /api/open: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/open: status %d", resp.StatusCode)
	}
	var body documentResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /api/open response: %v", err)
	}
	return body
}

// TestHandleOpen confirms a valid PDF upload succeeds and reports the
// right page count, and that an invalid upload is rejected rather than
// crashing the server.
func TestHandleOpen(t *testing.T) {
	ts := newTestServer(t)

	body := postPDF(t, ts, readFixture(t, "two-pages.pdf"))
	if !body.HasDocument {
		t.Fatal("HasDocument = false, want true")
	}
	if body.PageCount != 2 {
		t.Errorf("PageCount = %d, want 2", body.PageCount)
	}
	if body.Generation != 1 {
		t.Errorf("Generation = %d, want 1 for the first document opened", body.Generation)
	}

	resp, err := http.Post(ts.URL+"/api/open", "application/pdf", bytes.NewReader([]byte("not a pdf")))
	if err != nil {
		t.Fatalf("POST /api/open: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status for invalid PDF = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestHandleOpenReplacesDocument confirms a second upload replaces the
// first (rather than, say, being rejected because a document is already
// loaded) and advances the generation number so old thumbnail/page URLs
// are recognizably stale.
func TestHandleOpenReplacesDocument(t *testing.T) {
	ts := newTestServer(t)

	first := postPDF(t, ts, readFixture(t, "two-pages.pdf"))
	second := postPDF(t, ts, readFixture(t, "filled-rect.pdf"))

	if second.Generation <= first.Generation {
		t.Errorf("second.Generation = %d, want greater than first.Generation = %d", second.Generation, first.Generation)
	}
	if second.PageCount != 1 {
		t.Errorf("second.PageCount = %d, want 1 (filled-rect.pdf)", second.PageCount)
	}
}

// TestHandleThumbnailAndPage confirms both image-producing endpoints
// return valid, appropriately-sized PNGs for a freshly opened document,
// and that requesting a page index out of range or a stale generation
// number is rejected rather than served as if nothing were wrong.
func TestHandleThumbnailAndPage(t *testing.T) {
	ts := newTestServer(t)
	doc := postPDF(t, ts, readFixture(t, "two-pages.pdf"))

	for _, path := range []string{
		fmtURL(ts.URL+"/api/thumbnail", doc.Generation, 0),
		fmtURL(ts.URL+"/api/page", doc.Generation, 1),
	} {
		resp, err := http.Get(path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		cfg, err := png.DecodeConfig(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("GET %s: not a valid PNG: %v", path, err)
		}
		if cfg.Width == 0 || cfg.Height == 0 {
			t.Errorf("GET %s: empty image (%dx%d)", path, cfg.Width, cfg.Height)
		}
	}

	// Page index out of range.
	resp, err := http.Get(fmtURL(ts.URL+"/api/page", doc.Generation, 5))
	if err != nil {
		t.Fatalf("GET /api/page (out of range): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status for out-of-range page = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	// Stale generation.
	resp, err = http.Get(fmtURL(ts.URL+"/api/page", doc.Generation+1, 0))
	if err != nil {
		t.Fatalf("GET /api/page (stale generation): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status for stale generation = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
}

// TestHandlePageNoDocument confirms requesting a page before any
// document has been opened at all is reported as "not found" rather
// than a panic on a nil *pdfviewer.Document.
func TestHandlePageNoDocument(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(fmtURL(ts.URL+"/api/page", 0, 0))
	if err != nil {
		t.Fatalf("GET /api/page: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestHandleFontSubstitutionBeforeDocument confirms toggling the
// checkbox before anything has been dropped just records the
// preference (HasDocument: false) instead of erroring.
func TestHandleFontSubstitutionBeforeDocument(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Post(ts.URL+"/api/font-substitution", "application/json", bytes.NewReader([]byte(`{"enabled":true}`)))
	if err != nil {
		t.Fatalf("POST /api/font-substitution: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body documentResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if body.HasDocument {
		t.Error("HasDocument = true, want false (no document was ever opened)")
	}
}

// TestHandleFontSubstitutionReopensDocument confirms toggling the
// checkbox after a document is loaded re-opens it (new generation, same
// page count) rather than leaving the old document (and its old
// font-substitution setting) in place.
func TestHandleFontSubstitutionReopensDocument(t *testing.T) {
	ts := newTestServer(t)
	opened := postPDF(t, ts, readFixture(t, "two-pages.pdf"))

	resp, err := http.Post(ts.URL+"/api/font-substitution", "application/json", bytes.NewReader([]byte(`{"enabled":true}`)))
	if err != nil {
		t.Fatalf("POST /api/font-substitution: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body documentResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !body.HasDocument {
		t.Fatal("HasDocument = false, want true (a document was already loaded)")
	}
	if body.Generation <= opened.Generation {
		t.Errorf("Generation = %d, want greater than %d", body.Generation, opened.Generation)
	}
	if body.PageCount != opened.PageCount {
		t.Errorf("PageCount = %d, want unchanged at %d", body.PageCount, opened.PageCount)
	}

	// The old generation's URLs should now be stale.
	staleResp, err := http.Get(fmtURL(ts.URL+"/api/page", opened.Generation, 0))
	if err != nil {
		t.Fatalf("GET /api/page (old generation): %v", err)
	}
	staleResp.Body.Close()
	if staleResp.StatusCode != http.StatusConflict {
		t.Errorf("status for old generation after toggling = %d, want %d", staleResp.StatusCode, http.StatusConflict)
	}
}

// TestHandleDiagnostics confirms the diagnostics endpoint reports an
// empty list for a document with nothing to report, without erroring.
func TestHandleDiagnostics(t *testing.T) {
	ts := newTestServer(t)
	postPDF(t, ts, readFixture(t, "filled-rect.pdf"))

	resp, err := http.Get(ts.URL + "/api/diagnostics")
	if err != nil {
		t.Fatalf("GET /api/diagnostics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body diagnosticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(body.Messages) != 0 {
		t.Errorf("Messages = %v, want empty for filled-rect.pdf", body.Messages)
	}
}

// TestHandleQuitTriggersCallback confirms POSTing /api/quit calls the
// triggerQuit callback newMux was given - main.go's run relies on this
// to know when to start shutting the server down.
func TestHandleQuitTriggersCallback(t *testing.T) {
	srv := &browserServer{}
	triggered := make(chan struct{}, 1)
	ts := httptest.NewServer(newMux(srv, func() { triggered <- struct{}{} }))
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/api/quit", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /api/quit: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	select {
	case <-triggered:
	default:
		t.Error("triggerQuit was not called")
	}
}

func fmtURL(base string, gen, page int) string {
	return base + "?gen=" + strconv.Itoa(gen) + "&page=" + strconv.Itoa(page)
}
