package graphics

// DisplayList is the ordered sequence of drawing operations content
// interpretation produces from a page's content stream - the
// intermediate representation the repository README's "Proposed
// Internal Layout" section calls for between internal/content (which
// decodes operators) and internal/raster (which turns it into pixels):
// internal/content builds one by replaying Operators against a Stack,
// and internal/raster consumes it without needing to know anything
// about content stream syntax.
type DisplayList []DrawOp

// DrawOp is one paint operation - a single fill, a single stroke
// outline, or (Phase 3) a single image - captured with everything
// internal/raster needs to paint it correctly on its own: a Path already
// in device space, the fill rule to resolve overlapping/self-
// intersecting Subpaths with, the solid color to paint it, and every
// clip Path active at the moment it was painted. A stroke becomes a
// DrawOp by first being converted to its filled outline (see
// StrokeToFill) - by the time a DrawOp exists, "fill" and "stroke" are
// no longer different operations, only different Paths and Colors, which
// is what lets internal/raster implement only one rasterization
// algorithm for both.
//
// An image ("Do" painting an image XObject, or an inline "BI" image) is
// the third kind of DrawOp, distinguished by a non-nil Image field
// rather than by a separate DrawOp type: it still needs everything an
// ordinary fill needs (a device-space Path to rasterize coverage for,
// and a clip list), so reusing the same struct - with Image and
// ImageToDevice meaningful only when Image is non-nil, and Rule/Color
// meaningful only when it is nil - avoids internal/raster needing two
// near-identical code paths for "figure out which pixels this paints
// into and how clipped they are" (see internal/raster.rasterizeCoverage,
// which both cases share). Path, for an image DrawOp, is the
// quadrilateral that image space's unit square [0,1]x[0,1] maps to under
// ImageToDevice (see internal/content's "Do"/"BI" handling), evaluated
// under the NonZero fill rule.
type DrawOp struct {
	Path  *Path
	Rule  FillRule
	Color Color
	Clips []ClipPath

	// Image, when non-nil, means this DrawOp paints image pixels rather
	// than the solid Color above.
	Image *Image

	// ImageToDevice maps image space (the unit square [0,1]x[0,1], with
	// (0,0) at Image's top-left sample and (1,1) at its bottom-right
	// sample - see Image's own doc comment) to device space. It is only
	// meaningful when Image is non-nil. internal/raster inverts this (see
	// Matrix.Invert) to find, for each device pixel Path's coverage says
	// is painted, which Image pixel to sample.
	//
	// This is deliberately *not* simply the content stream's current CTM:
	// PDF's own content-stream coordinate conventions are y-up (matching
	// every other coordinate in this package - Path, Point, and so on),
	// but Image's top-left-origin, y-down row storage is not - see
	// internal/content's imageSpaceToDevice for the vertical flip that
	// reconciles the two before a DrawOp is ever constructed. Composing
	// that flip there (rather than compensating for it here in
	// internal/raster) keeps this package's own axis convention
	// internally consistent: ImageToDevice always means exactly what
	// Image's own doc comment says, with no caller-specific exception.
	ImageToDevice Matrix

	// Shading, when non-nil, means this DrawOp paints a gradient
	// (internal/content's "sh" operator, or a fill/stroke painted with a
	// shading pattern selected via "scn"/"SCN" - see that package's
	// shading.go) rather than the solid Color above; mutually exclusive
	// with Image, exactly like Color is.
	//
	// Path is nil only for a Shading DrawOp produced by "sh": per the
	// specification, "sh" paints across the entire current clipping
	// region, which is the whole page when unclipped - internal/content
	// has no way to know the canvas's pixel dimensions to build a
	// covering Path itself (it works entirely in an abstract coordinate
	// space until internal/raster rasterizes), so a nil Path is this
	// package's documented signal for "internal/raster should use its
	// own canvas bounds as the painted region" (see internal/raster's
	// Canvas.PaintShading). Every other DrawOp - including a shading
	// *pattern* fill, which paints only the shape being filled or
	// stroked, exactly like an ordinary solid-color fill would - always
	// sets Path.
	Shading *Shading
}
