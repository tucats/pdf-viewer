package pdfviewer_test

import (
	"bytes"
	"context"
	"errors"
	"image/color"
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

// TestOpenFileEncryptedIsRejected is the public-API-level regression
// test for this package's "password handling" decision (see errors.go's
// ErrEncrypted doc comment and docs/PLAN2.md's Phase 7): encrypted.pdf's
// /Encrypt dictionary uses placeholder, non-byte-accurate /O and /U
// values (see tools/genfixtures's buildEncrypted) that do not validate
// under an empty password, so - like any document that genuinely
// requires a password this package cannot supply - it must fail clearly
// and immediately at Open, rather than appearing to succeed and then
// failing confusingly the first time some still-encrypted stream or
// string is actually read. See TestOpenFileEncryptedEmptyPasswordSucceeds,
// just below, for the complementary case that now opens successfully.
func TestOpenFileEncryptedIsRejected(t *testing.T) {
	_, err := pdfviewer.OpenFile(fixturePath("encrypted.pdf"))
	if !errors.Is(err, pdfviewer.ErrEncrypted) {
		t.Fatalf("error = %v, want ErrEncrypted", err)
	}
	if !errors.Is(err, pdfviewer.ErrUnsupported) {
		t.Fatalf("error = %v, want it to also satisfy ErrUnsupported", err)
	}
}

// TestOpenFileEncryptedEmptyPasswordSucceeds is the public-API-level
// regression test for Phase 7a (see docs/PLAN2.md): a document
// genuinely protected with the Standard security handler, but whose
// user password is empty - the common "permissions-only" case, such as
// many bank statements and print-to-PDF output - must open successfully
// with no password-related error at all, and its page count must be
// readable exactly as if the document were not encrypted.
// pdfviewer_render_test.go's TestRenderEncryptedMatchesPlainContent
// covers the deeper claim that decrypted *content* renders correctly;
// this test only checks that the public API's Open/OpenFile path itself
// does not surface ErrEncrypted for this case.
func TestOpenFileEncryptedEmptyPasswordSucceeds(t *testing.T) {
	for _, name := range []string{"encrypted-rc4-40bit.pdf", "encrypted-aes128.pdf", "encrypted-aes256.pdf"} {
		t.Run(name, func(t *testing.T) {
			doc, err := pdfviewer.OpenFile(fixturePath(name))
			if err != nil {
				t.Fatalf("OpenFile(%s): %v", name, err)
			}
			defer doc.Close()
			if got, want := doc.PageCount(), 1; got != want {
				t.Errorf("PageCount() = %d, want %d", got, want)
			}
		})
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

// TestThumbnailDefaultMaxDimension confirms Thumbnail's default
// MaxDimension (used when ThumbnailOptions.MaxDimension is zero) is
// applied to a square page: minimal-blank-page.pdf's 200x200 MediaBox
// scaled up to the default 256-pixel bound.
func TestThumbnailDefaultMaxDimension(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	img, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{})
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 256 || bounds.Dy() != 256 {
		t.Errorf("Thumbnail() image size = %dx%d, want 256x256 (default MaxDimension, square page)", bounds.Dx(), bounds.Dy())
	}
}

// TestThumbnailPreservesAspectRatioAndRespectsMaxDimension confirms a
// non-square page (two-pages.pdf's first page is 100x200 points, a 1:2
// aspect ratio) scales down to fit within a custom MaxDimension while
// preserving that ratio, and that neither dimension exceeds
// MaxDimension - the two properties the README's Draft Public API calls
// for a thumbnail to guarantee.
func TestThumbnailPreservesAspectRatioAndRespectsMaxDimension(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("two-pages.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0) // 100x200 points
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	img, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{MaxDimension: 50})
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() > 50 || bounds.Dy() > 50 {
		t.Fatalf("Thumbnail() image size = %dx%d, want both dimensions <= 50", bounds.Dx(), bounds.Dy())
	}
	if bounds.Dy() != 50 {
		t.Errorf("Thumbnail() height = %d, want 50 (the longer axis should hit MaxDimension exactly)", bounds.Dy())
	}
	if bounds.Dx() != 25 {
		t.Errorf("Thumbnail() width = %d, want 25 (100x200 preserves its 1:2 aspect ratio at height 50)", bounds.Dx())
	}
}

// TestThumbnailRespectsBackground confirms ThumbnailOptions.Background
// is honored exactly like RenderOptions.Background - Thumbnail shares
// renderAtScale with Render, but this is worth its own regression test
// since it is easy to imagine an implementation that forgets to thread
// the option through.
func TestThumbnailRespectsBackground(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	img, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{
		MaxDimension: 20,
		Background:   color.RGBA{R: 0, G: 255, B: 0, A: 255},
	})
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Dx()/2, b.Dy()/2).RGBA()
	if r>>8 != 0 || g>>8 != 255 || bl>>8 != 0 {
		t.Errorf("Thumbnail() with a green background, center pixel = (%d,%d,%d), want (0,255,0)", r>>8, g>>8, bl>>8)
	}
}

// TestThumbnailExceedingPixelBoundIsUnsupported confirms an absurdly
// large MaxDimension still hits Render's own maxRenderPixels bound
// rather than being allowed to allocate an unbounded amount of memory -
// Thumbnail's whole point is to be a *bounded*-size render, but its
// bound is caller-controlled (MaxDimension), so this project's own
// independent safety bound must still apply underneath it.
func TestThumbnailExceedingPixelBoundIsUnsupported(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	_, err = page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{MaxDimension: 100_000})
	if !errors.Is(err, pdfviewer.ErrUnsupported) {
		t.Fatalf("Thumbnail with an oversized MaxDimension: got %v, want an error wrapping ErrUnsupported", err)
	}
}

// TestThumbnailRotatedPageSwapsAspectRatio confirms Thumbnail accounts
// for /Rotate exactly like Render does: rotated-page.pdf's MediaBox is
// 100x200 (portrait) but declares /Rotate 90, so its *rendered* aspect
// ratio is landscape (2:1), and a thumbnail's dimensions must reflect
// that rather than the raw, unrotated MediaBox.
func TestThumbnailRotatedPageSwapsAspectRatio(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("rotated-page.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	img, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{MaxDimension: 40})
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 40 || bounds.Dy() != 20 {
		t.Errorf("Thumbnail() of a /Rotate 90 page = %dx%d, want 40x20 (landscape, matching the rotated display orientation)", bounds.Dx(), bounds.Dy())
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
