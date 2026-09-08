package pdfviewer_test

import (
	"context"
	"image"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// renderFixtureWithOptions is renderFixture (pdfviewer_render_test.go)
// with caller-supplied RenderOptions, needed only by this file's
// HideAnnotations test - every other test in this package renders with
// the zero-value RenderOptions{}.
func renderFixtureWithOptions(t *testing.T, name string, opts pdfviewer.RenderOptions) image.Image {
	t.Helper()
	doc, err := pdfviewer.OpenFile(fixturePath(name))
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", name, err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	img, err := page.Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("Render(%s): %v", name, err)
	}
	return img
}

// TestRenderAnnotationAppearance exercises Page.Render's annotation
// appearance-stream support end to end: tools/genfixtures/main.go's
// buildAnnotationAppearance places a green square appearance whose
// /BBox exactly matches its annotation's /Rect size, landing it at
// device (20,20)-(80,80) after the page's own y-flip - see that
// function's doc comment for the hand-derived geometry.
func TestRenderAnnotationAppearance(t *testing.T) {
	img := renderFixture(t, "annotation-appearance.pdf")
	assertPixel(t, img, 50, 50, 0, 255, 0)     // deep interior of the appearance: green
	assertPixel(t, img, 5, 5, 255, 255, 255)   // outside the annotation's /Rect: untouched white
	assertPixel(t, img, 95, 95, 255, 255, 255) // outside the annotation's /Rect: untouched white
}

// TestRenderAnnotationHiddenFlagIsNeverPainted confirms an annotation
// with /F Hidden is never painted, regardless of RenderOptions -
// distinct from RenderOptions.HideAnnotations (a caller's own opt-out),
// which TestRenderHideAnnotationsOption below exercises separately.
func TestRenderAnnotationHiddenFlagIsNeverPainted(t *testing.T) {
	img := renderFixture(t, "annotation-hidden.pdf")
	assertPixel(t, img, 50, 50, 255, 255, 255) // would be solid red if painted; must stay white
}

// TestRenderHideAnnotationsOption confirms RenderOptions.HideAnnotations
// suppresses an otherwise-visible annotation appearance that
// TestRenderAnnotationAppearance already confirms paints by default.
func TestRenderHideAnnotationsOption(t *testing.T) {
	img := renderFixtureWithOptions(t, "annotation-appearance.pdf", pdfviewer.RenderOptions{HideAnnotations: true})
	assertPixel(t, img, 50, 50, 255, 255, 255) // would be green if annotations were shown
}
