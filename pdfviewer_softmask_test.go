package pdfviewer_test

import "testing"

// This file tests Page.Render's output for Phase 17's ExtGState-level
// soft mask work ("gs"'s /SMask parameter) - see
// tools/genfixtures/main.go's buildSoftMaskLuminosity and
// buildSoftMaskAlpha doc comments for exactly what each fixture paints
// and why.

// TestRenderSoftMaskLuminosity exercises "gs"'s /SMask with /S
// /Luminosity: the left half of the page (under the mask group's white
// half, luminosity 1, fully unmasked) should be painted solid black, and
// the right half (under the mask group's black half, luminosity 0,
// fully masked out) should stay the untouched white background.
func TestRenderSoftMaskLuminosity(t *testing.T) {
	img := renderFixture(t, "softmask-luminosity.pdf")

	r, g, b, _ := rgba8(img, 25, 50)
	if r > 5 || g > 5 || b > 5 {
		t.Errorf("left half (25,50) = (%d,%d,%d), want ~black (fully unmasked)", r, g, b)
	}
	r, g, b, _ = rgba8(img, 75, 50)
	if r < 250 || g < 250 || b < 250 {
		t.Errorf("right half (75,50) = (%d,%d,%d), want ~white (fully masked out)", r, g, b)
	}
}

// TestRenderSoftMaskAlpha exercises "gs"'s /SMask with /S /Alpha: the
// left half of the page (under the mask group's opaque-covered half)
// should be painted red, and the right half (under the mask group's
// entirely unpainted half, alpha 0) should stay the untouched white
// background - the same left/right split as the luminosity fixture,
// but driven by the mask group's own rendered coverage rather than its
// brightness.
func TestRenderSoftMaskAlpha(t *testing.T) {
	img := renderFixture(t, "softmask-alpha.pdf")

	r, g, b, _ := rgba8(img, 25, 50)
	if r < 250 || g > 5 || b > 5 {
		t.Errorf("left half (25,50) = (%d,%d,%d), want ~red (fully unmasked)", r, g, b)
	}
	r, g, b, _ = rgba8(img, 75, 50)
	if r < 250 || g < 250 || b < 250 {
		t.Errorf("right half (75,50) = (%d,%d,%d), want ~white (fully masked out)", r, g, b)
	}
}
