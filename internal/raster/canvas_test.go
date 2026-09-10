package raster

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

func TestNewCanvasBackground(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{R: 1, G: 0, B: 0})
	r, g, b, a := c.Image().At(1, 1).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 || a>>8 != 255 {
		t.Errorf("background pixel = (%d,%d,%d,%d), want (255,0,0,255)", r>>8, g>>8, b>>8, a>>8)
	}
}

// TestFillOpaqueColorReplacesBackground confirms a fully-covered
// interior pixel ends up as (approximately) exactly the fill color, not
// some blend with the background - i.e. full coverage really does mean
// full coverage.
func TestFillOpaqueColorReplacesBackground(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1}) // white background
	path := rectPath(0, 0, 10, 10)
	c.Fill(path, graphics.NonZero, graphics.Color{B: 1}, 1, graphics.BlendNormal, nil, nil) // solid blue, fully covering the canvas

	r, g, b, _ := c.Image().At(5, 5).RGBA()
	if r>>8 > 2 || g>>8 > 2 || b>>8 < 253 {
		t.Errorf("center pixel = (%d,%d,%d), want ~(0,0,255)", r>>8, g>>8, b>>8)
	}
}

func TestFillLeavesUncoveredPixelsAsBackground(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1})
	path := rectPath(2, 2, 4, 4)
	c.Fill(path, graphics.NonZero, graphics.Color{B: 1}, 1, graphics.BlendNormal, nil, nil)

	r, g, b, _ := c.Image().At(8, 8).RGBA()
	if r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("untouched corner pixel = (%d,%d,%d), want ~(255,255,255)", r>>8, g>>8, b>>8)
	}
}

// TestFillWithClipRestrictsPaintedArea confirms a clip narrows what a
// Fill actually paints: a full-canvas fill, clipped to a small
// rectangle, should leave everything outside that rectangle untouched.
func TestFillWithClipRestrictsPaintedArea(t *testing.T) {
	c := NewCanvas(20, 20, graphics.Color{R: 1, G: 1, B: 1})
	full := rectPath(0, 0, 20, 20)
	clip := rectPath(5, 5, 10, 10)

	c.Fill(full, graphics.NonZero, graphics.Color{}, 1, graphics.BlendNormal, nil, []graphics.ClipPath{{Path: clip, Rule: graphics.NonZero}})

	// Inside the clip: painted black.
	if r, g, b, _ := c.Image().At(7, 7).RGBA(); r>>8 > 2 || g>>8 > 2 || b>>8 > 2 {
		t.Errorf("inside-clip pixel = (%d,%d,%d), want ~(0,0,0)", r>>8, g>>8, b>>8)
	}
	// Outside the clip: still background white.
	if r, g, b, _ := c.Image().At(15, 15).RGBA(); r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("outside-clip pixel = (%d,%d,%d), want ~(255,255,255)", r>>8, g>>8, b>>8)
	}
}

// TestFillWithTwoClipsIntersects confirms multiple active clips combine
// by intersection: painting only where *every* clip covers, not where
// any one of them does.
func TestFillWithTwoClipsIntersects(t *testing.T) {
	c := NewCanvas(20, 20, graphics.Color{R: 1, G: 1, B: 1})
	full := rectPath(0, 0, 20, 20)
	clipA := rectPath(0, 0, 10, 20) // left half
	clipB := rectPath(5, 0, 20, 20) // right five-sixths

	clips := []graphics.ClipPath{{Path: clipA, Rule: graphics.NonZero}, {Path: clipB, Rule: graphics.NonZero}}
	c.Fill(full, graphics.NonZero, graphics.Color{}, 1, graphics.BlendNormal, nil, clips)

	// Intersection is x in [5,10): painted.
	if r, g, b, _ := c.Image().At(7, 10).RGBA(); r>>8 > 2 || g>>8 > 2 || b>>8 > 2 {
		t.Errorf("intersection pixel = (%d,%d,%d), want ~(0,0,0)", r>>8, g>>8, b>>8)
	}
	// In clipA but not clipB (x=2): not painted.
	if r, g, b, _ := c.Image().At(2, 10).RGBA(); r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("clipA-only pixel = (%d,%d,%d), want ~(255,255,255)", r>>8, g>>8, b>>8)
	}
	// In clipB but not clipA (x=15): not painted.
	if r, g, b, _ := c.Image().At(15, 10).RGBA(); r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("clipB-only pixel = (%d,%d,%d), want ~(255,255,255)", r>>8, g>>8, b>>8)
	}
}

// TestFillWithClipIntersectsExactlyAtSubPixelBoundary is Canvas.Fill's
// end-to-end counterpart to
// TestRasterizeIntersectedCoverageExactAtSubPixelBoundary
// (scanline_test.go): a path covering a pixel column's left half,
// clipped by a shape covering that same column's right half, must leave
// the whole column unpainted, not one-quarter blended toward the fill
// color - which is what Phase 15b replaced (multiplying the path's and
// the clip's independently-rasterized coverage) would have produced.
func TestFillWithClipIntersectsExactlyAtSubPixelBoundary(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{R: 1, G: 1, B: 1})
	path := rectPath(0, 0, 2.5, 4)
	clip := rectPath(2.5, 0, 4, 4)

	c.Fill(path, graphics.NonZero, graphics.Color{}, 1, graphics.BlendNormal, nil, []graphics.ClipPath{{Path: clip, Rule: graphics.NonZero}})

	if r, g, b, _ := c.Image().At(2, 1).RGBA(); r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("split-coverage pixel = (%d,%d,%d), want unchanged white (the two halves do not overlap)", r>>8, g>>8, b>>8)
	}
}

func TestFillOutsideCanvasBoundsIsClamped(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{})
	path := rectPath(-100, -100, 100, 100) // wildly overflows the canvas
	// Must not panic.
	c.Fill(path, graphics.NonZero, graphics.Color{R: 1}, 1, graphics.BlendNormal, nil, nil)
	r, _, _, _ := c.Image().At(2, 2).RGBA()
	if r>>8 < 253 {
		t.Errorf("center pixel red = %d, want ~255", r>>8)
	}
}

func TestFillEmptyPathIsNoOp(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{R: 1, G: 1, B: 1})
	var empty graphics.Path
	c.Fill(&empty, graphics.NonZero, graphics.Color{}, 1, graphics.BlendNormal, nil, nil) // must not panic
	r, g, b, _ := c.Image().At(1, 1).RGBA()
	if r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("pixel after filling an empty path = (%d,%d,%d), want unchanged white", r>>8, g>>8, b>>8)
	}
}

// checkerImage returns a 2x2 graphics.Image with a distinct opaque color
// in each quadrant, for tests that need to confirm DrawImage samples the
// right source pixel rather than always the same one.
func checkerImage() *graphics.Image {
	return &graphics.Image{
		Width: 2, Height: 2,
		Pix: []byte{
			255, 0, 0, 255, 0, 255, 0, 255, // row 0: red, green
			0, 0, 255, 255, 255, 255, 0, 255, // row 1: blue, yellow
		},
	}
}

// TestDrawImageFillsCanvasWithCorrectQuadrant confirms DrawImage maps
// image space correctly onto a canvas covering the whole unit square: an
// identity-mapped-to-[0,10]x[0,10] image should place image pixel (0,0)
// (red) at the canvas's top-left and image pixel (1,1) (yellow) at its
// bottom-right, matching image space's own top-left-origin convention
// (see graphics.Image's doc comment).
func TestDrawImageFillsCanvasWithCorrectQuadrant(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1})
	img := checkerImage()
	quad := rectPath(0, 0, 10, 10)
	imageToDevice := graphics.Scale(10, 10)

	c.DrawImage(quad, imageToDevice, img, false, 1, graphics.BlendNormal, nil, nil)

	if r, g, b, _ := c.Image().At(2, 2).RGBA(); r>>8 < 250 || g>>8 > 5 || b>>8 > 5 {
		t.Errorf("top-left region = (%d,%d,%d), want ~red", r>>8, g>>8, b>>8)
	}
	if r, g, b, _ := c.Image().At(7, 2).RGBA(); r>>8 > 5 || g>>8 < 250 || b>>8 > 5 {
		t.Errorf("top-right region = (%d,%d,%d), want ~green", r>>8, g>>8, b>>8)
	}
	if r, g, b, _ := c.Image().At(2, 7).RGBA(); r>>8 > 5 || g>>8 > 5 || b>>8 < 250 {
		t.Errorf("bottom-left region = (%d,%d,%d), want ~blue", r>>8, g>>8, b>>8)
	}
	if r, g, b, _ := c.Image().At(7, 7).RGBA(); r>>8 < 250 || g>>8 < 250 || b>>8 > 5 {
		t.Errorf("bottom-right region = (%d,%d,%d), want ~yellow", r>>8, g>>8, b>>8)
	}
}

// TestDrawImageRespectsAlpha confirms a partially-transparent source
// pixel blends with the canvas's existing background rather than
// replacing it outright.
func TestDrawImageRespectsAlpha(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{R: 1, G: 1, B: 1}) // white background
	img := &graphics.Image{Width: 1, Height: 1, Pix: []byte{0, 0, 0, 128}}
	quad := rectPath(0, 0, 4, 4)
	c.DrawImage(quad, graphics.Scale(4, 4), img, false, 1, graphics.BlendNormal, nil, nil)

	r, _, _, _ := c.Image().At(2, 2).RGBA()
	// Half-transparent black over white should land roughly in the middle.
	if v := r >> 8; v < 100 || v > 160 {
		t.Errorf("blended pixel red channel = %d, want roughly 128 (half-transparent black over white)", v)
	}
}

// TestDrawImageWithClipRestrictsPaintedArea confirms DrawImage honors
// clips exactly like Fill does, since both share the same underlying
// coverage rasterization (paint) - see canvas.go.
func TestDrawImageWithClipRestrictsPaintedArea(t *testing.T) {
	c := NewCanvas(20, 20, graphics.Color{R: 1, G: 1, B: 1})
	img := &graphics.Image{Width: 1, Height: 1, Pix: []byte{0, 0, 0, 255}}
	quad := rectPath(0, 0, 20, 20)
	clip := rectPath(5, 5, 10, 10)

	c.DrawImage(quad, graphics.Scale(20, 20), img, false, 1, graphics.BlendNormal, nil, []graphics.ClipPath{{Path: clip, Rule: graphics.NonZero}})

	if r, g, b, _ := c.Image().At(7, 7).RGBA(); r>>8 > 2 || g>>8 > 2 || b>>8 > 2 {
		t.Errorf("inside-clip pixel = (%d,%d,%d), want ~(0,0,0)", r>>8, g>>8, b>>8)
	}
	if r, g, b, _ := c.Image().At(15, 15).RGBA(); r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("outside-clip pixel = (%d,%d,%d), want ~(255,255,255) (untouched)", r>>8, g>>8, b>>8)
	}
}

func TestDrawImageNilOrEmptyImageIsNoOp(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{R: 1, G: 1, B: 1})
	quad := rectPath(0, 0, 4, 4)
	c.DrawImage(quad, graphics.Scale(4, 4), nil, false, 1, graphics.BlendNormal, nil, nil) // must not panic
	c.DrawImage(quad, graphics.Scale(4, 4), &graphics.Image{}, false, 1, graphics.BlendNormal, nil, nil)
	r, g, b, _ := c.Image().At(1, 1).RGBA()
	if r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("pixel after drawing a nil/empty image = (%d,%d,%d), want unchanged white", r>>8, g>>8, b>>8)
	}
}

func TestDrawImageDegenerateMatrixIsNoOp(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{R: 1, G: 1, B: 1})
	img := &graphics.Image{Width: 1, Height: 1, Pix: []byte{0, 0, 0, 255}}
	quad := rectPath(0, 0, 4, 4)
	degenerate := graphics.Matrix{} // all zero: not invertible
	c.DrawImage(quad, degenerate, img, false, 1, graphics.BlendNormal, nil, nil)
	r, g, b, _ := c.Image().At(1, 1).RGBA()
	if r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("pixel after drawing with a degenerate matrix = (%d,%d,%d), want unchanged white", r>>8, g>>8, b>>8)
	}
}

func TestRenderPaintsImageDrawOps(t *testing.T) {
	img := &graphics.Image{Width: 1, Height: 1, Pix: []byte{0, 128, 255, 255}}
	list := graphics.DisplayList{
		{Path: rectPath(0, 0, 10, 10), Image: img, ImageToDevice: graphics.Scale(10, 10), Alpha: 1},
	}
	out := Render(list, 10, 10, graphics.Color{R: 1, G: 1, B: 1})
	if r, g, b, _ := out.At(5, 5).RGBA(); r>>8 > 2 || g>>8 < 120 || g>>8 > 136 || b>>8 < 253 {
		t.Errorf("image DrawOp pixel = (%d,%d,%d), want ~(0,128,255)", r>>8, g>>8, b>>8)
	}
}

func TestRenderPaintsInOrder(t *testing.T) {
	list := graphics.DisplayList{
		{Path: rectPath(0, 0, 10, 10), Rule: graphics.NonZero, Color: graphics.Color{R: 1}, Alpha: 1},
		{Path: rectPath(2, 2, 8, 8), Rule: graphics.NonZero, Color: graphics.Color{B: 1}, Alpha: 1},
	}
	img := Render(list, 10, 10, graphics.Color{R: 1, G: 1, B: 1})

	// The blue square painted second should win over the red square
	// beneath it.
	if r, g, b, _ := img.At(5, 5).RGBA(); r>>8 > 2 || g>>8 > 2 || b>>8 < 253 {
		t.Errorf("overlap pixel = (%d,%d,%d), want ~(0,0,255) (later op should win)", r>>8, g>>8, b>>8)
	}
	// Outside the blue square but inside red: still red.
	if r, g, b, _ := img.At(1, 1).RGBA(); r>>8 < 253 || g>>8 > 2 || b>>8 > 2 {
		t.Errorf("red-only pixel = (%d,%d,%d), want ~(255,0,0)", r>>8, g>>8, b>>8)
	}
}
