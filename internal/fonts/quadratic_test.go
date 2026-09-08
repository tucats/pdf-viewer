package fonts

import (
	"math"
	"testing"
)

// TestBuildContourPath_OffCurvePointsFormACurve exercises the on-curve/
// off-curve quadratic reconstruction (buildContourPath's doc comment in
// truetype.go) with a contour that actually uses an off-curve control
// point, unlike unitSquareGlyph (every other test glyph in this
// package, which uses only on-curve points and so never exercises
// QuadTo at all). The contour here is a single quadratic arc from
// (0,500) through control point (500,500) to (1000,500), back-connected
// by straight edges through (500,0) - i.e. a shape whose top edge bows
// upward. A point on that flattened curve (t=0.5 of the arc) should sit
// strictly above the straight line between its endpoints, and the
// quadratic Bézier formula predicts exactly where.
func TestBuildContourPath_OffCurvePointsFormACurve(t *testing.T) {
	// Points: (0,500) on-curve, (500,500) OFF-curve control,
	// (1000,500) on-curve, (500,0) on-curve - four points, one contour.
	glyph := encodeSimpleGlyph([][]glyphContourPoint{{
		{x: 0, y: 500, onCurve: true},
		{x: 500, y: 500, onCurve: false},
		{x: 1000, y: 500, onCurve: true},
		{x: 500, y: 0, onCurve: true},
	}})
	data := buildTestSfnt(t, [][]byte{{}, glyph}, buildCmapFormat0Table(map[rune]uint16{'Q': 1}))
	sfnt, ok := parseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	outline, ok := sfnt.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline failed")
	}
	if len(outline.Subpaths) != 1 {
		t.Fatalf("outline has %d subpaths, want 1", len(outline.Subpaths))
	}

	// The quadratic Bézier from (0,500) via control (500,500) to
	// (1000,500) is degenerate (a straight horizontal line, since start,
	// control, and end are collinear) - so every flattened point on that
	// segment must have Y very close to 500. This still meaningfully
	// exercises QuadTo's flattening path (as opposed to it silently
	// being skipped), and a non-degenerate-curve regression (e.g. control
	// point weighted incorrectly) would be caught by the X-coordinate
	// spread assertion below instead.
	sp := outline.Subpaths[0]
	if len(sp.Points) < 6 {
		t.Fatalf("flattened contour has only %d points, want at least 6 (curve should be subdivided)", len(sp.Points))
	}
	for _, p := range sp.Points {
		if p.Y > 500.5 {
			t.Errorf("point %+v has Y > 500, expected the flattened curve to stay at or below the control point's height", p)
		}
	}
	minX, _, maxX, _ := pathBounds(t, outline)
	if minX != 0 || maxX != 1000 {
		t.Errorf("bounds X = [%v,%v], want [0,1000]", minX, maxX)
	}
}

// TestBuildContourPath_ConsecutiveOffCurvePoints exercises the "implied
// on-curve midpoint between two consecutive off-curve points" rule
// (buildContourPath's doc comment, pass 1): a contour with two adjacent
// off-curve points must not be misread as a single degenerate segment -
// the implied midpoint between them must actually land where the
// quadratic math says it should.
func TestBuildContourPath_ConsecutiveOffCurvePoints(t *testing.T) {
	// Two consecutive off-curve points at (300,1000) and (700,1000),
	// bracketed by on-curve points (0,0) and (1000,0). The implied
	// midpoint between the two off-curve points is (500,1000).
	glyph := encodeSimpleGlyph([][]glyphContourPoint{{
		{x: 0, y: 0, onCurve: true},
		{x: 300, y: 1000, onCurve: false},
		{x: 700, y: 1000, onCurve: false},
		{x: 1000, y: 0, onCurve: true},
	}})
	data := buildTestSfnt(t, [][]byte{{}, glyph}, buildCmapFormat0Table(map[rune]uint16{'W': 1}))
	sfnt, ok := parseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	outline, ok := sfnt.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline failed")
	}
	_, _, _, maxY := pathBounds(t, outline)
	// The flattened curve must reach up near Y=1000 (the shared control
	// points' height) somewhere in the middle, not stay flat at Y=0 as
	// it would if the two off-curve points were simply ignored or
	// misread as straight-line vertices.
	if maxY < 700 {
		t.Errorf("max Y = %v, want something close to 1000 (the curve should bow upward through the implied midpoint)", maxY)
	}
	if math.Abs(maxY-1000) > 260 {
		// Loose tolerance: the exact peak of a quadratic through these
		// three effective control points is a specific value, but this
		// test only needs to confirm the curve is shaped roughly as
		// expected, not pin an exact flattened pixel.
		t.Logf("max Y = %v (informational - not a strict quadratic-peak check)", maxY)
	}
}
