package image

import (
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements the two ways a PDF image can be partially or
// fully transparent beyond its own color data: a soft mask (/SMask, a
// separate grayscale image giving continuously variable per-pixel
// alpha) and a stencil or color-key mask (/Mask, either another image
// stream to be interpreted exactly like an /ImageMask, or an array
// describing raw sample ranges to treat as transparent). Per the PDF
// specification, /SMask takes priority over /Mask when an image
// dictionary somehow has both - decodeInternal only consults
// maskAlphaOrColorKey when smaskAlphaFn returned nil, implementing that
// precedence.

// smaskAlphaFn returns a function giving the alpha (0-1) an /SMask entry
// contributes at each (x, y) pixel of a width x height base image, or
// nil if the dictionary has no /SMask (or it resolves to null, which PDF
// treats the same as absent). The soft mask image is decoded exactly
// like any other image (via decodeInternal, recursing with depth+1 -
// see maxMaskRecursionDepth) since it is, in every structural respect,
// just another image; its own decoded gray value (identical across its
// R, G, and B channels, since it is required to use DeviceGray) is what
// becomes the alpha it contributes, sampled at whatever resolution the
// base image needs (see resample).
func smaskAlphaFn(dict syntax.Dictionary, width, height int, opts Options, depth int) (func(x, y int) float64, error) {
	v, ok := dict["SMask"]
	if !ok {
		return nil, nil
	}
	resolved, err := resolveIfRef(opts.Resolver, v)
	if err != nil {
		return nil, err
	}
	if _, isNull := resolved.(syntax.Null); isNull {
		return nil, nil
	}
	stream, ok := resolved.(syntax.Stream)
	if !ok {
		return nil, pdferror.Malformedf("/SMask did not resolve to a stream (found %T)", resolved)
	}

	smImg, err := decodeMaskImage(stream, opts, depth)
	if err != nil {
		return nil, err
	}
	return resample(smImg, width, height, func(r, g, b, a float64) float64 { return r }), nil
}

// embeddedAlphaFn returns a function giving the alpha (0-1) alpha - one
// byte (0-255) per pixel, row-major - contributes at each (x, y) pixel of
// a width x height base image. See Options.EmbeddedAlpha's doc comment
// for where this data comes from (JPXDecode's /SMaskInData - ISO
// 32000-1 7.4.9); unlike smaskAlphaFn/maskAlphaOrColorKey, there is no
// separate image stream to decode or resample here, since the alpha data
// already shares the base image's own width and height (both were
// decoded from the same JPX codestream - see internal/filter's
// DecodeImage).
func embeddedAlphaFn(alpha []byte, width, height int) (func(x, y int) float64, error) {
	if len(alpha) < width*height {
		return nil, pdferror.Malformedf("embedded alpha data is %d bytes, need at least %d for %dx%d", len(alpha), width*height, width, height)
	}
	return func(x, y int) float64 {
		return float64(alpha[y*width+x]) / 255
	}, nil
}

// maskAlphaOrColorKey returns whichever of the two forms of /Mask the
// dictionary uses: an alpha function (mirroring smaskAlphaFn, but
// sourced from the referenced image's own computed alpha channel rather
// than its color, since that referenced image is required to be an
// /ImageMask - see the package doc comment) if /Mask names another
// stream, or a set of color-key ranges if /Mask is an array. Exactly one
// of the two return values is non-nil (or neither, if /Mask is absent or
// null). n is the raw per-pixel component count of the *base* image
// (see rawComponentsPerPixel), which a color-key array's length must
// match.
func maskAlphaOrColorKey(dict syntax.Dictionary, width, height int, opts Options, depth int, n int) (func(x, y int) float64, [][2]uint32, error) {
	v, ok := dict["Mask"]
	if !ok {
		return nil, nil, nil
	}
	resolved, err := resolveIfRef(opts.Resolver, v)
	if err != nil {
		return nil, nil, err
	}
	switch mv := resolved.(type) {
	case syntax.Null:
		return nil, nil, nil
	case syntax.Stream:
		mImg, err := decodeMaskImage(mv, opts, depth)
		if err != nil {
			return nil, nil, err
		}
		return resample(mImg, width, height, func(r, g, b, a float64) float64 { return a }), nil, nil
	case syntax.Array:
		ranges, err := parseColorKeyRanges(mv, n)
		return nil, ranges, err
	default:
		return nil, nil, pdferror.Malformedf("/Mask is neither a stream, an array, nor null (found %T)", resolved)
	}
}

// decodeMaskImage resolves and decodes the image stream backing an
// /SMask or a stream-valued /Mask entry. It deliberately builds a fresh
// Options carrying only Resolver and Resources - not the outer image's
// FillColor - because a mask image is only ever consulted for its
// R-channel gray value (/SMask) or its own computed alpha (/Mask, which
// is required to be an /ImageMask and so has its own FillColor concern,
// irrelevant here since only its alpha channel is read).
func decodeMaskImage(stream syntax.Stream, opts Options, depth int) (*graphics.Image, error) {
	dict, err := opts.Resolver.ResolveDictionary(stream.Dict)
	if err != nil {
		return nil, err
	}
	samples, err := opts.Resolver.DecodeStream(stream)
	if err != nil {
		return nil, err
	}
	return decodeInternal(dict, samples, Options{Resolver: opts.Resolver, Resources: opts.Resources}, depth+1)
}

// resample returns a function that samples src's pixel nearest to each
// (x, y) of a targetW x targetH grid, reducing it through extract to a
// single alpha value. Nearest-neighbor sampling (rather than bilinear or
// another smoother filter) is a deliberate simplification consistent
// with internal/raster's own image-painting approach (see
// Canvas.DrawImage) - an /SMask or /Mask image is not required to share
// the base image's pixel dimensions, and real-world producers sometimes
// give a soft mask a lower resolution than its base image specifically
// because per-pixel alpha detail matters less than color detail.
func resample(src *graphics.Image, targetW, targetH int, extract func(r, g, b, a float64) float64) func(x, y int) float64 {
	return func(x, y int) float64 {
		sx := x * src.Width / targetW
		sy := y * src.Height / targetH
		r, g, b, a := src.At(sx, sy)
		return extract(r, g, b, a)
	}
}

// parseColorKeyRanges reads /Mask's array form: 2*n integers giving, for
// each of the base image's n raw per-pixel samples, an inclusive
// [min,max] range of *raw* (pre-/Decode-array, pre-color-space)
// values - per the specification, "shall be interpreted in the same way
// as the corresponding values in the Decode array" - such that a pixel
// is masked out (made fully transparent) only if every one of its raw
// samples falls within its own named range; see colorKeyMasked.
func parseColorKeyRanges(arr syntax.Array, n int) ([][2]uint32, error) {
	if len(arr) != 2*n {
		return nil, pdferror.Malformedf("/Mask color-key array has %d entries, want %d", len(arr), 2*n)
	}
	out := make([][2]uint32, n)
	for i := 0; i < n; i++ {
		lo, ok1 := numberValue(arr[2*i])
		hi, ok2 := numberValue(arr[2*i+1])
		if !ok1 || !ok2 {
			return nil, pdferror.Malformedf("/Mask color-key array entry %d is not a number", 2*i)
		}
		out[i] = [2]uint32{uint32(lo), uint32(hi)}
	}
	return out, nil
}

// colorKeyMasked reports whether raw (one image pixel's raw per-
// component sample values) falls entirely within ranges, meaning that
// pixel should be treated as fully transparent. A nil ranges (no
// color-key /Mask present) never masks anything.
func colorKeyMasked(ranges [][2]uint32, raw []uint32) bool {
	if ranges == nil {
		return false
	}
	for i, r := range ranges {
		if raw[i] < r[0] || raw[i] > r[1] {
			return false
		}
	}
	return true
}
