package jpx

import (
	"math/rand"
	"testing"
)

// This file tests encode.go's own new logic (EncodeGray/EncodeRGB and
// their shared codestream assembly) end to end, through the real public
// Decode entry point - the counterpart, for a real encoder producing a
// real codestream, to TestDecodeSingleTileGrayscale's (image_test.go)
// hand-assembled one. Every primitive encode.go promoted from an earlier
// sub-phase's own test file (bitWriter, tier1Encoder, forwardRCT, and so
// on) already has its own dedicated round-trip coverage where it always
// has (mq_test.go, tier1_test.go, mct_test.go, packet_roundtrip_test.go)
// - what these tests add is that EncodeGray/EncodeRGB actually compose
// them correctly into one working codestream.

func TestEncodeGrayRoundTrip(t *testing.T) {
	const w, h = 32, 32
	pix := make([]byte, w*h)
	rng := rand.New(rand.NewSource(1))
	for i := range pix {
		pix[i] = byte(rng.Intn(256))
	}

	codestream, err := EncodeGray(w, h, pix)
	if err != nil {
		t.Fatalf("EncodeGray: %v", err)
	}
	h2, err := ParseHeader(codestream)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	img, err := Decode(h2, codestream)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if img.Width != w || img.Height != h || len(img.Components) != 1 {
		t.Fatalf("unexpected shape: %dx%d, %d component(s)", img.Width, img.Height, len(img.Components))
	}
	for i, want := range pix {
		if got := img.Components[0].Samples[i]; got != int32(want) {
			t.Errorf("sample %d: got %d, want %d", i, got, want)
		}
	}
}

// TestEncodeRGBRoundTrip covers EncodeRGB's forward RCT plus random
// component values (the general case); TestEncodeRGBPrimariesRoundTrip
// below separately checks the saturated-primary-colour worst case
// encodeTileBitPlanes' own doc comment reasons about.
func TestEncodeRGBRoundTrip(t *testing.T) {
	const w, h = 32, 32
	r := make([]byte, w*h)
	g := make([]byte, w*h)
	b := make([]byte, w*h)
	rng := rand.New(rand.NewSource(2))
	for i := range r {
		r[i] = byte(rng.Intn(256))
		g[i] = byte(rng.Intn(256))
		b[i] = byte(rng.Intn(256))
	}

	codestream, err := EncodeRGB(w, h, r, g, b)
	if err != nil {
		t.Fatalf("EncodeRGB: %v", err)
	}
	h2, err := ParseHeader(codestream)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	img, err := Decode(h2, codestream)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if img.Width != w || img.Height != h || len(img.Components) != 3 {
		t.Fatalf("unexpected shape: %dx%d, %d component(s)", img.Width, img.Height, len(img.Components))
	}
	check := func(name string, comp []int32, want []byte) {
		for i, wv := range want {
			if comp[i] != int32(wv) {
				t.Errorf("%s sample %d: got %d, want %d", name, i, comp[i], wv)
			}
		}
	}
	check("R", img.Components[0].Samples, r)
	check("G", img.Components[1].Samples, g)
	check("B", img.Components[2].Samples, b)
}

// TestEncodeRGBPrimariesRoundTrip checks every saturated primary/
// secondary colour and black/white - the RCT inputs that push a Cb/Cr-
// like difference component to its extreme magnitude (255), the exact
// bound encodeTileBitPlanes' own doc comment reasons about.
func TestEncodeRGBPrimariesRoundTrip(t *testing.T) {
	const w, h = 4, 4
	solid := func(v byte) []byte {
		p := make([]byte, w*h)
		for i := range p {
			p[i] = v
		}
		return p
	}
	cases := []struct {
		name    string
		r, g, b byte
	}{
		{"red", 255, 0, 0},
		{"green", 0, 255, 0},
		{"blue", 0, 0, 255},
		{"yellow", 255, 255, 0},
		{"cyan", 0, 255, 255},
		{"magenta", 255, 0, 255},
		{"white", 255, 255, 255},
		{"black", 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codestream, err := EncodeRGB(w, h, solid(c.r), solid(c.g), solid(c.b))
			if err != nil {
				t.Fatalf("EncodeRGB: %v", err)
			}
			h2, err := ParseHeader(codestream)
			if err != nil {
				t.Fatalf("ParseHeader: %v", err)
			}
			img, err := Decode(h2, codestream)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			for i := 0; i < w*h; i++ {
				got := [3]int32{img.Components[0].Samples[i], img.Components[1].Samples[i], img.Components[2].Samples[i]}
				want := [3]int32{int32(c.r), int32(c.g), int32(c.b)}
				if got != want {
					t.Fatalf("sample %d: got %v, want %v", i, got, want)
				}
			}
		})
	}
}

// TestEncodeGrayWrongLength confirms a pixel slice whose length does not
// match width*height is rejected rather than panicking or silently
// reading out of bounds.
func TestEncodeGrayWrongLength(t *testing.T) {
	if _, err := EncodeGray(4, 4, make([]byte, 10)); err == nil {
		t.Fatal("expected an error for a mismatched pixel slice length, got nil")
	}
}

func TestEncodeGrayInvalidSize(t *testing.T) {
	if _, err := EncodeGray(0, 4, nil); err == nil {
		t.Fatal("expected an error for a zero image dimension, got nil")
	}
}

// TestEncodeGrayNonMultipleOfCodeBlockSize confirms an image whose
// dimensions do not divide evenly by encodeCodeBlockPixels (this file's
// fixed 16x16 code-block size) still round-trips - geometry.go's
// buildCodeBlocks already clips edge code-blocks to the subband's own
// bounds, so this is mostly confirming encodeCodestream extracts each
// clipped block's magnitude/sign data from the right (smaller) region.
func TestEncodeGrayNonMultipleOfCodeBlockSize(t *testing.T) {
	const w, h = 25, 19
	pix := make([]byte, w*h)
	rng := rand.New(rand.NewSource(3))
	for i := range pix {
		pix[i] = byte(rng.Intn(256))
	}

	codestream, err := EncodeGray(w, h, pix)
	if err != nil {
		t.Fatalf("EncodeGray: %v", err)
	}
	h2, err := ParseHeader(codestream)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	img, err := Decode(h2, codestream)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for i, want := range pix {
		if got := img.Components[0].Samples[i]; got != int32(want) {
			t.Errorf("sample %d: got %d, want %d", i, got, want)
		}
	}
}
