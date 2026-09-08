package function

import (
	"errors"
	"math"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestType2DefaultsToIdentityRamp(t *testing.T) {
	// With no C0/C1/N given, the specification's own defaults (C0=[0],
	// C1=[1], N=1) make this function simply "return x".
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1)}
	fn, err := parseType2(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType2: %v", err)
	}
	for _, x := range []float64{0, 0.3, 1} {
		out, err := fn.Eval([]float64{x})
		if err != nil {
			t.Fatalf("Eval(%v): %v", x, err)
		}
		if len(out) != 1 || math.Abs(out[0]-x) > 1e-9 {
			t.Fatalf("Eval(%v) = %v, want [%v]", x, out, x)
		}
	}
}

func TestType2InterpolatesBetweenC0AndC1(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(0, 0, 0), "C1": numArray(1, 0.5, 0), "N": syntax.Real(1),
	}
	fn, err := parseType2(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType2: %v", err)
	}
	out, err := fn.Eval([]float64{0.5})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	want := []float64{0.5, 0.25, 0}
	for i := range want {
		if math.Abs(out[i]-want[i]) > 1e-9 {
			t.Fatalf("Eval(0.5) = %v, want %v", out, want)
		}
	}
}

func TestType2ExponentAppliesBeforeInterpolation(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(0), "C1": numArray(1), "N": syntax.Real(2),
	}
	fn, err := parseType2(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType2: %v", err)
	}
	// x=0.5, N=2 => 0.5^2 = 0.25.
	out, err := fn.Eval([]float64{0.5})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if math.Abs(out[0]-0.25) > 1e-9 {
		t.Fatalf("Eval(0.5) = %v, want [0.25]", out)
	}
}

func TestType2ClipsInputToDomain(t *testing.T) {
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1)}
	fn, err := parseType2(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType2: %v", err)
	}
	out, err := fn.Eval([]float64{5})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out[0] != 1 {
		t.Fatalf("Eval(5) with Domain [0,1] = %v, want [1] (clipped)", out)
	}
}

func TestType2ClipsOutputToRange(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(-5), "C1": numArray(5), "Range": numArray(0, 1),
	}
	fn, err := parseType2(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType2: %v", err)
	}
	out, err := fn.Eval([]float64{0}) // raw output -5, clipped to Range [0,1]
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if out[0] != 0 {
		t.Fatalf("Eval(0) = %v, want [0] (clipped to Range)", out)
	}
}

func TestType2MismatchedC0C1LengthsIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(0, 0), "C1": numArray(1),
	}
	_, err := parseType2(&fakeResolver{}, dict)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType2 with mismatched C0/C1: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType2WrongInputCountIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1)}
	fn, err := parseType2(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType2: %v", err)
	}
	if _, err := fn.Eval([]float64{0, 1}); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Eval with 2 inputs: got %v, want an error wrapping ErrMalformed", err)
	}
}

// TestType2NegativeBaseFractionalExponentDoesNotProduceNaN exercises
// safeFloat's defensive handling: a negative Domain paired with a
// fractional N is malformed (the specification requires x>=0 whenever N
// is not an integer), but this package tolerates it rather than
// rejecting the function outright, and must never let math.Pow's
// resulting NaN leak into Eval's output.
func TestType2NegativeBaseFractionalExponentDoesNotProduceNaN(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(-1, 1),
		"C0": numArray(0), "C1": numArray(1), "N": syntax.Real(0.5),
	}
	fn, err := parseType2(&fakeResolver{}, dict)
	if err != nil {
		t.Fatalf("parseType2: %v", err)
	}
	out, err := fn.Eval([]float64{-1})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if math.IsNaN(out[0]) || math.IsInf(out[0], 0) {
		t.Fatalf("Eval(-1) = %v, want a finite number (NaN/Inf must be sanitized)", out[0])
	}
}

func TestType2MissingDomainIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(2)}
	_, err := parseType2(&fakeResolver{}, dict)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType2 with no /Domain: got %v, want an error wrapping ErrMalformed", err)
	}
}
