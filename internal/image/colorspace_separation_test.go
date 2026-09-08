package image

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// separationTintTransform returns a Type 2 (exponential interpolation)
// function dictionary mapping tint 0 -> white and tint 1 -> black in
// DeviceRGB - the simplest realistic Separation tint transform (a single
// spot ink that gets darker as more of it is applied).
func separationTintTransform() syntax.Dictionary {
	return syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(1, 1, 1), "C1": numArray(0, 0, 0),
	}
}

func numArray(vals ...float64) syntax.Array {
	arr := make(syntax.Array, len(vals))
	for i, v := range vals {
		arr[i] = syntax.Real(v)
	}
	return arr
}

func TestDecodeSeparationColorSpace(t *testing.T) {
	dict := syntax.Dictionary{
		"Width": syntax.Integer(2), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace": syntax.Array{
			syntax.Name("Separation"), syntax.Name("Spot1"), syntax.Name("DeviceRGB"), separationTintTransform(),
		},
	}
	// Tint 0 (raw byte 0) -> white; tint 1 (raw byte 255) -> black.
	img, err := Decode(dict, []byte{0, 255}, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 255, 255, 255, 255)
	assertPixel(t, img, 1, 0, 0, 0, 0, 255)
}

func TestDecodeSeparationTintTransformViaReference(t *testing.T) {
	r := &fakeResolver{objects: map[int]syntax.Object{
		5: separationTintTransform(),
	}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace": syntax.Array{
			syntax.Name("Separation"), syntax.Name("Spot1"), syntax.Name("DeviceRGB"), syntax.Reference{Number: 5},
		},
	}
	img, err := Decode(dict, []byte{255}, Options{Resolver: r})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 0, 0, 0, 255)
}

func TestDecodeSeparationWrongTintTransformArityIsMalformed(t *testing.T) {
	// A tint transform declaring 2 inputs cannot back a Separation space
	// (which always supplies exactly 1 tint component).
	badFn := syntax.Dictionary{
		"FunctionType": syntax.Integer(0), "Domain": numArray(0, 1, 0, 1),
		"Range": numArray(0, 1), "Size": numArray(2, 2), "BitsPerSample": syntax.Integer(8),
	}
	r := &fakeResolver{}
	// parseType0 needs a stream for its sample data; wrap it so the
	// dictionary is reachable both as the color space's tint-transform
	// entry and as a decodable stream.
	stream := syntax.Stream{Dict: badFn, Raw: []byte{0, 0, 0, 0}}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace": syntax.Array{
			syntax.Name("Separation"), syntax.Name("Spot1"), syntax.Name("DeviceRGB"), stream,
		},
	}
	_, err := Decode(dict, []byte{0}, Options{Resolver: r})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with a mismatched-arity tint transform: got %v, want an error wrapping ErrMalformed", err)
	}
}

// TestDecodeDeviceNColorSpace exercises a 2-tint DeviceN color space
// backed by a genuinely 2-input Type 0 (sampled) function - the function
// type real-world DeviceN tint transforms actually use, since a Type 2
// function only ever accepts a single input (see internal/function's
// type2.go) and so cannot express a multi-colorant tint transform.
func TestDecodeDeviceNColorSpace(t *testing.T) {
	tintFn := syntax.Dictionary{
		"FunctionType":  syntax.Integer(0),
		"Domain":        numArray(0, 1, 0, 1),
		"Range":         numArray(0, 1, 0, 1, 0, 1),
		"Size":          numArray(2, 2),
		"BitsPerSample": syntax.Integer(8),
	}
	// 4 grid points (2x2), 3 outputs each, 8 bits/sample: (0,0)->black,
	// (1,0)->red, (0,1)->green, (1,1)->white(-ish), dimension 0 fastest.
	raw := []byte{
		0, 0, 0, // (0,0)
		255, 0, 0, // (1,0)
		0, 255, 0, // (0,1)
		255, 255, 255, // (1,1)
	}
	stream := syntax.Stream{Dict: tintFn, Raw: raw}
	dict := syntax.Dictionary{
		"Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace": syntax.Array{
			syntax.Name("DeviceN"),
			syntax.Array{syntax.Name("Cyan"), syntax.Name("Magenta")},
			syntax.Name("DeviceRGB"),
			stream,
		},
	}
	// Both raw tint samples at 255 (max, decoded to 1.0) select grid
	// corner (1,1): white.
	img, err := Decode(dict, []byte{255, 255}, Options{Resolver: &fakeResolver{}})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertPixel(t, img, 0, 0, 255, 255, 255, 255)
}

func TestResolveColorSpacePublicAPI(t *testing.T) {
	resources := syntax.Dictionary{
		"ColorSpace": syntax.Dictionary{
			"CS0": syntax.Array{
				syntax.Name("Separation"), syntax.Name("Spot1"), syntax.Name("DeviceRGB"), separationTintTransform(),
			},
		},
	}
	cs, err := ResolveColorSpace(&fakeResolver{}, syntax.Name("CS0"), resources)
	if err != nil {
		t.Fatalf("ResolveColorSpace: %v", err)
	}
	if cs.Components() != 1 {
		t.Fatalf("Components() = %d, want 1", cs.Components())
	}
	r, g, b := cs.ToRGB([]float64{1})
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("ToRGB([1]) = (%v,%v,%v), want (0,0,0)", r, g, b)
	}
}

func TestResolveColorSpaceIndexedUsesRawIndex(t *testing.T) {
	lookup := syntax.String([]byte{255, 0, 0, 0, 255, 0})
	obj := syntax.Array{syntax.Name("Indexed"), syntax.Name("DeviceRGB"), syntax.Integer(1), lookup}
	cs, err := ResolveColorSpace(&fakeResolver{}, obj, nil)
	if err != nil {
		t.Fatalf("ResolveColorSpace: %v", err)
	}
	r, g, b := cs.ToRGB([]float64{1})
	if r != 0 || g != 1 || b != 0 {
		t.Fatalf("ToRGB([1]) (Indexed) = (%v,%v,%v), want (0,1,0)", r, g, b)
	}
}

func TestColorSpaceToRGBWrongComponentCountReturnsBlack(t *testing.T) {
	cs, err := ResolveColorSpace(&fakeResolver{}, syntax.Name("DeviceRGB"), nil)
	if err != nil {
		t.Fatalf("ResolveColorSpace: %v", err)
	}
	r, g, b := cs.ToRGB([]float64{1}) // DeviceRGB needs 3 components, not 1
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("ToRGB with wrong component count = (%v,%v,%v), want black rather than a panic or garbage", r, g, b)
	}
}
