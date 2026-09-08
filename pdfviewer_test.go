package pdfviewer_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file tests the public package end to end, against the same
// hand-authored fixture corpus internal/parser and internal/model test
// against (see testdata/fixtures/FIXTURES.md) - but exercised entirely
// through Open/OpenFile/Document/Page, the way an actual embedder of
// this module would use it, rather than through any internal package
// directly.

func fixturePath(name string) string {
	return filepath.Join(handmadeFixturesDir, name)
}

func TestOpenFileMinimalBlankPage(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	if got, want := doc.PageCount(), 1; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}

	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	want := pdfviewer.Rect{LLX: 0, LLY: 0, URX: 200, URY: 200}
	if got := page.Bounds(); got != want {
		t.Errorf("Bounds() = %+v, want %+v", got, want)
	}
}

func TestOpenFileTwoPages(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("two-pages.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	if got, want := doc.PageCount(), 2; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}

	wantBounds := []pdfviewer.Rect{
		{LLX: 0, LLY: 0, URX: 100, URY: 200},
		{LLX: 0, LLY: 0, URX: 300, URY: 400},
	}
	for i, want := range wantBounds {
		page, err := doc.Page(i)
		if err != nil {
			t.Fatalf("Page(%d): %v", i, err)
		}
		if got := page.Bounds(); got != want {
			t.Errorf("Page(%d).Bounds() = %+v, want %+v", i, got, want)
		}
	}
}

// TestOpenAcceptsArbitraryReaderAt confirms Open works against any
// io.ReaderAt, not just a file - see the README's "Draft Public API"
// section on why io.ReaderAt plus a size, rather than requiring a real
// file, is the primary entry point and OpenFile only a convenience.
func TestOpenAcceptsArbitraryReaderAt(t *testing.T) {
	data, err := os.ReadFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	doc, err := pdfviewer.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	if got, want := doc.PageCount(), 1; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}
}

func TestOpenFileNonexistentFile(t *testing.T) {
	if _, err := pdfviewer.OpenFile(filepath.Join(handmadeFixturesDir, "does-not-exist.pdf")); err == nil {
		t.Fatal("OpenFile on a nonexistent file succeeded, want an error")
	}
}

func TestOpenFileTruncatedIsMalformed(t *testing.T) {
	_, err := pdfviewer.OpenFile(fixturePath("truncated.pdf"))
	if !errors.Is(err, pdfviewer.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func TestPageIndexOutOfRange(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	for _, idx := range []int{-1, 1, 100} {
		if _, err := doc.Page(idx); !errors.Is(err, pdfviewer.ErrPageIndex) {
			t.Errorf("Page(%d) error = %v, want ErrPageIndex", idx, err)
		}
	}
}

// TestDocumentCloseIsIdempotent confirms Close's documented "safe to
// call more than once" behavior.
func TestDocumentCloseIsIdempotent(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if err := doc.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := doc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestPageAfterCloseIsRejected confirms the Document/Page ownership rule
// documented on Document.Close: a Page must not be usable once its
// Document is closed.
func TestPageAfterCloseIsRejected(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if err := doc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := doc.Page(0); !errors.Is(err, pdfviewer.ErrClosed) {
		t.Fatalf("Page(0) after Close error = %v, want ErrClosed", err)
	}
}

// TestPageCountValidAfterClose confirms the one documented exception to
// "nothing works after Close": PageCount does not require any resource
// Close releases, since it was computed once up front while opening the
// document.
func TestPageCountValidAfterClose(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if err := doc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got, want := doc.PageCount(), 1; got != want {
		t.Errorf("PageCount() after Close = %d, want %d", got, want)
	}
}

// TestRenderProducesImage confirms Page.Render (implemented in Phase 2)
// produces an image of the expected pixel dimensions for a blank page
// (minimal-blank-page.pdf has an empty content stream, so this only
// checks size and the default white background, not any painted
// content - see pdfviewer_render_test.go for tests against pages with
// actual vector content).
func TestRenderProducesImage(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 200 || bounds.Dy() != 200 {
		t.Errorf("Render() image size = %dx%d, want 200x200 (MediaBox is [0 0 200 200], default Scale 1)", bounds.Dx(), bounds.Dy())
	}
	r, g, b, a := img.At(100, 100).RGBA()
	if r>>8 != 255 || g>>8 != 255 || b>>8 != 255 || a>>8 != 255 {
		t.Errorf("blank page center pixel = (%d,%d,%d,%d), want opaque white (255,255,255,255)", r>>8, g>>8, b>>8, a>>8)
	}
}

// TestThumbnailIsNotYetImplemented confirms the documented placeholder
// behavior of Page.Thumbnail (see page.go): it must fail with
// ErrUnsupported, since Thumbnail is Phase 3 work per the README's
// phased plan.
func TestThumbnailIsNotYetImplemented(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	if _, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{}); !errors.Is(err, pdfviewer.ErrUnsupported) {
		t.Fatalf("Thumbnail error = %v, want ErrUnsupported", err)
	}
}

// TestRenderRespectsCanceledContext confirms Render checks its context
// before doing (what will eventually be) real work, a standard Go
// convention for any function accepting a context.Context.
func TestRenderRespectsCanceledContext(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := page.Render(ctx, pdfviewer.RenderOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Render with canceled context error = %v, want context.Canceled", err)
	}
}

func TestRectWidthAndHeight(t *testing.T) {
	r := pdfviewer.Rect{LLX: 10, LLY: 20, URX: 110, URY: 220}
	if got, want := r.Width(), 100.0; got != want {
		t.Errorf("Width() = %v, want %v", got, want)
	}
	if got, want := r.Height(), 200.0; got != want {
		t.Errorf("Height() = %v, want %v", got, want)
	}
}
