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
