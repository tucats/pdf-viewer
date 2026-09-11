package jpx

import (
	"math"
	"math/rand"
	"testing"
)

// forwardRCT (used below to build known-good Y/Cb/Cr-like input for
// inverseRCT's round-trip test) now lives in encode.go, promoted there
// by 14g so tools/genfixtures - an ordinary non-test build - can reach
// it too. See that file's own doc comment.

// forwardICT is forwardRCT's floating-point counterpart for the
// irreversible transform (§G.3's own encoder-direction matrix).
func forwardICT(r, g, b []float64) {
	for i := range r {
		ri, gi, bi := r[i], g[i], b[i]
		y := 0.299*ri + 0.587*gi + 0.114*bi
		cb := -0.168736*ri - 0.331264*gi + 0.5*bi
		cr := 0.5*ri - 0.418688*gi - 0.081312*bi
		r[i], g[i], b[i] = y, cb, cr
	}
}

func TestInverseRCTRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const n = 64
	r := make([]float64, n)
	g := make([]float64, n)
	b := make([]float64, n)
	for i := range r {
		r[i] = float64(rng.Intn(511) - 255)
		g[i] = float64(rng.Intn(511) - 255)
		b[i] = float64(rng.Intn(511) - 255)
	}
	wantR, wantG, wantB := append([]float64{}, r...), append([]float64{}, g...), append([]float64{}, b...)

	forwardRCT(r, g, b) // r,g,b now hold Y,Cb,Cr
	inverseRCT(r, g, b) // r,g,b now hold R,G,B again

	for i := range r {
		if r[i] != wantR[i] || g[i] != wantG[i] || b[i] != wantB[i] {
			t.Fatalf("sample %d: got (%v,%v,%v), want (%v,%v,%v)", i, r[i], g[i], b[i], wantR[i], wantG[i], wantB[i])
		}
	}
}

func TestInverseICTRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const n = 64
	r := make([]float64, n)
	g := make([]float64, n)
	b := make([]float64, n)
	for i := range r {
		r[i] = rng.Float64()*510 - 255
		g[i] = rng.Float64()*510 - 255
		b[i] = rng.Float64()*510 - 255
	}
	wantR, wantG, wantB := append([]float64{}, r...), append([]float64{}, g...), append([]float64{}, b...)

	forwardICT(r, g, b)
	inverseICT(r, g, b)

	// forwardICT and inverseICT each use independently-rounded (to 6
	// decimal places, per §G.3) matrix coefficients, so their composition
	// is only an approximate identity, not an exact one - the same
	// "irreversible" precision loss inverseICT's own doc comment
	// describes, just also present in the forward direction this test
	// builds ground truth with. A generous but still meaningful tolerance
	// (three decimal digits, comfortably wider than the ~1e-4 error these
	// roundings actually produce) is what a round trip can prove here.
	const tolerance = 1e-3
	for i := range r {
		if math.Abs(r[i]-wantR[i]) > tolerance ||
			math.Abs(g[i]-wantG[i]) > tolerance ||
			math.Abs(b[i]-wantB[i]) > tolerance {
			t.Fatalf("sample %d: got (%v,%v,%v), want (%v,%v,%v)", i, r[i], g[i], b[i], wantR[i], wantG[i], wantB[i])
		}
	}
}

// mctComponent builds a *reconstructedComponent covering a size x size
// tile at the origin, holding items (a size*size sample array).
func mctComponent(size int, items []float64) *reconstructedComponent {
	return &reconstructedComponent{width: size, height: size, items: items}
}

func TestApplyMultipleComponentTransformNoOp(t *testing.T) {
	h := &Header{DefaultCoding: CodingStyle{MultipleComponentTransform: false}}
	items := []float64{1, 2, 3, 4}
	components := []*reconstructedComponent{mctComponent(2, append([]float64{}, items...))}
	if err := applyMultipleComponentTransform(h, 0, components); err != nil {
		t.Fatalf("applyMultipleComponentTransform: %v", err)
	}
	for i, v := range items {
		if components[0].items[i] != v {
			t.Fatalf("item %d changed to %v despite MCT disabled", i, components[0].items[i])
		}
	}
}

func TestApplyMultipleComponentTransformTooFewComponents(t *testing.T) {
	h := &Header{DefaultCoding: CodingStyle{MultipleComponentTransform: true, Transform: Transform5x3}}
	components := []*reconstructedComponent{mctComponent(1, []float64{0}), mctComponent(1, []float64{0})}
	if err := applyMultipleComponentTransform(h, 0, components); err == nil {
		t.Fatal("expected an error for fewer than 3 components, got nil")
	}
}

func TestApplyMultipleComponentTransformMismatchedDimensions(t *testing.T) {
	h := &Header{DefaultCoding: CodingStyle{MultipleComponentTransform: true, Transform: Transform5x3}}
	components := []*reconstructedComponent{
		mctComponent(2, []float64{0, 0, 0, 0}),
		mctComponent(1, []float64{0}),
		mctComponent(2, []float64{0, 0, 0, 0}),
	}
	if err := applyMultipleComponentTransform(h, 0, components); err == nil {
		t.Fatal("expected an error for mismatched component dimensions, got nil")
	}
}

func TestApplyMultipleComponentTransformRCT(t *testing.T) {
	// R=100, G=50, B=25 at a single sample, forward-transformed by hand:
	// Y = (100+100+25)>>2 = 56, Cb = 25-50 = -25, Cr = 100-50 = 50.
	h := &Header{DefaultCoding: CodingStyle{MultipleComponentTransform: true, Transform: Transform5x3}}
	components := []*reconstructedComponent{
		mctComponent(1, []float64{56}),
		mctComponent(1, []float64{-25}),
		mctComponent(1, []float64{50}),
	}
	if err := applyMultipleComponentTransform(h, 0, components); err != nil {
		t.Fatalf("applyMultipleComponentTransform: %v", err)
	}
	if got := components[0].items[0]; got != 100 {
		t.Errorf("R: got %v, want 100", got)
	}
	if got := components[1].items[0]; got != 50 {
		t.Errorf("G: got %v, want 50", got)
	}
	if got := components[2].items[0]; got != 25 {
		t.Errorf("B: got %v, want 25", got)
	}
}

func TestApplyMultipleComponentTransformUsesTileOverride(t *testing.T) {
	// The codestream-wide default has MCT off; tile 0's own tile-part COD
	// override turns it on - effectiveTileDefaultCoding must be what
	// applyMultipleComponentTransform actually consults, not DefaultCoding
	// directly.
	tileCoding := CodingStyle{MultipleComponentTransform: true, Transform: Transform5x3}
	h := &Header{
		DefaultCoding: CodingStyle{MultipleComponentTransform: false},
		TileParts:     []TilePart{{TileIndex: 0, TileCoding: &tileCoding}},
	}
	components := []*reconstructedComponent{
		mctComponent(1, []float64{56}),
		mctComponent(1, []float64{-25}),
		mctComponent(1, []float64{50}),
	}
	if err := applyMultipleComponentTransform(h, 0, components); err != nil {
		t.Fatalf("applyMultipleComponentTransform: %v", err)
	}
	if got := components[0].items[0]; got != 100 {
		t.Errorf("R: got %v, want 100 (tile override should have enabled MCT)", got)
	}
}
