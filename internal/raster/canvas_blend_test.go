package raster

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

func TestFillWithPartialAlphaBlendsWithBackground(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1}) // white background
	path := rectPath(0, 0, 10, 10)
	c.Fill(path, graphics.NonZero, graphics.Color{}, 0.5, graphics.BlendNormal, nil) // 50% black over white

	r, g, b, _ := c.Image().At(5, 5).RGBA()
	// Expect ~50% gray (roughly 127-128 per channel), not solid black.
	if r>>8 < 100 || r>>8 > 155 {
		t.Fatalf("pixel (5,5) R = %d, want approximately 127 (50%% alpha blend)", r>>8)
	}
	if g>>8 < 100 || g>>8 > 155 || b>>8 < 100 || b>>8 > 155 {
		t.Fatalf("pixel (5,5) = (%d,%d,%d), want approximately (127,127,127)", r>>8, g>>8, b>>8)
	}
}

func TestFillWithZeroAlphaPaintsNothing(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1})
	path := rectPath(0, 0, 10, 10)
	c.Fill(path, graphics.NonZero, graphics.Color{}, 0, graphics.BlendNormal, nil)

	r, g, b, _ := c.Image().At(5, 5).RGBA()
	if r>>8 < 253 || g>>8 < 253 || b>>8 < 253 {
		t.Fatalf("pixel (5,5) with alpha=0 = (%d,%d,%d), want untouched white background", r>>8, g>>8, b>>8)
	}
}

func TestFillWithMultiplyBlendModeDarkensBackground(t *testing.T) {
	// Gray (0.5) background, multiplied by a fully-opaque gray (0.5)
	// fill should produce 0.25 - darker than either input alone, which
	// BlendNormal (an ordinary replace) could never produce here.
	c := NewCanvas(10, 10, graphics.Color{R: 0.5, G: 0.5, B: 0.5})
	path := rectPath(0, 0, 10, 10)
	c.Fill(path, graphics.NonZero, graphics.Color{R: 0.5, G: 0.5, B: 0.5}, 1, graphics.BlendMultiply, nil)

	r, _, _, _ := c.Image().At(5, 5).RGBA()
	got := r >> 8
	if got < 55 || got > 75 {
		t.Fatalf("pixel (5,5) R = %d, want approximately 64 (0.5*0.5*255)", got)
	}
}

func TestFillWithScreenBlendModeLightensBackground(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 0.5, G: 0.5, B: 0.5})
	path := rectPath(0, 0, 10, 10)
	c.Fill(path, graphics.NonZero, graphics.Color{R: 0.5, G: 0.5, B: 0.5}, 1, graphics.BlendScreen, nil)

	r, _, _, _ := c.Image().At(5, 5).RGBA()
	got := r >> 8
	// Screen(0.5,0.5) = 0.5+0.5-0.25 = 0.75 -> ~191.
	if got < 180 || got > 200 {
		t.Fatalf("pixel (5,5) R = %d, want approximately 191 (Screen(0.5,0.5))", got)
	}
}

// TestFillBlendModeAppliesOnlyWithinAlpha confirms a blend mode's effect
// is itself attenuated by alpha (per the compositing formula
// Cr=(1-alpha)Cb+alpha*B(Cb,Cs)), not applied at full strength regardless
// of alpha.
func TestFillBlendModeAppliesOnlyWithinAlpha(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1}) // white backdrop
	path := rectPath(0, 0, 10, 10)
	// Multiply(white=1, black=0) = 0 (pure black) - but at alpha 0.5,
	// the result should land halfway between the white backdrop and
	// that blended black, i.e. ~50% gray, not pure black.
	c.Fill(path, graphics.NonZero, graphics.Color{}, 0.5, graphics.BlendMultiply, nil)

	r, _, _, _ := c.Image().At(5, 5).RGBA()
	got := r >> 8
	if got < 100 || got > 155 {
		t.Fatalf("pixel (5,5) R = %d, want approximately 127 (half-strength Multiply)", got)
	}
}
