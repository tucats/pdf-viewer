package content

import (
	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements "Do" when the resolved XObject's /Subtype is
// /Form (image.go's doXObject dispatches here) - a form is, in effect,
// an entire nested content stream with its own coordinate system and
// resources, invoked at the point "Do" names it. Unlike an image
// XObject, a form is never rasterized to an intermediate offscreen
// buffer: doForm simply recursively interprets its content stream (via
// interpretAtDepth, the same machinery Interpret itself is built on) and
// flattens the resulting DrawOps directly into the calling interpreter's
// own DisplayList, clipped by both the form's own /BBox and whatever
// clip was already active where "Do" was invoked. This is what makes a
// form's content correctly show through to the page underneath wherever
// it does not paint - each flattened DrawOp still only ever covers its
// own shape's coverage on the one, single, always-opaque page canvas
// (internal/raster.Canvas) that every other DrawOp in this project
// paints onto; no separate alpha-blended "group" buffer is needed at
// all, unlike a tiling pattern (not yet implemented - see the package
// doc comment), which specifically needs one to rasterize a reusable
// repeating tile image once rather than re-interpreting its content
// stream for every repetition.

// maxFormDepth bounds how many levels deep a chain of Form XObjects
// invoking further Form XObjects (via their own "Do" operators) may
// nest, guarding against a self-referential or cyclic form - Form A's
// content stream names Form A again, directly or through an
// intermediate Form B - recursing without bound. internal/parser's own
// cyclic-reference guard does not catch this: each object individually
// resolves without error; only replaying through this package's own
// recursive interpretation would loop forever - the same shape of hazard
// internal/image's maxMaskRecursionDepth guards against for mutually
// referencing /SMask images, and given the same kind of small, generous
// bound for the same reason (no legitimate real-world content nests
// forms this deep).
const maxFormDepth = 8

// doForm implements the /Form half of "Do": dict is the form XObject's
// already-resolved dictionary; stream is the same object, still holding
// its own raw, filter-encoded content bytes in stream.Raw.
//
// Per 8.10.1/8.10.2: the form's /Matrix (default identity) is
// concatenated onto the CTM in effect where "Do" was invoked (st.CTM,
// *not* in.initialCTM - a form's coordinate system builds on whatever
// transform is live at the point it is painted, unlike a pattern's,
// which is deliberately independent of it - see shading.go's
// resolvePatternPaint for that contrast) to give the form's own initial
// CTM; its /BBox (in the form's own coordinate system) becomes an
// additional clip; and its /Resources, if present, replaces the calling
// content stream's for everything inside the form (falling back to the
// caller's own resources when the form specifies none, per the
// specification's inheritance rule).
func (in *interpreter) doForm(st *graphics.State, dict syntax.Dictionary, stream syntax.Stream) error {
	if in.formDepth >= maxFormDepth {
		return pdferror.Malformedf("Form XObject nesting exceeds %d levels", maxFormDepth)
	}

	formMatrix := graphics.Identity()
	if matrixVals, found, err := floatArrayEntry(in.resolver, dict, "Matrix"); err != nil {
		return err
	} else if found {
		if len(matrixVals) != 6 {
			return pdferror.Malformedf("Form XObject /Matrix must have 6 entries, has %d", len(matrixVals))
		}
		formMatrix = graphics.Matrix{
			A: matrixVals[0], B: matrixVals[1], C: matrixVals[2],
			D: matrixVals[3], E: matrixVals[4], F: matrixVals[5],
		}
	}
	formCTM := formMatrix.Mul(st.CTM)

	resources := in.resources
	if resObj, ok := dict["Resources"]; ok {
		resolved, err := resolveIfRef(in.resolver, resObj)
		if err != nil {
			return err
		}
		if resDict, ok := resolved.(syntax.Dictionary); ok {
			resources = resDict
		}
	}

	bboxClip, err := formBBoxClip(in.resolver, dict, formCTM)
	if err != nil {
		return err
	}

	samples, err := in.resolver.DecodeStream(stream)
	if err != nil {
		return err
	}
	ops, err := Parse(samples)
	if err != nil {
		return err
	}

	nested, err := interpretAtDepth(ops, formCTM, resources, in.resolver, in.formDepth+1)
	if err != nil {
		return err
	}

	// Every DrawOp the form's own content produced only carries whatever
	// clips accumulated *within* that content (from the form's own "W"/
	// "W*" operators, starting fresh - a nested interpretAtDepth call
	// gets its own brand-new graphics.State/Stack). It knows nothing
	// about the clip already active in the calling content stream at the
	// point "Do" ran, nor about the /BBox clip computed above - both must
	// be merged in here, once, rather than threading them through the
	// nested interpretation itself.
	outerAndBBox := mergeClips(st.Clips, bboxClip)
	for _, op := range nested {
		op.Clips = mergeClips(outerAndBBox, op.Clips)
		in.list = append(in.list, op)
	}
	return nil
}

// formBBoxClip maps dict's /BBox (required by the specification, but -
// consistent with this package's general tolerance for one missing
// field - simply contributes no additional clip if absent or malformed,
// rather than failing the whole form) through formCTM into device space,
// returning it as a single-element ClipPath slice ready to merge with
// any other active clips (see mergeClips).
func formBBoxClip(r pdfimage.Resolver, dict syntax.Dictionary, formCTM graphics.Matrix) ([]graphics.ClipPath, error) {
	vals, found, err := floatArrayEntry(r, dict, "BBox")
	if err != nil {
		return nil, err
	}
	if !found || len(vals) != 4 {
		return nil, nil
	}
	x0, y0, x1, y1 := vals[0], vals[1], vals[2], vals[3]
	userCorners := [4][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	var corners [4]graphics.Point
	for i, c := range userCorners {
		dx, dy := formCTM.Apply(c[0], c[1])
		corners[i] = graphics.Point{X: dx, Y: dy}
	}
	var p graphics.Path
	p.AppendRect(corners)
	return []graphics.ClipPath{{Path: &p, Rule: graphics.NonZero}}, nil
}

// mergeClips concatenates two ClipPath lists into one, avoiding an
// unnecessary allocation when either side is empty. Order does not
// affect correctness: internal/raster intersects every active clip by
// multiplying each one's independently rasterized coverage (see
// internal/raster.Canvas's paint), a commutative operation.
func mergeClips(a, b []graphics.ClipPath) []graphics.ClipPath {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([]graphics.ClipPath, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}
