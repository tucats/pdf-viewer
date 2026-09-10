package pdfviewer_test

import (
	"context"
	"image"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file tests Phase 16's page-box selection (docs/PLAN2.md):
// RenderOptions.Box and ThumbnailOptions.Box, exercised against
// tools/genfixtures/main.go's buildPageBoxes fixture ("page-boxes.pdf")
// - see that function's doc comment for the fixture's exact box
// geometry and its nested-ring coloring, which every expectation below
// is derived from.

// pageBoxCases enumerates every PageBox selection against
// page-boxes.pdf's nested geometry. wantSize is the selected box's own
// (width, height) in points - and, at RenderOptions.Scale 1 (Render's
// own default), in device pixels too, since Scale 1 means one device
// pixel per PDF point. wantColor is the color found a few points inside
// that box's own edge: safely within the 10-point-wide ring specific to
// that box (see buildPageBoxes), never close enough to a boundary for
// rounding to matter.
var pageBoxCases = []struct {
	name      string
	box       pdfviewer.PageBox
	wantSize  int // width and height: every box in this fixture is square
	wantColor [3]int
}{
	{"MediaBoxPage", pdfviewer.MediaBoxPage, 200, [3]int{153, 153, 153}}, // 0.6 gray -> 153/255
	{"CropBoxPage", pdfviewer.CropBoxPage, 180, [3]int{255, 0, 0}},
	{"BleedBoxPage", pdfviewer.BleedBoxPage, 160, [3]int{0, 255, 0}},
	{"TrimBoxPage", pdfviewer.TrimBoxPage, 140, [3]int{0, 0, 255}},
	{"ArtBoxPage", pdfviewer.ArtBoxPage, 120, [3]int{255, 255, 0}},
}

// TestRenderPageBoxSelection confirms RenderOptions.Box both sizes the
// rendered image to the selected box's own dimensions and re-anchors it
// at that box's own origin - not simply cropping a fixed, MediaBox-sized
// render after the fact (a size-only check could not tell that apart
// from correctly re-anchored geometry; sampling a color specific to the
// selected box's own ring can).
func TestRenderPageBoxSelection(t *testing.T) {
	for _, c := range pageBoxCases {
		t.Run(c.name, func(t *testing.T) {
			img := renderFixtureWithOptions(t, "page-boxes.pdf", pdfviewer.RenderOptions{Box: c.box})

			if got := img.Bounds(); got != image.Rect(0, 0, c.wantSize, c.wantSize) {
				t.Fatalf("Bounds() = %v, want (0,0)-(%d,%d)", got, c.wantSize, c.wantSize)
			}
			// 5 points inside the rendered image's top-left corner on
			// both axes: well inside the 10-point ring specific to this
			// box selection, and far enough from the image edge itself
			// (device pixel (0,0)) that edge rounding cannot affect it.
			assertPixel(t, img, 5, 5, c.wantColor[0], c.wantColor[1], c.wantColor[2])
		})
	}
}

// TestRenderPageBoxDefaultIsCropBox confirms RenderOptions{}'s zero
// value (no Box set at all) renders identically to explicitly selecting
// CropBoxPage - this package's long-standing default behavior, which
// must not change just because the Box field now exists.
func TestRenderPageBoxDefaultIsCropBox(t *testing.T) {
	withDefault := renderFixtureWithOptions(t, "page-boxes.pdf", pdfviewer.RenderOptions{})
	withExplicitCrop := renderFixtureWithOptions(t, "page-boxes.pdf", pdfviewer.RenderOptions{Box: pdfviewer.CropBoxPage})

	if got, want := withDefault.Bounds(), withExplicitCrop.Bounds(); got != want {
		t.Fatalf("RenderOptions{} Bounds() = %v, want %v (should match explicit CropBoxPage)", got, want)
	}
	assertPixel(t, withDefault, 5, 5, 255, 0, 0) // CropBoxPage's own ring: red
}

// TestThumbnailPageBoxSelection is TestRenderPageBoxSelection's
// Thumbnail counterpart: ThumbnailOptions.Box feeds the same selected
// box into both thumbnailScale (which the page's box, not always
// CropBox, must size the output image relative to - see
// pageImpl.thumbnailScale in page.go) and renderAtScale itself. A large
// MaxDimension is used so every box's 10-point ring maps to a generous
// number of device pixels regardless of the scale each box selection
// happens to compute, keeping the fixed 5-pixel inward sample point
// safely inside it.
func TestThumbnailPageBoxSelection(t *testing.T) {
	const maxDimension = 1000

	for _, c := range pageBoxCases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := pdfviewer.OpenFile(fixturePath("page-boxes.pdf"))
			if err != nil {
				t.Fatalf("OpenFile: %v", err)
			}
			defer doc.Close()
			page, err := doc.Page(0)
			if err != nil {
				t.Fatalf("Page(0): %v", err)
			}
			img, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{
				MaxDimension: maxDimension,
				Box:          c.box,
			})
			if err != nil {
				t.Fatalf("Thumbnail: %v", err)
			}

			bounds := img.Bounds()
			if bounds.Dx() != bounds.Dy() {
				t.Fatalf("Thumbnail() = %dx%d, want a square image (every page-boxes.pdf box is square)", bounds.Dx(), bounds.Dy())
			}
			if bounds.Dx() > maxDimension {
				t.Fatalf("Thumbnail() = %dx%d, want both dimensions <= %d", bounds.Dx(), bounds.Dy(), maxDimension)
			}
			assertPixel(t, img, 5, 5, c.wantColor[0], c.wantColor[1], c.wantColor[2])
		})
	}
}
