package function

import (
	"errors"
	"math"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// oneDimType0 builds a 1-input, 1-output, 8-bit Type 0 function over 2
// grid points (Size=[2]): sample 0 (at input=0) decodes to 0.0, sample 1
// (at input=1) decodes to 1.0 - the simplest possible sampled function,
// used to test the linear-interpolation (m=1) path of Eval.
func oneDimType0(t *testing.T) Function {
	t.Helper()
	dict := syntax.Dictionary{
		"FunctionType":  syntax.Integer(0),
		"Domain":        numArray(0, 1),
		"Range":         numArray(0, 1),
		"Size":          numArray(2),
		"BitsPerSample": syntax.Integer(8),
	}
	stream := syntax.Stream{Dict: dict, Raw: []byte{0x00, 0xFF}}
	fn, err := parseType0(&fakeResolver{}, dict, stream)
	if err != nil {
		t.Fatalf("parseType0: %v", err)
	}
	return fn
}

func TestType0InterpolatesBetweenGridPoints(t *testing.T) {
	fn := oneDimType0(t)
	cases := []struct {
		x, want float64
	}{
		{0, 0},
		{1, 1},
		{0.5, 0.5},
		{0.25, 0.25},
	}
	for _, c := range cases {
		out, err := fn.Eval([]float64{c.x})
		if err != nil {
			t.Fatalf("Eval(%v): %v", c.x, err)
		}
		if len(out) != 1 || math.Abs(out[0]-c.want) > 0.01 {
			t.Fatalf("Eval(%v) = %v, want ~[%v]", c.x, out, c.want)
		}
	}
}

func TestType0ClipsInputToDomain(t *testing.T) {
	fn := oneDimType0(t)
	out, err := fn.Eval([]float64{5})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if math.Abs(out[0]-1) > 0.01 {
		t.Fatalf("Eval(5) with Domain [0,1] = %v, want ~[1] (clipped)", out)
	}
}

// TestType0TwoDimensionalBilinearInterpolation builds a 2-input, 1-output
// function over a 2x2 grid (Size=[2,2]) with corner values 0, 10, 100,
// 110 (varying by 10 along dimension 0, by 100 along dimension 1 - see
// the layout comment below) and checks the center point averages all
// four corners, exercising Eval's general m-dimensional path (not just
// the 1D special case every other test in this file uses) and confirming
// flatIndex's "dimension 0 varies fastest" byte layout.
func TestType0TwoDimensionalBilinearInterpolation(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType":  syntax.Integer(0),
		"Domain":        numArray(0, 1, 0, 1),
		"Range":         numArray(0, 255),
		"Size":          numArray(2, 2),
		"BitsPerSample": syntax.Integer(8),
	}
	// Flat index = i0 + i1*2 (dimension 0 fastest): (0,0)=0, (1,0)=10,
	// (0,1)=100, (1,1)=110.
	raw := []byte{0, 10, 100, 110}
	stream := syntax.Stream{Dict: dict, Raw: raw}
	fn, err := parseType0(&fakeResolver{}, dict, stream)
	if err != nil {
		t.Fatalf("parseType0: %v", err)
	}
	if fn.NumInputs() != 2 || fn.NumOutputs() != 1 {
		t.Fatalf("NumInputs/NumOutputs = %d/%d, want 2/1", fn.NumInputs(), fn.NumOutputs())
	}

	// Default Encode/Decode: Encode is [0 1 0 1] (Size-1 per dimension),
	// Decode defaults to Range [0 255] - so Domain [0,1]x[0,1] maps
	// directly onto the 2x2 grid index space, and each raw sample value
	// (already in [0,255]) is its own decoded output.
	corners := []struct {
		x, y, want float64
	}{
		{0, 0, 0}, {1, 0, 10}, {0, 1, 100}, {1, 1, 110},
	}
	for _, c := range corners {
		out, err := fn.Eval([]float64{c.x, c.y})
		if err != nil {
			t.Fatalf("Eval(%v,%v): %v", c.x, c.y, err)
		}
		if math.Abs(out[0]-c.want) > 0.01 {
			t.Fatalf("Eval(%v,%v) = %v, want ~[%v]", c.x, c.y, out, c.want)
		}
	}
	out, err := fn.Eval([]float64{0.5, 0.5})
	if err != nil {
		t.Fatalf("Eval(0.5,0.5): %v", err)
	}
	want := (0.0 + 10 + 100 + 110) / 4
	if math.Abs(out[0]-want) > 0.01 {
		t.Fatalf("Eval(0.5,0.5) = %v, want ~[%v] (average of all 4 corners)", out, want)
	}
}

func TestType0RequiresRange(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(0), "Domain": numArray(0, 1),
		"Size": numArray(2), "BitsPerSample": syntax.Integer(8),
	}
	stream := syntax.Stream{Dict: dict, Raw: []byte{0, 255}}
	_, err := parseType0(&fakeResolver{}, dict, stream)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType0 with no /Range: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType0InvalidBitsPerSampleIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(0), "Domain": numArray(0, 1),
		"Range": numArray(0, 1), "Size": numArray(2), "BitsPerSample": syntax.Integer(7),
	}
	stream := syntax.Stream{Dict: dict, Raw: []byte{0, 255}}
	_, err := parseType0(&fakeResolver{}, dict, stream)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType0 with /BitsPerSample 7: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType0TruncatedSampleDataIsMalformed(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(0), "Domain": numArray(0, 1),
		"Range": numArray(0, 1), "Size": numArray(4), "BitsPerSample": syntax.Integer(8),
	}
	// Size=4 needs 4 bytes; only 1 is provided.
	stream := syntax.Stream{Dict: dict, Raw: []byte{0}}
	_, err := parseType0(&fakeResolver{}, dict, stream)
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("parseType0 with truncated sample data: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestType0TooManyInputsIsUnsupported(t *testing.T) {
	domain := make([]float64, 0, 2*(maxType0Inputs+1))
	size := make([]float64, 0, maxType0Inputs+1)
	for i := 0; i < maxType0Inputs+1; i++ {
		domain = append(domain, 0, 1)
		size = append(size, 2)
	}
	dict := syntax.Dictionary{
		"FunctionType": syntax.Integer(0), "Domain": numArray(domain...),
		"Range": numArray(0, 1), "Size": numArray(size...), "BitsPerSample": syntax.Integer(8),
	}
	stream := syntax.Stream{Dict: dict, Raw: []byte{}}
	_, err := parseType0(&fakeResolver{}, dict, stream)
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("parseType0 with %d inputs: got %v, want an error wrapping ErrUnsupported", maxType0Inputs+1, err)
	}
}

// TestType0BitsPerSample1PacksMultipleSamplesPerByte exercises a bit
// width narrower than a byte, confirming sampleAt's bit-level addressing
// (as opposed to byte-level, which the 8-bit tests above cannot
// distinguish from an accidentally-byte-oriented implementation).
func TestType0BitsPerSample1PacksMultipleSamplesPerByte(t *testing.T) {
	dict := syntax.Dictionary{
		"FunctionType":  syntax.Integer(0),
		"Domain":        numArray(0, 1),
		"Range":         numArray(0, 1),
		"Size":          numArray(4),
		"BitsPerSample": syntax.Integer(1),
	}
	// 4 grid points, 1 bit each, MSB-first in a single byte: samples
	// 1,0,1,1 -> bits 1 0 1 1 0 0 0 0 = 0xB0.
	stream := syntax.Stream{Dict: dict, Raw: []byte{0xB0}}
	fn, err := parseType0(&fakeResolver{}, dict, stream)
	if err != nil {
		t.Fatalf("parseType0: %v", err)
	}
	// Encode defaults to [0 3] (Size-1); Domain [0,1] maps input 2/3 -
	// grid index 2 - exactly onto sample index 2 (value 1, decoded to
	// Range [0,1] as 1.0).
	out, err := fn.Eval([]float64{2.0 / 3.0})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if math.Abs(out[0]-1) > 0.01 {
		t.Fatalf("Eval(2/3) = %v, want ~[1]", out)
	}
}
