package function

import (
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// Function evaluates a PDF function object (ISO 32000-1 section 7.10) at
// a given set of input values. See the package doc comment for which
// function types this package implements.
type Function interface {
	// Eval evaluates the function at inputs, returning its output
	// values. Implementations clip inputs to the function's own /Domain
	// before computing (per 7.10.1, "input values shall be clipped to
	// the domain") and clip outputs to /Range afterward when the
	// function declares one - a caller never needs to perform either
	// clamp itself. Eval returns an error only for a call that does not
	// match the function's own declared input count (NumInputs); a
	// well-formed function, called correctly, never fails at evaluation
	// time - see the package doc comment's note on safeFloat for how
	// otherwise-invalid arithmetic (rather than a wrong call shape) is
	// handled.
	Eval(inputs []float64) ([]float64, error)

	// NumInputs reports the function's declared input arity (the number
	// of Domain pairs), letting a caller validate wiring - for example,
	// that a Separation color space's tint transform accepts exactly the
	// one tint component that color space provides - before ever calling
	// Eval.
	NumInputs() int

	// NumOutputs reports the function's output arity: the number of
	// Range pairs for a function type that requires one (Type 0), or the
	// number of values its C0/C1 arrays carry for a function type that
	// does not (Type 2), or the sum of each subfunction's NumOutputs for
	// a Type 3 stitching function or a multi-function array (see
	// parseMulti).
	NumOutputs() int
}

// Parse resolves obj (a function dictionary, a function stream, or an
// array of single-output functions - see parseMulti) into a Function.
//
// A shading dictionary's own /Function entry, and a Separation or
// DeviceN color space's tint transform entry, are the two places this
// project currently calls Parse; both PDF constructs permit exactly this
// same three-way choice of representation (a lone function, a lone
// function stream, or an array of them), so one entry point serves both
// callers.
func Parse(r Resolver, obj syntax.Object) (Function, error) {
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, err
	}
	switch v := resolved.(type) {
	case syntax.Array:
		return parseMulti(r, v)
	case syntax.Stream:
		return parseSingle(r, v.Dict, &v)
	case syntax.Dictionary:
		return parseSingle(r, v, nil)
	default:
		return nil, pdferror.Malformedf("function object is neither a dictionary, stream, nor array (found %T)", resolved)
	}
}

// parseSingle dispatches on dict's required /FunctionType entry. stream
// is non-nil when obj was itself a syntax.Stream (as a Type 0 function's
// sample data must be - see type0.go); it is nil for a plain-dictionary
// function (Type 2 and Type 3 carry no sample data of their own, only
// numbers and nested function objects).
func parseSingle(r Resolver, dict syntax.Dictionary, stream *syntax.Stream) (Function, error) {
	ft, ok := dict["FunctionType"].(syntax.Integer)
	if !ok {
		return nil, pdferror.Malformedf("function dictionary has no integer /FunctionType")
	}
	switch ft {
	case 0:
		if stream == nil {
			return nil, pdferror.Malformedf("Type 0 (sampled) function has no sample data stream")
		}
		return parseType0(r, dict, *stream)
	case 2:
		return parseType2(r, dict)
	case 3:
		return parseType3(r, dict)
	case 4:
		return nil, pdferror.Unsupportedf("Type 4 (PostScript calculator) function")
	default:
		return nil, pdferror.Malformedf("function /FunctionType %d is not one of 0, 2, 3, 4", ft)
	}
}

// multiFunction adapts an array of several single-input functions (each
// typically producing one output component) into one Function whose
// outputs are the concatenation of each subfunction's own outputs,
// evaluated on the same input vector - the representation a shading
// dictionary's /Function entry may use instead of one N-output function,
// most often one exponential-interpolation (Type 2) function per color
// component.
type multiFunction struct {
	fns []Function
}

// parseMulti builds a multiFunction from arr, requiring at least one
// element and that every element itself parses as a Function (nested
// arrays are not meaningful here and are rejected by the same Parse call
// that would otherwise recurse into parseMulti again).
func parseMulti(r Resolver, arr syntax.Array) (Function, error) {
	if len(arr) == 0 {
		return nil, pdferror.Malformedf("function array is empty")
	}
	fns := make([]Function, len(arr))
	for i, elem := range arr {
		fn, err := Parse(r, elem)
		if err != nil {
			return nil, err
		}
		fns[i] = fn
	}
	return &multiFunction{fns: fns}, nil
}

func (m *multiFunction) NumInputs() int {
	return m.fns[0].NumInputs()
}

func (m *multiFunction) NumOutputs() int {
	n := 0
	for _, fn := range m.fns {
		n += fn.NumOutputs()
	}
	return n
}

// Eval calls every subfunction with the same inputs and concatenates
// their outputs in array order, matching the specification's description
// of a shading's function array: "the number of functions shall be
// consistent with the number of components... one function per
// component".
func (m *multiFunction) Eval(inputs []float64) ([]float64, error) {
	out := make([]float64, 0, m.NumOutputs())
	for _, fn := range m.fns {
		vals, err := fn.Eval(inputs)
		if err != nil {
			return nil, err
		}
		out = append(out, vals...)
	}
	return out, nil
}
