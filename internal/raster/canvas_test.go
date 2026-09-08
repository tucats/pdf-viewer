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
	c.Fill(path, graphics.NonZero, graphics.Color{B: 1}, nil) // solid blue, fully covering the canvas

	r, g, b, _ := c.Image().At(5, 5).RGBA()
	if r>>8 > 2 || g>>8 > 2 || b>>8 < 253 {
		t.Errorf("center pixel = (%d,%d,%d), want ~(0,0,255)", r>>8, g>>8, b>>8)
	}
}

func TestFillLeavesUncoveredPixelsAsBackground(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1})
	path := rectPath(2, 2, 4, 4)
	c.Fill(path, graphics.NonZero, graphics.Color{B: 1}, nil)

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

	c.Fill(full, graphics.NonZero, graphics.Color{}, []graphics.ClipPath{{Path: clip, Rule: graphics.NonZero}})

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
	c.Fill(full, graphics.NonZero, graphics.Color{}, clips)

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

func TestFillOutsideCanvasBoundsIsClamped(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{})
	path := rectPath(-100, -100, 100, 100) // wildly overflows the canvas
	// Must not panic.
	c.Fill(path, graphics.NonZero, graphics.Color{R: 1}, nil)
	r, _, _, _ := c.Image().At(2, 2).RGBA()
	if r>>8 < 253 {
		t.Errorf("center pixel red = %d, want ~255", r>>8)
	}
}

func TestFillEmptyPathIsNoOp(t *testing.T) {
	c := NewCanvas(4, 4, graphics.Color{R: 1, G: 1, B: 1})
	var empty graphics.Path
	c.Fill(&empty, graphics.NonZero, graphics.Color{}, nil) // must not panic
	r, g, b, _ := c.Image().At(1, 1).RGBA()
	if r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Errorf("pixel after filling an empty path = (%d,%d,%d), want unchanged white", r>>8, g>>8, b>>8)
	}
}

func TestRenderPaintsInOrder(t *testing.T) {
	list := graphics.DisplayList{
		{Path: rectPath(0, 0, 10, 10), Rule: graphics.NonZero, Color: graphics.Color{R: 1}},
		{Path: rectPath(2, 2, 8, 8), Rule: graphics.NonZero, Color: graphics.Color{B: 1}},
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
