package graphics

import "testing"

const epsilon = 1e-9

func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < epsilon
}

func assertPointClose(t *testing.T, got Point, wantX, wantY float64) {
	t.Helper()
	if !approxEqual(got.X, wantX) || !approxEqual(got.Y, wantY) {
		t.Errorf("point = (%v, %v), want (%v, %v)", got.X, got.Y, wantX, wantY)
	}
}

func TestIdentityLeavesPointsUnchanged(t *testing.T) {
	x, y := Identity().Apply(3, 4)
	if !approxEqual(x, 3) || !approxEqual(y, 4) {
		t.Errorf("Identity().Apply(3,4) = (%v,%v), want (3,4)", x, y)
	}
}

func TestTranslate(t *testing.T) {
	x, y := Translate(10, -5).Apply(1, 1)
	if !approxEqual(x, 11) || !approxEqual(y, -4) {
		t.Errorf("Translate(10,-5).Apply(1,1) = (%v,%v), want (11,-4)", x, y)
	}
}

func TestScale(t *testing.T) {
	x, y := Scale(2, 3).Apply(5, 5)
	if !approxEqual(x, 10) || !approxEqual(y, 15) {
		t.Errorf("Scale(2,3).Apply(5,5) = (%v,%v), want (10,15)", x, y)
	}
}

// TestMulAppliesFirstMatrixFirst confirms the concatenation order
// matches PDF's "cm" semantics (see Matrix.Mul's doc comment): a point
// transformed by m.Mul(n) must equal that point transformed by m and
// then by n, applied as two separate steps.
func TestMulAppliesFirstMatrixFirst(t *testing.T) {
	m := Translate(10, 0)
	n := Scale(2, 2)

	combined := m.Mul(n)
	gotX, gotY := combined.Apply(1, 1)

	stepX, stepY := m.Apply(1, 1)
	stepX, stepY = n.Apply(stepX, stepY)

	if !approxEqual(gotX, stepX) || !approxEqual(gotY, stepY) {
		t.Errorf("m.Mul(n).Apply(1,1) = (%v,%v), want (%v,%v) (apply m then n separately)", gotX, gotY, stepX, stepY)
	}
	// Concretely: (1,1) translated by (10,0) -> (11,1), then scaled 2x ->
	// (22,2).
	assertPointClose(t, Point{gotX, gotY}, 22, 2)
}

func TestApplyVectorIgnoresTranslation(t *testing.T) {
	m := Translate(100, 100).Mul(Scale(2, 3))
	dx, dy := m.ApplyVector(1, 1)
	if !approxEqual(dx, 2) || !approxEqual(dy, 3) {
		t.Errorf("ApplyVector(1,1) = (%v,%v), want (2,3) (translation must not affect a vector)", dx, dy)
	}
}

func TestMulWithIdentityIsNoOp(t *testing.T) {
	m := Translate(3, 4).Mul(Scale(2, 5))
	left := m.Mul(Identity())
	right := Identity().Mul(m)
	if left != m {
		t.Errorf("m.Mul(Identity()) = %#v, want %#v", left, m)
	}
	if right != m {
		t.Errorf("Identity().Mul(m) = %#v, want %#v", right, m)
	}
}

// TestInvertRoundTrips confirms that applying m and then m's inverse to
// a handful of points returns each point unchanged - the defining
// property of an inverse, and the property internal/raster's image
// sampling (which maps a device pixel back into image space via
// Invert()) actually depends on.
func TestInvertRoundTrips(t *testing.T) {
	m := Translate(10, -20).Mul(Scale(3, 4)).Mul(Rotate90ForTest())
	inv, ok := m.Invert()
	if !ok {
		t.Fatal("Invert() reported not invertible for a clearly invertible matrix")
	}
	for _, p := range []Point{{X: 0, Y: 0}, {X: 1, Y: 1}, {X: -5, Y: 12.5}, {X: 100, Y: -100}} {
		fx, fy := m.Apply(p.X, p.Y)
		bx, by := inv.Apply(fx, fy)
		if !approxEqual(bx, p.X) || !approxEqual(by, p.Y) {
			t.Errorf("Invert() round trip for %v: forward+inverse = (%v,%v), want (%v,%v)", p, bx, by, p.X, p.Y)
		}
	}
}

// Rotate90ForTest returns a matrix that rotates 90 degrees about the
// origin, used only to give TestInvertRoundTrips a non-axis-aligned
// matrix to test against (Invert's 2x2 linear-part inversion is trivial
// to get right for a pure scale/translate but easy to get wrong once
// off-diagonal terms are involved).
func Rotate90ForTest() Matrix {
	return Matrix{A: 0, B: 1, C: -1, D: 0}
}

func TestInvertDegenerateMatrixReportsNotOK(t *testing.T) {
	// A matrix that collapses the entire plane onto a single point (both
	// rows of its linear part are zero) has no inverse.
	degenerate := Matrix{A: 0, B: 0, C: 0, D: 0, E: 5, F: 5}
	if _, ok := degenerate.Invert(); ok {
		t.Fatal("Invert() on a degenerate matrix: want ok=false, got ok=true")
	}
}

func TestInvertIdentityIsIdentity(t *testing.T) {
	inv, ok := Identity().Invert()
	if !ok {
		t.Fatal("Invert() on Identity: want ok=true")
	}
	if inv != Identity() {
		t.Errorf("Identity().Invert() = %#v, want Identity()", inv)
	}
}
