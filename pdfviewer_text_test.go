package pdfviewer_test

import (
	"context"
	"image"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file exercises Phase 4's text and font support end to end
// through the public API, against the fixtures tools/genfixtures'
// text.go adds - the text counterpart to pdfviewer_render_test.go
// (vector graphics) and pdfviewer_image_test.go (images). Every
// fixture's exact expected pixel geometry is worked out by hand in that
// generator file's doc comments; the assertions here just check the
// arithmetic actually landed where expected once rendered.
//
// assertInk (a pixel that should be painted the fixture's chosen fill
// color) and assertBackground (a pixel that should be untouched white)
// are used throughout rather than assertRGB8 (from
// pdfviewer_image_test.go) directly, mostly to keep each test's sample
// points self-documenting about *which* claim ("ink" vs. "background")
// they are making.

func assertInk(t *testing.T, img image.Image, x, y int, r, g, b uint32) {
	t.Helper()
	assertRGB8(t, img, x, y, r, g, b, 4)
}

func assertBackground(t *testing.T, img image.Image, x, y int) {
	t.Helper()
	assertRGB8(t, img, x, y, 255, 255, 255, 4)
}

// TestRenderSimpleTrueTypeText confirms an embedded TrueType simple font
// (buildTextSimpleTrueType) paints its test glyph (a square, glyph
// index 1, reached via the 'A' cmap entry) at the exactly-derived device
// rectangle x:[15,85] y:[15,85] (see that generator function's doc
// comment) - the baseline Phase 4 rendering fixture, sampling well
// inside the square, well outside it, and just inside each edge.
func TestRenderSimpleTrueTypeText(t *testing.T) {
	img := renderPage(t, "text-simple-truetype.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	assertInk(t, img, 50, 50, 255, 0, 0) // center of the glyph square
	assertInk(t, img, 20, 20, 255, 0, 0) // just inside the top-left corner
	assertInk(t, img, 80, 80, 255, 0, 0) // just inside the bottom-right corner
	assertBackground(t, img, 5, 5)       // well outside (top-left corner of the page)
	assertBackground(t, img, 95, 95)     // well outside (bottom-right corner of the page)
	assertBackground(t, img, 50, 5)      // outside vertically, centered horizontally
}

// TestRenderSimpleType1Text is Phase 11's counterpart to
// TestRenderSimpleTrueTypeText above: confirms an embedded Type 1
// simple font (buildTextSimpleType1) - the from-scratch Type 1
// charstring interpreter internal/fonts/type1.go adds - paints its test
// glyph (the same square shape and device-space rectangle
// buildTextSimpleTrueType's own doc comment derives) reached via
// /Encoding rather than a TrueType cmap subtable.
func TestRenderSimpleType1Text(t *testing.T) {
	img := renderPage(t, "text-simple-type1.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	assertInk(t, img, 50, 50, 255, 0, 0) // center of the glyph square
	assertInk(t, img, 20, 20, 255, 0, 0) // just inside the top-left corner
	assertInk(t, img, 80, 80, 255, 0, 0) // just inside the bottom-right corner
	assertBackground(t, img, 5, 5)       // well outside (top-left corner of the page)
	assertBackground(t, img, 95, 95)     // well outside (bottom-right corner of the page)
	assertBackground(t, img, 50, 5)      // outside vertically, centered horizontally
}

// TestRenderType0IdentityTextMatchesSimpleFont confirms a composite
// (/Type0, /Encoding /Identity-H) font showing CID 1 - which
// /CIDToGIDMap /Identity maps straight to glyph index 1, the same
// square glyph buildTextSimpleTrueType's simple font reaches through
// its cmap - renders pixel-for-pixel the same as
// TestRenderSimpleTrueTypeText above, despite going through an entirely
// different internal/fonts code path (cid.go instead of simple.go). Per
// buildTextType0Identity's doc comment, this fixture was built with
// identical size and position to buildTextSimpleTrueType specifically
// so the two can share this exact assertion.
func TestRenderType0IdentityTextMatchesSimpleFont(t *testing.T) {
	img := renderPage(t, "text-type0-identity.pdf")
	assertInk(t, img, 50, 50, 255, 0, 0)
	assertInk(t, img, 20, 20, 255, 0, 0)
	assertInk(t, img, 80, 80, 255, 0, 0)
	assertBackground(t, img, 5, 5)
	assertBackground(t, img, 95, 95)
}

// TestRenderType0EmbeddedCMapTextMatchesIdentity is Phase 9's end-to-end
// confirmation that an embedded CMap /Encoding stream actually drives
// real rendering, not just cid.go's own unit tests: buildTextType0EmbeddedCMap
// shows the arbitrary 2-byte code 0x1234, which this fixture's own CMap
// (parsed by internal/fonts/cidcmap.go's parseCMap, via cid.go's
// loadType0Encoding) maps to CID 1 - the same CID
// TestRenderType0IdentityTextMatchesSimpleFont's Identity-H fixture
// reaches directly - so the two fixtures should render pixel-for-pixel
// identically despite the embedded-CMap fixture never using "code ==
// CID" at all.
func TestRenderType0EmbeddedCMapTextMatchesIdentity(t *testing.T) {
	img := renderPage(t, "text-type0-embedded-cmap.pdf")
	assertInk(t, img, 50, 50, 255, 0, 0)
	assertInk(t, img, 20, 20, 255, 0, 0)
	assertInk(t, img, 80, 80, 255, 0, 0)
	assertBackground(t, img, 5, 5)
	assertBackground(t, img, 95, 95)
}

// TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault confirms
// that, without pdfviewer.WithPredefinedCMaps configured (this project's
// default for every document), a Type0 font naming a predefined CJK
// encoding ("UniGB-UCS2-H" - buildTextType0PredefinedEncoding) falls
// back to internal/fonts' notdefGlyph placeholder box, the same
// documented behavior as an unresolvable simple font
// (TestRenderNotdefFallback) - not a blank page, and not the real square
// glyph. See pdfviewer_predefinedcmap_test.go for the same fixture
// rendered *with* the option configured, which does show the real
// glyph.
func TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault(t *testing.T) {
	img := renderPage(t, "text-type0-predefined-encoding.pdf")
	assertInk(t, img, 8, 60, 255, 0, 0) // outer ring of the notdef box (/DW 1000 sized)
	assertBackground(t, img, 50, 60)    // hollow interior
	assertBackground(t, img, 95, 20)    // well outside the whole box
}

// TestRenderScaledText confirms buildTextScaled's two differently-sized
// glyphs (size 40 at text origin (0,0), then size 80 after "60 0 Td")
// land at their two independently hand-derived device rectangles - see
// that generator function's doc comment for the full derivation:
// device x:[6,34] y:[66,94] for the first (smaller) glyph, and device
// x:[72,128] y:[32,88] for the second (larger) glyph, which also
// partially extends above the page's own top edge (y<0 in text space)
// without this project's rasterizer misbehaving.
func TestRenderScaledText(t *testing.T) {
	img := renderPage(t, "text-scaled.pdf")
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 200x100", b.Dx(), b.Dy())
	}
	// First glyph (size 40): device x:[6,34] y:[66,94].
	assertInk(t, img, 20, 80, 0, 0, 255)
	assertBackground(t, img, 20, 20) // same X, but outside the first glyph's Y range

	// Second glyph (size 80): device x:[72,128] y:[32,88].
	assertInk(t, img, 100, 60, 0, 0, 255)
	assertBackground(t, img, 150, 10) // outside both glyphs

	// The two glyphs must not overlap (distinct X ranges [6,34] vs.
	// [72,128]) - a point between them should be background.
	assertBackground(t, img, 50, 50)
}

// TestRenderNotdefFallback confirms a simple font with no embedded font
// program at all (buildTextNotdefFallback: /BaseFont /Helvetica, no
// /FontFile2) falls back to internal/fonts' notdefGlyph placeholder - a
// hollow rectangle, not a solid block and not nothing - by sampling a
// point in the ring (painted), a point in the hollow interior
// (unpainted), and a point outside the whole box (unpainted). See that
// generator function's doc comment for the exact derivation of all
// three device-space regions.
func TestRenderNotdefFallback(t *testing.T) {
	img := renderPage(t, "text-notdef-fallback.pdf")
	assertInk(t, img, 8, 60, 255, 0, 0) // in the outer ring, outside the inner hole
	assertBackground(t, img, 30, 60)    // inside the hollow interior
	assertBackground(t, img, 90, 90)    // well outside the whole notdef box
}

// TestRenderTextOnRotatedPage confirms text painting composes correctly
// with a page's /Rotate attribute (established for vector content in
// Phase 2/3 - see pdfviewer_render_test.go's rotated-page coverage):
// buildTextRotatedPage's glyph square, per that generator function's
// doc comment, lands at device x:[12,68] y:[12,68] on the resulting
// 200x100 rotated canvas - not simply "somewhere," but the exact
// coordinate swap this fixture's own /Rotate 90 dictates, since the
// glyph's own symmetric shape and (0,0) text position happen to make
// the swap's effect independently verifiable by hand.
func TestRenderTextOnRotatedPage(t *testing.T) {
	img := renderPage(t, "text-rotated-page.pdf")
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 200x100 (Rotate 90 swaps a 100x200 MediaBox)", b.Dx(), b.Dy())
	}
	assertInk(t, img, 40, 40, 255, 0, 0) // inside the square, [12,68]x[12,68]
	assertBackground(t, img, 90, 90)     // outside it
	assertBackground(t, img, 150, 20)    // well outside, opposite corner region
}

// --- Phase 10: Page.Text --------------------------------------------
//
// The tests below exercise Page.Text (text.go, page.go), the public API
// backed by internal/content's ExtractText - see docs/PLAN2.md's Phase
// 10 entry and internal/content/text_extract_test.go for the lower-level
// coverage of ExtractText's own operator handling. These tests care
// about the two things Phase 10's exit criteria call for: correct
// Unicode text and a defensible per-glyph position, through the full
// public API against real fixtures (not just internal/content's
// synthetic ones).

// textFixture opens fixture, extracts its first page's text via
// Page.Text, and returns the result - shared setup for every test below,
// the Page.Text counterpart to renderPage (pdfviewer_image_test.go).
func textFixture(t *testing.T, fixture string) []pdfviewer.TextGlyph {
	t.Helper()
	doc, err := pdfviewer.OpenFile(fixturePath(fixture))
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", fixture, err)
	}
	t.Cleanup(func() { doc.Close() })
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	glyphs, err := page.Text(context.Background())
	if err != nil {
		t.Fatalf("Text(%s): %v", fixture, err)
	}
	return glyphs
}

// TestPageTextSimpleFont is Phase 10's simple-font exit-criteria
// fixture: buildTextToUnicodeSimple (tools/genfixtures/text.go) shows
// "ABC" at font size 10, text origin (0,0), with a /ToUnicode CMap
// exercising both a plain beginbfchar entry ('A') and a beginbfrange
// array-form entry ('B','C'). Every code is 1000 units wide (1 em) per
// its own /Widths entry, so at font size 10 each glyph should advance
// the pen by exactly 10 page-space points - see that generator
// function's doc comment for the full derivation.
func TestPageTextSimpleFont(t *testing.T) {
	glyphs := textFixture(t, "text-tounicode-simple.pdf")
	if len(glyphs) != 3 {
		t.Fatalf("len(glyphs) = %d, want 3", len(glyphs))
	}

	want := []struct {
		text string
		x    float64
	}{
		{"A", 0},
		{"B", 10},
		{"C", 20},
	}
	for i, w := range want {
		g := glyphs[i]
		if g.Text != w.text {
			t.Errorf("glyphs[%d].Text = %q, want %q", i, g.Text, w.text)
		}
		if g.X != w.x || g.Y != 0 {
			t.Errorf("glyphs[%d] position = (%v,%v), want (%v,0)", i, g.X, g.Y, w.x)
		}
		if g.Width != 10 {
			t.Errorf("glyphs[%d].Width = %v, want 10", i, g.Width)
		}
		if g.FontSize != 10 {
			t.Errorf("glyphs[%d].FontSize = %v, want 10", i, g.FontSize)
		}
	}
}

// TestPageTextType0Font is Phase 10's Type0/CID exit-criteria fixture:
// buildTextToUnicodeType0 shows CID 1 (via Identity-H) at font size 100,
// text origin (0,0), with a /ToUnicode CMap mapping code 1 to U+5B57
// ("字") - a deliberately non-ASCII, non-coincidental character (see
// that generator function's doc comment for why this specific choice
// rules out every fallback path producing the same answer by accident).
// The descendant font's /DW (default width) 1000 applies (no /W entry
// for CID 1 in this particular fixture), so at font size 100 the glyph's
// advance is exactly 100 page-space points.
func TestPageTextType0Font(t *testing.T) {
	glyphs := textFixture(t, "text-tounicode-type0.pdf")
	if len(glyphs) != 1 {
		t.Fatalf("len(glyphs) = %d, want 1", len(glyphs))
	}
	g := glyphs[0]
	if g.Text != "字" {
		t.Errorf("glyphs[0].Text = %q, want %q", g.Text, "字")
	}
	if g.X != 0 || g.Y != 0 {
		t.Errorf("glyphs[0] position = (%v,%v), want (0,0)", g.X, g.Y)
	}
	if g.Width != 100 {
		t.Errorf("glyphs[0].Width = %v, want 100", g.Width)
	}
	if g.FontSize != 100 {
		t.Errorf("glyphs[0].FontSize = %v, want 100", g.FontSize)
	}
}

// TestPageTextType0PredefinedEncodingFallsBackToNoText confirms a Type0
// font with no /ToUnicode CMap at all (text-type0-predefined-encoding.pdf,
// which Render already falls back to notdefGlyph for - see
// TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault above)
// still produces a correctly positioned TextGlyph from Page.Text, just
// with an empty Text field - the "honest gap, not a wrong answer" policy
// TextGlyph.Text's own doc comment describes, exercised end to end.
func TestPageTextType0PredefinedEncodingFallsBackToNoText(t *testing.T) {
	glyphs := textFixture(t, "text-type0-predefined-encoding.pdf")
	if len(glyphs) != 1 {
		t.Fatalf("len(glyphs) = %d, want 1", len(glyphs))
	}
	if glyphs[0].Text != "" {
		t.Errorf("glyphs[0].Text = %q, want \"\" (no /ToUnicode CMap and no /Encoding fallback for a Type0 font)", glyphs[0].Text)
	}
	if glyphs[0].X != 0 || glyphs[0].Y != 0 {
		t.Errorf("glyphs[0] position = (%v,%v), want (0,0)", glyphs[0].X, glyphs[0].Y)
	}
}
