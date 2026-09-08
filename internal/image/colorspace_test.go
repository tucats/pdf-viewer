package image

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestDecodeIndexedColorSpace(t *testing.T) {
	// A 2-entry palette over DeviceRGB: index 0 = red, index 1 = blue.
	lookup := syntax.String([]byte{255, 0, 0, 0, 0, 255})
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace": syntax.Array{
			syntax.Name("Indexed"), syntax.Name("DeviceRGB"), syntax.Integer(1), lookup,
		},
	}
	samples := []byte{0, 1}
	img, err := Decode(dict, samples, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 255, 0, 0, 255)
	assertPixel(t, img, 1, 0, 0, 0, 255, 255)
}

func TestDecodeIndexedColorSpaceClampsOutOfRangeIndex(t *testing.T) {
	lookup := syntax.String([]byte{10, 20, 30})
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace": syntax.Array{
			syntax.Name("Indexed"), syntax.Name("DeviceRGB"), syntax.Integer(0), lookup,
		},
	}
	// A raw sample of 255 is far beyond hival (0); indexedToRGB must
	// clamp rather than reading out of bounds or panicking.
	img, err := Decode(dict, []byte{255}, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 10, 20, 30, 255)
}

func TestDecodeIndexedLookupTooShortIsMalformed(t *testing.T) {
	lookup := syntax.String([]byte{1, 2, 3}) // only one DeviceRGB entry
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace": syntax.Array{
			syntax.Name("Indexed"), syntax.Name("DeviceRGB"), syntax.Integer(1), lookup, // hival=1 needs 2 entries
		},
	}
	_, err := Decode(dict, []byte{0}, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with a too-short Indexed lookup table: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeICCBasedByComponentCount(t *testing.T) {
	r := &fakeResolver{objects: map[int]syntax.Object{
		10: syntax.Stream{Dict: syntax.Dictionary{"N": syntax.Integer(3)}},
	}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Array{syntax.Name("ICCBased"), syntax.Reference{Number: 10}},
	}
	img, err := Decode(dict, []byte{9, 8, 7}, Options{Resolver: r})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 9, 8, 7, 255)
}

func TestDecodeNamedColorSpaceResource(t *testing.T) {
	resources := syntax.Dictionary{
		"ColorSpace": syntax.Dictionary{
			"CS0": syntax.Name("DeviceGray"),
		},
	}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("CS0"),
	}
	img, err := Decode(dict, []byte{200}, Options{Resolver: &fakeResolver{}, Resources: resources})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 200, 200, 200, 255)
}

func TestDecodeUnresolvableNamedColorSpaceIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("CS0"),
	}
	_, err := Decode(dict, []byte{200}, Options{Resolver: &fakeResolver{}, Resources: nil})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with an unresolvable named color space: got %v, want an error wrapping ErrMalformed", err)
	}
}

// TestDecodeLabColorSpace exercises /Lab end to end through Decode: L*=0
// with neutral a*=b*=0 (black) and L*=100 with neutral a*=b*=0 (white)
// are the two points every plausible CIELAB->sRGB conversion must agree
// on, making them a robust choice for a coarse-grained decode test (as
// opposed to pinning exact intermediate-gray values, which would be
// sensitive to exactly which of several defensible white-point/matrix
// choices labToRGB makes - see that function's doc comment). a*/b* are
// left neutral rather than at their extremes deliberately: an extreme
// a*/b* paired with L*=0 is outside the real sRGB gamut and does not
// convert back to pure black, which is a property of CIELAB itself, not
// a bug in this conversion.
func TestDecodeLabColorSpace(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Array{syntax.Name("Lab"), syntax.Dictionary{}},
	}
	// Default /Decode for /Lab is [0 100 -100 100 -100 100]; a raw byte
	// of 128 decodes a*/b* to (approximately) 0, their neutral midpoint.
	// Pixel 0: L*=0,a*=~0,b*=~0 (should be black). Pixel 1: L*=100,
	// a*=~0,b*=~0 (should be white).
	samples := []byte{0, 128, 128, 255, 128, 128}
	img, err := Decode(dict, samples, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 0, 0, 0, 255)
	r, g, b, a := img.At(1, 0)
	if a != 1 {
		t.Fatalf("pixel 1 alpha = %v, want 1", a)
	}
	for name, v := range map[string]float64{"r": r, "g": g, "b": b} {
		if v < 0.95 {
			t.Fatalf("pixel 1 (L*=100) channel %s = %v, want near-white (>=0.95)", name, v)
		}
	}
}

func TestDecodeLabMissingDictionaryIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Array{syntax.Name("Lab")},
	}
	_, err := Decode(dict, []byte{0, 0, 0}, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with a /Lab array missing its dictionary: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeIndexedOfIndexedIsUnsupported(t *testing.T) {
	inner := syntax.Array{syntax.Name("Indexed"), syntax.Name("DeviceGray"), syntax.Integer(1), syntax.String([]byte{0, 255})}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Array{syntax.Name("Indexed"), inner, syntax.Integer(1), syntax.String([]byte{0, 1})},
	}
	_, err := Decode(dict, []byte{0}, Options{Resolver: &fakeResolver{}})
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Decode with Indexed-of-Indexed: got %v, want an error wrapping ErrUnsupported", err)
	}
}
