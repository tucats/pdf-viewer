package pdfviewer_test

import "testing"

// This file tests Page.Render's output for Phase 5's shading and
// shading-pattern work - the "sh" operator and a shading selected as a
// fill/stroke paint source via "scn"/"SCN" - see
// tools/genfixtures/main.go's buildAxialShading, buildRadialShading, and
// buildShadingPatternFill doc comments for exactly what each fixture
// paints and why.

// TestRenderAxialShading exercises "sh" painting a black-to-white
// gradient across the whole (clipped-to-page) canvas.
func TestRenderAxialShading(t *testing.T) {
	img := renderFixture(t, "axial-shading.pdf")
	assertPixel(t, img, 0, 50, 0, 0, 0)        // pixel center near the gradient's x=0 end: black
	assertPixel(t, img, 99, 50, 255, 255, 255) // pixel center near the gradient's x=100 end: white
	rMid, _, _, _ := rgba8(img, 50, 50)
	if rMid < 100 || rMid > 155 {
		t.Fatalf("pixel (50,50) red channel = %d, want approximately mid-gray (~127)", rMid)
	}
}

// TestRenderRadialShading exercises "sh" painting a radial gradient
// (black center to blue edge) with /Extend [false true] carrying the
// edge color out to the page's corners, which lie outside the shading's
// own outer circle.
func TestRenderRadialShading(t *testing.T) {
	img := renderFixture(t, "radial-shading.pdf")
	// The pixel nominally "at the center" is sampled at its own center
	// (50.5, 50.5), a hair off the true geometric center (50,50), so its
	// blue channel is very small but not exactly 0 - checked with a
	// wider manual tolerance than assertPixel's fixed +/-2 rather than
	// picking a less obviously-meaningful sample point.
	r, g, b, _ := rgba8(img, 50, 50)
	if r > 10 || g > 10 || b > 10 {
		t.Fatalf("pixel (50,50) = (%d,%d,%d), want near-black (the shading's own center)", r, g, b)
	}
	assertPixel(t, img, 1, 1, 0, 0, 255) // corner, beyond the outer circle: extended blue
}

// TestRenderShadingPatternFill exercises a shading pattern selected via
// "cs Pattern"/"scn" used to fill an 80x80 square: the gradient (black
// to white, spanning the square's own x extent) must be visible inside
// the filled shape, and the page's white background must remain
// untouched outside it.
func TestRenderShadingPatternFill(t *testing.T) {
	img := renderFixture(t, "shading-pattern-fill.pdf")
	assertPixel(t, img, 10, 50, 0, 0, 0)       // near the square's left edge: black
	assertPixel(t, img, 89, 50, 255, 255, 255) // near the square's right edge: white
	assertPixel(t, img, 5, 5, 255, 255, 255)   // outside the square: untouched background
}
