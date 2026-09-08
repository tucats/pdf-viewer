package graphics

// FillRule selects which points enclosed by a Path's Subpaths count as
// "inside" for filling, stroking (a stroke outline is itself filled -
// see stroke.go), or clipping - the two rules PDF defines (section 8.5.3
// of the specification) for resolving self-intersecting or nested
// Subpaths.
type FillRule int

const (
	// NonZero is PDF's "nonzero winding number rule" (the default for
	// "f", "B", "b", and "W"): a point is inside if a ray cast from it
	// to infinity crosses a net nonzero number of Subpath edges, counting
	// direction (crossings that wind clockwise cancel crossings that
	// wind counterclockwise).
	NonZero FillRule = iota
	// EvenOdd is PDF's "even-odd rule" ("f*", "B*", "b*", "W*"): a point
	// is inside if a ray cast from it to infinity crosses an odd number
	// of Subpath edges, regardless of direction.
	EvenOdd
)

// Color is a solid color in linear (non-gamma-corrected) device RGB,
// each component in [0,1]. This is the single representation every PDF
// color space internal/content currently understands (DeviceGray,
// DeviceRGB, DeviceCMYK) is converted down to before reaching this
// package - see internal/content's color-operator handling for the
// conversion formulas; broader color-space and color-management support
// is Phase 3/5 work per the repository README's phased plan.
type Color struct {
	R, G, B float64
}

// Black is the default fill and stroke color a fresh graphics state
// starts with, per the PDF specification (DeviceGray 0).
var Black = Color{}

// LineCap selects how the two ends of an open stroked Subpath (and each
// dash segment, once dash patterns are supported - see stroke.go's doc
// comment on that limitation) are drawn, matching PDF's "J" operator and
// its three defined values.
type LineCap int

const (
	ButtCap   LineCap = 0
	RoundCap  LineCap = 1
	SquareCap LineCap = 2
)

// LineJoin selects how two connected stroked segments meet at a vertex,
// matching PDF's "j" operator and its three defined values.
type LineJoin int

const (
	MiterJoin LineJoin = 0
	RoundJoin LineJoin = 1
	BevelJoin LineJoin = 2
)
