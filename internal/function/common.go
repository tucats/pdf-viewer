package function

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file holds small helpers shared by type0.go, type2.go, and
// type3.go: reading a numeric array out of a function dictionary
// (Domain, Range, Encode, ...), the linear interpolation formula the
// specification defines identically for several of those arrays'
// purposes, and a couple of numeric-safety helpers. Each is small enough,
// and used by at most a handful of call sites all within this one
// package, that there is no separate "utility" file beyond this one -
// consistent with how other internal packages in this module (see, for
// example, internal/image/colorspace.go's cmykToRGB doc comment) prefer a
// short local duplicate over a shared dependency for anything this
// small.

// numberValue extracts a float64 from a syntax.Integer or syntax.Real,
// reporting ok=false for any other value.
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

// numberArray resolves obj (following a top-level reference, then each
// element's own reference - the specification technically permits either,
// though real files essentially always write these arrays with direct
// numbers) into a []float64, requiring every element to be numeric.
func numberArray(r Resolver, obj syntax.Object) ([]float64, error) {
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, err
	}
	arr, ok := resolved.(syntax.Array)
	if !ok {
		return nil, pdferror.Malformedf("expected a numeric array, found %T", resolved)
	}
	out := make([]float64, len(arr))
	for i, e := range arr {
		re, err := resolveIfRef(r, e)
		if err != nil {
			return nil, err
		}
		v, ok := numberValue(re)
		if !ok {
			return nil, pdferror.Malformedf("array element %d is not a number (found %T)", i, re)
		}
		out[i] = v
	}
	return out, nil
}

// requiredNumberArray is numberArray, but treats a missing dictionary key
// as an error - used for the several arrays (Domain always; Range,
// Functions, Bounds, Encode for a Type 3 function) the specification
// marks required for a given function type, where "silently proceed with
// no data" would be a worse failure mode than a clear malformed-input
// error.
func requiredNumberArray(r Resolver, dict syntax.Dictionary, key syntax.Name) ([]float64, error) {
	obj, ok := dict[key]
	if !ok {
		return nil, pdferror.Malformedf("function dictionary has no required /%s entry", key)
	}
	return numberArray(r, obj)
}

// optionalNumberArray is like requiredNumberArray, but returns (nil, nil)
// - not an error - when the key is absent, for the several arrays the
// specification gives a documented default for when they are omitted
// (Encode, Decode, C0, C1).
func optionalNumberArray(r Resolver, dict syntax.Dictionary, key syntax.Name) ([]float64, error) {
	obj, ok := dict[key]
	if !ok {
		return nil, nil
	}
	return numberArray(r, obj)
}

// interpolate implements the linear interpolation formula the PDF
// specification defines identically for a Type 0 function's Encode/
// Decode arrays (7.10.2) and a Type 3 function's Encode array (7.10.4):
// map x, itself expected to lie in [xmin,xmax], onto the corresponding
// point of [ymin,ymax].
//
// A zero-width input range (xmin == xmax) has no well-defined slope; this
// returns ymin in that case (an arbitrary but stable choice for
// degenerate function data) rather than dividing by zero and propagating
// a NaN into every output that depends on it.
func interpolate(x, xmin, xmax, ymin, ymax float64) float64 {
	if xmax == xmin {
		return ymin
	}
	return ymin + (x-xmin)*(ymax-ymin)/(xmax-xmin)
}

// clip returns x restricted to [lo, hi] (or [hi, lo] if lo > hi, which
// the specification does not forbid for a Range/Domain pair, however
// unusual) - the "input values shall be clipped to the domain" and
// "output values shall be clipped to the range" rules 7.10.1 states
// apply to every function type uniformly.
func clip(x, lo, hi float64) float64 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// clipToRange clips each of vals against the corresponding [min,max] pair
// in rangeArr (a flattened 2*len(vals) array, exactly like Domain/Range
// are always stored), or returns vals unchanged if rangeArr is empty -
// matching that a function's /Range entry is optional for some function
// types (Type 2, Type 3) and its absence means "do not clip output".
func clipToRange(vals []float64, rangeArr []float64) []float64 {
	if len(rangeArr) == 0 {
		return vals
	}
	if len(rangeArr) < 2*len(vals) {
		// A malformed (too-short) Range: clip only as many outputs as
		// there are pairs for, rather than indexing out of bounds.
		vals = vals[:len(rangeArr)/2]
	}
	for i := range vals {
		vals[i] = clip(vals[i], rangeArr[2*i], rangeArr[2*i+1])
	}
	return vals
}

// safeFloat replaces a NaN or +/-Inf (which malformed function data can
// produce - a zero exponent base raised to a negative power, for
// instance) with 0, so a bad function can only ever produce a
// wrong-looking finite color rather than propagate a non-number into
// internal/raster's pixel arithmetic (which has no NaN/Inf handling of
// its own to fall back on).
func safeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
