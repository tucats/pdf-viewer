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

// BlendMode selects how a newly painted color combines with whatever is
// already on the canvas underneath it, matching PDF's "/BM" ExtGState
// parameter (11.3.5) - set via "gs" (see internal/content's
// applyExtGState) and carried on both graphics.State (the currently
// selected mode) and graphics.DrawOp (the mode a specific paint
// operation was made under, baked in at the time it was recorded).
//
// Only PDF's six "separable" blend modes (11.3.5.2 - each output channel
// computed independently from the same input channel, with no
// dependency on the other channels) are implemented; see blend.go's
// doc comment for the formulas. The four "non-separable" modes (Hue,
// Saturation, Color, Luminosity, 11.3.5.3 - each needs all three
// channels together, since they operate on HSL-like properties of the
// whole color) are not implemented: an unrecognized or unsupported mode
// name resolves to BlendNormal rather than an error, per the
// specification's own documented fallback rule ("If the blend mode ...
// is not a supported one, the application shall use Normal instead").
type BlendMode int

const (
	// BlendNormal simply replaces (is not blended with) the backdrop,
	// exactly as every DrawOp painted before this project supported
	// blend modes already did - it produces bit-identical output to
	// that unblended compositing, so it is deliberately the zero value.
	BlendNormal BlendMode = iota
	// BlendMultiply darkens: the result is never lighter than either
	// input.
	BlendMultiply
	// BlendScreen lightens: the result is never darker than either
	// input - the inverse operation of Multiply.
	BlendScreen
	// BlendDarken keeps, per channel, whichever of the backdrop or
	// source is darker.
	BlendDarken
	// BlendLighten keeps, per channel, whichever of the backdrop or
	// source is lighter.
	BlendLighten
	// BlendDifference subtracts the darker channel value from the
	// lighter one, producing 0 wherever the two already agree.
	BlendDifference
	// BlendExclusion is similar to Difference but with lower contrast
	// (it never reaches full black or full white except at the input
	// extremes).
	BlendExclusion
)
