package function

import (
	"errors"
	"math"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// twoStopType3 builds a Type 3 stitching function over Domain [0,1] with
// two Type 2 subfunctions meeting at Bounds=[0.5]: the first ramps 0->10
// over [0,0.5), the second ramps 10->20 over [0.5,1] - a three-stop
// gradient's worth of function data, the shape a real multi-stop PDF
// gradient's tint transform actually takes.
func twoStopType3(t *testing.T) Function {
	t.Helper()
	fn0 := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(0), "C1": numArray(10), "N": syntax.Real(1),
	}
	fn1 := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(10), "C1": numArray(20), "N": syntax.Real(1),
	}
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(3),
		"Domain":       numArray(0, 1),
		"Functions":    syntax.Array{fn0, fn1},
		"Bounds":       numArray(0.5),
		"Encode":       numArray(0, 1, 0, 1),
	}
	fn, err := parseType3(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType3: %v", err)
	}
	return fn
}

func TestType3SelectsSubfunctionByBounds(t *testing.T) {
	fn := twoStopType3(t)
	cases := []struct {
		x, want float64
	}{
		{0, 0},
		{0.25, 5},  // first subfunction, remapped 0.25 -> t=0.5 -> 5
		{0.5, 10},  // exactly on the boundary: belongs to the second subfunction
		{0.75, 15}, // second subfunction, remapped 0.75 -> t=0.5 -> 15
		{1, 20},
	}
	for _, c := range cases {
		out, err := fn.Eval([]float64{c.x})
		if err != nil {
			t.Fatalf("Eval(%v): %v", c.x, err)
		}
		if len(out) != 1 || math.Abs(out[0]-c.want) > 1e-9 {
			t.Fatalf("Eval(%v) = %v, want [%v]", c.x, out, c.want)
		}
	}
}

func TestType3ClipsInputToDomain(t *testing.T) {
	fn := twoStopType3(t)
	out, err := fn.Eval([]float64{5})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out[0] != 20 {
		t.Fatalf("Eval(5) with Domain [0,1] = %v, want [20] (clipped then evaluated at the top)", out)
	}
}

func TestType3BoundsCountMustMatchFunctionsMinusOne(t *testing.T) {
	fn0 := syntax.Dictionary{"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1)}
	fn1 := syntax.Dictionary{"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1)}
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(3), "Domain": numArray(0, 1),
		"Functions": syntax.Array{fn0, fn1},
		"Bounds":    numArray(0.3, 0.6), // should be exactly 1 entry for 2 functions
		"Encode":    numArray(0, 1, 0, 1),
	}
	_, err := parseType3(&fakeResolver{}, dict)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType3 with wrong /Bounds length: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType3MissingFunctionsIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(3), "Domain": numArray(0, 1),
		"Bounds": numArray(), "Encode": numArray(),
	}
	_, err := parseType3(&fakeResolver{}, dict)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType3 with no /Functions: got %v, want an error wrapping ErrMalformed", err)
	}
}

// TestType3SingleFunctionDegeneratesToItsEncode confirms the k=1 case
// (no /Bounds entries at all - legal, if unusual, since len(Bounds) must
// be k-1) is handled without an off-by-one panic in the "last segment"
// branch of Eval.
func TestType3SingleFunctionDegeneratesToItsEncode(t *testing.T) {
	fn0 := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(100), "C1": numArray(200),
	}
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(3), "Domain": numArray(0, 1),
		"Functions": syntax.Array{fn0},
		"Bounds":    numArray(),
		"Encode":    numArray(0, 1),
	}
	fn, err := parseType3(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType3: %v", err)
	}
	out, err := fn.Eval([]float64{0.5})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if math.Abs(out[0]-150) > 1e-9 {
		t.Fatalf("Eval(0.5) = %v, want [150]", out)
	}
}
