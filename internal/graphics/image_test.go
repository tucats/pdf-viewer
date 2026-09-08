package graphics

import "testing"

func TestImageAtReadsPixel(t *testing.T) {
	// A 2x1 image: an opaque red pixel at (0,0), a half-transparent green
	// pixel at (1,0).
	img := &Image{
		Width: 2, Height: 1,
		Pix: []byte{
			255, 0, 0, 255,
			0, 255, 0, 128,
		},
	}

	r, g, b, a := img.At(0, 0)
	if !approxEqual(r, 1) || !approxEqual(g, 0) || !approxEqual(b, 0) || !approxEqual(a, 1) {
		t.Errorf("At(0,0) = (%v,%v,%v,%v), want (1,0,0,1)", r, g, b, a)
	}

	r, g, b, a = img.At(1, 0)
	if !approxEqual(r, 0) || !approxEqual(g, 1) || !approxEqual(b, 0) || !approxEqual(a, 128.0/255) {
		t.Errorf("At(1,0) = (%v,%v,%v,%v), want (0,1,0,%v)", r, g, b, a, 128.0/255)
	}
}

func TestImageAtOutOfBoundsIsTransparentBlack(t *testing.T) {
	img := &Image{Width: 1, Height: 1, Pix: []byte{255, 255, 255, 255}}
	cases := [][2]int{{-1, 0}, {0, -1}, {1, 0}, {0, 1}, {5, 5}}
	for _, c := range cases {
		r, g, b, a := img.At(c[0], c[1])
		if r != 0 || g != 0 || b != 0 || a != 0 {
			t.Errorf("At(%d,%d) = (%v,%v,%v,%v), want (0,0,0,0)", c[0], c[1], r, g, b, a)
		}
	}
}

func TestImageAtNilImageIsTransparentBlack(t *testing.T) {
	var img *Image
	r, g, b, a := img.At(0, 0)
	if r != 0 || g != 0 || b != 0 || a != 0 {
		t.Errorf("nil Image.At(0,0) = (%v,%v,%v,%v), want (0,0,0,0)", r, g, b, a)
	}
}
