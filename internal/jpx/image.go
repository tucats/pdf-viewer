package jpx

import "math"

// This file is 14e's other half, alongside mct.go: DC level shifting
// (Annex G's own reconstruction procedure - every component the encoder
// declared unsigned in SIZ had 2^(BitDepth-1) subtracted before its
// wavelet transform and any multiple component transform, so the decoder
// must add it back here, after mct.go's inverse transform, not before -
// undoing the two steps in the opposite order they were applied), then
// rounding and clamping each finished sample to its component's declared
// range, and finally tile compositing: copying every tile's own finished
// samples into their correct location within one whole-image buffer per
// component (idwt.go's reconstructedComponent.left/top, already absolute
// reference-grid pixel coordinates via geometry.go's tileComponentBounds).
//
// Together with mct.go, this is 14e - see doc.go's development plan.
// Decode below is this package's first public, whole-image entry point;
// 14f is what makes it reachable from internal/filter.

// Image is this package's final decoded result: a plain multi-component
// raster, resolved from a codestream's own tiling into one contiguous
// buffer per component. It carries no PDF-specific concept (colour
// space, soft mask) - see doc.go's "Why this package exists" section;
// 14f's internal/filter adapter is what reshapes this into whatever byte
// layout internal/image expects.
type Image struct {
	Width, Height int
	Components    []ImageComponent
}

// ImageComponent is one fully reconstructed component: one integer
// sample per pixel of the whole image (row-major, Width*Height long),
// already inverse-multiple-component-transformed (if this codestream
// used one - mct.go), DC-level-shifted back to its original signed or
// unsigned representation, and clamped to the range BitDepth/Signed
// implies - see finalizeSample.
type ImageComponent struct {
	BitDepth int
	Signed   bool
	Samples  []int32
}

// maxSupportedBitDepth bounds this file's int32 sample representation.
// SIZ's own limit is 38 bits (siz.go's parseSIZ), comfortably wider than
// an unsigned sample's [0, 2^BitDepth-1] range can fit in an int32; no
// real-world image remotely approaches even 16 bits, so this only ever
// rejects a hostile or corrupt file's absurd declared bit depth - the
// same "bounded work" policy siz.go's maxReasonableDimension applies to
// image dimensions.
const maxSupportedBitDepth = 31

// newImage allocates an Image sized and component-typed from h, with
// every component's Samples slice zero-filled - compositeTile fills each
// tile's own share of it in as Decode walks the codestream's tiles.
func newImage(h *Header) (*Image, error) {
	img := &Image{Width: h.Width, Height: h.Height}
	img.Components = make([]ImageComponent, len(h.Components))
	for i, ci := range h.Components {
		if ci.BitDepth > maxSupportedBitDepth {
			return nil, unsupportedf("component %d declares a %d-bit sample depth, exceeding this package's %d-bit limit", i, ci.BitDepth, maxSupportedBitDepth)
		}
		img.Components[i] = ImageComponent{
			BitDepth: ci.BitDepth,
			Signed:   ci.Signed,
			Samples:  make([]int32, h.Width*h.Height),
		}
	}
	return img, nil
}

// finalizeSample undoes DC level shifting (adding back 2^(bitDepth-1) for
// an unsigned component - Annex G's reconstruction procedure) and rounds
// and clamps v - already inverse-MCT'd, if this tile used one - to the
// range bitDepth/signed implies: [-2^(bitDepth-1), 2^(bitDepth-1)-1] for
// a signed component, [0, 2^bitDepth-1] for an unsigned one. math.Round,
// not truncation: only the irreversible path (the 9/7 wavelet filter,
// optionally the ICT) ever produces a non-integer v, and rounding to the
// nearest integer is what that path's own lossy reconstruction is
// defined against. Clamping guards against a corrupt or hostile
// codestream's dequantized/transformed value falling outside the range
// its own declared bit depth allows - never expected for a well-formed
// file, but not itself a malformed-input error worth rejecting the whole
// decode over.
func finalizeSample(v float64, bitDepth int, signed bool) int32 {
	if !signed {
		v += float64(int64(1) << uint(bitDepth-1))
	}
	r := math.Round(v)

	lo, hi := -(int64(1) << uint(bitDepth-1)), (int64(1)<<uint(bitDepth-1))-1
	if !signed {
		lo, hi = 0, (int64(1)<<uint(bitDepth))-1
	}

	switch {
	case r < float64(lo):
		return int32(lo)
	case r > float64(hi):
		return int32(hi)
	default:
		return int32(r)
	}
}

// compositeTile writes one tile's already-reconstructed, already-inverse-
// MCT'd components into their correct location within img, finalizing
// each sample (finalizeSample) along the way. left/top on each
// reconstructedComponent are absolute reference-grid coordinates
// (geometry.go's tileComponentBounds); img's own buffers are indexed from
// the image area's own origin (h.XOsiz, h.YOsiz), so this subtracts that
// offset back out.
func compositeTile(h *Header, img *Image, components []*reconstructedComponent) {
	for c, rc := range components {
		dst := img.Components[c]
		ox := rc.left - h.XOsiz
		oy := rc.top - h.YOsiz
		for row := 0; row < rc.height; row++ {
			dstRow := (oy + row) * img.Width
			srcRow := row * rc.width
			for col := 0; col < rc.width; col++ {
				dst.Samples[dstRow+ox+col] = finalizeSample(rc.items[srcRow+col], dst.BitDepth, dst.Signed)
			}
		}
	}
}

// Decode runs this package's whole decode pipeline over every tile in h:
// tier-2 packet parsing and tier-1 entropy decoding (packet.go/tier1.go),
// dequantization and the inverse wavelet transform (dequantize.go/
// idwt.go's decodeTile), the inverse multiple component transform
// (mct.go), and this file's own DC level shifting and clamping -
// compositing the result into one whole-image Image. codestream must be
// the same byte slice ParseHeader produced h from - see decodeTile's own
// doc comment (a JP2-wrapped file's caller must pass the unwrapped
// codestream, not the original file bytes). Not yet reachable from
// internal/filter - 14f wires that up.
func Decode(h *Header, codestream []byte) (*Image, error) {
	img, err := newImage(h)
	if err != nil {
		return nil, err
	}

	for t := 0; t < h.NumTilesX*h.NumTilesY; t++ {
		components, err := decodeTile(h, codestream, t)
		if err != nil {
			return nil, err
		}
		if err := applyMultipleComponentTransform(h, t, components); err != nil {
			return nil, err
		}
		compositeTile(h, img, components)
	}
	return img, nil
}
