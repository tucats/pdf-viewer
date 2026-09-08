package graphics

import "math"

// StrokeToFill converts path into a new Path representing the filled
// outline a stroke of the given style would paint - PDF's own model for
// "S"/"s" (and the stroke half of "B"/"b" and their "*" variants):
// stroking is defined as if the region swept by a pen of the given width
// moving along the path were filled with the stroke color, which is
// exactly what this function computes explicitly, so internal/raster
// only ever needs one core algorithm (fill a Path under a FillRule) -
// see internal/raster's package doc comment. The returned Path is meant
// to be filled with FillRule NonZero: every piece (segment rectangles,
// caps, joins) is wound consistently, and overlapping pieces (which
// happen constantly - every join deliberately overlaps its two adjacent
// segment rectangles to avoid a gap) still resolve to "inside" under
// nonzero winding, which is not guaranteed under the even-odd rule.
//
// Dash patterns ("d") are not applied here: this project does not yet
// implement PDF's dash-pattern operator (see docs/capability-matrix.md),
// so every stroke is currently painted solid regardless of the graphics
// state's dash array.
//
// Line joins are approximated as round joins at every interior vertex,
// regardless of joinStyle's value - true miter and bevel join geometry
// (computing each join's actual wedge shape, and falling back from miter
// to bevel past miterLimit) is not yet implemented. A round join never
// leaves a visible gap at a joint, which is the property that matters
// most for a "minimal raster backend" (see the README's Phase 2 scope);
// refining join geometry to match joinStyle exactly is left for a later
// pass. joinStyle and miterLimit are accepted now, ahead of that work, so
// this function's signature will not need to change once it lands.
func StrokeToFill(path *Path, width float64, capStyle LineCap, joinStyle LineJoin, miterLimit float64) *Path {
	_ = joinStyle
	_ = miterLimit
	if width <= 0 {
		width = minStrokeWidth
	}
	half := width / 2

	out := &Path{}
	for _, sp := range path.Subpaths {
		strokeSubpath(out, sp, half, capStyle)
	}
	return out
}

// minStrokeWidth is substituted for a zero or negative line width. PDF
// defines a 0-width line as "the thinnest line that can be rendered at
// device resolution" - this project's raster backend has no notion of a
// device-specific thinnest line, so a small fixed width (clearly
// visible, but thin) is used instead of a stroke that vanishes entirely.
const minStrokeWidth = 0.35

// strokeSubpath appends sp's stroke outline (segment rectangles, joins,
// and - for an open subpath - end caps) to out.
func strokeSubpath(out *Path, sp Subpath, half float64, capStyle LineCap) {
	pts := effectivePoints(sp)
	if len(pts) < 2 {
		return
	}

	for i := 0; i+1 < len(pts); i++ {
		addSegmentRect(out, pts[i], pts[i+1], half)
	}

	n := len(pts)
	for i := 1; i < n-1; i++ {
		addCircle(out, pts[i], half)
	}
	if sp.Closed {
		addCircle(out, pts[0], half)
	} else {
		addCap(out, pts[0], pts[1], half, capStyle)
		addCap(out, pts[n-1], pts[n-2], half, capStyle)
	}
}

// effectivePoints returns sp's points with its closing segment (last
// point back to first) made explicit when sp.Closed, so the segment loop
// in strokeSubpath does not need a special case for it.
func effectivePoints(sp Subpath) []Point {
	if !sp.Closed || len(sp.Points) == 0 {
		return sp.Points
	}
	first := sp.Points[0]
	last := sp.Points[len(sp.Points)-1]
	if first == last {
		return sp.Points
	}
	return append(append([]Point{}, sp.Points...), first)
}

// addSegmentRect appends the rectangle a straight pen of width 2*half
// sweeps out moving from p0 to p1.
func addSegmentRect(out *Path, p0, p1 Point, half float64) {
	dx, dy := p1.X-p0.X, p1.Y-p0.Y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return
	}
	nx, ny := -dy/length*half, dx/length*half
	out.AppendRect([4]Point{
		{p0.X + nx, p0.Y + ny},
		{p1.X + nx, p1.Y + ny},
		{p1.X - nx, p1.Y - ny},
		{p0.X - nx, p0.Y - ny},
	})
}

// circleSegments is the polygon side count used to approximate a round
// join or round cap; see StrokeToFill's doc comment on why round joins
// are used unconditionally for now.
const circleSegments = 12

func addCircle(out *Path, center Point, radius float64) {
	pts := make([]Point, circleSegments)
	for i := range pts {
		theta := 2 * math.Pi * float64(i) / float64(circleSegments)
		pts[i] = Point{center.X + radius*math.Cos(theta), center.Y + radius*math.Sin(theta)}
	}
	out.Subpaths = append(out.Subpaths, Subpath{Points: pts, Closed: true})
}

// addCap extends the stroke outline at an open subpath's endpoint (end),
// where dirFrom is the adjacent path point the final segment approaches
// end from - used only to determine the stroke's direction at that
// endpoint.
func addCap(out *Path, end, dirFrom Point, half float64, capStyle LineCap) {
	dx, dy := end.X-dirFrom.X, end.Y-dirFrom.Y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return
	}
	ux, uy := dx/length, dy/length // unit vector pointing outward, away from the rest of the path
	nx, ny := -uy*half, ux*half    // perpendicular to it, magnitude half

	switch capStyle {
	case RoundCap:
		addCircle(out, end, half)
	case SquareCap:
		ex, ey := end.X+ux*half, end.Y+uy*half
		out.AppendRect([4]Point{
			{end.X + nx, end.Y + ny},
			{ex + nx, ey + ny},
			{ex - nx, ey - ny},
			{end.X - nx, end.Y - ny},
		})
	case ButtCap:
		// No extension: the segment rectangle's own flat end is the cap.
	}
}
