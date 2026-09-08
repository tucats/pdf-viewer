package graphics

import "testing"

// identityColorAt is a ColorAt function whose output directly reveals
// which parametric t value it was called with (R=t), so a test can
// assert on At's t computation without needing a real PDF function or
// color space.
func identityColorAt(t float64) Color {
	return Color{R: t}
}

func TestShadingAxialInterpolatesAlongLine(t *testing.T) {
	sh := &Shading{
		Kind:            AxialShading,
		Coords:          [6]float64{0, 0, 100, 0},
		Domain:          [2]float64{0, 1},
		ShadingToDevice: Identity(),
		ColorAt:         identityColorAt,
	}
	cases := []struct {
		x, wantT float64
	}{
		{0, 0}, {100, 1}, {50, 0.5}, {25, 0.25},
	}
	for _, c := range cases {
		col, ok := sh.At(c.x, 0)
		if !ok {
			t.Fatalf("At(%v,0): not covered, want covered", c.x)
		}
		if diff := col.R - c.wantT; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("At(%v,0) = t %v, want %v", c.x, col.R, c.wantT)
		}
	}
}

func TestShadingAxialPerpendicularOffsetDoesNotChangeT(t *testing.T) {
	// A point off the axis still projects onto the same t (the
	// perpendicular distance is irrelevant to an axial shading's color -
	// only the projection along the line matters).
	sh := &Shading{
		Kind: AxialShading, Coords: [6]float64{0, 0, 100, 0},
		Domain: [2]float64{0, 1}, ShadingToDevice: Identity(), ColorAt: identityColorAt,
	}
	col, ok := sh.At(50, 1000)
	if !ok || col.R != 0.5 {
		t.Fatalf("At(50,1000) = (%v,%v), want (0.5,true)", col.R, ok)
	}
}

func TestShadingAxialWithoutExtendIsUncoveredOutsideSegment(t *testing.T) {
	sh := &Shading{
		Kind: AxialShading, Coords: [6]float64{0, 0, 100, 0},
		Domain: [2]float64{0, 1}, ShadingToDevice: Identity(), ColorAt: identityColorAt,
	}
	if _, ok := sh.At(-10, 0); ok {
		t.Fatalf("At(-10,0) with Extend[0]=false: covered, want uncovered")
	}
	if _, ok := sh.At(110, 0); ok {
		t.Fatalf("At(110,0) with Extend[1]=false: covered, want uncovered")
	}
}

func TestShadingAxialWithExtendUsesEdgeColorBeyondSegment(t *testing.T) {
	sh := &Shading{
		Kind: AxialShading, Coords: [6]float64{0, 0, 100, 0},
		Domain: [2]float64{0, 1}, Extend: [2]bool{true, true},
		ShadingToDevice: Identity(), ColorAt: identityColorAt,
	}
	col, ok := sh.At(-50, 0)
	if !ok || col.R != 0 {
		t.Fatalf("At(-50,0) extended before t0 = (%v,%v), want (0,true) (edge color, not extrapolated)", col.R, ok)
	}
	col, ok = sh.At(500, 0)
	if !ok || col.R != 1 {
		t.Fatalf("At(500,0) extended past t1 = (%v,%v), want (1,true) (edge color, not extrapolated)", col.R, ok)
	}
}

func TestShadingAxialDegenerateIsUncovered(t *testing.T) {
	sh := &Shading{
		Kind: AxialShading, Coords: [6]float64{5, 5, 5, 5},
		ShadingToDevice: Identity(), ColorAt: identityColorAt,
	}
	if _, ok := sh.At(5, 5); ok {
		t.Fatalf("At on a degenerate (zero-length) axial shading: covered, want uncovered")
	}
}

// TestShadingRadialConcentricCircles exercises the common "simple
// radial" case: two circles sharing a center, growing from r0=0 to
// r1=100 - t should equal distance-from-center/100 inside the outer
// circle.
func TestShadingRadialConcentricCircles(t *testing.T) {
	sh := &Shading{
		Kind: RadialShading, Coords: [6]float64{50, 50, 0, 50, 50, 100},
		Domain: [2]float64{0, 1}, ShadingToDevice: Identity(), ColorAt: identityColorAt,
	}
	cases := []struct {
		x, y, wantT float64
	}{
		{50, 50, 0},  // center: s=0
		{150, 50, 1}, // on the outer circle: s=1
		{100, 50, 0.5},
	}
	for _, c := range cases {
		col, ok := sh.At(c.x, c.y)
		if !ok {
			t.Fatalf("At(%v,%v): not covered, want covered", c.x, c.y)
		}
		if diff := col.R - c.wantT; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("At(%v,%v) = t %v, want %v", c.x, c.y, col.R, c.wantT)
		}
	}
}

func TestShadingRadialOutsideOuterCircleUncoveredWithoutExtend(t *testing.T) {
	sh := &Shading{
		Kind: RadialShading, Coords: [6]float64{50, 50, 0, 50, 50, 100},
		Domain: [2]float64{0, 1}, ShadingToDevice: Identity(), ColorAt: identityColorAt,
	}
	if _, ok := sh.At(500, 50); ok {
		t.Fatalf("At far outside the outer circle, no Extend: covered, want uncovered")
	}
	// Inside the inner circle (r0=0 means the "inner circle" is just the
	// center point, so anywhere strictly inside the outer circle other
	// than requiring s>=0 is fine) is already covered by the concentric
	// test above; this test only checks the outer boundary.
}

func TestShadingRadialExtendPastOuterCircleUsesEdgeColor(t *testing.T) {
	sh := &Shading{
		Kind: RadialShading, Coords: [6]float64{50, 50, 0, 50, 50, 100},
		Domain: [2]float64{0, 1}, Extend: [2]bool{false, true},
		ShadingToDevice: Identity(), ColorAt: identityColorAt,
	}
	col, ok := sh.At(500, 50)
	if !ok || col.R != 1 {
		t.Fatalf("At(500,50) extended past the outer circle = (%v,%v), want (1,true)", col.R, ok)
	}
}

// TestShadingAppliesShadingToDeviceMatrix confirms At inverts
// ShadingToDevice before evaluating shading-space geometry - using a
// translated+scaled matrix so a naive implementation that forgot to
// invert (or inverted the wrong direction) would compute a visibly wrong
// t.
func TestShadingAppliesShadingToDeviceMatrix(t *testing.T) {
	sh := &Shading{
		Kind:   AxialShading,
		Coords: [6]float64{0, 0, 1, 0}, // shading space: a short unit-length segment
		Domain: [2]float64{0, 1},
		// Shading space (0,0)-(1,0) maps to device space (200,200)-(300,200):
		// scale by 100, translate by (200,200).
		ShadingToDevice: Matrix{A: 100, D: 100, E: 200, F: 200},
		ColorAt:         identityColorAt,
	}
	col, ok := sh.At(250, 200) // device midpoint -> shading-space (0.5, 0) -> t=0.5
	if !ok || col.R != 0.5 {
		t.Fatalf("At(250,200) = (%v,%v), want (0.5,true)", col.R, ok)
	}
}

func TestShadingNilColorAtIsUncovered(t *testing.T) {
	sh := &Shading{Kind: AxialShading, Coords: [6]float64{0, 0, 1, 0}, ShadingToDevice: Identity()}
	if _, ok := sh.At(0.5, 0); ok {
		t.Fatalf("At with a nil ColorAt: covered, want uncovered (safe default)")
	}
}
