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

// identityColorAt2 is FunctionBasedShading's two-input counterpart to
// identityColorAt above: it reveals exactly which (x, y) domain
// coordinates At computed, via R=x and G=y, so a test can check the
// Matrix-inversion and domain-clipping logic without a real PDF function.
func identityColorAt2(x, y float64) Color {
	return Color{R: x, G: y}
}

func TestShadingFunctionBasedInsideDomain(t *testing.T) {
	sh := &Shading{
		Kind:            FunctionBasedShading,
		Domain2:         [4]float64{0, 1, 0, 1},
		Matrix:          Identity(),
		ShadingToDevice: Identity(),
		ColorAt2:        identityColorAt2,
	}
	col, ok := sh.At(0.25, 0.75)
	if !ok || col.R != 0.25 || col.G != 0.75 {
		t.Fatalf("At(0.25,0.75) = (%v,%v,ok=%v), want (0.25,0.75,true)", col.R, col.G, ok)
	}
}

func TestShadingFunctionBasedOutsideDomainIsUncovered(t *testing.T) {
	sh := &Shading{
		Kind:            FunctionBasedShading,
		Domain2:         [4]float64{0, 1, 0, 1},
		Matrix:          Identity(),
		ShadingToDevice: Identity(),
		ColorAt2:        identityColorAt2,
	}
	cases := [][2]float64{{-0.1, 0.5}, {1.1, 0.5}, {0.5, -0.1}, {0.5, 1.1}}
	for _, c := range cases {
		if _, ok := sh.At(c[0], c[1]); ok {
			t.Fatalf("At(%v,%v) outside Domain2: covered, want uncovered", c[0], c[1])
		}
	}
}

// TestShadingFunctionBasedAppliesMatrix confirms At inverts the
// shading's own Matrix (domain-to-shading-space) before checking Domain2
// and calling ColorAt2 - using a translated+scaled Matrix so a naive
// implementation that forgot to invert (or applied it in the wrong
// direction) would compute a visibly wrong domain coordinate.
func TestShadingFunctionBasedAppliesMatrix(t *testing.T) {
	sh := &Shading{
		Kind:    FunctionBasedShading,
		Domain2: [4]float64{0, 1, 0, 1},
		// Domain space (0,0)-(1,1) maps to shading space (10,10)-(20,20).
		Matrix:          Matrix{A: 10, D: 10, E: 10, F: 10},
		ShadingToDevice: Identity(),
		ColorAt2:        identityColorAt2,
	}
	col, ok := sh.At(15, 10) // shading-space (15,10) -> domain-space (0.5, 0)
	if !ok || col.R != 0.5 || col.G != 0 {
		t.Fatalf("At(15,10) = (%v,%v,ok=%v), want (0.5,0,true)", col.R, col.G, ok)
	}
}

func TestShadingFunctionBasedNilColorAt2IsUncovered(t *testing.T) {
	sh := &Shading{Kind: FunctionBasedShading, Domain2: [4]float64{0, 1, 0, 1}, Matrix: Identity(), ShadingToDevice: Identity()}
	if _, ok := sh.At(0.5, 0.5); ok {
		t.Fatalf("At with a nil ColorAt2: covered, want uncovered (safe default)")
	}
}

// A right triangle with red, green and blue corners, used by every mesh
// test below to check barycentric interpolation and containment.
func rgbTriangle() MeshTriangle {
	return MeshTriangle{
		X0: 0, Y0: 0, C0: Color{R: 1},
		X1: 10, Y1: 0, C1: Color{G: 1},
		X2: 0, Y2: 10, C2: Color{B: 1},
	}
}

func TestMeshTriangleCornersReturnTheirOwnColor(t *testing.T) {
	tri := rgbTriangle()
	cases := []struct {
		x, y float64
		want Color
	}{
		{0, 0, Color{R: 1}},
		{10, 0, Color{G: 1}},
		{0, 10, Color{B: 1}},
	}
	for _, c := range cases {
		col, ok := tri.colorAt(c.x, c.y)
		if !ok || col != c.want {
			t.Fatalf("colorAt(%v,%v) = (%v,ok=%v), want (%v,true)", c.x, c.y, col, ok, c.want)
		}
	}
}

func TestMeshTriangleCentroidIsAverageOfCorners(t *testing.T) {
	tri := rgbTriangle()
	col, ok := tri.colorAt(10.0/3, 10.0/3)
	if !ok {
		t.Fatalf("colorAt(centroid): not covered, want covered")
	}
	want := 1.0 / 3
	if diff := col.R - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("colorAt(centroid).R = %v, want %v", col.R, want)
	}
	if diff := col.G - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("colorAt(centroid).G = %v, want %v", col.G, want)
	}
	if diff := col.B - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("colorAt(centroid).B = %v, want %v", col.B, want)
	}
}

func TestMeshTriangleOutsideIsUncovered(t *testing.T) {
	tri := rgbTriangle()
	if _, ok := tri.colorAt(10, 10); ok {
		t.Fatalf("colorAt(10,10) outside the triangle: covered, want uncovered")
	}
}

func TestMeshTriangleDegenerateIsUncovered(t *testing.T) {
	tri := MeshTriangle{X0: 0, Y0: 0, X1: 5, Y1: 5, X2: 10, Y2: 10} // collinear: zero area
	if _, ok := tri.colorAt(5, 5); ok {
		t.Fatalf("colorAt on a degenerate (zero-area) triangle: covered, want uncovered")
	}
}

func TestShadingMeshKindFindsCoveringTriangle(t *testing.T) {
	sh := &Shading{
		Kind: FreeFormTriangleMesh,
		Triangles: []MeshTriangle{
			rgbTriangle(),
			{X0: 100, Y0: 100, X1: 110, Y1: 100, X2: 100, Y2: 110, C0: Color{R: 1, G: 1, B: 1}},
		},
		ShadingToDevice: Identity(),
	}
	col, ok := sh.At(0, 0)
	if !ok || col != (Color{R: 1}) {
		t.Fatalf("At(0,0) = (%v,ok=%v), want (red,true) from the first triangle", col, ok)
	}
	if _, ok := sh.At(50, 50); ok {
		t.Fatalf("At(50,50), covered by neither triangle: covered, want uncovered")
	}
}

func TestShadingMeshKindAppliesShadingToDeviceMatrix(t *testing.T) {
	sh := &Shading{
		Kind:            LatticeFormTriangleMesh,
		Triangles:       []MeshTriangle{rgbTriangle()},
		ShadingToDevice: Matrix{A: 2, D: 2, E: 100, F: 100}, // shading space *2, then +(100,100)
	}
	// Device point (100,100) -> shading space (0,0), the triangle's own
	// red corner.
	col, ok := sh.At(100, 100)
	if !ok || col != (Color{R: 1}) {
		t.Fatalf("At(100,100) = (%v,ok=%v), want (red,true)", col, ok)
	}
}
