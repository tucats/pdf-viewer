package raster

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// halfMask returns a SoftMask covering the whole canvas 1:1 with device
// pixels (DeviceToMask is the identity matrix - a mask pixel's index is
// exactly the device pixel's own (col, row)), whose left half is fully
// unmasked (255) and whose right half is fully masked out (0) - a simple
// shape that makes "did the mask actually apply, and only where it
// should" easy to check by sampling one pixel from each half.
func halfMask(width, height int) *graphics.SoftMask {
	values := make([]byte, width*height)
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			if col < width/2 {
				values[row*width+col] = 255
			}
		}
	}
	return &graphics.SoftMask{
		Width:        width,
		Height:       height,
		Values:       values,
		DeviceToMask: graphics.Identity(),
	}
}

// TestFillWithSoftMaskAttenuatesHalfTheShape fills the entire canvas
// black over a white background with halfMask active: the left half
// (mask value 1) should end up solid black, the right half (mask value
// 0) should be untouched white, since a zero mask value means "paint
// nothing here" exactly like zero shape coverage or zero constant alpha
// would.
func TestFillWithSoftMaskAttenuatesHalfTheShape(t *testing.T) {
	c := NewCanvas(20, 20, graphics.Color{R: 1, G: 1, B: 1})
	full := rectPath(0, 0, 20, 20)
	mask := halfMask(20, 20)

	c.Fill(full, graphics.NonZero, graphics.Color{}, 1, graphics.BlendNormal, mask, nil)

	if r, _, _, _ := c.Image().At(5, 10).RGBA(); r != 0 {
		t.Errorf("left half (mask=1) should be fully painted black, got R=%d", r)
	}
	if r, _, _, _ := c.Image().At(15, 10).RGBA(); r>>8 != 255 {
		t.Errorf("right half (mask=0) should be untouched white, got R=%d", r>>8)
	}
}

// TestFillWithNilSoftMaskIsUnchanged confirms that passing a nil mask
// (the overwhelmingly common case - "no gs /SMask is active") reproduces
// exactly the same result as the pre-soft-mask Fill signature did: a
// regression test for the "nil must mean unmasked, not masked-out"
// distinction graphics.SoftMask.At's own doc comment calls out as an
// easy mistake to make.
func TestFillWithNilSoftMaskIsUnchanged(t *testing.T) {
	c := NewCanvas(10, 10, graphics.Color{R: 1, G: 1, B: 1})
	full := rectPath(0, 0, 10, 10)
	c.Fill(full, graphics.NonZero, graphics.Color{B: 1}, 1, graphics.BlendNormal, nil, nil)

	if _, _, b, _ := c.Image().At(5, 5).RGBA(); b>>8 != 255 {
		t.Errorf("expected solid blue with no soft mask active, got B=%d", b>>8)
	}
}

// TestFillWithSoftMaskCombinesWithConstantAlpha checks that a soft mask
// and a constant "ca"/"CA" alpha both apply together (multiplied, not
// one overriding the other) - a 50% mask value combined with 50%
// constant alpha over a white background should land at 75% white / 25%
// black (1 - 0.5*0.5 = 0.75), not 50% either way alone.
func TestFillWithSoftMaskCombinesWithConstantAlpha(t *testing.T) {
	c := NewCanvas(4, 1, graphics.Color{R: 1, G: 1, B: 1})
	full := rectPath(0, 0, 4, 1)
	mask := &graphics.SoftMask{
		Width:        4,
		Height:       1,
		Values:       []byte{128, 128, 128, 128}, // ~50%
		DeviceToMask: graphics.Identity(),
	}
	c.Fill(full, graphics.NonZero, graphics.Color{}, 0.5, graphics.BlendNormal, mask, nil)

	r, _, _, _ := c.Image().At(2, 0).RGBA()
	got := r >> 8
	// Expect close to 0.75*255 ~= 191, allowing rounding slack from the
	// 128/255 mask byte not being an exact 0.5.
	if got < 185 || got > 197 {
		t.Errorf("R channel = %d, want ~191 (25%% opaque black over white)", got)
	}
}

// TestDrawImageWithSoftMask checks that Canvas.DrawImage (not just Fill)
// honors a soft mask too - both share the same paint core, but this
// guards against a future refactor accidentally bypassing it for one of
// the two.
func TestDrawImageWithSoftMask(t *testing.T) {
	c := NewCanvas(20, 20, graphics.Color{R: 1, G: 1, B: 1})
	quad := rectPath(0, 0, 20, 20)
	img := &graphics.Image{Width: 1, Height: 1, Pix: []byte{0, 0, 0, 255}} // solid black
	mask := halfMask(20, 20)

	c.DrawImage(quad, graphics.Scale(20, 20), img, false, 1, graphics.BlendNormal, mask, nil)

	if r, _, _, _ := c.Image().At(5, 10).RGBA(); r != 0 {
		t.Errorf("left half (mask=1) should be fully painted black, got R=%d", r)
	}
	if r, _, _, _ := c.Image().At(15, 10).RGBA(); r>>8 != 255 {
		t.Errorf("right half (mask=0) should be untouched white, got R=%d", r>>8)
	}
}
