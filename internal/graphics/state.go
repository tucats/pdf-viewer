package graphics

import "errors"

// State is the mutable PDF graphics state internal/content's operator
// interpreter drives while replaying a content stream: the current
// transformation matrix, path construction and painting parameters, and
// the active clip - everything PDF's "q"/"Q" save/restore operators
// preserve and restore as a unit. It deliberately does not hold the path
// currently under construction (a run of "m"/"l"/"c"/"re" operators
// between two painting operators): PDF's own specification (8.5.2.1)
// notes that the current path is *not* part of the graphics state saved
// by "q" - it belongs to internal/content's interpreter instead.
type State struct {
	CTM Matrix

	FillColor   Color
	StrokeColor Color

	LineWidth  float64
	LineCap    LineCap
	LineJoin   LineJoin
	MiterLimit float64

	// --- Text state (Phase 4) -----------------------------------------
	//
	// PDF's specification (9.3, "Text State Parameters and Operators")
	// places these seven parameters in the graphics state itself, not in
	// the separate text-object-local state (the text and line matrices,
	// Tm/Tlm) that only exists between a "BT" and its matching "ET" - so
	// unlike Tm/Tlm (which internal/content's interpreter keeps as its
	// own fields, reset on every "BT"), these belong here: a "q" saves
	// them and a "Q" restores them exactly like FillColor or LineWidth
	// above, and they persist unchanged across a "BT"/"ET" pair.
	//
	// CharSpace ("Tc"), WordSpace ("Tw"), and FontSize ("Tf"'s second
	// operand) are all already in unscaled text space units (which happen
	// to equal user space units for CharSpace/WordSpace, since neither is
	// itself scaled by FontSize); Hscale ("Tz") is a percentage (100 =
	// no change, matching the operator's own units) rather than the
	// 0-1 fraction its formula effectively needs - see
	// internal/content's text.go for where that /100 conversion happens.
	CharSpace, WordSpace, Hscale, Leading, FontSize, Rise float64

	// RenderMode is PDF's "Tr" text rendering mode (0-7): 0 is "fill"
	// (the default), 3 is "invisible" (used for an OCR text layer placed
	// over a scanned image, painted nowhere but still advancing the text
	// position), and the rest select stroking, clipping, or a combination
	// - see internal/content's text.go for exactly which of the eight
	// modes this project distinguishes versus treats as an alias of
	// another (a documented simplification, like this package's
	// round-only stroke joins).
	RenderMode int

	// Font holds the currently selected font (set by "Tf"), typed as
	// `any` rather than a concrete type so that this low-level package -
	// which knows about matrices, paths, and colors, but nothing about
	// PDF font dictionaries or embedded font programs - does not need to
	// import internal/fonts. internal/fonts itself already needs to
	// import this package (a font's glyph outlines are graphics.Path
	// values), so the reverse import would be a cycle; see internal/
	// content's text.go, which is the only code that ever writes to this
	// field or reads it back out (via a type assertion to *fonts.Font). A
	// nil Font means no font has been selected yet - showing text with no
	// font selected paints nothing, the same "missing resource" tolerance
	// this project applies elsewhere (see, for example, "Do" with an
	// unresolvable XObject name).
	//
	// Font is copied by Clone as a plain field, exactly like every other
	// State field: this is safe because a *fonts.Font, once built by
	// "Tf", is never mutated in place (see internal/fonts.Font's own doc
	// comment) - two States sharing the same Font pointer after a Clone
	// can never observe each other's changes, because there are none to
	// observe.
	Font any

	// FillColorSpace and StrokeColorSpace hold the color space most
	// recently selected by "cs" and "CS" respectively (7.10, "Colour
	// Spaces" - a content-stream color space selection is itself part of
	// the graphics state, saved and restored by "q"/"Q" exactly like
	// FillColor or LineWidth), consulted by "sc"/"scn"/"SC"/"SCN" to
	// interpret their numeric operands correctly for any color space
	// beyond the three Device families internal/content can already infer
	// directly from operand count (see that package's colorFromComponents
	// and colorForOperandsWithSpace in colorspace.go).
	//
	// Typed `any` for the same reason Font is (see Font's doc comment):
	// internal/graphics cannot import internal/image (which itself
	// imports this package for graphics.Image), so the concrete type
	// (image.ColorSpace, or internal/content's own pattern-selection
	// marker) is only ever produced and consumed by internal/content, via
	// a type assertion. A nil value means no color space has been
	// explicitly selected, or the one that was could not be resolved -
	// either way, sc/scn's own component-count fallback applies.
	FillColorSpace, StrokeColorSpace any

	// FillShading and StrokeShading, when non-nil, mean the current
	// fill/stroke paint is a shading pattern (a "scn"/"SCN" operand that
	// named a /PatternType 2 resource - see internal/content/shading.go)
	// rather than the solid FillColor/StrokeColor above; a painting
	// operator (see internal/content's fillCurrentPath/strokeCurrentPath)
	// checks these first and only falls back to the solid color when they
	// are nil. Unlike FillColorSpace/StrokeColorSpace, these are typed as
	// a concrete *Shading rather than `any`: Shading is itself defined in
	// this package (shading.go), so storing it directly needs no type
	// assertion and creates no import-cycle risk. Selecting an ordinary
	// (non-pattern) color, via "sc"/"scn"/"SC"/"SCN" (see internal/
	// content's setPaintColor) or directly via "g"/"rg"/"k"/"G"/"RG"/"K",
	// clears the corresponding field back to nil - every content-stream
	// operator that sets FillColor/StrokeColor also clears
	// FillShading/StrokeShading, so a stale pattern from earlier in the
	// content stream can never leak into a later, ordinary-colored fill.
	FillShading, StrokeShading *Shading

	// FillTiling and StrokeTiling are FillShading/StrokeShading's tiling-
	// pattern counterpart: non-nil when the current fill/stroke paint is
	// a tiling pattern (a "scn"/"SCN" operand naming a /PatternType 1
	// resource - see internal/content/tilingpattern.go) rather than the
	// solid FillColor/StrokeColor or a shading. Exactly one of
	// Fill(Shading|Tiling) is non-nil at a time - selecting a shading
	// pattern clears any previously selected tiling pattern and vice
	// versa, and selecting an ordinary color clears both - see
	// internal/content's setPaintColor.
	FillTiling, StrokeTiling *TilingPattern

	// FillAlpha and StrokeAlpha are PDF's "ca" and "CA" ExtGState
	// parameters respectively (11.6.4.3): a constant alpha, in [0,1],
	// applied to every non-stroking (ca) or stroking (CA) paint
	// operation - a fill, an image, a shading, or a stroke - in addition
	// to whatever coverage a shape's own edges (anti-aliasing) or an
	// image's own per-pixel alpha already contribute. Both default to 1
	// (fully opaque, i.e. "no additional attenuation") per the
	// specification, which NewState sets explicitly since the Go zero
	// value (0, fully transparent) would otherwise silently make every
	// paint operation invisible.
	FillAlpha, StrokeAlpha float64

	// BlendMode is PDF's "BM" ExtGState parameter (11.3.5): unlike
	// FillAlpha/StrokeAlpha, there is only one blend mode, used for both
	// non-stroking and stroking operations alike. BlendNormal (the Go
	// zero value) needs no explicit initialization in NewState.
	BlendMode BlendMode

	// Clips holds every currently-active clipping path, most recently
	// intersected last, together with the fill rule each was intersected
	// under. The *effective* clip region is the intersection of all of
	// them - PDF's clipping operators only ever narrow the current clip,
	// never widen it, so accumulating a list (rather than attempting to
	// compute one single combined polygon via a general polygon-boolean
	// algorithm, which internal/raster's minimal scanline rasterizer does
	// not implement) is both correct and simple: internal/raster
	// evaluates each clip Path's coverage independently and combines them
	// by multiplication. An empty (nil) Clips means "unclipped" (besides
	// the page itself).
	Clips []ClipPath
}

// ClipPath is one entry in State.Clips: a device-space Path together
// with the fill rule it should be evaluated under.
type ClipPath struct {
	Path *Path
	Rule FillRule
}

// NewState returns a fresh graphics state with the PDF specification's
// documented initial values (black fill and stroke color, 1-unit line
// width, miter joins with limit 10, butt caps, no clip) and the given
// initial CTM - the mapping from the page's default user space to device
// (pixel) space that internal/raster's caller establishes before any
// content stream operator has run; see page.go's Render implementation
// in the root package.
func NewState(initialCTM Matrix) *State {
	return &State{
		CTM:         initialCTM,
		FillColor:   Black,
		StrokeColor: Black,
		LineWidth:   1,
		LineJoin:    MiterJoin,
		MiterLimit:  10,
		// Hscale's initial value is 100 (percent, i.e. "no horizontal
		// scaling") per the specification - the zero value would mean
		// "scale every glyph to zero width", which is not what a fresh
		// graphics state (before any "Tz" operator) means.
		Hscale: 100,
		// FillAlpha/StrokeAlpha default to 1 (fully opaque) - see their
		// own doc comment for why this needs to be explicit.
		FillAlpha:   1,
		StrokeAlpha: 1,
	}
}

// Clone returns an independent copy of s suitable for pushing onto a
// Stack: every field is a plain value or a slice header, so a shallow
// copy is sufficient - nothing in State is ever mutated through a shared
// pointer after being cloned (Clips is only ever replaced wholesale by
// WithClip, never appended to in place; see WithClip's doc comment).
func (s *State) Clone() *State {
	clone := *s
	return &clone
}

// WithClip returns a copy of s whose Clips additionally intersects with
// clip, leaving s itself unmodified. A new backing array is always
// allocated (rather than appending to s.Clips directly, which could
// silently share and mutate another State's backing array if one had
// enough spare capacity - a classic Go slice-aliasing hazard) so that
// two States produced by Clone from the same original never see each
// other's later clip changes.
func (s *State) WithClip(clip *Path, rule FillRule) *State {
	next := s.Clone()
	combined := make([]ClipPath, len(s.Clips)+1)
	copy(combined, s.Clips)
	combined[len(s.Clips)] = ClipPath{Path: clip, Rule: rule}
	next.Clips = combined
	return next
}

// ErrStackUnderflow is returned by Stack.Pop when there is no saved
// state to restore - a content stream with more "Q" operators than
// matching "q" operators, which is malformed content but not itself a
// disqualifying reason to stop rendering the rest of the page. See
// internal/content's operator interpreter for how it responds to this.
var ErrStackUnderflow = errors.New("graphics: q/Q stack underflow")

// Stack implements PDF's "q"/"Q" graphics state save/restore as a plain
// stack of saved State snapshots, with Current always available without
// needing to Pop first.
type Stack struct {
	cur  *State
	rest []*State
}

// NewStack returns a Stack whose current state is initial.
func NewStack(initial *State) *Stack {
	return &Stack{cur: initial}
}

// Current returns the stack's current (top-of-stack, live) State. The
// returned pointer is shared, not copied - callers mutate it directly
// (e.g. for "cm", "rg", "w") exactly as PDF operators mutate the current
// graphics state in place.
func (s *Stack) Current() *State {
	return s.cur
}

// Push saves a snapshot of the current state (PDF's "q"), leaving
// Current() still pointing at the same live state for further mutation -
// only a later Pop reverts to the snapshot.
func (s *Stack) Push() {
	s.rest = append(s.rest, s.cur.Clone())
}

// Pop restores the most recently pushed state (PDF's "Q"), discarding
// whatever mutations were made to the current state since the matching
// Push. It returns ErrStackUnderflow if there is no saved state, in
// which case Current is left unchanged.
func (s *Stack) Pop() error {
	if len(s.rest) == 0 {
		return ErrStackUnderflow
	}
	n := len(s.rest) - 1
	s.cur = s.rest[n]
	s.rest = s.rest[:n]
	return nil
}
