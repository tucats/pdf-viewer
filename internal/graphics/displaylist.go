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

// DrawOp is one paint operation - a single fill or a single stroke
// outline - captured with everything internal/raster needs to paint it
// correctly on its own: a Path already in device space, the fill rule to
// resolve overlapping/self-intersecting Subpaths with, the solid color
// to paint it, and every clip Path active at the moment it was painted.
// A stroke becomes a DrawOp by first being converted to its filled
// outline (see StrokeToFill) - by the time a DrawOp exists, "fill" and
// "stroke" are no longer different operations, only different Paths and
// Colors, which is what lets internal/raster implement only one
// rasterization algorithm.
type DrawOp struct {
	Path  *Path
	Rule  FillRule
	Color Color
	Clips []ClipPath
}
