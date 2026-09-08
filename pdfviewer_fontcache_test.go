package pdfviewer_test

import (
	"context"
	"image"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// TestRepeatedRenderOfTextPageReusesFontCache is the public-API-level
// integration test for Phase 6b's resource-caching work (see
// internal/content/fontcache_test.go for the lower-level tests that
// directly observe the cache avoiding a redundant font-dictionary
// resolve): rendering the same text-showing page more than once from
// one shared *Document - both as a repeated full Render and as a
// Thumbnail of the same page - must keep succeeding and keep producing
// the exact same pixels every time. This does not observe the internal
// cache hit/miss directly (Document.fontCache is unexported, and
// rightly so - see its doc comment on why cache lifetime should not be
// observable through the public API, matching the README's Phase 5
// caching bullet), but it is exactly the calling pattern the cache
// exists to make cheaper, and a wiring bug (for example, passing the
// wrong *FontCache, or a stale one, into a later render) would most
// likely show up here as a wrong or inconsistent image rather than only
// as a performance regression nothing in this test suite would catch.
func TestRepeatedRenderOfTextPageReusesFontCache(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("text-simple-truetype.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	first, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
	if err != nil {
		t.Fatalf("first Render: %v", err)
	}
	second, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	if !imagesEqual(first, second) {
		t.Error("rendering the same text page twice from one Document produced different images")
	}

	thumb, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{})
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	if b := thumb.Bounds(); b.Dx() == 0 || b.Dy() == 0 {
		t.Error("Thumbnail of the same page produced an empty image")
	}
}

// imagesEqual reports whether a and b have the same bounds and pixel
// values, comparing via the standard image.Image interface (At/Bounds)
// so it works regardless of each image's concrete underlying type.
func imagesEqual(a, b image.Image) bool {
	ab, bb := a.Bounds(), b.Bounds()
	if ab != bb {
		return false
	}
	for y := ab.Min.Y; y < ab.Max.Y; y++ {
		for x := ab.Min.X; x < ab.Max.X; x++ {
			ar, ag, abl, aa := a.At(x, y).RGBA()
			br, bg, bbl, ba := b.At(x, y).RGBA()
			if ar != br || ag != bg || abl != bbl || aa != ba {
				return false
			}
		}
	}
	return true
}
