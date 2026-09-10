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
// Line joins honor joinStyle: RoundJoin draws a full circle at the
// vertex (as every join unconditionally did before this), MiterJoin
// extends the two segment edges until they meet at a point (falling back
// to BevelJoin automatically once that point would land further than
// miterLimit half-widths from the vertex, per the specification), and
// BevelJoin connects the two edges directly with a straight line. See
// addJoin for the geometry.
func StrokeToFill(path *Path, width float64, capStyle LineCap, joinStyle LineJoin, miterLimit float64) *Path {
	if width <= 0 {
		width = minStrokeWidth
	}
	half := width / 2

	out := &Path{}
	for _, sp := range path.Subpaths {
		strokeSubpath(out, sp, half, capStyle, joinStyle, miterLimit)
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
func strokeSubpath(out *Path, sp Subpath, half float64, capStyle LineCap, joinStyle LineJoin, miterLimit float64) {
	pts := effectivePoints(sp)
	if len(pts) < 2 {
		return
	}

	for i := 0; i+1 < len(pts); i++ {
		addSegmentRect(out, pts[i], pts[i+1], half)
	}

	n := len(pts)
	for i := 1; i < n-1; i++ {
		addJoin(out, pts[i-1], pts[i], pts[i+1], half, joinStyle, miterLimit)
	}
	if sp.Closed {
		// The subpath's closing vertex (pts[0], which effectivePoints also
		// duplicated as pts[n-1] to close the loop) has its own join too:
		// its "previous" point is the second-to-last point (the one just
		// before that duplicate), and its "next" point is pts[1].
		addJoin(out, pts[n-2], pts[0], pts[1], half, joinStyle, miterLimit)
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

// joinEpsilon bounds how close to exactly parallel (pointing the same
// way, or pointing exactly opposite ways) two segments meeting at a
// vertex can be before addJoin gives up on finding a well-defined "outer
// side" for the join. Two segments pointing the same way need no join at
// all (there is no gap between their edges to fill). Two segments
// pointing exactly opposite ways (the path folding straight back on
// itself, a rare and pathological case in real content) are left without
// a bevel/miter join too - round joins already handle this case
// correctly (a full circle needs no "which side" decision), so only
// RoundJoin is guaranteed gap-free at a perfect reversal; this is a
// deliberate, narrow simplification for the miter/bevel case, not a
// general limitation of miter/bevel joins.
const joinEpsilon = 1e-9

// addJoin appends the join geometry connecting the segment arriving at
// vertex from prev to the segment leaving vertex toward next, to out.
//
// Every stroke segment is already drawn as its own rectangle (see
// addSegmentRect), each one reaching exactly up to the vertex on both of
// its long edges. Where the path turns at vertex, those two rectangles'
// edges do not meet flush: on the side the path turns away from (the
// "inner"/concave side) they overlap - which is harmless, since
// StrokeToFill's outline is filled with the NonZero rule and overlapping
// coverage there does not create a hole - but on the side the path turns
// toward (the "outer"/convex side) they pull apart, leaving a
// wedge-shaped gap that only the join geometry below fills in.
func addJoin(out *Path, prev, vertex, next Point, half float64, joinStyle LineJoin, miterLimit float64) {
	if joinStyle == RoundJoin {
		// A full circle needs no "which side is the gap on" reasoning at
		// all, so it is unconditionally correct - see joinEpsilon's doc
		// comment on why this is also the only join style that behaves
		// well at an exact 180-degree reversal.
		addCircle(out, vertex, half)
		return
	}

	// u0 is the unit vector the path arrives at vertex along; u1 is the
	// unit vector it leaves along.
	d0x, d0y := vertex.X-prev.X, vertex.Y-prev.Y
	d1x, d1y := next.X-vertex.X, next.Y-vertex.Y
	len0, len1 := math.Hypot(d0x, d0y), math.Hypot(d1x, d1y)
	if len0 == 0 || len1 == 0 {
		// A zero-length segment on either side has no direction to join
		// toward/from; addSegmentRect already skips drawing it, so there is
		// nothing here for a join to connect either.
		return
	}
	u0x, u0y := d0x/len0, d0y/len0
	u1x, u1y := d1x/len1, d1y/len1

	// The sign of this 2D cross product tells us which way the path turns:
	// positive means turning left (counter-clockwise), negative means
	// turning right. See joinEpsilon's doc comment for the near-zero case.
	cross := u0x*u1y - u0y*u1x
	if math.Abs(cross) < joinEpsilon {
		return
	}

	// nX is segment X's own perpendicular offset, at the stroke's
	// half-width, in the same "rotate direction 90 degrees" sense
	// addSegmentRect uses for its own rectangle corners - the outer
	// corner point of that segment's rectangle at this vertex is always
	// vertex+n or vertex-n. Left turns need the "-n" side (the gap opens
	// up opposite the turn direction); right turns need the "+n" side.
	n0x, n0y := -u0y*half, u0x*half
	n1x, n1y := -u1y*half, u1x*half
	if cross > 0 {
		n0x, n0y = -n0x, -n0y
		n1x, n1y = -n1x, -n1y
	}

	p0 := Point{vertex.X + n0x, vertex.Y + n0y}
	p1 := Point{vertex.X + n1x, vertex.Y + n1y}

	if joinStyle == MiterJoin {
		if tip, ok := miterTip(vertex, n0x, n0y, n1x, n1y, miterLimit); ok {
			out.Subpaths = append(out.Subpaths, Subpath{Points: []Point{vertex, p0, tip, p1}, Closed: true})
			return
		}
		// The miter point would land further than miterLimit half-widths
		// from vertex - the specification requires falling back to a bevel
		// join in exactly this case, which is what the shared code below
		// does.
	}

	// BevelJoin (and MiterJoin's miterLimit fallback): a straight-edged
	// triangle connecting the vertex to both segments' outer corners.
	out.Subpaths = append(out.Subpaths, Subpath{Points: []Point{vertex, p0, p1}, Closed: true})
}

// miterTip computes the point where a MiterJoin's two segment edges,
// extended past vertex, actually meet - the "miter point" - given each
// segment's own outer-corner offset from vertex (n0, n1; see addJoin).
// ok is false when that point would land further than miterLimit
// half-widths away, per the specification's rule that a miter join must
// fall back to a bevel join past its miterLimit.
//
// The formula follows directly from similar triangles: if theta is the
// angle between the two segments' directions (equivalently, between n0
// and n1, since both are rotated 90 degrees from their segment's
// direction), the miter point sits at distance half/cos(theta/2) from
// vertex along the bisector of n0 and n1 - and
// (n0+n1)/(1+cos(theta)) already equals exactly that point relative to
// vertex, with no separate normalization step needed (1+cos(theta) =
// 2*cos²(theta/2), which cancels the extra cos(theta/2) that dividing by
// |n0+n1| alone would leave behind).
func miterTip(vertex Point, n0x, n0y, n1x, n1y, miterLimit float64) (Point, bool) {
	// n0 and n1 both have length half (the stroke half-width), so their
	// dot product, divided by half*half, is cos(theta).
	half := math.Hypot(n0x, n0y)
	cosTheta := (n0x*n1x + n0y*n1y) / (half * half)

	denom := 1 + cosTheta
	if denom < joinEpsilon {
		// theta is at or past 180 degrees (a near-total reversal): the
		// miter point would be infinitely far away. Definitely past any
		// finite miterLimit.
		return Point{}, false
	}

	// ratio is how many half-widths away the miter point sits - exactly
	// the quantity the specification's miterLimit bounds.
	ratio := math.Sqrt(2 / denom)
	if ratio > miterLimit {
		return Point{}, false
	}

	return Point{
		X: vertex.X + (n0x+n1x)/denom,
		Y: vertex.Y + (n0y+n1y)/denom,
	}, true
}

// circleSegments is the polygon side count used to approximate a round
// join or round cap.
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
