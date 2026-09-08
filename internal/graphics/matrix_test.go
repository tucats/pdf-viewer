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
