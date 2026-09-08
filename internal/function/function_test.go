package function

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// numArray builds a syntax.Array of syntax.Real values from vals, the
// form every test in this file uses for Domain/Range/C0/C1/... entries.
func numArray(vals ...float64) syntax.Array {
	arr := make(syntax.Array, len(vals))
	for i, v := range vals {
		arr[i] = syntax.Real(v)
	}
	return arr
}

func TestParseDispatchesOnFunctionType(t *testing.T) {
	r := &fakeResolver{}
	// A Type 2 dictionary (the simplest to construct without a stream)
	// confirms Parse's dictionary path reaches parseSingle correctly.
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(2),
		"Domain":       numArray(0, 1),
	}
	fn, err := Parse(r, dict)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if fn.NumInputs() != 1 || fn.NumOutputs() != 1 {
		t.Fatalf("NumInputs/NumOutputs = %d/%d, want 1/1", fn.NumInputs(), fn.NumOutputs())
	}
}

func TestParseUnknownFunctionTypeIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(99), "Domain": numArray(0, 1)}
	_, err := Parse(&fakeResolver{}, dict)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse with /FunctionType 99: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestParseType4IsUnsupported(t *testing.T) {
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(4), "Domain": numArray(0, 1)}
	_, err := Parse(&fakeResolver{}, dict)
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Parse of a Type 4 function: got %v, want an error wrapping ErrUnsupported", err)
	}
}

func TestParseNeitherDictNorStreamNorArrayIsMalformed(t *testing.T) {
	_, err := Parse(&fakeResolver{}, syntax.Integer(5))
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse of a bare integer: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestParseType0RequiresAStream(t *testing.T) {
	// A Type 0 dictionary with no attached stream (Parse was handed a
	// plain dictionary, not a syntax.Stream) cannot supply sample data.
	dict := syntax.Dictionary{
		"FunctionType":  syntax.Integer(0),
		"Domain":        numArray(0, 1),
		"Range":         numArray(0, 1),
		"Size":          numArray(2),
		"BitsPerSample": syntax.Integer(8),
	}
	_, err := Parse(&fakeResolver{}, dict)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse of a Type 0 dictionary with no stream: got %v, want an error wrapping ErrMalformed", err)
	}
}

// TestParseMultiFunctionArray builds a /Function-style array of two Type
// 2 functions (each a simple identity ramp over a different output
// range) and confirms Eval concatenates their outputs in array order -
// the representation a shading dictionary uses when it wants, e.g., a
// separate R/G/B ramp function rather than one 3-output function.
func TestParseMultiFunctionArray(t *testing.T) {
	fn0 := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(0), "C1": numArray(10), "N": syntax.Real(1),
	}
	fn1 := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1),
		"C0": numArray(100), "C1": numArray(200), "N": syntax.Real(1),
	}
	fn, err := Parse(&fakeResolver{}, syntax.Array{fn0, fn1})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := fn.NumInputs(); got != 1 {
		t.Fatalf("NumInputs = %d, want 1", got)
	}
	if got := fn.NumOutputs(); got != 2 {
		t.Fatalf("NumOutputs = %d, want 2", got)
	}
	out, err := fn.Eval([]float64{0.5})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if len(out) != 2 || out[0] != 5 || out[1] != 150 {
		t.Fatalf("Eval(0.5) = %v, want [5 150]", out)
	}
}

func TestParseEmptyMultiFunctionArrayIsMalformed(t *testing.T) {
	_, err := Parse(&fakeResolver{}, syntax.Array{})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse of an empty function array: got %v, want an error wrapping ErrMalformed", err)
	}
}

// TestParseResolvesTopLevelReference confirms Parse follows an indirect
// reference to the function object itself, not just to entries within it
// - a shading's /Function entry is very commonly written as "N 0 R"
// rather than an inline dictionary.
func TestParseResolvesTopLevelReference(t *testing.T) {
	dict := syntax.Dictionary{"FunctionType": syntax.Integer(2), "Domain": numArray(0, 1)}
	r := &fakeResolver{objects: map[int]syntax.Object{7: dict}}
	fn, err := Parse(r, syntax.Reference{Number: 7})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := fn.Eval([]float64{0.25})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if len(out) != 1 || out[0] != 0.25 {
		t.Fatalf("Eval(0.25) = %v, want [0.25]", out)
	}
}
