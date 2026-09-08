package graphics

import "math"

// maxCoordinate bounds every coordinate ever stored in a Path, applied
// by clampPoint below at every point of entry (MoveTo, LineTo,
// AppendRect's corners). This is a deliberate choke point: a content
// stream operand is an arbitrary PDF number, and a real number literal
// like "1e400" overflows during parsing to +Inf without itself being a
// parse error (see strconv.ParseFloat), so a hostile or corrupted
// content stream can hand this package Inf or (after further arithmetic
// involving Inf) NaN coordinates. Clamping here, once, means every
// consumer of a Path - internal/raster's rasterizer in particular, which
// converts coordinates to pixel row/column integers and is exactly the
// kind of code where a NaN or out-of-range float silently becomes
// undefined float-to-int conversion behavior or an out-of-bounds slice
// index - never has to defend against it again. 1<<24 is far larger than
// any real rendered canvas or page geometry this project targets, while
// comfortably fitting in a float64 (and, later, an int32 pixel
// coordinate) without precision loss.
const maxCoordinate = 1 << 24

func clampPoint(p Point) Point {
	return Point{X: clampCoordinate(p.X), Y: clampCoordinate(p.Y)}
}

func clampCoordinate(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case v > maxCoordinate:
		return maxCoordinate
	case v < -maxCoordinate:
		return -maxCoordinate
	default:
		return v
	}
}

// Point is a 2D point in device space - i.e. already transformed by
// whatever CTM was active at the moment it was added to a Path. Storing
// paths in device space (rather than user space plus a matrix to apply
// later) matches how PDF content streams themselves work: an operator
// like "cm" changes the CTM for *subsequent* path construction, but
// never retroactively moves points already added to the current path,
// so a path's points must already be "baked" into device space the
// moment they are constructed.
type Point struct {
	X, Y float64
}

// Subpath is one contiguous, possibly-closed run of straight line
// segments - the unit a Path is built from. PDF's curve operators ("c",
// "v", "y") are flattened into additional Points at construction time
// (see Path.CurveTo) rather than kept as curves, so that every later
// stage (stroking, filling, clipping) only ever has to deal with
// straight-line polygons; see internal/raster's package doc comment on
// why a fixed, deterministic flattening matters for this project
// specifically.
type Subpath struct {
	Points []Point
	Closed bool
}

// Path is a sequence of Subpaths - PDF allows a single path object to be
// built from several disconnected "moveto...lineto..." runs before it is
// finally painted (filled, stroked, or used as a clip) as one unit, e.g.
// "m l l m l l f" fills two triangles with one fill operation.
type Path struct {
	Subpaths []Subpath

	// cur and curSet track the path's current point, per the PDF
	// specification's own notion of one - the position "l", "c", "v",
	// "y", and "h" all act relative to - independently of which Subpath
	// (if any) is currently open, since "h" (closepath) closes the
	// current subpath without discarding the current point (a following
	// "l" continues from where "h" left it: the subpath's starting
	// point).
	cur    Point
	curSet bool
}

// MoveTo starts a new Subpath at pt, becoming both the path's current
// point and that subpath's only point so far.
func (p *Path) MoveTo(pt Point) {
	pt = clampPoint(pt)
	p.Subpaths = append(p.Subpaths, Subpath{Points: []Point{pt}})
	p.cur = pt
	p.curSet = true
}

// LineTo appends a straight line segment from the current point to pt.
// If no Subpath is open yet (a content stream beginning with "l" before
// any "m", which is malformed PDF but not worth rejecting outright),
// this behaves like MoveTo instead - matching how real-world PDF
// consumers commonly tolerate this rather than discarding the whole
// content stream over one missing "m".
func (p *Path) LineTo(pt Point) {
	pt = clampPoint(pt)
	if len(p.Subpaths) == 0 {
		p.MoveTo(pt)
		return
	}
	last := &p.Subpaths[len(p.Subpaths)-1]
	last.Points = append(last.Points, pt)
	p.cur = pt
	p.curSet = true
}

// Close closes the current Subpath - PDF's "h" operator - by marking it
// Closed (an implicit straight segment back to the subpath's first
// point) and resetting the current point to that first point, per the
// specification. Close on a Path with no open Subpath is a no-op.
func (p *Path) Close() {
	if len(p.Subpaths) == 0 {
		return
	}
	last := &p.Subpaths[len(p.Subpaths)-1]
	last.Closed = true
	if len(last.Points) > 0 {
		p.cur = last.Points[0]
		p.curSet = true
	}
}

// Current returns the path's current point and whether one has been
// established yet (false only before the first MoveTo/LineTo).
func (p *Path) Current() (Point, bool) {
	return p.cur, p.curSet
}

// bezierSegments is the fixed number of straight line segments each
// cubic Bézier curve is flattened into. A fixed count (rather than an
// adaptive, curvature- or viewport-scale-dependent tolerance) is chosen
// deliberately: internal/raster's package doc comment calls out that
// rendering the same input twice must produce the same pixels, which is
// simplest to guarantee when flattening never depends on anything but
// the four control points themselves. 16 segments is smooth enough that
// the polygonal approximation error is well under a pixel for any curve
// at the page sizes and scales this project's fixture corpus and
// reasonable real-world use are expected to need.
const bezierSegments = 16

// CurveTo appends a cubic Bézier curve from the current point through
// control points c1 and c2 to end point c3, flattened into
// bezierSegments straight line segments via direct evaluation of the
// Bézier polynomial (equivalent to, but simpler to keep deterministic
// than, recursive De Casteljau subdivision). If no current point is set
// yet, c1 doubles as an implicit starting MoveTo, matching this
// package's general tolerance for a missing leading "m" (see LineTo).
func (p *Path) CurveTo(c1, c2, c3 Point) {
	c0, ok := p.Current()
	if !ok {
		p.MoveTo(c1)
		c0 = c1
	}
	for i := 1; i <= bezierSegments; i++ {
		t := float64(i) / float64(bezierSegments)
		p.LineTo(cubicBezierPoint(c0, c1, c2, c3, t))
	}
}

func cubicBezierPoint(p0, p1, p2, p3 Point, t float64) Point {
	mt := 1 - t
	a := mt * mt * mt
	b := 3 * mt * mt * t
	c := 3 * mt * t * t
	d := t * t * t
	return Point{
		X: a*p0.X + b*p1.X + c*p2.X + d*p3.X,
		Y: a*p0.Y + b*p1.Y + c*p2.Y + d*p3.Y,
	}
}

// AppendRect appends a complete, already-closed 4-point rectangular
// Subpath with corners (x,y), (x+w,y), (x+w,y+h), (x,y+h), matching
// PDF's "re" operator - which, per the specification, "append[s] a
// rectangle... as a complete subpath" independently of whatever Subpath
// might already be open, then leaves the current point at (x,y). Note
// that the four corners passed in here are expected to already be in
// device space (i.e. the caller has applied the CTM to each corner
// itself) - a non-axis-aligned CTM (a rotation, for instance) means the
// four device-space corners are not generally axis-aligned even though
// "re"'s own x/y/w/h describe an axis-aligned rectangle in user space.
func (p *Path) AppendRect(corners [4]Point) {
	for i := range corners {
		corners[i] = clampPoint(corners[i])
	}
	p.Subpaths = append(p.Subpaths, Subpath{Points: corners[:], Closed: true})
	p.cur = corners[0]
	p.curSet = true
}

// Bounds returns the smallest axis-aligned rectangle (in device space)
// containing every point in every Subpath, and false if the Path is
// empty. internal/raster uses this to limit how many pixel rows a fill
// or stroke needs to scan.
func (p *Path) Bounds() (minX, minY, maxX, maxY float64, ok bool) {
	first := true
	for _, sp := range p.Subpaths {
		for _, pt := range sp.Points {
			if first {
				minX, maxX = pt.X, pt.X
				minY, maxY = pt.Y, pt.Y
				first = false
				continue
			}
			if pt.X < minX {
				minX = pt.X
			}
			if pt.X > maxX {
				maxX = pt.X
			}
			if pt.Y < minY {
				minY = pt.Y
			}
			if pt.Y > maxY {
				maxY = pt.Y
			}
		}
	}
	return minX, minY, maxX, maxY, !first
}
