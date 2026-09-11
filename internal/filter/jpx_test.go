package filter

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/jpx"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file tests jpx.go's own logic - packing decoded samples into PDF's
// interleaved byte layout, and DecodeImage's /ColorSpace-fallback and
// /SMaskInData-splitting rules - directly against hand-built *jpx.Image
// values (jpx.Image and jpx.ImageComponent are plain exported structs, so
// no real encoded JPEG 2000 codestream is needed for any of this; see
// buildJPXImageResult's own doc comment). A real end-to-end "encode, feed
// through Decode/DecodeWith, check pixels" round trip - the kind
// dct_test.go's tests are for DCTDecode - is 14g's job, once this
// package's own from-scratch encoder exists for fixture-building.

func grayComponent(bitDepth int, samples ...int32) jpx.ImageComponent {
	return jpx.ImageComponent{BitDepth: bitDepth, Samples: samples}
}

func TestPackJPXComponentsGray8Bit(t *testing.T) {
	comps := []jpx.ImageComponent{grayComponent(8, 0, 128, 255, 64)}
	got, err := packJPXComponents(comps, 2, 2)
	if err != nil {
		t.Fatalf("packJPXComponents: %v", err)
	}
	want := []byte{0, 128, 255, 64}
	if !bytes.Equal(got, want) {
		t.Fatalf("packJPXComponents gray8 = %v, want %v", got, want)
	}
}

func TestPackJPXComponentsRGB8BitInterleaved(t *testing.T) {
	comps := []jpx.ImageComponent{
		grayComponent(8, 10, 20),
		grayComponent(8, 30, 40),
		grayComponent(8, 50, 60),
	}
	got, err := packJPXComponents(comps, 2, 1)
	if err != nil {
		t.Fatalf("packJPXComponents: %v", err)
	}
	want := []byte{10, 30, 50, 20, 40, 60}
	if !bytes.Equal(got, want) {
		t.Fatalf("packJPXComponents rgb8 = %v, want %v", got, want)
	}
}

func TestPackJPXComponents16Bit(t *testing.T) {
	comps := []jpx.ImageComponent{grayComponent(16, 0x0102, 0xFFFF)}
	got, err := packJPXComponents(comps, 2, 1)
	if err != nil {
		t.Fatalf("packJPXComponents: %v", err)
	}
	want := []byte{0x01, 0x02, 0xFF, 0xFF}
	if !bytes.Equal(got, want) {
		t.Fatalf("packJPXComponents 16-bit = %v, want %v", got, want)
	}
}

func TestPackJPXComponentsSubByteBitPacking(t *testing.T) {
	// 1-bit, 8 samples across one row: 1,0,1,1,0,0,1,0 -> 0xB2.
	comps := []jpx.ImageComponent{grayComponent(1, 1, 0, 1, 1, 0, 0, 1, 0)}
	got, err := packJPXComponents(comps, 8, 1)
	if err != nil {
		t.Fatalf("packJPXComponents: %v", err)
	}
	want := []byte{0xB2}
	if !bytes.Equal(got, want) {
		t.Fatalf("packJPXComponents 1-bit = %08b, want %08b", got, want)
	}
}

func TestPackJPXComponentsRejectsSigned(t *testing.T) {
	comps := []jpx.ImageComponent{{BitDepth: 8, Signed: true, Samples: []int32{0}}}
	_, err := packJPXComponents(comps, 1, 1)
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("packJPXComponents with signed component: got %v, want an error wrapping ErrUnsupported", err)
	}
}

func TestPackJPXComponentsRejectsMismatchedBitDepths(t *testing.T) {
	comps := []jpx.ImageComponent{grayComponent(8, 0), grayComponent(16, 0)}
	_, err := packJPXComponents(comps, 1, 1)
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("packJPXComponents with mismatched bit depths: got %v, want an error wrapping ErrUnsupported", err)
	}
}

func TestPackJPXComponentsRejectsUnsupportedBitDepth(t *testing.T) {
	comps := []jpx.ImageComponent{grayComponent(12, 0)}
	_, err := packJPXComponents(comps, 1, 1)
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("packJPXComponents with 12-bit component: got %v, want an error wrapping ErrUnsupported", err)
	}
}

func TestPackAlphaComponentNormalizesToByteRange(t *testing.T) {
	// 4-bit component: max value 15 should normalize to 255, half (7 or
	// 8) to roughly the middle.
	c := grayComponent(4, 0, 15, 8)
	got := packAlphaComponent(c)
	want := []byte{0, 255, 8 * 255 / 15}
	if !bytes.Equal(got, want) {
		t.Fatalf("packAlphaComponent = %v, want %v", got, want)
	}
}

func TestBuildJPXImageResultColorSpaceFallbackByComponentCount(t *testing.T) {
	cases := []struct {
		n    int
		want syntax.Name
	}{
		{1, "DeviceGray"},
		{3, "DeviceRGB"},
		{4, "DeviceCMYK"},
		{2, ""}, // no fallback this package knows how to express
	}
	for _, c := range cases {
		comps := make([]jpx.ImageComponent, c.n)
		for i := range comps {
			comps[i] = grayComponent(8, 1)
		}
		img := &jpx.Image{Width: 1, Height: 1, Components: comps}
		_, info, err := buildJPXImageResult(img, 0)
		if err != nil {
			t.Fatalf("buildJPXImageResult(%d components): %v", c.n, err)
		}
		if info.FallbackColorSpace != c.want {
			t.Errorf("%d components: FallbackColorSpace = %q, want %q", c.n, info.FallbackColorSpace, c.want)
		}
		if info.Alpha != nil {
			t.Errorf("%d components, smaskInData=0: Alpha = %v, want nil", c.n, info.Alpha)
		}
	}
}

func TestBuildJPXImageResultSplitsTrailingAlphaComponent(t *testing.T) {
	// RGBA: 3 color components plus a trailing alpha component - the
	// convention DecodeImage documents for /SMaskInData.
	img := &jpx.Image{
		Width:  1,
		Height: 1,
		Components: []jpx.ImageComponent{
			grayComponent(8, 200), // R
			grayComponent(8, 100), // G
			grayComponent(8, 50),  // B
			grayComponent(8, 255), // alpha (fully opaque)
		},
	}
	samples, info, err := buildJPXImageResult(img, 1)
	if err != nil {
		t.Fatalf("buildJPXImageResult: %v", err)
	}
	if info.FallbackColorSpace != "DeviceRGB" {
		t.Fatalf("FallbackColorSpace = %q, want DeviceRGB (alpha component should already be split off)", info.FallbackColorSpace)
	}
	wantSamples := []byte{200, 100, 50}
	if !bytes.Equal(samples, wantSamples) {
		t.Fatalf("samples = %v, want %v (color components only)", samples, wantSamples)
	}
	wantAlpha := []byte{255}
	if !bytes.Equal(info.Alpha, wantAlpha) {
		t.Fatalf("Alpha = %v, want %v", info.Alpha, wantAlpha)
	}
}

func TestBuildJPXImageResultIgnoresSMaskInDataWithOneComponent(t *testing.T) {
	// A single-component (grayscale) image has nothing left to call
	// "color" if its only component were split off as alpha, so
	// smaskInData is ignored in that case.
	img := &jpx.Image{Width: 1, Height: 1, Components: []jpx.ImageComponent{grayComponent(8, 128)}}
	samples, info, err := buildJPXImageResult(img, 1)
	if err != nil {
		t.Fatalf("buildJPXImageResult: %v", err)
	}
	if info.Alpha != nil {
		t.Fatalf("Alpha = %v, want nil (only one component available)", info.Alpha)
	}
	if !bytes.Equal(samples, []byte{128}) {
		t.Fatalf("samples = %v, want [128]", samples)
	}
	if info.FallbackColorSpace != "DeviceGray" {
		t.Fatalf("FallbackColorSpace = %q, want DeviceGray", info.FallbackColorSpace)
	}
}

func TestIsJPXImage(t *testing.T) {
	cases := []struct {
		name string
		dict syntax.Dictionary
		want bool
	}{
		{"bare name", syntax.Dictionary{"Filter": syntax.Name("JPXDecode")}, true},
		{"array, last entry", syntax.Dictionary{"Filter": syntax.Array{syntax.Name("FlateDecode"), syntax.Name("JPXDecode")}}, true},
		{"array, not last", syntax.Dictionary{"Filter": syntax.Array{syntax.Name("JPXDecode"), syntax.Name("FlateDecode")}}, false},
		{"different filter", syntax.Dictionary{"Filter": syntax.Name("DCTDecode")}, false},
		{"no filter", syntax.Dictionary{}, false},
		{"malformed filter", syntax.Dictionary{"Filter": syntax.Integer(5)}, false},
	}
	for _, c := range cases {
		if got := IsJPXImage(c.dict); got != c.want {
			t.Errorf("%s: IsJPXImage = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDecodeJPXMalformedInput(t *testing.T) {
	_, err := decodeJPX([]byte("not a JPEG 2000 file"))
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("decodeJPX with garbage input: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeDispatchesJPXDecode(t *testing.T) {
	dict := syntax.Dictionary{"Filter": syntax.Name("JPXDecode")}
	_, err := Decode(dict, []byte("not a JPEG 2000 file"))
	// JPXDecode is now implemented (Phase 14f): a malformed stream must
	// fail as malformed input, not as an unsupported filter name - that
	// would mean decodeOne is still not dispatching to decodeJPX at all.
	if errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Decode with JPXDecode filter: got %v, want decodeJPX to run (ErrMalformed for this garbage input), not ErrUnsupported", err)
	}
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with JPXDecode filter and garbage data: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeImagePassesThroughNonJPXChains(t *testing.T) {
	dict := syntax.Dictionary{"Filter": syntax.Name("ASCII85Decode")}
	samples, info, err := DecodeImage(dict, []byte("~>"), nil, 0)
	if err != nil {
		t.Fatalf("DecodeImage: %v", err)
	}
	if info != nil {
		t.Fatalf("DecodeImage info = %+v, want nil for a non-JPXDecode chain", info)
	}
	if len(samples) != 0 {
		t.Fatalf("DecodeImage samples = %v, want empty (ASCII85Decode of \"~>\" is empty input)", samples)
	}
}
