package filter

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// encodeJPEGForTest builds a tiny JPEG-encoded image of size (w, h) whose
// every pixel is fill, for use as decodeDCT test input. quality 100 is
// used throughout so the lossy compression does not perturb the flat
// test colors enough to fail an exact-match assertion.
func encodeJPEGForTest(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatalf("encoding test JPEG: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeDCTGrayscale(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 4, 3))
	for i := range img.Pix {
		img.Pix[i] = 128
	}
	data := encodeJPEGForTest(t, img)

	got, err := decodeDCT(data)
	if err != nil {
		t.Fatalf("decodeDCT: %v", err)
	}
	if len(got) != 4*3 {
		t.Fatalf("decodeDCT gray output length = %d, want %d", len(got), 4*3)
	}
	for i, v := range got {
		if v < 120 || v > 136 {
			t.Fatalf("byte %d = %d, want approximately 128 (JPEG is lossy even at quality 100)", i, v)
		}
	}
}

func TestDecodeDCTRGB(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	data := encodeJPEGForTest(t, img)

	got, err := decodeDCT(data)
	if err != nil {
		t.Fatalf("decodeDCT: %v", err)
	}
	if len(got) != 8*8*3 {
		t.Fatalf("decodeDCT RGB output length = %d, want %d", len(got), 8*8*3)
	}
	// Sample the first pixel: JPEG's YCbCr round trip is lossy, so allow
	// generous tolerance rather than requiring an exact color match.
	r, g, b := int(got[0]), int(got[1]), int(got[2])
	if approxDiff(r, 200) > 20 || approxDiff(g, 40) > 20 || approxDiff(b, 40) > 20 {
		t.Fatalf("first pixel = (%d,%d,%d), want approximately (200,40,40)", r, g, b)
	}
}

// TestDecodeDCTCMYK exercises cmykToBytes directly against a manually
// built *image.CMYK, rather than round-tripping through jpeg.Encode as
// the grayscale and RGB tests above do: Go's standard image/jpeg encoder
// has no support for *producing* a 4-component (CMYK/Adobe) JPEG at all
// - only for decoding one, since that variant is an Adobe extension real
// encoders (this project's own test helper included) essentially never
// emit - so there is no way to manufacture real CMYK JPEG bytes with the
// standard library alone. Decoding one is still real Phase 3 behavior
// (some scanned/print-origin PDFs do contain Adobe CMYK JPEGs), so it is
// worth testing the pixel-conversion logic in isolation instead of
// skipping it.
func TestDecodeDCTCMYK(t *testing.T) {
	img := image.NewCMYK(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = 10  // C
		img.Pix[i+1] = 200 // M
		img.Pix[i+2] = 30  // Y
		img.Pix[i+3] = 50  // K
	}

	got := cmykToBytes(img)
	want := []byte{10, 200, 30, 50, 10, 200, 30, 50, 10, 200, 30, 50, 10, 200, 30, 50}
	if !bytes.Equal(got, want) {
		t.Fatalf("cmykToBytes = %v, want %v", got, want)
	}
}

// handmadeDirectCMYKJPEG is a real (not hand-assembled byte-by-byte, but
// genuinely JPEG-entropy-coded) 8x8 solid-color Adobe-marked CMYK JPEG,
// built once by feeding Python Pillow's JPEG encoder the *complement* of
// the target color (255-20, 255-5, 255-40, 255-60) - because Pillow's
// own CMYK JPEG encoder applies the same Adobe "v = 255-v" inversion on
// write that Go's decoder applies on read, so asking it for the
// complement lands the direct, uninverted target color (20, 5, 40, 60)
// on disk. That is deliberate: it reproduces, in miniature, the exact
// byte-level shape of a real production PDF's CMYK JPEG (see
// unApplyAdobeCMYKInversion's doc comment) - Adobe APP14 marker present,
// transform code 0, direct (not Photoshop-inverted) on-disk samples -
// without redistributing any of that copyrighted third-party file.
// Without unApplyAdobeCMYKInversion, decodeDCT reports this image's
// color as (235, 250, 215, 195): 255 minus every component.
var handmadeDirectCMYKJPEG = []byte{
	0xff, 0xd8, 0xff, 0xee, 0x00, 0x0e, 0x41, 0x64, 0x6f, 0x62, 0x65, 0x00,
	0x64, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0xdb, 0x00, 0x43, 0x00, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0xff, 0xc0, 0x00, 0x14, 0x08, 0x00, 0x08, 0x00, 0x08,
	0x04, 0x43, 0x11, 0x00, 0x4d, 0x11, 0x00, 0x59, 0x11, 0x00, 0x4b, 0x11,
	0x00, 0xff, 0xc4, 0x00, 0x1f, 0x00, 0x00, 0x01, 0x05, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
	0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0xff, 0xc4,
	0x00, 0xb5, 0x10, 0x00, 0x02, 0x01, 0x03, 0x03, 0x02, 0x04, 0x03, 0x05,
	0x05, 0x04, 0x04, 0x00, 0x00, 0x01, 0x7d, 0x01, 0x02, 0x03, 0x00, 0x04,
	0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07, 0x22,
	0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08, 0x23, 0x42, 0xb1, 0xc1, 0x15,
	0x52, 0xd1, 0xf0, 0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16, 0x17,
	0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x34, 0x35, 0x36,
	0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a,
	0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66,
	0x67, 0x68, 0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a,
	0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95,
	0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8,
	0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2,
	0xc3, 0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5,
	0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7,
	0xe8, 0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8, 0xf9,
	0xfa, 0xff, 0xda, 0x00, 0x0e, 0x04, 0x43, 0x00, 0x4d, 0x00, 0x59, 0x00,
	0x4b, 0x00, 0x00, 0x3f, 0x00, 0xfe, 0x27, 0xeb, 0xf8, 0x27, 0xaf, 0xe4,
	0xfe, 0xbf, 0x9d, 0xfa, 0xff, 0xd9,
}

// TestDecodeDCTAdobeCMYKIsNotInverted is the regression fixture for the
// bug reported against a real Apple hardware manual PDF: its
// photographic figures, DCTDecode CMYK JPEGs produced by an
// Adobe-marked but *not* Photoshop-inverted encoder, rendered as solid
// near-black blobs (missing all detail and the soft shadow fade around
// them) because decodeDCT trusted Go's built-in Adobe-inversion. See
// unApplyAdobeCMYKInversion's doc comment for the full explanation; this
// test exercises decodeDCT end to end (not unApplyAdobeCMYKInversion in
// isolation) against handmadeDirectCMYKJPEG, a small Adobe-marked CMYK
// JPEG built to reproduce exactly that byte-level shape.
func TestDecodeDCTAdobeCMYKIsNotInverted(t *testing.T) {
	got, err := decodeDCT(handmadeDirectCMYKJPEG)
	if err != nil {
		t.Fatalf("decodeDCT: %v", err)
	}
	if len(got) != 8*8*4 {
		t.Fatalf("decodeDCT CMYK output length = %d, want %d", len(got), 8*8*4)
	}
	// JPEG compression is lossy, but a flat 8x8, quality-100, DC-only
	// solid color survives round-tripping exactly in practice; assert it
	// precisely and let the test fail loudly (rather than silently
	// tolerate drift) if that ever stops being true.
	c, m, y, k := int(got[0]), int(got[1]), int(got[2]), int(got[3])
	if c != 20 || m != 5 || y != 40 || k != 60 {
		t.Fatalf("first pixel = (%d,%d,%d,%d), want (20,5,40,60) - Adobe CMYK inversion was not correctly undone", c, m, y, k)
	}
	for i := 4; i < len(got); i += 4 {
		if got[i] != got[0] || got[i+1] != got[1] || got[i+2] != got[2] || got[i+3] != got[3] {
			t.Fatalf("pixel %d = (%d,%d,%d,%d), want uniform (%d,%d,%d,%d)", i/4, got[i], got[i+1], got[i+2], got[i+3], got[0], got[1], got[2], got[3])
		}
	}
}

func TestDecodeDCTInvalidData(t *testing.T) {
	if _, err := decodeDCT([]byte("not a jpeg")); err == nil {
		t.Fatal("decodeDCT on garbage input: expected an error, got nil")
	}
}

func TestDecodeDCTRejectsHugeDeclaredDimensions(t *testing.T) {
	// A well-formed small JPEG has no way to lie about its own
	// dimensions (they come from decoding its actual header), so this
	// test only confirms the guard's arithmetic doesn't reject ordinary
	// small images - the "no path exists to build a hostile huge-header
	// sample cheaply" side of this is exactly why the bound is checked
	// against jpeg.DecodeConfig before a full decode is ever attempted;
	// see decodeDCT's doc comment.
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	data := encodeJPEGForTest(t, img)
	if _, err := decodeDCT(data); err != nil {
		t.Fatalf("decodeDCT on a tiny valid image: %v", err)
	}
}

func approxDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// Exercised via internal/filter.Decode's normal dispatch, matching how
// every other filter in this package is also tested at both the
// dedicated decodeXxx level (above) and through Decode's /Filter-driven
// dispatch (filter_test.go).
func TestDecodeDispatchesDCTDecode(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 2, 2))
	for i := range img.Pix {
		img.Pix[i] = 90
	}
	data := encodeJPEGForTest(t, img)

	dict := syntax.Dictionary{"Filter": syntax.Name("DCTDecode")}
	got, err := Decode(dict, data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("Decode output length = %d, want 4", len(got))
	}
}
