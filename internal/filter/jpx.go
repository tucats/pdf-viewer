package filter

import (
	"errors"
	"fmt"

	"github.com/tucats/pdf-viewer/internal/jpx"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Phase 14f: wiring internal/jpx's from-scratch
// JPEG 2000 decoder into this package's filter chain (decodeJPX, the
// JPXDecode case decodeOne now dispatches to - the same "flatten a
// decoded image to the raw interleaved sample bytes internal/image
// expects" role dct.go's decodeDCT plays for DCTDecode), plus the two
// PDF-specific behaviors ISO 32000-1 7.4.9 documents for JPXDecode
// specifically, which need more context (an image dictionary, not just a
// byte slice) than an ordinary filter can express - see DecodeImage and
// JPXInfo below.

// decodeJPX reverses PDF's JPXDecode filter for the generic filter-chain
// path (decodeOne, and so Decode/DecodeWith): it decodes data - a bare
// JPEG 2000 codestream or a JP2-boxed file, either of which
// internal/jpx.DecodeStream accepts - and flattens every decoded
// component into plain interleaved sample bytes, in codestream order.
// This path has no image dictionary to consult, so it cannot apply
// either of this filter's own PDF-specific behaviors (a /ColorSpace
// fallback, or splitting off a /SMaskInData alpha component) - a caller
// decoding an actual image XObject or inline image should call
// DecodeImage instead, which does.
func decodeJPX(data []byte) ([]byte, error) {
	img, _, err := jpx.DecodeStream(data)
	if err != nil {
		return nil, translateJPXError(err)
	}
	return packJPXComponents(img.Components, img.Width, img.Height)
}

// translateJPXError wraps an internal/jpx error in this project's own
// pdferror classification (malformed vs. unsupported input) - see
// internal/jpx's own doc comment ("Why this package exists") on why it
// deliberately does not import pdferror itself, leaving this adapter as
// the one place that translates between the two packages' otherwise
// identically-shaped two-sentinel error schemes.
func translateJPXError(err error) error {
	if errors.Is(err, jpx.ErrUnsupported) {
		return pdferror.Unsupportedf("JPXDecode: %v", err)
	}
	return pdferror.Malformedf("JPXDecode: %v", err)
}

// maxJPXBitDepth bounds the per-component bit depth packJPXComponents
// will pack - the four PDF /BitsPerComponent values internal/image's own
// bitReader (decode.go there) knows how to unpack, plus 16. A JPX
// component with any other declared bit depth (internal/jpx.ComponentInfo
// permits up to 38) is rejected as unsupported rather than silently
// rescaled - no real-world PDF-embedded JPX sample motivating this phase
// uses anything but 8, and rescaling would silently lose or fabricate
// precision instead of reporting the mismatch.
var jpxSupportedBitDepths = map[int]bool{1: true, 2: true, 4: true, 8: true, 16: true}

// packJPXComponents flattens components (each one plane, w*h samples, in
// codestream order) into row-major interleaved sample bytes - the same
// byte layout every filter in this package's chain produces, and
// internal/image's bitReader expects (PDF 8.9.5.2: components packed
// high-order-bit-first per pixel, rows padded out to a whole byte).
// Every component must share one unsigned bit depth from
// jpxSupportedBitDepths; components is assumed non-empty (checked by
// both of this file's callers before it could be).
func packJPXComponents(components []jpx.ImageComponent, w, h int) ([]byte, error) {
	if len(components) == 0 {
		return nil, pdferror.Malformedf("JPXDecode: image has no components")
	}
	bitDepth := components[0].BitDepth
	for i, c := range components {
		if c.Signed {
			return nil, pdferror.Unsupportedf("JPXDecode: component %d uses signed samples", i)
		}
		if c.BitDepth != bitDepth {
			return nil, pdferror.Unsupportedf("JPXDecode: components have differing bit depths (%d vs %d)", bitDepth, c.BitDepth)
		}
	}
	if !jpxSupportedBitDepths[bitDepth] {
		return nil, pdferror.Unsupportedf("JPXDecode: %d-bit samples are not one of 1, 2, 4, 8, 16", bitDepth)
	}

	n := len(components)
	switch bitDepth {
	case 8:
		out := make([]byte, w*h*n)
		for i := 0; i < w*h; i++ {
			for c := 0; c < n; c++ {
				out[i*n+c] = byte(components[c].Samples[i])
			}
		}
		return out, nil
	case 16:
		out := make([]byte, w*h*n*2)
		for i := 0; i < w*h; i++ {
			for c := 0; c < n; c++ {
				v := uint16(components[c].Samples[i])
				out[(i*n+c)*2] = byte(v >> 8)
				out[(i*n+c)*2+1] = byte(v)
			}
		}
		return out, nil
	default: // 1, 2, or 4
		rowBits := w * n * bitDepth
		rowBytes := (rowBits + 7) / 8
		out := make([]byte, rowBytes*h)
		for y := 0; y < h; y++ {
			row := out[y*rowBytes : (y+1)*rowBytes]
			bitPos := 0
			for x := 0; x < w; x++ {
				i := y*w + x
				for c := 0; c < n; c++ {
					// writeBitsMSB (predictor.go) is this package's
					// existing big-endian sub-byte bitfield writer - the
					// same packing PDF sample data (and this function's
					// own caller, internal/image's bitReader) use.
					writeBitsMSB(row, bitPos, bitDepth, uint32(components[c].Samples[i]))
					bitPos += bitDepth
				}
			}
		}
		return out, nil
	}
}

// packAlphaComponent normalizes one JPX component's samples to a plain
// 0-255 byte of opacity per pixel, regardless of that component's own
// declared bit depth - the layout DecodeImage's JPXInfo.Alpha, and
// internal/image's Options.EmbeddedAlpha that eventually consumes it,
// both expect (one byte per pixel, matching every other alpha source
// this project already handles - see internal/image/mask.go).
func packAlphaComponent(c jpx.ImageComponent) []byte {
	maxVal := int64(1)<<uint(c.BitDepth) - 1
	if maxVal <= 0 {
		maxVal = 1
	}
	out := make([]byte, len(c.Samples))
	for i, v := range c.Samples {
		iv := int64(v)
		switch {
		case iv < 0:
			iv = 0
		case iv > maxVal:
			iv = maxVal
		}
		out[i] = byte(iv * 255 / maxVal)
	}
	return out
}

// JPXInfo is the extra PDF-specific information DecodeImage reports
// alongside a JPXDecode-filtered image's decoded sample bytes - see ISO
// 32000-1 7.4.9. Both fields may be zero-valued: FallbackColorSpace is
// empty when this package cannot derive one (a color component count
// other than 1, 3, or 4), and Alpha is nil whenever smaskInData was zero
// or the image had only one component to begin with (nothing left to
// treat as color if the sole component were split off as alpha).
type JPXInfo struct {
	// FallbackColorSpace is the /ColorSpace name (DeviceGray, DeviceRGB,
	// or DeviceCMYK) a caller should use when the image dictionary itself
	// has no /ColorSpace entry - derived from the returned samples' own
	// color component count (after any Alpha component has already been
	// split off, if smaskInData asked for one), the same "N components ->
	// Device family of that arity" approximation this project's own
	// /ICCBased handling already uses (internal/image/colorspace.go's
	// resolveICCBased) rather than interpreting the JP2 container's own
	// "colr" box (an enumerated color space or an embedded ICC profile)
	// in full. This is sufficient rather than merely convenient: this
	// package's own multiple component transform (internal/jpx/mct.go)
	// has already converted a codestream using RCT/ICT back to plain R,
	// G, B by the time DecodeImage sees it, so "how many components" is
	// already exactly "which Device family", for every enumerated JP2
	// color space (greyscale, sRGB, sYCC) this project is aware of any
	// real-world PDF producer using.
	FallbackColorSpace syntax.Name

	// Alpha, when non-nil, is one byte of opacity (0-255) per pixel,
	// row-major - this image's own /SMaskInData-declared embedded
	// opacity channel, already split out of the returned samples and
	// normalized to 8 bits (packAlphaComponent) regardless of its own
	// declared bit depth. See DecodeImage's doc comment for the
	// "trailing component is the alpha channel" convention this relies
	// on.
	Alpha []byte
}

// DecodeImage is Decode/DecodeWith's counterpart for an image XObject or
// inline image dictionary specifically (internal/content is this
// project's only caller with an image dictionary in hand, rather than
// just a filter chain and raw bytes): identical to DecodeWith for every
// filter chain that does not end in JPXDecode, but when it does,
// additionally applies the two PDF-specific behaviors ISO 32000-1 7.4.9
// documents for that filter - see JPXInfo. info is nil whenever the
// filter chain did not end in JPXDecode (the ordinary DecodeWith result
// carries no such information to report).
//
// smaskInData is the image dictionary's own /SMaskInData value (0 if
// absent - the specification's own default, meaning "no embedded alpha
// channel"). When nonzero, this function trusts the producer's own
// assertion that an opacity channel is embedded in the JPX data and
// treats the *last* decoded component as that channel, splitting it out
// of the returned samples into JPXInfo.Alpha - the remaining components,
// in codestream order, are this image's actual color data. This is a
// deliberate simplification: a fully general reading would need to parse
// the JP2 container's own "cdef" (Channel Definition) box to know
// exactly which component the encoder meant as opacity (internal/jpx's
// own box parsing does not implement "cdef" - see that package's doc
// comment's "Scope" section), but the trailing-component convention is
// what every real-world JPX-with-alpha encoder this project is aware of
// already follows (the same convention other PDF renderers, e.g. pdf.js,
// rely on for this same case), and is a documented scope decision in the
// same spirit as this package's other ones rather than a silent
// approximation.
//
// resolver satisfies the one filter earlier in the chain that could need
// it (JBIG2Decode's own /JBIG2Globals - see StreamResolver); nil is
// always correct for an actual JPXDecode-filtered image, since this
// project has no real-world evidence of JPXDecode ever being preceded by
// a filter that needs one (a JBIG2-then-JPX chain has no meaning: both
// filters independently produce a complete raster image, so combining
// them is not something any real encoder does).
func DecodeImage(dict syntax.Dictionary, raw []byte, resolver StreamResolver, smaskInData int) ([]byte, *JPXInfo, error) {
	names, err := filterNames(dict)
	if err != nil {
		return nil, nil, err
	}
	if len(names) == 0 || names[len(names)-1] != "JPXDecode" {
		data, err := DecodeWith(dict, raw, resolver)
		return data, nil, err
	}

	parmsList, err := decodeParmsList(dict, len(names))
	if err != nil {
		return nil, nil, err
	}

	data := raw
	for i := 0; i < len(names)-1; i++ {
		data, err = decodeOne(names[i], parmsList[i], data, resolver)
		if err != nil {
			return nil, nil, fmt.Errorf("filter %q: %w", names[i], err)
		}
	}

	img, _, err := jpx.DecodeStream(data)
	if err != nil {
		return nil, nil, translateJPXError(err)
	}
	return buildJPXImageResult(img, smaskInData)
}

// buildJPXImageResult is DecodeImage's own logic once internal/jpx has
// already produced img - split out so it can be unit-tested directly
// against a hand-built *jpx.Image (jpx.Image and jpx.ImageComponent are
// plain exported structs - see image.go there), without needing a real
// encoded JPEG 2000 codestream on hand for every case this function's
// own branching needs to cover. See DecodeImage's doc comment for the
// /SMaskInData splitting and /ColorSpace fallback rules this implements.
func buildJPXImageResult(img *jpx.Image, smaskInData int) ([]byte, *JPXInfo, error) {
	info := &JPXInfo{}
	components := img.Components
	if smaskInData != 0 && len(components) > 1 {
		info.Alpha = packAlphaComponent(components[len(components)-1])
		components = components[:len(components)-1]
	}
	switch len(components) {
	case 1:
		info.FallbackColorSpace = "DeviceGray"
	case 3:
		info.FallbackColorSpace = "DeviceRGB"
	case 4:
		info.FallbackColorSpace = "DeviceCMYK"
	}

	samples, err := packJPXComponents(components, img.Width, img.Height)
	if err != nil {
		return nil, nil, err
	}
	return samples, info, nil
}

// IsJPXImage reports whether dict's /Filter chain (a single Name or an
// Array of them) names JPXDecode as its last - and, per this project's
// real-world experience, always only - filter. internal/content uses
// this to decide whether an image stream needs DecodeImage's extra
// handling instead of the ordinary DecodeWith/Decode path; a malformed
// /Filter entry is reported as "not a JPX image" here (false, no error)
// rather than propagating the error, since the caller's own subsequent
// ordinary decode call will report the same malformed entry properly.
func IsJPXImage(dict syntax.Dictionary) bool {
	names, err := filterNames(dict)
	if err != nil {
		return false
	}
	return len(names) > 0 && names[len(names)-1] == "JPXDecode"
}
