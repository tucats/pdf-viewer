package graphics

import "math"

// TilingPattern is a resolved tiling pattern paint source (PDF's
// /PatternType 1): a single pre-rendered repetition of the pattern
// cell's own content, together with the matrix that maps that one
// repetition's unit square [0,1]x[0,1] to exactly one XStep x YStep step
// in device space. Painting with it is then just an ordinary image draw
// with wraparound sampling (see DrawOp.Repeat and internal/raster's
// Canvas.DrawImage) - the same "precompute once, sample many times"
// shape as an ordinary image, which is why this type carries an *Image
// rather than anything content-stream-specific: by the time a
// TilingPattern exists, its cell has already been fully interpreted and
// rasterized (see internal/content's tilingpattern.go and
// internal/raster's RenderTransparent), exactly like any other
// graphics.Image.
type TilingPattern struct {
	Tile          *Image
	ImageToDevice Matrix
}

// ShadingKind identifies which of PDF's shading types (8.7.4.5) this
// project implements: the geometric ones (axial and radial, by far the
// most common in real-world content), the function-based one (a color
// computed directly from a 2-D position rather than a 1-D parametric
// line/circle), and the four mesh types (a gradient described as an
// explicit triangle or patch mesh with per-vertex/per-corner colors,
// rather than any single formula).
type ShadingKind int

const (
	// FunctionBasedShading is PDF's Type 1 shading: color is the direct
	// output of a 2-input (x, y) function evaluated over a rectangular
	// domain, with no line/circle geometry at all - see Shading's
	// Domain2/Matrix/ColorAt2 fields and atFunctionBased.
	FunctionBasedShading ShadingKind = 1
	// AxialShading is PDF's Type 2 shading: color varies linearly along
	// a straight line between two points.
	AxialShading ShadingKind = 2
	// RadialShading is PDF's Type 3 shading: color varies between two
	// circles, which may differ in both center and radius - the general
	// form that also produces a simple "radial" (single expanding
	// circle) gradient when the two circles share a center.
	RadialShading ShadingKind = 3
	// FreeFormTriangleMesh is PDF's Type 4 shading: a stream of vertices,
	// each carrying its own color, grouped into triangles either
	// explicitly (a fresh triangle) or by sharing an edge with the
	// previously decoded triangle (see internal/content's mesh-decoding
	// code, which is what actually parses the packed vertex stream -
	// this package only ever sees the resulting Triangles).
	FreeFormTriangleMesh ShadingKind = 4
	// LatticeFormTriangleMesh is PDF's Type 5 shading: like
	// FreeFormTriangleMesh, but the vertex stream is a regular grid
	// (/VerticesPerRow wide) with triangles implied by adjacency rather
	// than explicit edge-sharing flags.
	LatticeFormTriangleMesh ShadingKind = 5
	// CoonsPatchMesh is PDF's Type 6 shading: a stream of Coons patches
	// (12 boundary Bezier control points plus 4 corner colors per
	// patch), each internally subdivided into a fine triangle lattice
	// before reaching this package - see this field's Triangles.
	CoonsPatchMesh ShadingKind = 6
	// TensorProductPatchMesh is PDF's Type 7 shading: like
	// CoonsPatchMesh, but each patch additionally carries its own 4
	// internal control points (16 total) rather than having them derived
	// from the boundary.
	TensorProductPatchMesh ShadingKind = 7
)

// isMeshKind reports whether kind is one of the four mesh shading types
// (4-7), which all share the same rendering representation (a flat list
// of colored triangles - see Shading.Triangles) regardless of how
// differently internal/content had to decode each one's own stream
// format to produce that list.
func isMeshKind(kind ShadingKind) bool {
	switch kind {
	case FreeFormTriangleMesh, LatticeFormTriangleMesh, CoonsPatchMesh, TensorProductPatchMesh:
		return true
	default:
		return false
	}
}

// Shading is a resolved PDF shading of any supported type, ready to
// compute a device-space pixel's color with no further PDF-specific
// knowledge. Two different representations are used depending on Kind:
//
//   - AxialShading/RadialShading/FunctionBasedShading are each a formula
//     evaluated live, per pixel, via a closure (ColorAt or ColorAt2) -
//     the color-per-position computation, which requires evaluating a PDF
//     function and converting through a color space (internal/function
//     and internal/image - two packages this one does not import, to
//     avoid an import cycle, since both need graphics types such as Color
//     themselves), is captured once, ahead of time, in that closure. This
//     mirrors how graphics.State.Font avoids the same cycle by storing
//     `any` plus a type assertion done elsewhere; these closures need no
//     type assertion at all, since a plain function value already has
//     exactly the shape this package needs.
//   - The four mesh kinds (FreeFormTriangleMesh through
//     TensorProductPatchMesh) instead carry their entire geometry
//     up front as a flat Triangles list - there is no compact formula for
//     "the color at (x,y)" the way a line or circle has, so internal/content
//     does all of the PDF-specific mesh-stream decoding ahead of time and
//     hands this package only the fully-resolved result.
//
// internal/content builds one of these for two different PDF
// constructs that share identical color math but differ in how their
// device mapping is established: the "sh" operator (ShadingToDevice is
// simply the current CTM at the moment "sh" runs) and a shading pattern
// selected via "scn"/"SCN" (ShadingToDevice instead combines the
// pattern's own /Matrix with the *default* coordinate system of the
// content stream that defined it - PDF patterns are deliberately
// independent of whatever CTM happens to be active when they are later
// used to paint). See internal/content/shading.go for both cases.
type Shading struct {
	Kind ShadingKind

	// Coords is the shading's geometry, in shading space (see
	// ShadingToDevice) and in the specification's own parameter order:
	// for AxialShading, [x0 y0 x1 y1] (indices 4 and 5 are unused); for
	// RadialShading, [x0 y0 r0 x1 y1 r1] (all six used). Unused (left
	// zero) for every other ShadingKind.
	Coords [6]float64

	// Domain is the parametric range [t0 t1] the shading's color
	// function is evaluated over - PDF's own default, per 8.7.4.5.3, is
	// [0 1]. Used only by AxialShading/RadialShading.
	Domain [2]float64

	// Extend[0] (respectively Extend[1]) reports whether the shading
	// paints beyond its t0 (respectively t1) end - using that end's own
	// color, per the specification, rather than the boundary being the
	// edge of the painted region. Used only by AxialShading/RadialShading.
	Extend [2]bool

	// Domain2 is FunctionBasedShading's rectangular input domain
	// [xmin xmax ymin ymax] (8.7.4.5.2's default is [0 1 0 1]) - a point
	// outside it is simply not covered by the shading at all (unlike
	// AxialShading/RadialShading's Extend, Type 1 has no "paint the edge
	// color beyond the domain" concept). Unused by every other Kind.
	Domain2 [4]float64

	// Matrix is FunctionBasedShading's own mapping from Domain2's (x, y)
	// space into shading space (8.7.4.5.2's /Matrix, default identity) -
	// a second coordinate transform layered underneath ShadingToDevice,
	// specific to this one shading type. Unused by every other Kind.
	Matrix Matrix

	// Triangles backs every mesh Kind (FreeFormTriangleMesh through
	// TensorProductPatchMesh): the fully decoded, flattened set of
	// Gouraud-shaded triangles internal/content produced from the
	// shading stream's own packed vertex or patch data - by the time a
	// Shading exists, a mesh has already been reduced to "a list of
	// triangles, each with 3 corner colors", the same "precompute once"
	// shape ColorAt gives axial/radial shadings. Unused by every other
	// Kind.
	Triangles []MeshTriangle

	// ShadingToDevice maps shading space (the coordinate system Coords,
	// or a mesh's own triangle vertices, is expressed in) to device
	// space - see this type's own doc comment for the two different ways
	// internal/content establishes it depending on whether this Shading
	// backs "sh" or a pattern.
	ShadingToDevice Matrix

	// ColorAt evaluates the shading's color at parametric position t,
	// already clamped into Domain by At below - a caller of At never
	// needs to clip t itself. Used only by AxialShading/RadialShading. A
	// nil ColorAt makes At always report ok=false, the same "safe
	// default" every other optional closure field in this package (see,
	// for instance, DrawOp.Image being nil) degrades to rather than a
	// nil-call panic.
	ColorAt func(t float64) Color

	// ColorAt2 is ColorAt's FunctionBasedShading counterpart: evaluates
	// the shading's color at a domain-space (x, y) position already
	// known to lie within Domain2 - see atFunctionBased. Used only by
	// FunctionBasedShading; nil degrades the same safe way ColorAt does.
	ColorAt2 func(x, y float64) Color
}

// MeshTriangle is one Gouraud-shaded triangle of a decoded mesh shading
// (see Shading.Triangles): three vertices in shading space, each with its
// own color, linearly (barycentrically) interpolated across the
// triangle's interior - see meshColorAt.
type MeshTriangle struct {
	X0, Y0     float64
	X1, Y1     float64
	X2, Y2     float64
	C0, C1, C2 Color
}

// At computes the color visible at device-space point (deviceX,
// deviceY), reporting ok=false for a point this shading does not cover
// at all: outside its geometry with no applicable /Extend, or a
// degenerate/non-invertible ShadingToDevice (possible from malformed
// content, e.g. a pattern or "sh" active under a singular CTM).
func (s *Shading) At(deviceX, deviceY float64) (Color, bool) {
	deviceToShading, ok := s.ShadingToDevice.Invert()
	if !ok {
		return Color{}, false
	}
	sx, sy := deviceToShading.Apply(deviceX, deviceY)

	// FunctionBasedShading and the four mesh kinds each have their own,
	// entirely different notion of "covered" and "color here" - neither
	// fits the single-parameter t computed below, so they are handled by
	// their own helpers and return directly.
	if s.Kind == FunctionBasedShading {
		return s.atFunctionBased(sx, sy)
	}
	if isMeshKind(s.Kind) {
		return meshColorAt(s.Triangles, sx, sy)
	}

	if s.ColorAt == nil {
		return Color{}, false
	}

	var param float64
	switch s.Kind {
	case AxialShading:
		param, ok = axialParameter(sx, sy, s.Coords, s.Extend)
	case RadialShading:
		param, ok = radialParameter(sx, sy, s.Coords, s.Extend)
	default:
		return Color{}, false
	}
	if !ok {
		return Color{}, false
	}

	// The geometric parameter (s in the specification's own notation,
	// named param here to avoid colliding with this method's receiver)
	// may lie outside [0,1] when an /Extend flag permitted it - but per
	// 8.7.4.5.3/.4, an extended region always paints with the *edge*
	// color (t0 or t1), not an extrapolated function value beyond the
	// function's own declared Domain, so it is clamped to [0,1] before
	// being mapped into Domain.
	clamped := param
	switch {
	case clamped < 0:
		clamped = 0
	case clamped > 1:
		clamped = 1
	}
	t := s.Domain[0] + clamped*(s.Domain[1]-s.Domain[0])
	return s.ColorAt(t), true
}

// axialParameter implements 8.7.4.5.3: project (px, py) onto the
// infinite line through (x0,y0) and (x1,y1), returning the parametric
// position s such that s=0 lands exactly on (x0,y0) and s=1 exactly on
// (x1,y1) - the standard vector-projection formula, s = ((p-p0)·d) /
// (d·d) where d = p1-p0. ok is false when the two points coincide (a
// degenerate shading with no defined direction - not meaningfully
// paintable) or when s falls outside [0,1] and the corresponding /Extend
// flag does not permit it.
func axialParameter(px, py float64, coords [6]float64, extend [2]bool) (float64, bool) {
	x0, y0, x1, y1 := coords[0], coords[1], coords[2], coords[3]
	dx, dy := x1-x0, y1-y0
	lenSq := dx*dx + dy*dy
	if lenSq == 0 {
		return 0, false
	}
	s := ((px-x0)*dx + (py-y0)*dy) / lenSq
	return clampToExtend(s, extend)
}

// clampToExtend reports whether parametric position s is covered by the
// shading given its /Extend flags: s within [0,1] always is; s outside
// that range is covered only when the corresponding Extend flag is true.
// s itself is returned unchanged (At clamps it separately, only once
// coverage has already been decided) so a caller can distinguish "this
// is exactly at the boundary" from "this is far beyond it" if it ever
// needs to - axialParameter's only caller does not, but this keeps the
// function's contract simple and total rather than baking in a second,
// hidden clamp.
func clampToExtend(s float64, extend [2]bool) (float64, bool) {
	switch {
	case s < 0:
		return s, extend[0]
	case s > 1:
		return s, extend[1]
	default:
		return s, true
	}
}

// radialParameter implements 8.7.4.5.4's two-circle gradient geometry:
// the family of circles interpolated between (x0,y0,r0) and (x1,y1,r1)
// as s ranges over the reals (center = lerp(center0,center1,s), radius =
// lerp(r0,r1,s)), and finds the greatest s for which (px,py) lies
// exactly on that family's circle - which the specification requires
// using when more than one such s exists, since a radial gradient's
// circles can overlap. Solving "distance from (px,py) to the
// s-interpolated center equals the s-interpolated radius" for s is a
// single quadratic equation in s (derived by squaring both sides);  see
// the local variable names below for each of that quadratic's
// coefficients.
//
// A candidate s only counts as valid if its own interpolated radius is
// non-negative (a "circle" of negative radius is not geometrically
// meaningful - the specification calls the corresponding sub-range
// "undefined") and if s itself is either within [0,1] or extended
// beyond it via the matching /Extend flag, exactly as axialParameter
// checks. ok is false when no candidate s satisfies both.
func radialParameter(px, py float64, coords [6]float64, extend [2]bool) (float64, bool) {
	x0, y0, r0, x1, y1, r1 := coords[0], coords[1], coords[2], coords[3], coords[4], coords[5]
	dx, dy, dr := x1-x0, y1-y0, r1-r0
	fx, fy := px-x0, py-y0

	// Expanding (fx-s*dx)^2 + (fy-s*dy)^2 = (r0+s*dr)^2 and collecting
	// powers of s gives a*s^2 + b*s + c = 0 with these coefficients.
	a := dx*dx + dy*dy - dr*dr
	b := -2 * (fx*dx + fy*dy + r0*dr)
	c := fx*fx + fy*fy - r0*r0

	validRadius := func(s float64) bool { return r0+s*dr >= 0 }

	var candidates []float64
	switch {
	case math.Abs(a) < 1e-9:
		// The quadratic degenerates to linear (a well-known special case:
		// r1==r0 and the two centers coincide, or more generally when the
		// "cone" formed by the two circles is parallel to the viewing
		// direction implied by this formula) - at most one root.
		if b == 0 {
			return 0, false
		}
		candidates = []float64{-c / b}
	default:
		disc := b*b - 4*a*c
		if disc < 0 {
			return 0, false
		}
		sq := math.Sqrt(disc)
		candidates = []float64{(-b + sq) / (2 * a), (-b - sq) / (2 * a)}
	}

	best, ok := 0.0, false
	for _, s := range candidates {
		if !validRadius(s) {
			continue
		}
		covered, extendOK := clampToExtend(s, extend)
		if !extendOK {
			continue
		}
		if !ok || covered > best {
			best, ok = covered, true
		}
	}
	return best, ok
}

// atFunctionBased implements Type 1 (function-based) shadings (8.7.4.5.2):
// (sx, sy) - already in shading space, per At's own doc comment - is
// mapped *back* through this shading's own Matrix into Domain2's (x, y)
// space (Matrix's documented direction is domain-to-shading, so a point
// already in shading space needs the inverse to recover its domain
// coordinates), then simply handed to ColorAt2 if it falls inside
// Domain2. Unlike an axial/radial shading, there is no "beyond the
// domain, use the edge color" extension rule for this type at all - a
// point outside Domain2 is just not covered.
func (s *Shading) atFunctionBased(sx, sy float64) (Color, bool) {
	if s.ColorAt2 == nil {
		return Color{}, false
	}
	shadingToDomain, ok := s.Matrix.Invert()
	if !ok {
		return Color{}, false
	}
	dx, dy := shadingToDomain.Apply(sx, sy)
	if dx < s.Domain2[0] || dx > s.Domain2[1] || dy < s.Domain2[2] || dy > s.Domain2[3] {
		return Color{}, false
	}
	return s.ColorAt2(dx, dy), true
}

// meshColorAt finds whichever triangle (if any) covers shading-space
// point (sx, sy) and returns its interpolated color there. A real
// mesh's triangles never overlap, so which one "wins" when more than one
// nominally contains the point (possible only for a malformed mesh, or
// exactly on a shared edge where either neighbor gives the same answer
// anyway) is not a meaningful choice - the first match found is used.
// Triangle count is typically small enough (real-world gradient meshes
// are dozens to low hundreds of patches, not millions of triangles) that
// this linear scan, rather than a spatial index, is an acceptable,
// documented simplification.
func meshColorAt(triangles []MeshTriangle, sx, sy float64) (Color, bool) {
	for i := range triangles {
		if c, ok := triangles[i].colorAt(sx, sy); ok {
			return c, true
		}
	}
	return Color{}, false
}

// colorAt computes (sx, sy)'s barycentric coordinates (a, b, c) with
// respect to this triangle's three corners - the standard formula, valid
// for any non-degenerate triangle - and reports ok=false whenever the
// point falls outside it (any coordinate meaningfully negative) or the
// triangle itself is degenerate (zero area, so barycentric coordinates
// are not even well-defined). Inside the triangle, the corner colors are
// blended by exactly those same three weights - the definition of Gouraud
// shading.
func (t MeshTriangle) colorAt(sx, sy float64) (Color, bool) {
	denom := (t.Y1-t.Y2)*(t.X0-t.X2) + (t.X2-t.X1)*(t.Y0-t.Y2)
	if math.Abs(denom) < 1e-12 {
		return Color{}, false
	}
	a := ((t.Y1-t.Y2)*(sx-t.X2) + (t.X2-t.X1)*(sy-t.Y2)) / denom
	b := ((t.Y2-t.Y0)*(sx-t.X2) + (t.X0-t.X2)*(sy-t.Y2)) / denom
	c := 1 - a - b
	// A small negative tolerance (rather than requiring exactly >= 0)
	// keeps adjacent triangles from both rejecting a point that falls
	// exactly on their shared edge due to ordinary floating-point error.
	const epsilon = -1e-9
	if a < epsilon || b < epsilon || c < epsilon {
		return Color{}, false
	}
	return Color{
		R: a*t.C0.R + b*t.C1.R + c*t.C2.R,
		G: a*t.C0.G + b*t.C1.G + c*t.C2.G,
		B: a*t.C0.B + b*t.C1.B + c*t.C2.B,
	}, true
}
