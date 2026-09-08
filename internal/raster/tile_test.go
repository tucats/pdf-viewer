package raster

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

func TestRenderTransparentUncoveredPixelsStayTransparent(t *testing.T) {
	list := graphics.DisplayList{
		{Path: rectPath(2, 2, 4, 4), Color: graphics.Color{R: 1}, Alpha: 1},
	}
	img := RenderTransparent(list, 10, 10)

	r, g, b, a := img.At(8, 8) // well outside the painted rectangle
	if a != 0 {
		t.Fatalf("uncovered pixel alpha = %v, want 0 (transparent)", a)
	}
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("uncovered pixel = (%v,%v,%v), want (0,0,0)", r, g, b)
	}
}

func TestRenderTransparentFullyCoveredPixelIsOpaque(t *testing.T) {
	list := graphics.DisplayList{
		{Path: rectPath(0, 0, 10, 10), Color: graphics.Color{R: 1}, Alpha: 1},
	}
	img := RenderTransparent(list, 10, 10)

	r, g, b, a := img.At(5, 5)
	if a < 0.99 {
		t.Fatalf("fully covered pixel alpha = %v, want ~1", a)
	}
	if r < 0.99 || g > 0.01 || b > 0.01 {
		t.Fatalf("fully covered pixel = (%v,%v,%v), want ~(1,0,0)", r, g, b)
	}
}

// TestRenderTransparentPartialAlphaAccumulatesRealAlpha confirms a
// half-alpha fill over the transparent buffer ends up with alpha ~0.5,
// not fully opaque (which Canvas's fixed 255 output alpha would give) -
// the whole point of this function existing separately from Render.
func TestRenderTransparentPartialAlphaAccumulatesRealAlpha(t *testing.T) {
	list := graphics.DisplayList{
		{Path: rectPath(0, 0, 10, 10), Color: graphics.Color{R: 1}, Alpha: 0.5},
	}
	img := RenderTransparent(list, 10, 10)

	_, _, _, a := img.At(5, 5)
	if a < 0.45 || a > 0.55 {
		t.Fatalf("alpha = %v, want ~0.5", a)
	}
}

// TestRenderTransparentSecondOpCompositesOverFirst confirms two
// overlapping DrawOps composite via "over" (later on top), with the
// combined alpha and color reflecting both layers rather than either
// one alone.
func TestRenderTransparentSecondOpCompositesOverFirst(t *testing.T) {
	list := graphics.DisplayList{
		{Path: rectPath(0, 0, 10, 10), Color: graphics.Color{R: 1}, Alpha: 1},
		{Path: rectPath(0, 0, 10, 10), Color: graphics.Color{B: 1}, Alpha: 0.5},
	}
	img := RenderTransparent(list, 10, 10)

	r, _, b, a := img.At(5, 5)
	if a < 0.99 {
		t.Fatalf("alpha = %v, want ~1 (fully opaque red, then half-alpha blue over it)", a)
	}
	// Over an opaque red backdrop, a 50%-alpha blue source lands at 50%
	// red + 50% blue.
	if r < 0.45 || r > 0.55 || b < 0.45 || b > 0.55 {
		t.Fatalf("color = (%v,_,%v), want ~(0.5,_,0.5)", r, b)
	}
}

func TestRenderTransparentNilPathIsSkipped(t *testing.T) {
	list := graphics.DisplayList{{Path: nil}} // must not panic
	img := RenderTransparent(list, 4, 4)
	_, _, _, a := img.At(2, 2)
	if a != 0 {
		t.Fatalf("alpha = %v, want 0", a)
	}
}

func TestRenderTransparentImageDrawOpWithRepeat(t *testing.T) {
	tile := &graphics.Image{Width: 1, Height: 1, Pix: []byte{0, 255, 0, 255}}
	list := graphics.DisplayList{
		{
			Path: rectPath(0, 0, 10, 10), Image: tile,
			ImageToDevice: graphics.Scale(2, 2), // one repetition is only 2x2 device pixels
			Repeat:        true, Alpha: 1,
		},
	}
	img := RenderTransparent(list, 10, 10)
	// Far from the origin, well beyond the single 2x2 repetition, the
	// repeated tile should still cover the pixel (green, opaque).
	r, g, b, a := img.At(8, 8)
	if a < 0.99 || r > 0.05 || g < 0.95 || b > 0.05 {
		t.Fatalf("repeated tile pixel = (%v,%v,%v,%v), want ~(0,1,0,1)", r, g, b, a)
	}
}
