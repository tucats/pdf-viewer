package graphics

import "math"

// This file implements the per-channel formulas for PDF's six separable
// blend modes (11.3.5.2) - see BlendMode's own doc comment for which
// modes this project implements and why the non-separable ones are out
// of scope. Every formula here operates on a single channel value in
// [0,1] (cb, the backdrop/existing color; cs, the newly painted source
// color) and is applied identically, independently, to each of R, G,
// and B - internal/raster's Canvas is what actually calls this once per
// channel per pixel (see its blendChannel).

// Blend computes one output channel value for mode, given the existing
// backdrop channel cb and the newly painted source channel cs (both
// already in [0,1] - internal/raster is responsible for that
// normalization, from its own 8-bit-per-channel pixel storage). This is
// the "B(cb,cs)" blend function the specification defines per mode; it
// is not itself the final compositing formula (which also accounts for
// alpha - see internal/raster.Canvas's blendChannel) - for BlendNormal
// specifically, B(cb,cs) is simply cs, since "Normal" performs no actual
// blending against the backdrop at all.
func Blend(mode BlendMode, cb, cs float64) float64 {
	switch mode {
	case BlendMultiply:
		return cb * cs
	case BlendScreen:
		return cb + cs - cb*cs
	case BlendDarken:
		return math.Min(cb, cs)
	case BlendLighten:
		return math.Max(cb, cs)
	case BlendDifference:
		return math.Abs(cb - cs)
	case BlendExclusion:
		return cb + cs - 2*cb*cs
	case BlendNormal:
		fallthrough
	default:
		// Per the specification, an unrecognized mode falls back to
		// Normal (see BlendMode's doc comment) - handled here too, in
		// addition to at parse time (internal/content's
		// resolveBlendMode), purely as a defensive default so this
		// function is total over BlendMode's full underlying int range,
		// not just its named constants.
		return cs
	}
}
