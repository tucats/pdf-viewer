package image

import (
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// Options carries everything Decode needs beyond an image's own
// dictionary and sample bytes.
type Options struct {
	// Resolver reaches back into the document for whatever this
	// package's own dictionary and sample bytes cannot supply on their
	// own - see the Resolver type's doc comment.
	Resolver Resolver

	// Resources is the page's (or, once forms are supported, a form
	// XObject's) /Resources dictionary, needed only to look up a named
	// /ColorSpace resource - see lookupNamedColorSpace. It may be nil for
	// an image whose /ColorSpace is a Device family name or that is an
	// /ImageMask (which has no /ColorSpace at all).
	Resources syntax.Dictionary

	// FillColor is the graphics state's current fill color at the moment
	// this image is painted, used only when the image dictionary
	// declares /ImageMask true: an image mask carries no color
	// information of its own (see Decode's doc comment), so it paints
	// using whatever color a "g"/"rg"/"k"/"scn" operator most recently
	// set, exactly like an ordinary path fill would.
	FillColor graphics.Color

	// EmbeddedAlpha, when non-nil, supplies this image's own per-pixel
	// alpha directly - one byte (0-255) per pixel, row-major, width*height
	// long - instead of via a /SMask or /Mask dictionary entry. This
	// exists for JPXDecode's /SMaskInData behavior (ISO 32000-1 7.4.9): an
	// opacity channel embedded directly in the already-decoded JPX image
	// data, which internal/filter's adapter (DecodeImage) splits out
	// before this package ever sees the image's sample bytes - see
	// internal/content, the only caller that sets this field. Checked
	// after /SMask but before /Mask - see decodeInternal - matching the
	// specification's requirement that a conforming file never combines a
	// nonzero /SMaskInData with an explicit /SMask of its own, so the
	// exact ordering between the two rarely matters in practice.
	EmbeddedAlpha []byte
}

// maxImagePixels bounds the total pixel count (width * height) Decode
// will attempt to allocate and rasterize - the same "bounded work
// against a hostile or merely oversized input" policy the root package's
// maxRenderPixels enforces for a whole rendered page (see page.go), sized
// identically here since a single image is not expected to legitimately
// need more pixels than an entire rendered page does.
const maxImagePixels = 64_000_000

// maxMaskRecursionDepth bounds how many levels deep an /SMask or /Mask
// chain (an image's soft/stencil mask is itself decoded by this same
// function, which could in principle name another /SMask or /Mask of its
// own) may nest before Decode gives up rather than recursing further.
// This exists specifically to defend against a maliciously constructed
// pair of image streams whose /SMask entries point at each other: each
// object individually resolves without error (internal/parser.Resolve's
// own cyclic-reference guard only catches an object referencing itself,
// not two distinct objects referencing each other through an unrelated
// dictionary key it has no way to know is mask-shaped), so without this
// depth counter such a pair would recurse through this function forever
// rather than through Resolve, where the existing guard could catch it -
// see the repository README's "Dependency and safety policy" on bounded
// work. A soft mask referencing a further soft mask of its own has no
// legitimate use in real PDF content, so a small bound costs nothing.
const maxMaskRecursionDepth = 8

// Decode turns one PDF image object into a graphics.Image. dict is the
// image's dictionary with every top-level indirect reference already
// resolved (see Resolver.ResolveDictionary); samples is its raw sample
// data with every filter in its /Filter chain already applied (see
// Resolver.DecodeStream, or for an inline image, internal/filter.Decode
// directly - see internal/content).
func Decode(dict syntax.Dictionary, samples []byte, opts Options) (*graphics.Image, error) {
	return decodeInternal(dict, samples, opts, 0)
}

func decodeInternal(dict syntax.Dictionary, samples []byte, opts Options, depth int) (*graphics.Image, error) {
	if depth > maxMaskRecursionDepth {
		return nil, pdferror.Malformedf("image /SMask or /Mask nesting exceeds %d levels", maxMaskRecursionDepth)
	}

	width, height, err := dimensions(dict)
	if err != nil {
		return nil, err
	}
	if width*height > maxImagePixels {
		return nil, pdferror.Unsupportedf("image is %dx%d pixels, exceeding this package's %d-pixel limit", width, height, maxImagePixels)
	}

	isMask, _ := dict["ImageMask"].(syntax.Boolean)

	bpc := 8
	if bool(isMask) {
		bpc = 1
	} else if v, ok := numberValue(dict["BitsPerComponent"]); ok {
		bpc = int(v)
	}
	switch bpc {
	case 1, 2, 4, 8, 16:
	default:
		return nil, pdferror.Malformedf("image /BitsPerComponent %d is not one of 1, 2, 4, 8, 16", bpc)
	}

	var cs colorSpace
	if !bool(isMask) {
		csObj, ok := dict["ColorSpace"]
		if !ok {
			return nil, pdferror.Malformedf("image has no /ColorSpace and is not an /ImageMask")
		}
		resolvedCS, err := resolveIfRef(opts.Resolver, csObj)
		if err != nil {
			return nil, err
		}
		cs, err = resolveColorSpace(opts.Resolver, resolvedCS, opts.Resources)
		if err != nil {
			return nil, err
		}
	}
	isIndexed := cs.indexed != nil
	rawComponents := rawComponentsPerPixel(bool(isMask), isIndexed, cs.components)

	decodeArr, err := decodeArray(dict, bool(isMask), isIndexed, cs, bpc)
	if err != nil {
		return nil, err
	}

	rowBits := width * bpc * rawComponents
	rowBytes := (rowBits + 7) / 8
	if len(samples) < rowBytes*height {
		return nil, pdferror.Malformedf("image sample data is %d bytes, need at least %d for %dx%d at %d bit(s)/component, %d component(s)", len(samples), rowBytes*height, width, height, bpc, rawComponents)
	}

	alphaFn, err := smaskAlphaFn(dict, width, height, opts, depth)
	if err != nil {
		return nil, err
	}
	if alphaFn == nil && opts.EmbeddedAlpha != nil {
		alphaFn, err = embeddedAlphaFn(opts.EmbeddedAlpha, width, height)
		if err != nil {
			return nil, err
		}
	}
	var colorKey [][2]uint32
	if alphaFn == nil {
		// /Mask is only consulted when /SMask (and EmbeddedAlpha) are
		// absent - see the package doc comment's masking section.
		alphaFn, colorKey, err = maskAlphaOrColorKey(dict, width, height, opts, depth, rawComponents)
		if err != nil {
			return nil, err
		}
	}

	out := &graphics.Image{Width: width, Height: height, Pix: make([]byte, width*height*4)}
	var br bitReader
	raw := make([]uint32, rawComponents)
	comps := make([]float64, cs.components)

	for y := 0; y < height; y++ {
		br.reset(samples[y*rowBytes : (y+1)*rowBytes])
		for x := 0; x < width; x++ {
			for c := 0; c < rawComponents; c++ {
				raw[c] = br.read(bpc)
			}

			var r, g, b, a float64
			switch {
			case bool(isMask):
				v := decodeSample(raw[0], bpc, decodeArr[0], decodeArr[1])
				maskAlpha := 1 - v
				r, g, b = opts.FillColor.R, opts.FillColor.G, opts.FillColor.B
				a = clamp01(maskAlpha)
			case isIndexed:
				idx := decodeSample(raw[0], bpc, decodeArr[0], decodeArr[1])
				r, g, b = indexedToRGB(cs, int(idx+0.5))
				a = 1
			default:
				for c := 0; c < cs.components; c++ {
					comps[c] = decodeSample(raw[c], bpc, decodeArr[2*c], decodeArr[2*c+1])
				}
				r, g, b = cs.toRGB(comps)
				a = 1
			}

			if !bool(isMask) {
				if alphaFn != nil {
					a = alphaFn(x, y)
				}
				if colorKeyMasked(colorKey, raw) {
					a = 0
				}
			}

			i := (y*width + x) * 4
			out.Pix[i+0] = to8(r)
			out.Pix[i+1] = to8(g)
			out.Pix[i+2] = to8(b)
			out.Pix[i+3] = to8(a)
		}
	}
	return out, nil
}

// rawComponentsPerPixel is how many raw bit-packed samples one pixel
// carries in the image's own sample data - not to be confused with a
// resolved color space's own "meaningful" component count (colorSpace.
// components), which for an /Indexed color space is the *base* space's
// component count, while an Indexed *image*'s raw per-pixel sample data
// is always a single index value.
func rawComponentsPerPixel(isMask, isIndexed bool, csComponents int) int {
	if isMask || isIndexed {
		return 1
	}
	return csComponents
}

// dimensions reads and validates an image dictionary's required /Width
// and /Height entries.
func dimensions(dict syntax.Dictionary) (width, height int, err error) {
	w, ok1 := numberValue(dict["Width"])
	h, ok2 := numberValue(dict["Height"])
	if !ok1 || !ok2 {
		return 0, 0, pdferror.Malformedf("image has no numeric /Width and /Height")
	}
	width, height = int(w+0.5), int(h+0.5)
	if width <= 0 || height <= 0 {
		return 0, 0, pdferror.Malformedf("image /Width and /Height must be positive (got %d x %d)", width, height)
	}
	const maxDimension = 1 << 20
	if width > maxDimension || height > maxDimension {
		return 0, 0, pdferror.Unsupportedf("image is %dx%d pixels, exceeding this package's %d-pixel-per-axis limit", width, height, maxDimension)
	}
	return width, height, nil
}

// decodeArray returns the effective /Decode array to use: the image
// dictionary's own /Decode if present (validated against the expected
// length), or the color space family's documented default range
// otherwise (per the PDF specification: [0 1] repeated per component for
// most continuous color spaces, [0 2^BitsPerComponent-1] for an /Indexed
// color space's single index "component", [0 1] for an /ImageMask's
// single sample, and - the one exception to the "[0 1] repeated"
// continuous-space rule - whatever cs.decodeDefault gives for a color
// space like /Lab whose components do not range over [0,1] at all; see
// colorSpace.decodeDefault's own doc comment).
func decodeArray(dict syntax.Dictionary, isMask, isIndexed bool, cs colorSpace, bpc int) ([]float64, error) {
	n := rawComponentsPerPixel(isMask, isIndexed, cs.components)

	var def []float64
	switch {
	case isIndexed:
		def = []float64{0, float64((uint64(1) << uint(bpc)) - 1)}
	case cs.decodeDefault != nil:
		def = cs.decodeDefault
	default:
		def = make([]float64, 2*n)
		for i := 0; i < n; i++ {
			def[2*i], def[2*i+1] = 0, 1
		}
	}

	v, ok := dict["Decode"]
	if !ok {
		return def, nil
	}
	arr, ok := v.(syntax.Array)
	if !ok {
		return nil, pdferror.Malformedf("image /Decode is not an array")
	}
	if len(arr) != 2*n {
		return nil, pdferror.Malformedf("image /Decode has %d entries, want %d", len(arr), 2*n)
	}
	out := make([]float64, len(arr))
	for i, e := range arr {
		f, ok := numberValue(e)
		if !ok {
			return nil, pdferror.Malformedf("image /Decode entry %d is not a number", i)
		}
		out[i] = f
	}
	return out, nil
}

// decodeSample maps a raw bpc-bit sample value through the linear
// /Decode mapping the PDF specification defines (8.9.5.2): decoded =
// dmin + raw * (dmax-dmin) / (2^bpc - 1).
func decodeSample(raw uint32, bpc int, dmin, dmax float64) float64 {
	maxRaw := float64((uint64(1) << uint(bpc)) - 1)
	if maxRaw == 0 {
		return dmin
	}
	return dmin + float64(raw)*(dmax-dmin)/maxRaw
}

// numberValue extracts a float64 from a syntax.Integer or syntax.Real,
// returning ok=false for any other value (including a missing
// dictionary entry, which indexing a nil-safe Go map returns as a nil
// Object that matches neither case).
func numberValue(obj syntax.Object) (float64, bool) {
	switch v := obj.(type) {
	case syntax.Integer:
		return float64(v), true
	case syntax.Real:
		return float64(v), true
	default:
		return 0, false
	}
}

func to8(v float64) byte {
	v = clamp01(v)
	return byte(v*255 + 0.5)
}

// bitReader walks a single image row's already-loaded bytes, extracting
// successive most-significant-bit-first samples of a given bit width -
// exactly how the PDF specification requires image sample data to be
// packed (8.9.5.2: "the components ... shall be packed into bytes, high-
// order bit first"). Reading past the end of the row's byte slice
// returns zero bits rather than panicking, which happens naturally
// (rather than needing an explicit check) whenever a byte index falls
// outside the slice.
type bitReader struct {
	data   []byte
	bitPos int
}

func (br *bitReader) reset(row []byte) {
	br.data = row
	br.bitPos = 0
}

func (br *bitReader) read(bits int) uint32 {
	var v uint32
	for i := 0; i < bits; i++ {
		byteIdx := br.bitPos / 8
		bitIdx := 7 - (br.bitPos % 8)
		var bit uint32
		if byteIdx < len(br.data) {
			bit = uint32(br.data[byteIdx]>>uint(bitIdx)) & 1
		}
		v = v<<1 | bit
		br.bitPos++
	}
	return v
}
