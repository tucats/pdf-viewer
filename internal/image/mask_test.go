package image

import (
	"errors"
	"testing"
	"time"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// timeoutForTest returns a channel that fires well before Go's own test
// timeout, used only by TestDecodeMaskRecursionIsBounded to fail fast
// (with a clear message) instead of hanging until the whole test binary
// times out if maxMaskRecursionDepth's guard were ever removed or broken.
func timeoutForTest() <-chan time.Time {
	return time.After(2 * time.Second)
}

func TestDecodeWithSMaskAppliesPerPixelAlpha(t *testing.T) {
	smaskDict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	smaskStream := syntax.Stream{Dict: smaskDict, Raw: []byte{0, 255}}

	r := &fakeResolver{objects: map[int]syntax.Object{20: smaskStream}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceRGB"),
		"SMask":            syntax.Reference{Number: 20},
	}
	samples := []byte{255, 0, 0, 255, 0, 0} // two red pixels
	img, err := Decode(dict, samples, Options{Resolver: r})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 255, 0, 0, 0)   // smask gray 0 -> fully transparent
	assertPixel(t, img, 1, 0, 255, 0, 0, 255) // smask gray 255 -> fully opaque
}

func TestDecodeWithSMaskOfDifferentDimensionsResamples(t *testing.T) {
	// A 1x1 smask (all fully transparent) applied to a 2x2 base image:
	// resample must map every base pixel back to the smask's only pixel
	// rather than panicking on an out-of-range coordinate.
	smaskDict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	smaskStream := syntax.Stream{Dict: smaskDict, Raw: []byte{0}}
	r := &fakeResolver{objects: map[int]syntax.Object{20: smaskStream}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(2),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
		"SMask":            syntax.Reference{Number: 20},
	}
	samples := []byte{100, 100, 100, 100}
	img, err := Decode(dict, samples, Options{Resolver: r})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			assertPixel(t, img, x, y, 100, 100, 100, 0)
		}
	}
}

func TestDecodeWithMaskStreamIsStencil(t *testing.T) {
	maskRow := packRow(t, []uint32{0, 1}, 1) // pixel 0 painted, pixel 1 masked out
	maskDict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"ImageMask": syntax.Boolean(true),
	}
	maskStream := syntax.Stream{Dict: maskDict, Raw: maskRow}

	r := &fakeResolver{objects: map[int]syntax.Object{30: maskStream}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
		"Mask":             syntax.Reference{Number: 30},
	}
	img, err := Decode(dict, []byte{50, 50}, Options{Resolver: r})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 50, 50, 50, 255)
	assertPixel(t, img, 1, 0, 50, 50, 50, 0)
}

func TestDecodeWithColorKeyMask(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
		// Raw sample 200 (and only 200) is transparent.
		"Mask": syntax.Array{syntax.Integer(200), syntax.Integer(200)},
	}
	img, err := Decode(dict, []byte{200, 201}, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 200, 200, 200, 0)
	assertPixel(t, img, 1, 0, 201, 201, 201, 255)
}

func TestDecodeWithEmbeddedAlphaAppliesPerPixelAlpha(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceRGB"),
	}
	samples := []byte{255, 0, 0, 0, 255, 0} // red pixel, green pixel
	img, err := Decode(dict, samples, Options{EmbeddedAlpha: []byte{0, 255}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 255, 0, 0, 0)   // alpha 0 -> fully transparent
	assertPixel(t, img, 1, 0, 0, 255, 0, 255) // alpha 255 -> fully opaque
}

func TestDecodeSMaskTakesPriorityOverEmbeddedAlpha(t *testing.T) {
	smaskDict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	smaskStream := syntax.Stream{Dict: smaskDict, Raw: []byte{255}} // fully opaque via SMask

	r := &fakeResolver{objects: map[int]syntax.Object{41: smaskStream}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
		"SMask":            syntax.Reference{Number: 41},
	}
	// EmbeddedAlpha would make this pixel transparent if it were
	// consulted despite /SMask being present.
	img, err := Decode(dict, []byte{200}, Options{Resolver: r, EmbeddedAlpha: []byte{0}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 200, 200, 200, 255)
}

func TestDecodeSMaskTakesPriorityOverMask(t *testing.T) {
	smaskDict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
	}
	smaskStream := syntax.Stream{Dict: smaskDict, Raw: []byte{255}} // fully opaque via SMask

	r := &fakeResolver{objects: map[int]syntax.Object{40: smaskStream}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Name("DeviceGray"),
		"SMask":            syntax.Reference{Number: 40},
		// If /Mask were consulted despite /SMask being present, this
		// color-key entry would make the only pixel transparent.
		"Mask": syntax.Array{syntax.Integer(77), syntax.Integer(77)},
	}
	img, err := Decode(dict, []byte{77}, Options{Resolver: r})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 77, 77, 77, 255)
}

// TestDecodeMaskRecursionIsBounded confirms that two image streams whose
// /SMask entries reference each other cause Decode to fail with a
// bounded, classified error instead of recursing until the goroutine
// stack overflows - see maxMaskRecursionDepth's doc comment for why this
// specific failure mode needs its own guard rather than relying on
// internal/parser.Resolve's cyclic-reference detection.
func TestDecodeMaskRecursionIsBounded(t *testing.T) {
	grayDict := func(smaskRef int) syntax.Dictionary {
		return syntax.Dictionary{
			"Width": syntax.Integer(1), "Height": syntax.Integer(1),
			"BitsPerComponent": syntax.Integer(8),
			"ColorSpace":       syntax.Name("DeviceGray"),
			"SMask":            syntax.Reference{Number: smaskRef},
		}
	}
	r := &fakeResolver{objects: map[int]syntax.Object{}}
	r.objects[50] = syntax.Stream{Dict: grayDict(51), Raw: []byte{100}}
	r.objects[51] = syntax.Stream{Dict: grayDict(50), Raw: []byte{100}}

	dict := grayDict(50)
	done := make(chan error, 1)
	go func() {
		_, err := Decode(dict, []byte{100}, Options{Resolver: r})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, pdferror.ErrMalformed) {
			t.Fatalf("Decode with a cyclic /SMask chain: got %v, want an error wrapping ErrMalformed", err)
		}
	case <-timeoutForTest():
		t.Fatal("Decode with a cyclic /SMask chain did not return within the timeout - likely unbounded recursion")
	}
}
