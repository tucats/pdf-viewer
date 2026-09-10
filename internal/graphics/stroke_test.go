package graphics

import (
	"math"
	"testing"
)

// TestStrokeToFillSingleSegmentBounds checks the outline produced for a
// simple horizontal line has the expected bounding box: the line runs
// from (0,0) to (10,0) with a butt cap and width 4, so the outline
// (ignoring the round-join circles, which don't apply here - there is
// only one segment and no interior vertex) should span exactly
// x in [0,10], y in [-2,2].
func TestStrokeToFillSingleSegmentBounds(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})

	outline := StrokeToFill(&p, 4, ButtCap, MiterJoin, 10)
	minX, minY, maxX, maxY, ok := outline.Bounds()
	if !ok {
		t.Fatal("outline is empty")
	}
	if !approxEqual(minX, 0) || !approxEqual(maxX, 10) {
		t.Errorf("outline x range = [%v,%v], want [0,10]", minX, maxX)
	}
	if !approxEqual(minY, -2) || !approxEqual(maxY, 2) {
		t.Errorf("outline y range = [%v,%v], want [-2,2]", minY, maxY)
	}
}

// TestStrokeToFillSquareCapExtendsBounds confirms a square cap extends
// the outline half the line width beyond each open endpoint, unlike a
// butt cap.
func TestStrokeToFillSquareCapExtendsBounds(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})

	outline := StrokeToFill(&p, 4, SquareCap, MiterJoin, 10)
	minX, _, maxX, _, ok := outline.Bounds()
	if !ok {
		t.Fatal("outline is empty")
	}
	if !approxEqual(minX, -2) || !approxEqual(maxX, 12) {
		t.Errorf("square-cap outline x range = [%v,%v], want [-2,12]", minX, maxX)
	}
}

// TestStrokeToFillRoundCapExtendsBounds is RoundCap's counterpart to
// TestStrokeToFillSquareCapExtendsBounds: a round cap's bounding box
// extends by the same half-width (the circle's radius), even though its
// shape differs from a square cap's.
func TestStrokeToFillRoundCapExtendsBounds(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})

	outline := StrokeToFill(&p, 4, RoundCap, MiterJoin, 10)
	minX, _, maxX, _, ok := outline.Bounds()
	if !ok {
		t.Fatal("outline is empty")
	}
	if !approxEqual(minX, -2) || !approxEqual(maxX, 12) {
		t.Errorf("round-cap outline x range = [%v,%v], want [-2,12]", minX, maxX)
	}
}

// TestStrokeToFillClosedSubpathHasNoOpenEnds confirms a closed subpath
// (a triangle) produces an outline without relying on end caps at all -
// there is no "start" or "end" to cap, only interior joins (here, miter
// joins at every vertex, including the closing one - see addJoin).
func TestStrokeToFillClosedSubpathHasNoOpenEnds(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})
	p.LineTo(Point{5, 10})
	p.Close()

	outline := StrokeToFill(&p, 2, ButtCap, MiterJoin, 10)
	if len(outline.Subpaths) == 0 {
		t.Fatal("outline has no subpaths")
	}
	// 3 segment rectangles + 3 joins (one per vertex, since the subpath is
	// closed) - each join is one subpath regardless of its style.
	if got, want := len(outline.Subpaths), 6; got != want {
		t.Errorf("len(outline.Subpaths) = %d, want %d", got, want)
	}
}

func TestStrokeToFillZeroWidthUsesMinimum(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})

	outline := StrokeToFill(&p, 0, ButtCap, MiterJoin, 10)
	_, minY, _, maxY, ok := outline.Bounds()
	if !ok {
		t.Fatal("outline is empty")
	}
	if maxY-minY <= 0 {
		t.Errorf("zero-width stroke produced a degenerate (zero-height) outline")
	}
}

func TestStrokeToFillDegenerateSegmentIsSkipped(t *testing.T) {
	var p Path
	p.MoveTo(Point{5, 5})
	p.LineTo(Point{5, 5}) // zero-length segment

	outline := StrokeToFill(&p, 4, ButtCap, MiterJoin, 10)
	if len(outline.Subpaths) != 0 {
		t.Errorf("degenerate segment produced %d subpaths, want 0", len(outline.Subpaths))
	}
}

// a120CornerPath builds a two-segment open path (ButtCap, so no cap
// geometry is appended) that turns 120 degrees at its one interior
// vertex (10,0): the incoming segment travels along +X, the outgoing
// segment leaves at a 120-degree angle from it. This angle is chosen
// because it makes the miter ratio (see miterTip's doc comment) come out
// to exactly 2 half-widths, a clean number to check against.
func a120CornerPath() *Path {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})
	p.LineTo(Point{10 - 5, 5 * math.Sqrt(3)}) // 10*(cos(120deg), sin(120deg)) added to (10,0)
	return &p
}

// jointSubpath returns the single Subpath addJoin appended for
// a120CornerPath's one interior vertex - the third Subpath StrokeToFill
// produces for it, after the two segment rectangles.
func jointSubpath(t *testing.T, outline *Path) Subpath {
	t.Helper()
	if len(outline.Subpaths) != 3 {
		t.Fatalf("outline has %d subpaths, want 3 (2 segment rectangles + 1 join)", len(outline.Subpaths))
	}
	return outline.Subpaths[2]
}

// assertPointsClose fails t unless got and want contain the same points,
// in the same order, within a small tolerance (irrational coordinates
// like sqrt(3) cannot be compared for exact equality).
func assertPointsClose(t *testing.T, got []Point, want []Point) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d points, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	const tol = 1e-9
	for i := range want {
		if math.Abs(got[i].X-want[i].X) > tol || math.Abs(got[i].Y-want[i].Y) > tol {
			t.Errorf("point %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestStrokeToFillMiterJoinReachesComputedTip confirms a MiterJoin, when
// within miterLimit, produces the exact quadrilateral (vertex, the two
// segments' outer corners, and the miter tip) predicted by miterTip's own
// formula - not just "some shape wider than a bevel join".
func TestStrokeToFillMiterJoinReachesComputedTip(t *testing.T) {
	outline := StrokeToFill(a120CornerPath(), 2, ButtCap, MiterJoin, 10) // half-width 1, miterLimit 10 (ratio 2 fits easily)
	join := jointSubpath(t, outline)

	sqrt3 := math.Sqrt(3)
	vertex := Point{10, 0}
	p0 := Point{10, -1}            // vertex's outer corner on the incoming segment
	p1 := Point{10 + sqrt3/2, 0.5} // vertex's outer corner on the outgoing segment
	tip := Point{10 + sqrt3, -1}   // the miter point itself, 2 half-widths from vertex

	if !join.Closed {
		t.Error("miter join subpath is not closed")
	}
	assertPointsClose(t, join.Points, []Point{vertex, p0, tip, p1})
}

// TestStrokeToFillMiterPastLimitFallsBackToBevel confirms the same
// 120-degree corner (whose miter ratio is exactly 2, see
// a120CornerPath) produces a plain bevel triangle - no tip point -  once
// miterLimit is set below that ratio, per the specification's fallback
// rule.
func TestStrokeToFillMiterPastLimitFallsBackToBevel(t *testing.T) {
	outline := StrokeToFill(a120CornerPath(), 2, ButtCap, MiterJoin, 1) // ratio 2 > miterLimit 1
	join := jointSubpath(t, outline)

	sqrt3 := math.Sqrt(3)
	vertex := Point{10, 0}
	p0 := Point{10, -1}
	p1 := Point{10 + sqrt3/2, 0.5}

	assertPointsClose(t, join.Points, []Point{vertex, p0, p1})
}

// TestStrokeToFillBevelJoinNeverReachesTip confirms BevelJoin always
// produces the plain triangle, even when a miter join at the same corner
// would easily have fit within miterLimit - BevelJoin is a distinct,
// explicit choice, not "miter capped very aggressively".
func TestStrokeToFillBevelJoinNeverReachesTip(t *testing.T) {
	outline := StrokeToFill(a120CornerPath(), 2, ButtCap, BevelJoin, 100) // miterLimit generously large
	join := jointSubpath(t, outline)

	sqrt3 := math.Sqrt(3)
	vertex := Point{10, 0}
	p0 := Point{10, -1}
	p1 := Point{10 + sqrt3/2, 0.5}

	assertPointsClose(t, join.Points, []Point{vertex, p0, p1})
}

// TestStrokeToFillCollinearSegmentsAddNoJoin confirms two segments that
// continue in the same direction (no actual turn - the common case after
// flattening a smooth curve into many small straight segments) produce
// no join geometry at all: addSegmentRect's two rectangles already meet
// flush, with no gap for a join to fill.
func TestStrokeToFillCollinearSegmentsAddNoJoin(t *testing.T) {
	var p Path
	p.MoveTo(Point{0, 0})
	p.LineTo(Point{10, 0})
	p.LineTo(Point{20, 0}) // continues straight along +X

	outline := StrokeToFill(&p, 2, ButtCap, MiterJoin, 10)
	// Only the two segment rectangles - no third subpath for the
	// (nonexistent) join.
	if got, want := len(outline.Subpaths), 2; got != want {
		t.Errorf("len(outline.Subpaths) = %d, want %d (collinear join should add nothing)", got, want)
	}
}
