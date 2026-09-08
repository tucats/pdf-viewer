// Package annotation resolves a page's /Annots array into the
// appearance streams that should actually be painted when the page is
// rendered - a Phase 5 capability per the repository README's phased
// plan ("Add annotations and form appearance streams only after
// ordinary page content is stable").
//
// # What an annotation appearance stream is
//
// A PDF annotation (a comment, a form field's on-screen widget, a
// highlight, a stamp, ...) can carry its own pre-rendered visual
// appearance: a Form XObject (see internal/content's form.go) referenced
// by the annotation dictionary's /AP (appearance dictionary) entry. This
// package only concerns itself with *painting that existing appearance*
// - it never generates one from a field's value, computes a checkbox's
// on/off state, or does anything else an interactive form-filling engine
// would; see the repository README's non-goals and
// docs/capability-matrix.md's "Annotations and forms" section.
//
// # The appearance-to-rectangle mapping (12.5.5)
//
// An annotation's /Rect gives the rectangle it occupies on the page, in
// the page's own default user space - but an appearance stream's content
// is drawn in its *own* coordinate system, described by its own /BBox
// and /Matrix, which need not match /Rect at all (a producer might, for
// instance, draw a checkbox's appearance in a convenient 10x10 box and
// rely on this mapping to stretch it to fit wherever the checkbox
// actually appears on the page). The specification's algorithm:
//
//  1. Transform the appearance's /BBox by its own /Matrix and take the
//     smallest upright rectangle enclosing the four transformed corners
//     - this is "BBox′" below.
//  2. Compute a matrix A that maps BBox′ onto /Rect: translate and
//     independently scale x and y so the two rectangles' corners align
//     exactly.
//
// Resolve computes A for every annotation with a usable appearance (see
// Appearance.Matrix's doc comment for exactly how a caller is expected
// to use it) - it does not itself apply /Matrix or paint anything; that
// is the caller's job, reusing internal/content's own Form XObject
// execution (see the root package's annotations.go) rather than this
// package duplicating it.
package annotation
