package graphics

import "testing"

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
// there is no "start" or "end" to cap, only interior joins (here,
// approximated as round joins at every vertex, including the closing
// one - see StrokeToFill's doc comment).
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
	// 3 segment rectangles + 3 round joins (one per vertex, since the
	// subpath is closed).
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
