package graphics

import "testing"

func TestPathMoveLineClose(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})
	p.LineTo(Point{10, 10})
	p.Close()

	if len(p.Subpaths) != 1 {
		t.Fatalf("len(Subpaths) = %d, want 1", len(p.Subpaths))
	}
	sp := p.Subpaths[0]
	if !sp.Closed {
		t.Error("subpath not marked Closed after Close()")
	}
	want := []Point{{0, 0}, {10, 0}, {10, 10}}
	if len(sp.Points) != len(want) {
		t.Fatalf("len(Points) = %d, want %d", len(sp.Points), len(want))
	}
	for i, p := range want {
		if sp.Points[i] != p {
			t.Errorf("Points[%d] = %v, want %v", i, sp.Points[i], p)
		}
	}

	cur, ok := p.Current()
	if !ok || cur != (Point{0, 0}) {
		t.Errorf("Current() = (%v, %v), want ((0,0), true) - Close should reset the current point to the subpath start", cur, ok)
	}
}

// TestLineToWithoutMoveToStartsASubpath confirms the documented
// tolerance for content missing a leading "m": LineTo before any
// MoveTo behaves like MoveTo instead of panicking on an empty
// Subpaths slice.
func TestLineToWithoutMoveToStartsASubpath(t *testing.T) {
	var p Path
	p.LineTo(Point{5, 5})
	if len(p.Subpaths) != 1 || len(p.Subpaths[0].Points) != 1 {
		t.Fatalf("Subpaths = %#v, want one subpath with one point", p.Subpaths)
	}
}

func TestPathMultipleSubpaths(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{1, 0})
	p.MoveTo(Point{5, 5})
	p.LineTo(Point{6, 5})
	if len(p.Subpaths) != 2 {
		t.Fatalf("len(Subpaths) = %d, want 2", len(p.Subpaths))
	}
}

func TestAppendRect(t *testing.T) {
	var p Path
	p.AppendRect([4]Point{{0, 0}, {10, 0}, {10, 20}, {0, 20}})
	if len(p.Subpaths) != 1 {
		t.Fatalf("len(Subpaths) = %d, want 1", len(p.Subpaths))
	}
	sp := p.Subpaths[0]
	if !sp.Closed {
		t.Error("rect subpath should be Closed")
	}
	if len(sp.Points) != 4 {
		t.Fatalf("len(Points) = %d, want 4", len(sp.Points))
	}
}

func TestBounds(t *testing.T) {
	var p Path
	p.MoveTo(Point{-5, 2})
	p.LineTo(Point{10, -3})
	p.LineTo(Point{4, 8})

	minX, minY, maxX, maxY, ok := p.Bounds()
	if !ok {
		t.Fatal("Bounds() ok = false, want true for a non-empty path")
	}
	if minX != -5 || minY != -3 || maxX != 10 || maxY != 8 {
		t.Errorf("Bounds() = (%v,%v,%v,%v), want (-5,-3,10,8)", minX, minY, maxX, maxY)
	}
}

func TestBoundsEmptyPath(t *testing.T) {
	var p Path
	if _, _, _, _, ok := p.Bounds(); ok {
		t.Error("Bounds() ok = true for an empty path, want false")
	}
}

// TestCurveToEndpointsMatch confirms a flattened cubic Bézier starts at
// the current point and ends exactly at its specified end point,
// regardless of how many straight segments bezierSegments flattens it
// into - the property every caller of CurveTo actually depends on
// (internal/content's "c"/"v"/"y" operators all continue drawing from
// wherever the curve ends).
func TestCurveToEndpointsMatch(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.CurveTo(Point{0, 10}, Point{10, 10}, Point{10, 0})

	sp := p.Subpaths[0]
	if got := sp.Points[0]; got != (Point{0, 0}) {
		t.Errorf("first point = %v, want (0,0)", got)
	}
	last := sp.Points[len(sp.Points)-1]
	if !approxEqual(last.X, 10) || !approxEqual(last.Y, 0) {
		t.Errorf("last point = %v, want (10,0)", last)
	}
	// bezierSegments extra points plus the starting point.
	if want := bezierSegments + 1; len(sp.Points) != want {
		t.Errorf("len(Points) = %d, want %d", len(sp.Points), want)
	}

	cur, ok := p.Current()
	if !ok || !approxEqual(cur.X, 10) || !approxEqual(cur.Y, 0) {
		t.Errorf("Current() after CurveTo = (%v,%v), want (10,0)", cur.X, cur.Y)
	}
}

// TestCurveToMidpointOnKnownCurve checks the flattener's math directly
// against the well-known symmetric case of a cubic Bézier from (0,0) to
// (1,0) with control points (1/3,1) and (2/3,1): by symmetry, the curve
// passes through (0.5, 0.75) at t=0.5.
func TestCurveToMidpointOnKnownCurve(t *testing.T) {
	got := cubicBezierPoint(Point{0, 0}, Point{1.0 / 3, 1}, Point{2.0 / 3, 1}, Point{1, 0}, 0.5)
	assertPointClose(t, got, 0.5, 0.75)
}
