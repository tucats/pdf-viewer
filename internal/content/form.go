package content

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/raster"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements "Do" when the resolved XObject's /Subtype is
// /Form (image.go's doXObject dispatches here) - a form is, in effect,
// an entire nested content stream with its own coordinate system and
// resources, invoked at the point "Do" names it. Ordinarily (doInlineForm,
// below) a form is never rasterized to an intermediate offscreen buffer:
// its content stream is simply recursively interpreted (via
// interpretFormAtDepth, built on the same machinery Interpret itself
// uses) and the resulting DrawOps are flattened directly into the
// calling interpreter's own DisplayList, clipped by both the form's own
// /BBox and whatever clip was already active where "Do" was invoked.
// This is what makes a form's content correctly show through to the page
// underneath wherever it does not paint - each flattened DrawOp still
// only ever covers its own shape's coverage on the one, single,
// always-opaque page canvas (internal/raster.Canvas) that every other
// DrawOp in this project paints onto.
//
// A form declaring a /Group /S /Transparency dictionary is different:
// per 11.4.7.2, entering such a group resets the constant alpha, blend
// mode, and soft mask to their initial values (opaque, Normal, none) for
// use *within* the group, and the group's rendered result is then
// composited into the backdrop as a single unit using whichever alpha,
// blend mode, and soft mask were active in the *calling* graphics state
// at the point "Do" was invoked - not whatever the group's own content
// stream set them to internally. Flattening DrawOps directly (as
// doInlineForm does) cannot reproduce this: every flattened DrawOp would
// instead pick up whatever FillAlpha/BlendMode/SoftMask happened to be
// live in the *nested* interpreter's own state at the moment it was
// recorded, which - for content that resets those parameters internally
// before painting, a common pattern in producer-generated PDFs - silently
// discards the outer, invocation-time attenuation entirely (see
// doIsolatedGroupForm's own doc comment for the real-world example that
// motivated this). doIsolatedGroupForm handles that case instead, by
// rendering the group to an offscreen buffer (internal/raster.
// RenderTransparent, the same machinery tilingpattern.go and softmask.go
// already use for their own "needs its own buffer" cases) and emitting
// one image DrawOp for the whole group, carrying the outer alpha/blend/
// soft mask. doForm only takes this path when the outer state could
// actually make a visible difference (see needsIsolatedGroupComposite) -
// when it could not, doInlineForm's cheaper direct flattening produces
// an identical result.

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
// its own raw, filter-encoded content bytes in stream.Raw. It computes
// the form's own CTM and resources (shared by both of the package-level
// doc comment's two paths) and then dispatches to whichever of
// doInlineForm/doIsolatedGroupForm actually interprets the form's
// content, based on needsIsolatedGroupComposite.
//
// Per 8.10.1/8.10.2: the form's /Matrix (default identity) is
// concatenated onto the CTM in effect where "Do" was invoked (st.CTM,
// *not* in.initialCTM - a form's coordinate system builds on whatever
// transform is live at the point it is painted, unlike a pattern's,
// which is deliberately independent of it - see shading.go's
// resolvePatternPaint for that contrast) to give the form's own initial
// CTM; and its /Resources, if present, replaces the calling content
// stream's for everything inside the form (falling back to the caller's
// own resources when the form specifies none, per the specification's
// inheritance rule).
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

	isGroup, err := isTransparencyGroup(in.resolver, dict)
	if err != nil {
		return err
	}
	if isGroup && needsIsolatedGroupComposite(st) {
		if ok, err := in.doIsolatedGroupForm(st, dict, stream, formCTM, resources); err != nil || ok {
			return err
		}
		// doIsolatedGroupForm only returns ok=false for a group whose
		// /BBox is missing or degenerate - it recorded no diagnostic of
		// its own for that (formBBoxClip's callers already tolerate a
		// missing /BBox the same way), so fall through to the ordinary
		// path exactly as if this form had no /Group at all.
	}
	return in.doInlineForm(st, dict, stream, formCTM, resources)
}

// needsIsolatedGroupComposite reports whether the graphics state active
// where "Do" was invoked could make isolated group compositing
// (doIsolatedGroupForm) produce a visibly different result than simply
// flattening the group's content directly (doInlineForm): only when an
// outer constant alpha below 1, a non-Normal blend mode, or an active
// soft mask is actually in effect. When none of those apply, compositing
// an offscreen-rendered buffer "over" the backdrop at full alpha with
// Normal blending and no mask produces pixel-for-pixel the same result
// as painting the group's DrawOps directly - so doForm skips the extra
// offscreen render entirely in that (by far the most common) case.
func needsIsolatedGroupComposite(st *graphics.State) bool {
	return st.FillAlpha < 1 || st.BlendMode != graphics.BlendNormal || st.SoftMask != nil
}

// isTransparencyGroup reports whether dict's /Group entry (if any),
// resolved, is a transparency group dictionary - i.e. has /S
// /Transparency (11.4.7.2 doesn't recognize any other /S value, but a
// /Group present with some other /S is tolerated here, like every other
// resource this package cannot make sense of, by simply not treating the
// form as a group).
func isTransparencyGroup(r pdfimage.Resolver, dict syntax.Dictionary) (bool, error) {
	gEntry, ok := dict["Group"]
	if !ok {
		return false, nil
	}
	resolved, err := resolveIfRef(r, gEntry)
	if err != nil {
		return false, err
	}
	groupDict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return false, nil
	}
	sResolved, err := resolveIfRef(r, groupDict["S"])
	if err != nil {
		return false, err
	}
	name, _ := sResolved.(syntax.Name)
	return name == "Transparency", nil
}

// doInlineForm is doForm's original, still far more common path (see the
// package doc comment's "Ordinarily" paragraph): it recursively
// interprets the form's content stream and flattens the resulting
// DrawOps directly into the calling interpreter's own DisplayList,
// clipped by both the form's own /BBox and whatever clip was already
// active where "Do" was invoked.
func (in *interpreter) doInlineForm(st *graphics.State, dict syntax.Dictionary, stream syntax.Stream, formCTM graphics.Matrix, resources syntax.Dictionary) error {
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

	// The form's initial graphics state inherits the calling state's own
	// paint parameters (fill/stroke color, alpha, blend mode, soft mask,
	// line style, text state, ...) per 8.10.2 - "Do" behaves as if the
	// form's content were embedded directly at the point of invocation,
	// bracketed by an implicit q/Q, not as if a brand-new default state
	// applied. Only CTM (formCTM, computed above) and Clips (left empty;
	// merged in below instead, once, alongside the /BBox clip) differ
	// from st itself.
	formState := st.Clone()
	formState.CTM = formCTM
	formState.Clips = nil

	nested, err := interpretFormAtDepth(ops, formState, resources, in.resolver, in.fontCache, in.formDepth+1)
	if err != nil {
		return err
	}

	// Every DrawOp the form's own content produced only carries whatever
	// clips accumulated *within* that content (from the form's own "W"/
	// "W*" operators, starting fresh - a nested interpretFormAtDepth call
	// gets its own brand-new graphics.Stack). It knows nothing about the
	// clip already active in the calling content stream at the point
	// "Do" ran, nor about the /BBox clip computed above - both must be
	// merged in here, once, rather than threading them through the
	// nested interpretation itself.
	outerAndBBox := mergeClips(st.Clips, bboxClip)
	for _, op := range nested {
		op.Clips = mergeClips(outerAndBBox, op.Clips)
		in.list = append(in.list, op)
	}
	return nil
}

// maxGroupDimension bounds the pixel width and height of the offscreen
// buffer doIsolatedGroupForm renders a transparency group into - the
// same bounded-work policy as softmask.go's maxSoftMaskDimension (and
// tilingpattern.go's maxPatternTileDimension before it), reusing that
// constant's own value: a transparency group, like a soft mask's own
// group, commonly covers a large fraction of the page.
const maxGroupDimension = maxSoftMaskDimension

// doIsolatedGroupForm implements doForm's isolated-group path (see the
// package doc comment): dict/stream are the group form XObject itself;
// formCTM/resources are exactly what doForm already computed for it. It
// renders the group's content to an offscreen, fully transparent buffer
// sized to the /BBox's device-space bounding box, then emits a single
// image DrawOp painting that buffer using the *outer* st's alpha, blend
// mode, and soft mask - the ones active where "Do" was invoked, per
// 11.4.7.2, rather than whatever the group's own content stream set them
// to internally while painting into the buffer (which is why the nested
// interpretation below explicitly resets alpha/blend/soft mask to their
// initial values first: the specification requires exactly that reset
// for a group's own internal painting, precisely so the outer
// compositing step below is never applied twice).
//
// This is the concrete case a real-world file (a DJI product's PDF quick
// start guide) surfaced: a page set ExtGState ca=0 before "/Fm Do"-ing a
// decorative background/highlight group, expecting it to be fully
// transparent - but the group's own content stream immediately set its
// *own* local ca=1 (via a same-named but differently-numbered /GS0
// resource) before painting, since 11.4.7.2 guarantees that reset can
// never leak back out to affect the group's own outer compositing.
// doInlineForm's direct flattening has no way to honor that: each
// flattened DrawOp only ever remembers the nested interpreter's *own*
// FillAlpha at the moment it painted (1, after the group's internal
// reset) - never the outer ca=0 - so the "invisible" decoration rendered
// fully opaque, covering a large fraction of the page.
//
// Returns ok=false (with a nil error) only when the group's /BBox is
// missing or maps to a degenerate device-space region - doForm falls
// back to doInlineForm in that case, the same tolerant "skip, don't
// abort" response formBBoxClip's own callers already give a missing
// /BBox elsewhere.
func (in *interpreter) doIsolatedGroupForm(st *graphics.State, dict syntax.Dictionary, stream syntax.Stream, formCTM graphics.Matrix, resources syntax.Dictionary) (bool, error) {
	bbox, found, err := floatArrayEntry(in.resolver, dict, "BBox")
	if err != nil {
		return false, err
	}
	if !found || len(bbox) != 4 {
		return false, nil
	}

	// Map /BBox's four corners through formCTM to find the smallest
	// upright device-space rectangle enclosing the group - exactly
	// softmask.go's buildSoftMask does for its own mask group's BBox,
	// for the same reason (the only region worth allocating an offscreen
	// buffer for).
	x0, y0, x1, y1 := bbox[0], bbox[1], bbox[2], bbox[3]
	corners := [4][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	devMinX, devMinY := math.Inf(1), math.Inf(1)
	devMaxX, devMaxY := math.Inf(-1), math.Inf(-1)
	for _, c := range corners {
		dx, dy := formCTM.Apply(c[0], c[1])
		devMinX, devMaxX = math.Min(devMinX, dx), math.Max(devMaxX, dx)
		devMinY, devMaxY = math.Min(devMinY, dy), math.Max(devMaxY, dy)
	}
	devW, devH := devMaxX-devMinX, devMaxY-devMinY
	if !(devW > 0) || !(devH > 0) {
		// !(devW > 0) catches NaN too (every comparison against NaN is
		// false), unlike devW <= 0 - see buildSoftMask's identical check.
		return false, nil
	}

	bufW := clampGroupDimension(devW)
	bufH := clampGroupDimension(devH)
	scaleX := float64(bufW) / devW
	scaleY := float64(bufH) / devH

	// deviceToBuffer/bufferCTM mirror softmask.go's deviceToMask/maskCTM
	// exactly - device space in this project already increases downward
	// (see page.go's pageDeviceGeometry), the same direction as buffer
	// row order, so only a shift-then-scale is needed, no axis flip.
	deviceToBuffer := graphics.Matrix{A: scaleX, D: scaleY, E: -devMinX * scaleX, F: -devMinY * scaleY}
	bufferCTM := formCTM.Mul(deviceToBuffer)

	bboxClip, err := formBBoxClip(in.resolver, dict, bufferCTM)
	if err != nil {
		return false, err
	}

	samples, err := in.resolver.DecodeStream(stream)
	if err != nil {
		return false, err
	}
	ops, err := Parse(samples)
	if err != nil {
		return false, err
	}

	// The group's initial state otherwise inherits the caller's (color,
	// line style, text state, ...) exactly like doInlineForm's formState
	// does, but per 11.4.7.2 resets alpha/blend mode/soft mask to their
	// initial values for use *within* the group - see this function's
	// own doc comment for why that reset is exactly what makes the outer
	// compositing step below (not double-counting it) correct.
	groupState := st.Clone()
	groupState.CTM = bufferCTM
	groupState.Clips = nil
	groupState.FillAlpha = 1
	groupState.StrokeAlpha = 1
	groupState.BlendMode = graphics.BlendNormal
	groupState.SoftMask = nil

	nested, err := interpretFormAtDepth(ops, groupState, resources, in.resolver, in.fontCache, in.formDepth+1)
	if err != nil {
		return false, err
	}
	for i := range nested {
		nested[i].Clips = mergeClips(bboxClip, nested[i].Clips)
	}

	rendered := raster.RenderTransparent(nested, bufW, bufH)

	var p graphics.Path
	p.AppendRect([4]graphics.Point{
		{X: devMinX, Y: devMinY}, {X: devMaxX, Y: devMinY},
		{X: devMaxX, Y: devMaxY}, {X: devMinX, Y: devMaxY},
	})
	// imageToDevice maps the buffer's own unit square (matching
	// graphics.DrawOp.ImageToDevice's documented convention) to the same
	// devMinX/devMinY..devMaxX/devMaxY rectangle the buffer was sized to
	// - the inverse shape of deviceToBuffer above, expressed directly
	// rather than via Matrix.Invert since it is no more code either way.
	imageToDevice := graphics.Matrix{A: devMaxX - devMinX, D: devMaxY - devMinY, E: devMinX, F: devMinY}

	in.list = append(in.list, graphics.DrawOp{
		Path:          &p,
		Image:         rendered,
		ImageToDevice: imageToDevice,
		Clips:         st.Clips,
		Alpha:         st.FillAlpha,
		BlendMode:     st.BlendMode,
		SoftMask:      st.SoftMask,
	})
	return true, nil
}

// clampGroupDimension converts a device-space length (in pixels) to a
// group buffer pixel dimension of at least 1 and at most
// maxGroupDimension - identical to softmask.go's clampSoftMaskDimension,
// duplicated rather than shared for the same "two unrelated call sites,
// three lines long" reason given there.
func clampGroupDimension(v float64) int {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 1 {
		return 1
	}
	n := int(v + 0.5)
	if n > maxGroupDimension {
		return maxGroupDimension
	}
	return n
}

// interpretFormAtDepth is like interpretAtDepth, but starts the nested
// interpreter from a caller-supplied initial graphics state (already a
// clone of the state active where "Do" invoked this form, per 8.10.2 -
// see doInlineForm's and doIsolatedGroupForm's own formState/groupState)
// rather than interpretAtDepth's own graphics.NewState defaults. A form
// is the only construct in this package where the specification requires
// that inheritance: interpretAtDepth's other two callers - a tiling
// pattern's own content stream (tilingpattern.go) and a soft mask's own
// group (softmask.go) - are each independent of the invoking graphics
// state instead (a tiling pattern's colored cell always starts from
// PDF's own documented defaults regardless of what was selected where it
// was painted with; a soft mask group's own content is reduced to a
// luminosity/alpha value before it ever reaches Alpha/BlendMode/SoftMask
// at all), so interpretAtDepth itself is left exactly as it was rather
// than widened to take an initial state for every caller.
func interpretFormAtDepth(ops []Operator, initial *graphics.State, resources syntax.Dictionary, resolver pdfimage.Resolver, fontCache *FontCache, formDepth int) (graphics.DisplayList, error) {
	in := &interpreter{
		stack:      graphics.NewStack(initial),
		resources:  resources,
		resolver:   resolver,
		initialCTM: initial.CTM,
		fontCache:  fontCache,
		formDepth:  formDepth,
	}
	for _, op := range ops {
		if err := in.exec(op); err != nil {
			return nil, err
		}
	}
	return in.list, nil
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
