package pdfviewer_test

import (
	"testing"
)

// This file tests Page.Render's output for Phase 5's color-space work:
// a content stream's "cs"/"scn" resolving a named, non-Device color
// space (Separation, Lab) through internal/image's exported color-space
// logic (see internal/image/public.go and internal/content/colorspace.go)
// instead of only ever guessing DeviceGray/RGB/CMYK from operand count -
// see tools/genfixtures/main.go's buildSeparationFill and buildLabFill
// doc comments for exactly what each fixture paints and why.

// TestRenderSeparationFill exercises a Separation color space whose tint
// transform maps tint 1 to (0, 0.5, 1) - a color no component-count
// fallback could produce for a single numeric "scn" operand (which would
// otherwise be read as DeviceGray, painting a mid-gray instead).
func TestRenderSeparationFill(t *testing.T) {
	img := renderFixture(t, "separation-fill.pdf")
	assertPixel(t, img, 50, 50, 0, 128, 255) // deep interior of the square
	assertPixel(t, img, 5, 5, 255, 255, 255) // outside the square: white background
}

// TestRenderLabFill exercises a Lab color space's two unambiguous
// extremes (L*=0 and L*=100, both with neutral a*=b*=0) painted as the
// left and right halves of the page respectively.
func TestRenderLabFill(t *testing.T) {
	img := renderFixture(t, "lab-fill.pdf")
	assertPixel(t, img, 25, 50, 0, 0, 0)       // left half: L*=0, black
	assertPixel(t, img, 75, 50, 255, 255, 255) // right half: L*=100, white
}
