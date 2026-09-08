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
// project implements directly: the two ("axial" and "radial") that
// dominate real-world gradient content. The function-based (Type 1) and
// mesh (Types 4-7, which describe a gradient as a triangle/patch mesh
// rather than a simple geometric formula) shading types are rejected as
// unsupported by internal/content's shading resolution before a Shading
// value is ever constructed - see that package's shading.go.
type ShadingKind int

const (
	// AxialShading is PDF's Type 2 shading: color varies linearly along
	// a straight line between two points.
	AxialShading ShadingKind = 2
	// RadialShading is PDF's Type 3 shading: color varies between two
	// circles, which may differ in both center and radius - the general
	// form that also produces a simple "radial" (single expanding
	// circle) gradient when the two circles share a center.
	RadialShading ShadingKind = 3
)

// Shading is a resolved axial or radial gradient, ready to compute a
// device-space pixel's color with no further PDF-specific knowledge: the
// color-per-parametric-position computation, which requires evaluating a
// PDF function and converting through a color space (internal/function
// and internal/image - two packages this one does not import, to avoid
// an import cycle, since both need graphics types such as Color
// themselves), is captured once, ahead of time, in the ColorAt closure -
// see that field's doc comment. This mirrors how graphics.State.Font
// avoids the same cycle by storing `any` plus a type assertion done
// elsewhere; Shading's ColorAt needs no type assertion at all, since a
// plain function value already has exactly the shape this package needs.
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
	// RadialShading, [x0 y0 r0 x1 y1 r1] (all six used).
	Coords [6]float64

	// Domain is the parametric range [t0 t1] the shading's color
	// function is evaluated over - PDF's own default, per 8.7.4.5.3, is
	// [0 1].
	Domain [2]float64

	// Extend[0] (respectively Extend[1]) reports whether the shading
	// paints beyond its t0 (respectively t1) end - using that end's own
	// color, per the specification, rather than the boundary being the
	// edge of the painted region.
	Extend [2]bool

	// ShadingToDevice maps shading space (the coordinate system Coords
	// is expressed in) to device space - see this type's own doc comment
	// for the two different ways internal/content establishes it
	// depending on whether this Shading backs "sh" or a pattern.
	ShadingToDevice Matrix

	// ColorAt evaluates the shading's color at parametric position t,
	// already clamped into Domain by At below - a caller of At never
	// needs to clip t itself. A nil ColorAt makes At always report
	// ok=false, the same "safe default" every other optional closure
	// field in this package (see, for instance, DrawOp.Image being nil)
	// degrades to rather than a nil-call panic.
	ColorAt func(t float64) Color
}

// At computes the color visible at device-space point (deviceX,
// deviceY), reporting ok=false for a point this shading does not cover
// at all: outside its geometry with no applicable /Extend, or a
// degenerate/non-invertible ShadingToDevice (possible from malformed
// content, e.g. a pattern or "sh" active under a singular CTM).
func (s *Shading) At(deviceX, deviceY float64) (Color, bool) {
	if s.ColorAt == nil {
		return Color{}, false
	}
	deviceToShading, ok := s.ShadingToDevice.Invert()
	if !ok {
		return Color{}, false
	}
	sx, sy := deviceToShading.Apply(deviceX, deviceY)

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
