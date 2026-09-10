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

// TestRenderFunctionBasedShading exercises "sh" painting a function-based
// (/ShadingType 1) shading, whose color comes directly from a 2-input
// /Function rather than any axial/radial line-or-circle geometry - see
// tools/genfixtures's buildFunctionBasedShading doc comment for exactly
// which corner ends up which color and why (the render pipeline's
// PDF-to-device y-axis flip swaps top and bottom relative to the
// shading's own domain space).
func TestRenderFunctionBasedShading(t *testing.T) {
	img := renderFixture(t, "function-based-shading.pdf")
	assertPixel(t, img, 0, 0, 0, 255, 0)    // top-left: domain ~(0,1), green
	assertPixel(t, img, 99, 0, 255, 255, 0) // top-right: domain ~(1,1), yellow
	assertPixel(t, img, 0, 99, 0, 0, 0)     // bottom-left: domain ~(0,0), black
	assertPixel(t, img, 99, 99, 255, 0, 0)  // bottom-right: domain ~(1,0), red
}

// TestRenderFreeFormTriangleMeshShading exercises "sh" painting a Type 4
// (free-form Gouraud-shaded triangle mesh) shading: one triangle, red at
// PDF-space (0,0), green at (100,0), blue at (0,100) - see
// tools/genfixtures's buildFreeFormTriangleMeshShading doc comment for
// the exact geometry and the y-axis flip that puts red at the device
// image's bottom-left rather than top-left. Unlike every other shading
// type this project supports, a mesh shading also leaves part of the
// page entirely unpainted (wherever no triangle covers it) - checked
// here via the top-right corner, which lies outside this fixture's one
// triangle.
func TestRenderFreeFormTriangleMeshShading(t *testing.T) {
	img := renderFixture(t, "mesh-shading-type4.pdf")
	// Gouraud shading blends linearly across the *whole* triangle, so
	// even a pixel close to a vertex already carries a few percent of the
	// other two corners' colors (over a ~100-unit-wide triangle, a couple
	// of pixels in is a couple of percent of the way across) - too much
	// for assertPixel's fixed +/-2 tolerance, so "dominant color" checks
	// are used here instead, the same reasoning as
	// TestRenderAxialShading's own manual mid-gradient tolerance check.
	dominant := func(x, y int, wantChannel int, other1, other2 int) {
		t.Helper()
		vals := []int{0, 0, 0}
		vals[0], vals[1], vals[2], _ = rgba8(img, x, y)
		if vals[wantChannel] < 200 || vals[other1] > 60 || vals[other2] > 60 {
			t.Errorf("pixel (%d,%d) = (%d,%d,%d), want channel %d clearly dominant", x, y, vals[0], vals[1], vals[2], wantChannel)
		}
	}
	dominant(1, 98, 0, 1, 2)                  // near the red vertex (bottom-left): R dominant
	dominant(98, 98, 1, 0, 2)                 // near the green vertex (bottom-right): G dominant
	dominant(1, 1, 2, 0, 1)                   // near the blue vertex (top-left): B dominant
	assertPixel(t, img, 98, 1, 255, 255, 255) // top-right: outside the triangle, untouched background
}

// TestRenderLatticeFormTriangleMeshShading exercises "sh" painting a
// Type 5 (lattice-form Gouraud-shaded triangle mesh) shading: a 2x2 grid
// covering the whole page, red/green/blue/yellow at its four corners -
// see tools/genfixtures's buildLatticeFormTriangleMeshShading doc comment
// for the exact geometry. Unlike the Type 4 fixture, this one covers the
// entire page, so there is no "outside the mesh" background check here.
func TestRenderLatticeFormTriangleMeshShading(t *testing.T) {
	img := renderFixture(t, "mesh-shading-type5.pdf")
	// See TestRenderFreeFormTriangleMeshShading's comment on why a
	// "dominant channel" check, not an exact-color assertPixel, is used
	// this close to a vertex under linear Gouraud interpolation.
	dominant := func(x, y int, wantChannel int, other1, other2 int) {
		t.Helper()
		vals := []int{0, 0, 0}
		vals[0], vals[1], vals[2], _ = rgba8(img, x, y)
		if vals[wantChannel] < 200 || vals[other1] > 60 || vals[other2] > 60 {
			t.Errorf("pixel (%d,%d) = (%d,%d,%d), want channel %d clearly dominant", x, y, vals[0], vals[1], vals[2], wantChannel)
		}
	}
	dominant(1, 98, 0, 1, 2)  // near red, PDF-space (0,0): bottom-left
	dominant(98, 98, 1, 0, 2) // near green, PDF-space (100,0): bottom-right
	dominant(1, 1, 2, 0, 1)   // near blue, PDF-space (0,100): top-left

	// Near yellow, PDF-space (100,100), top-right: both R and G should be
	// clearly present and B clearly absent - yellow needs its own check
	// since it is not a single dominant channel the way the other three
	// corners are.
	r, g, b, _ := rgba8(img, 98, 1)
	if r < 200 || g < 200 || b > 60 {
		t.Errorf("pixel (98,1) = (%d,%d,%d), want near-yellow (high R, high G, low B)", r, g, b)
	}
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
