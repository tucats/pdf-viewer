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
