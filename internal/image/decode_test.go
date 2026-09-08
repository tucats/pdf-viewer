package image

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// packRow bit-packs one row's worth of already-interleaved raw component
// values (e.g. for a 2-pixel DeviceRGB row, [r0,g0,b0,r1,g1,b1]) into
// bytes, most-significant-bit first, matching the PDF specification's
// image sample packing (and this package's own bitReader) - see
// decode.go's package-level doc comment on bitReader.
func packRow(t *testing.T, samples []uint32, bpc int) []byte {
	t.Helper()
	totalBits := len(samples) * bpc
	out := make([]byte, (totalBits+7)/8)
	bitPos := 0
	for _, s := range samples {
		for i := bpc - 1; i >= 0; i-- {
			bit := byte((s >> uint(i)) & 1)
			byteIdx := bitPos / 8
			shift := 7 - (bitPos % 8)
			out[byteIdx] |= bit << uint(shift)
			bitPos++
		}
	}
	return out
}

func approxEqualByte(a, b byte, tol int) bool {
	d := int(a) - int(b)
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func assertPixel(t *testing.T, img *graphics.Image, x, y int, wantR, wantG, wantB, wantA byte) {
	t.Helper()
	i := (y*img.Width + x) * 4
	got := img.Pix[i : i+4]
	want := [4]byte{wantR, wantG, wantB, wantA}
	for c := 0; c < 4; c++ {
		if !approxEqualByte(got[c], want[c], 1) {
			t.Errorf("pixel (%d,%d) channel %d = %d, want %d (full pixel %v, want %v)", x, y, c, got[c], want[c], got, want)
		}
	}
}

func TestDecodeDeviceGray8Bit(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	samples := []byte{0, 255}
	img, err := Decode(dict, samples, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 0, 0, 0, 255)
	assertPixel(t, img, 1, 0, 255, 255, 255, 255)
}

func TestDecodeDeviceRGB8Bit(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceRGB"),
	}
	samples := []byte{10, 20, 30}
	img, err := Decode(dict, samples, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 10, 20, 30, 255)
}

func TestDecodeDeviceCMYK(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceCMYK"),
	}
	// Full black via K alone: C=M=Y=0, K=255 -> R=G=B=0.
	samples := []byte{0, 0, 0, 255}
	img, err := Decode(dict, samples, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 0, 0, 0, 255)
}

// TestDecodeBitPacking1And4Bit exercises bitReader across two of the
// unusual bit depths (1 and 4) with a row that does not end on a byte
// boundary, confirming samples are extracted MSB-first and each row
// starts fresh on its own byte.
func TestDecodeBitPacking1And4Bit(t *testing.T) {
	// 1 bit per component DeviceGray, 3 pixels per row (values 1,0,1 -
	// packed high bits first into a single byte: 1 0 1 0 0 0 0 0 = 0xA0),
	// two rows.
	row0 := packRow(t, []uint32{1, 0, 1}, 1)
	row1 := packRow(t, []uint32{0, 1, 0}, 1)
	dict := syntax.Dictionary{
		"Width": syntax.Integer(3), "Height": syntax.Integer(2),
		"BitsPerComponent": syntax.Integer(1),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	img, err := Decode(dict, append(append([]byte{}, row0...), row1...), Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 255, 255, 255, 255)
	assertPixel(t, img, 1, 0, 0, 0, 0, 255)
	assertPixel(t, img, 2, 0, 255, 255, 255, 255)
	assertPixel(t, img, 0, 1, 0, 0, 0, 255)
	assertPixel(t, img, 1, 1, 255, 255, 255, 255)
	assertPixel(t, img, 2, 1, 0, 0, 0, 255)
}

func TestDecodeBitPacking4Bit(t *testing.T) {
	// 4 bits per component DeviceGray: max value is 15 -> decoded to
	// full white (255); 0 -> black.
	row := packRow(t, []uint32{15, 0}, 4)
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(4),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	img, err := Decode(dict, row, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 255, 255, 255, 255)
	assertPixel(t, img, 1, 0, 0, 0, 0, 255)
}

func TestDecodeDecodeArrayReversesGray(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
		"Decode":           syntax.Array{syntax.Integer(1), syntax.Integer(0)},
	}
	samples := []byte{0, 255}
	img, err := Decode(dict, samples, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// Reversed: raw 0 -> decoded 1 (white); raw 255 -> decoded 0 (black).
	assertPixel(t, img, 0, 0, 255, 255, 255, 255)
	assertPixel(t, img, 1, 0, 0, 0, 0, 255)
}

func TestDecodeImageMaskUsesFillColor(t *testing.T) {
	// Default Decode [0 1]: sample 0 paints, sample 1 masks out.
	row := packRow(t, []uint32{0, 1, 0}, 1)
	dict := syntax.Dictionary{
		"Width": syntax.Integer(3), "Height": syntax.Integer(1),
		"ImageMask": syntax.Boolean(true),
	}
	fill := graphics.Color{R: 0.2, G: 0.4, B: 0.6}
	img, err := Decode(dict, row, Options{Resolver: &fakeResolver{}, FillColor: fill})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, to8(0.2), to8(0.4), to8(0.6), 255) // painted
	assertPixel(t, img, 1, 0, to8(0.2), to8(0.4), to8(0.6), 0)   // masked out
	assertPixel(t, img, 2, 0, to8(0.2), to8(0.4), to8(0.6), 255) // painted
}

func TestDecodeImageMaskDecodeArrayReverses(t *testing.T) {
	row := packRow(t, []uint32{0, 1}, 1)
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"ImageMask": syntax.Boolean(true),
		"Decode":    syntax.Array{syntax.Integer(1), syntax.Integer(0)},
	}
	img, err := Decode(dict, row, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// Reversed: sample 0 now masks out, sample 1 paints.
	assertPixel(t, img, 0, 0, 0, 0, 0, 0)
	assertPixel(t, img, 1, 0, 0, 0, 0, 255)
}

func TestDecodeMissingDimensionsIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{"ColorSpace": syntax.Name("DeviceGray")}
	_, err := Decode(dict, nil, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with no /Width /Height: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeInvalidBitsPerComponentIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(3),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	_, err := Decode(dict, []byte{0}, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with /BitsPerComponent 3: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeTruncatedSampleDataIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(4), "Height": syntax.Integer(4),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceRGB"),
	}
	_, err := Decode(dict, []byte{1, 2, 3}, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with truncated samples: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeOversizedImageIsUnsupported(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(100000), "Height": syntax.Integer(100000),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	_, err := Decode(dict, nil, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Decode with an oversized image: got %v, want an error wrapping ErrUnsupported", err)
	}
}

func TestDecodeMissingColorSpaceIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{"Width": syntax.Integer(1), "Height": syntax.Integer(1)}
	_, err := Decode(dict, []byte{0}, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with no /ColorSpace and not an /ImageMask: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeWrongLengthDecodeArrayIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceRGB"),
		"Decode":           syntax.Array{syntax.Integer(0), syntax.Integer(1)}, // needs 6 entries for 3 components
	}
	_, err := Decode(dict, []byte{1, 2, 3}, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with wrong-length /Decode: got %v, want an error wrapping ErrMalformed", err)
	}
}
