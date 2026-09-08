package raster

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// linearGray returns a Shading whose ColorAt directly reports t as a
// gray level, so a test can check exactly which t value landed at a
// given pixel without needing a real PDF color space or function.
func linearGray(shadingToDevice graphics.Matrix, extend [2]bool) *graphics.Shading {
	return &graphics.Shading{
		Kind:            graphics.AxialShading,
		Coords:          [6]float64{0, 0, 10, 0},
		Domain:          [2]float64{0, 1},
		Extend:          extend,
		ShadingToDevice: shadingToDevice,
		ColorAt:         func(t float64) graphics.Color { return graphics.Color{R: t, G: t, B: t} },
	}
}

func TestFillShadingPaintsGradientAcrossShape(t *testing.T) {
	c := NewCanvas(10, 1, graphics.Color{R: 1, G: 1, B: 1})
	path := rectPath(0, 0, 10, 1)
	sh := linearGray(graphics.Identity(), [2]bool{false, false})

	c.FillShading(path, graphics.NonZero, sh, 1, graphics.BlendNormal, nil)

	// Pixel 0 (center x=0.5) should be near-black (t~0.05); pixel 9
	// (center x=9.5) should be near-white (t~0.95).
	r0, _, _, _ := c.Image().At(0, 0).RGBA()
	r9, _, _, _ := c.Image().At(9, 0).RGBA()
	if r0>>8 > r9>>8 {
		t.Fatalf("gradient did not increase left to right: pixel0 R=%d, pixel9 R=%d", r0>>8, r9>>8)
	}
	if r0>>8 > 40 {
		t.Fatalf("pixel 0 R = %d, want near 0 (near the shading's t=0 end)", r0>>8)
	}
	if r9>>8 < 215 {
		t.Fatalf("pixel 9 R = %d, want near 255 (near the shading's t=1 end)", r9>>8)
	}
}

func TestFillShadingUncoveredRegionPaintsNothing(t *testing.T) {
	c := NewCanvas(20, 1, graphics.Color{R: 1, G: 0, B: 0}) // red background
	path := rectPath(0, 0, 20, 1)
	sh := linearGray(graphics.Identity(), [2]bool{false, false}) // gradient only spans x in [0,10]

	c.FillShading(path, graphics.NonZero, sh, 1, graphics.BlendNormal, nil)

	// Beyond the shading's own geometry (x>10), with no Extend, nothing
	// should be painted - the background (red) must show through.
	r, g, b, _ := c.Image().At(15, 0).RGBA()
	if r>>8 < 250 || g>>8 > 5 || b>>8 > 5 {
		t.Fatalf("pixel (15,0) beyond the shading's geometry = (%d,%d,%d), want the untouched red background", r>>8, g>>8, b>>8)
	}
}

func TestFillShadingRespectsClip(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1})
	full := rectPath(0, 0, 10, 10)
	clip := rectPath(0, 0, 5, 10)
	sh := linearGray(graphics.Scale(1, 1), [2]bool{true, true})

	c.FillShading(full, graphics.NonZero, sh, 1, graphics.BlendNormal, []graphics.ClipPath{{Path: clip, Rule: graphics.NonZero}})

	// Outside the clip (x=8), still untouched white background even
	// though the shading itself (extended) would otherwise cover it.
	r, g, b, _ := c.Image().At(8, 5).RGBA()
	if r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Fatalf("pixel (8,5) outside the clip = (%d,%d,%d), want the untouched white background", r>>8, g>>8, b>>8)
	}
}

// TestPaintShadingCoversWholeCanvasWithoutAPath confirms the "sh"
// operator's whole-canvas painting (graphics.DrawOp's nil-Path
// convention) actually reaches every pixel, not just some
// interpreter-supplied region.
func TestPaintShadingCoversWholeCanvasWithoutAPath(t *testing.T) {
	c := NewCanvas(10, 1, graphics.Color{R: 1, G: 0, B: 0})
	sh := linearGray(graphics.Identity(), [2]bool{true, true})

	c.PaintShading(sh, 1, graphics.BlendNormal, nil)

	// Every pixel across the whole 10-wide canvas should now be some
	// shade of gray (background red fully replaced), including pixel 9
	// at the far right edge.
	r, g, b, _ := c.Image().At(9, 0).RGBA()
	if g>>8 == 0 && b>>8 == 0 {
		t.Fatalf("pixel (9,0) after PaintShading = (%d,%d,%d), still looks like the untouched red background", r>>8, g>>8, b>>8)
	}
}

func TestPaintShadingRespectsClip(t *testing.T) {
	c := NewCanvas(10, 1, graphics.Color{R: 1, G: 0, B: 0})
	clip := rectPath(0, 0, 5, 1)
	sh := linearGray(graphics.Identity(), [2]bool{true, true})

	c.PaintShading(sh, 1, graphics.BlendNormal, []graphics.ClipPath{{Path: clip, Rule: graphics.NonZero}})

	// Outside the clip: still the untouched red background.
	r, g, b, _ := c.Image().At(8, 0).RGBA()
	if r>>8 < 250 || g>>8 > 5 || b>>8 > 5 {
		t.Fatalf("pixel (8,0) outside the clip = (%d,%d,%d), want the untouched red background", r>>8, g>>8, b>>8)
	}
}
