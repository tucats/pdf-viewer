package function

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// type2 implements a PDF Type 2 (exponential interpolation) function
// (7.10.3): a single input x, clipped to domain, produces outputs
// y_j = C0_j + x^N * (C1_j - C0_j) for each j. With the specification's
// own defaults (C0=[0.0], C1=[1.0], N=1) this is simply "x itself" - the
// identity ramp most often seen as one leg of a Type 3 stitching
// function (see type3.go) or as a shading's /Function when the caller
// really just wants "interpolate linearly between two colors".
type type2 struct {
	domain [2]float64
	c0, c1 []float64
	n      float64
	// rangeArr is the optional /Range entry (nil if absent, meaning "do
	// not clip output") - unlike Type 0, Type 2 does not require one.
	rangeArr []float64
}

// parseType2 reads a Type 2 function's dictionary. /Domain is required
// (7.10.3's function-dictionary table marks it required for every
// function type); C0, C1, and N each fall back to their documented
// default when absent.
func parseType2(r Resolver, dict syntax.Dictionary) (Function, error) {
	domain, err := requiredNumberArray(r, dict, "Domain")
	if err != nil {
		return nil, err
	}
	if len(domain) != 2 {
		return nil, pdferror.Malformedf("Type 2 function /Domain must have 2 entries, has %d", len(domain))
	}

	c0, err := optionalNumberArray(r, dict, "C0")
	if err != nil {
		return nil, err
	}
	if c0 == nil {
		c0 = []float64{0.0}
	}
	c1, err := optionalNumberArray(r, dict, "C1")
	if err != nil {
		return nil, err
	}
	if c1 == nil {
		c1 = []float64{1.0}
	}
	if len(c0) != len(c1) {
		return nil, pdferror.Malformedf("Type 2 function /C0 has %d entries but /C1 has %d", len(c0), len(c1))
	}

	n := 1.0
	if v, ok := dict["N"]; ok {
		resolved, err := resolveIfRef(r, v)
		if err != nil {
			return nil, err
		}
		nv, ok := numberValue(resolved)
		if !ok {
			return nil, pdferror.Malformedf("Type 2 function /N is not a number (found %T)", resolved)
		}
		n = nv
	}

	rangeArr, err := optionalNumberArray(r, dict, "Range")
	if err != nil {
		return nil, err
	}

	return &type2{
		domain:   [2]float64{domain[0], domain[1]},
		c0:       c0,
		c1:       c1,
		n:        n,
		rangeArr: rangeArr,
	}, nil
}

func (f *type2) NumInputs() int  { return 1 }
func (f *type2) NumOutputs() int { return len(f.c0) }

func (f *type2) Eval(inputs []float64) ([]float64, error) {
	if len(inputs) != 1 {
		return nil, pdferror.Malformedf("Type 2 function expects 1 input, got %d", len(inputs))
	}
	x := clip(inputs[0], f.domain[0], f.domain[1])

	// math.Pow(negative, non-integer) is defined by the standard library
	// to return NaN - a real possibility here, since a malformed or
	// unusual function could pair a negative Domain with a fractional N
	// (the specification requires x>=0 whenever N is not an integer, but
	// this package tolerates violations of that rule rather than
	// rejecting the whole function over it, per this project's general
	// "hostile input still renders something" policy). safeFloat below
	// turns any resulting NaN/Inf into a plain 0 rather than letting it
	// propagate into a pixel color.
	xn := math.Pow(x, f.n)

	out := make([]float64, len(f.c0))
	for i := range out {
		out[i] = safeFloat(f.c0[i] + xn*(f.c1[i]-f.c0[i]))
	}
	return clipToRange(out, f.rangeArr), nil
}
