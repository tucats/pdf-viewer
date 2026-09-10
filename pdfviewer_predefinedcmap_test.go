package pdfviewer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file tests WithPredefinedCMaps (Phase 9c, docs/PLAN2.md) at the
// public-API level, mirroring pdfviewer_fontsubstitution_test.go's own
// structure for WithFontSubstitution closely: a real (but entirely
// project-owned, never any of Adobe's actual licensed data - see
// WithPredefinedCMaps' own doc comment) CMap resource file written to a
// temporary directory, opened against text-type0-predefined-encoding.pdf
// (tools/genfixtures/text.go's buildTextType0PredefinedEncoding), which
// TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault
// (pdfviewer_text_test.go) already confirms falls back to notdefGlyph
// with no option configured at all.

// predefinedCMapFileBytes is the tiny CMap this file writes to disk to
// stand in for a real "UniGB-UCS2-H" resource: it maps the exact code
// (0x0041) buildTextType0PredefinedEncoding's content stream shows to
// CID 1 - the same CID buildTextType0Identity/buildTextType0EmbeddedCMap
// reach directly, so a document configured with this data should render
// pixel-identically to those two fixtures.
func predefinedCMapFileBytes() []byte {
	return []byte("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n" +
		"1 begincidrange\n<0041> <0041> 1\nendcidrange\nendcmap\n")
}

// TestRenderType0PredefinedEncodingWithConfiguredCMapSource confirms
// that, once WithPredefinedCMaps points at a directory containing real
// data for the name a Type0 font's /Encoding declares, that data is
// actually used: the same fixture
// TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault shows
// falling back to notdefGlyph now renders the real embedded square
// glyph instead, at the same device rectangle
// TestRenderType0IdentityTextMatchesSimpleFont asserts.
func TestRenderType0PredefinedEncodingWithConfiguredCMapSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "UniGB-UCS2-H"), predefinedCMapFileBytes(), 0o644); err != nil {
		t.Fatalf("writing predefined CMap file: %v", err)
	}

	doc, err := pdfviewer.OpenFile(
		fixturePath("text-type0-predefined-encoding.pdf"),
		pdfviewer.WithPredefinedCMaps(pdfviewer.PredefinedCMaps{Directories: []string{dir}}),
	)
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

	assertInk(t, img, 50, 50, 255, 0, 0)
	assertInk(t, img, 20, 20, 255, 0, 0)
	assertInk(t, img, 80, 80, 255, 0, 0)
	assertBackground(t, img, 5, 5)
	assertBackground(t, img, 95, 95)
}

// TestRenderWithPredefinedCMapsOptionDoesNotBreakOrdinaryRendering
// confirms that simply opting into predefined-CMap resolution - even
// pointed at a directory with nothing useful in it - does not change
// the outcome for a document that doesn't use a predefined encoding at
// all, and does not error or panic. This is
// TestRenderWithFontSubstitutionOptionDoesNotBreakOrdinaryRendering's
// exact counterpart.
func TestRenderWithPredefinedCMapsOptionDoesNotBreakOrdinaryRendering(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("two-pages.pdf"), pdfviewer.WithPredefinedCMaps(pdfviewer.PredefinedCMaps{
		Directories: []string{t.TempDir()},
	}))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if _, err := page.Render(context.Background(), pdfviewer.RenderOptions{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
}

// TestPredefinedCMapsZeroValueHasNoEffect confirms opening succeeds with
// a zero-value PredefinedCMaps (no Directories at all) - unlike
// FontSubstitution's zero value (which usefully scans platform
// defaults), PredefinedCMaps has no default directory list, so this
// should behave identically to not passing the option at all: the same
// fixture still falls back to notdefGlyph.
func TestPredefinedCMapsZeroValueHasNoEffect(t *testing.T) {
	doc, err := pdfviewer.OpenFile(
		fixturePath("text-type0-predefined-encoding.pdf"),
		pdfviewer.WithPredefinedCMaps(pdfviewer.PredefinedCMaps{}),
	)
	if err != nil {
		t.Fatalf("OpenFile with a zero-value PredefinedCMaps: %v", err)
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
	assertInk(t, img, 8, 60, 255, 0, 0) // outer ring of the notdef box - see TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault
	assertBackground(t, img, 50, 60)    // hollow interior
}
