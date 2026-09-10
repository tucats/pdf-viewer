package content

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/diag"
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/raster"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements ExtGState's /SMask entry (11.6.4.3, "Specifying
// Soft Masks", pointing at 11.6.5.2's "Soft-Mask Dictionaries") - the
// other half of extgstate.go's "gs" handling.
//
// # What a soft mask actually is
//
// Every other way this project already knows how to make something
// partially transparent - a constant "ca"/"CA" alpha, or a per-image
// /SMask (Phase 3, see internal/image/mask.go) - uses one single number,
// or one image's own built-in transparency. An ExtGState /SMask is a
// third, more roundabout way: it says "go render this *completely
// separate* piece of content (a Form XObject named by /G, exactly like
// the ones form.go already knows how to interpret) off to the side, look
// at how bright (or how opaque) each pixel of the result turned out, and
// use *that* as a per-pixel transparency map for everything painted
// here afterward." This is how a PDF producer creates a soft-edged
// vignette, a gradient-faded watermark, or a drop shadow: the "mask
// group" paints a white-to-black (or opaque-to-transparent) gradient or
// shape, and every DrawOp painted while that mask is active gets faded
// according to it.
//
// # How this package builds one
//
// buildSoftMask (below) is deliberately built out of pieces this
// package already has, rather than inventing new machinery:
//   - The mask's /G is interpreted exactly like an ordinary Form XObject
//     (form.go's doForm) would be: its own /Matrix combines with the CTM
//     active where "gs" ran, and its /BBox becomes a clip - see
//     formBBoxClip, reused directly.
//   - The mask's content is rendered to an offscreen buffer via
//     internal/raster.RenderTransparent - the exact same function
//     tilingpattern.go already uses to render one repetition of a
//     tiling pattern's cell with real per-pixel transparency preserved.
//
// What is new here is only the "reduce the rendered result to one
// number per pixel" step (luminosityValue/alphaValue below) and picking
// where in device space that offscreen buffer should sit (see
// buildSoftMask's own comments on devMinX/devMinY/maskW/maskH).
//
// # Scope cuts (each documented in docs/capability-matrix.md too)
//
//   - /TR, the mask's own transfer function (an arbitrary PDF function
//     remapping every computed mask value before use) is not applied -
//     the raw computed luminosity/alpha is used as-is, exactly as if
//     /TR were /Identity (its documented default).
//   - True isolated/knockout transparency-group compositing for the
//     mask's own /Group is not implemented, for the same reason ordinary
//     Form XObjects do not implement it either (see form.go's own doc
//     comment) - ordinary "paint each DrawOp straight into the buffer"
//     compositing is used instead.
//   - /BC (an explicit backdrop color for a Luminosity mask, used
//     wherever the mask group itself never painted) supports only
//     DeviceGray/DeviceRGB-shaped numeric arrays (1 or 3 components),
//     matching this package's existing colorFromComponents fallback
//     (interpret.go) used elsewhere for a bare numeric color with no
//     named color space to resolve it against. A missing /BC defaults to
//     black, per the specification.

// maxSoftMaskDimension bounds the pixel width and height of the
// offscreen buffer buildSoftMask renders the mask group into - the same
// "bounded work even under a hostile or merely oversized request" policy
// tilingpattern.go's maxPatternTileDimension already enforces for a
// tiling pattern's own tile image (see that constant's doc comment for
// the general reasoning). A soft mask is picked larger than a tiling
// pattern's typical tile (2048 vs. 1024) because a mask commonly covers
// a large fraction of the whole page - rendered at too coarse a
// resolution, its edges would visibly resample blocky where the
// specification intends a smooth fade - while still being bounded well
// short of what could exhaust memory on its own.
const maxSoftMaskDimension = 2048

// applySoftMask implements /SMask's value, once extgstate.go's
// applyExtGState has already found the entry: v is either the Name
// "None" (clears any active mask - the specification's own way to say
// "stop masking"), some other Name (malformed; tolerated, see below), or
// a soft-mask dictionary to build and activate. gsName is only used for
// diagnostic messages, identifying which /Resources /ExtGState entry
// this /SMask came from.
//
// Following this package's general "missing/malformed resource" policy
// (see, for example, resolveNumber's doc comment in extgstate.go): a
// malformed /SMask value - anything that is not a Name or a Dictionary,
// or a dictionary buildSoftMask cannot make sense of (a required field
// missing or the wrong type) - is tolerated. It leaves st.SoftMask
// exactly as it already was (as if this "gs" had not mentioned /SMask at
// all) rather than either aborting the render or forcibly clearing an
// existing mask, and records a diagnostic note so the problem is
// discoverable without failing anything. Only an explicit /None, or a
// dictionary that resolves all the way to a usable graphics.SoftMask,
// actually changes st.SoftMask.
func (in *interpreter) applySoftMask(st *graphics.State, v syntax.Object, gsName syntax.Name) error {
	resolved, err := resolveIfRef(in.resolver, v)
	if err != nil {
		return err
	}
	if name, ok := resolved.(syntax.Name); ok {
		if name != "None" {
			diag.Note(in.resolver, "ExtGState %q /SMask names %q, not /None; ignoring", gsName, name)
			return nil
		}
		st.SoftMask = nil
		return nil
	}
	dict, ok := resolved.(syntax.Dictionary)
	if !ok {
		diag.Note(in.resolver, "ExtGState %q /SMask is neither a name nor a dictionary (found %T); ignoring", gsName, resolved)
		return nil
	}

	mask, err := in.buildSoftMask(dict, st.CTM)
	if err != nil {
		return err
	}
	if mask == nil {
		// buildSoftMask already recorded a diagnostic note explaining
		// which field made the dictionary unusable - see its own doc
		// comment for why "tolerate, leave st.SoftMask unchanged" is the
		// right response rather than clearing to nil here.
		return nil
	}
	st.SoftMask = mask
	return nil
}

// buildSoftMask turns dict (an already-resolved soft-mask dictionary -
// /S, /G, optionally /BC) into a *graphics.SoftMask by rendering /G's
// content offscreen and reducing it to one value per pixel, or returns
// (nil, nil) - not an error - for a dictionary that is present but
// unusable in a way this package tolerates elsewhere (see this file's
// own doc comment and applySoftMask's). ctm is the CTM active where the
// owning "gs" operator ran (st.CTM, *not* in.initialCTM) - the mask
// group's own /Matrix builds on top of it, exactly like an ordinary Form
// XObject's does in doForm, since a soft mask's group is itself just a
// Form XObject.
func (in *interpreter) buildSoftMask(dict syntax.Dictionary, ctm graphics.Matrix) (*graphics.SoftMask, error) {
	if in.formDepth >= maxFormDepth {
		return nil, pdferror.Malformedf("soft mask group nesting exceeds %d levels", maxFormDepth)
	}

	sResolved, err := resolveIfRef(in.resolver, dict["S"])
	if err != nil {
		return nil, err
	}
	subtype, _ := sResolved.(syntax.Name)
	var luminosity bool
	switch subtype {
	case "Luminosity":
		luminosity = true
	case "Alpha":
		luminosity = false
	default:
		diag.Note(in.resolver, "soft mask /S is %q, not /Luminosity or /Alpha; ignoring soft mask", subtype)
		return nil, nil
	}

	gEntry, ok := dict["G"]
	if !ok {
		diag.Note(in.resolver, "soft mask has no /G transparency group; ignoring soft mask")
		return nil, nil
	}
	gResolved, err := resolveIfRef(in.resolver, gEntry)
	if err != nil {
		return nil, err
	}
	stream, ok := gResolved.(syntax.Stream)
	if !ok {
		diag.Note(in.resolver, "soft mask /G is not a stream (found %T); ignoring soft mask", gResolved)
		return nil, nil
	}
	groupDict, err := in.resolver.ResolveDictionary(stream.Dict)
	if err != nil {
		return nil, err
	}

	// The mask group's own /Matrix (default identity) combines with the
	// CTM in effect where "gs" ran - see this function's own doc comment
	// for why that is ctm, not in.initialCTM, exactly mirroring
	// form.go's doForm.
	groupMatrix := graphics.Identity()
	if vals, found, err := floatArrayEntry(in.resolver, groupDict, "Matrix"); err != nil {
		return nil, err
	} else if found {
		if len(vals) != 6 {
			return nil, pdferror.Malformedf("soft mask group /Matrix must have 6 entries, has %d", len(vals))
		}
		groupMatrix = graphics.Matrix{A: vals[0], B: vals[1], C: vals[2], D: vals[3], E: vals[4], F: vals[5]}
	}
	groupCTM := groupMatrix.Mul(ctm)

	bbox, found, err := floatArrayEntry(in.resolver, groupDict, "BBox")
	if err != nil {
		return nil, err
	}
	if !found || len(bbox) != 4 {
		diag.Note(in.resolver, "soft mask group has no valid /BBox; ignoring soft mask")
		return nil, nil
	}

	// Map /BBox's four corners through groupCTM to find the smallest
	// upright device-space rectangle enclosing the mask group - the
	// region of the page this mask can possibly affect, and therefore
	// the only region worth allocating an offscreen buffer for (the
	// specification's own default backdrop, black/fully-transparent,
	// applies everywhere else - see graphics.SoftMask.At's doc comment).
	x0, y0, x1, y1 := bbox[0], bbox[1], bbox[2], bbox[3]
	corners := [4][2]float64{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}}
	devMinX, devMinY := math.Inf(1), math.Inf(1)
	devMaxX, devMaxY := math.Inf(-1), math.Inf(-1)
	for _, c := range corners {
		dx, dy := groupCTM.Apply(c[0], c[1])
		devMinX, devMaxX = math.Min(devMinX, dx), math.Max(devMaxX, dx)
		devMinY, devMaxY = math.Min(devMinY, dy), math.Max(devMaxY, dy)
	}
	devW, devH := devMaxX-devMinX, devMaxY-devMinY
	if !(devW > 0) || !(devH > 0) {
		// A degenerate (zero-area, or NaN/Inf from a malformed CTM) BBox
		// has no pixels to render into at all - !(devW > 0) catches NaN
		// too (every comparison against NaN is false), unlike devW <= 0.
		diag.Note(in.resolver, "soft mask group's /BBox maps to a degenerate device-space region; ignoring soft mask")
		return nil, nil
	}

	maskW := clampSoftMaskDimension(devW)
	maskH := clampSoftMaskDimension(devH)
	scaleX := float64(maskW) / devW
	scaleY := float64(maskH) / devH

	// deviceToMask maps a device-space point to this mask's own pixel
	// grid: shift so the BBox's device-space top-left corner lands at
	// (0,0), then scale so the far corner lands at (maskW,maskH) - the
	// same "shift by the box's own origin, then scale" shape
	// tilingpattern.go's cellToPatternSpace uses, just built directly in
	// the device-to-mask direction graphics.SoftMask.DeviceToMask wants,
	// rather than the mask-to-device direction an image's ImageToDevice
	// would use (see that field's own doc comment for why this
	// direction was chosen - no per-pixel matrix inversion needed later).
	deviceToMask := graphics.Matrix{A: scaleX, D: scaleY, E: -devMinX * scaleX, F: -devMinY * scaleY}
	// maskCTM is what the mask group's own content stream is actually
	// interpreted with: it maps the group's user space directly into
	// mask-buffer pixel space (groupCTM's user-space-to-device mapping,
	// followed by deviceToMask's device-to-buffer mapping) - the
	// interpretation-time counterpart to deviceToMask, which is only
	// used later, at sampling time.
	maskCTM := groupCTM.Mul(deviceToMask)

	resources := in.resources
	if resObj, ok := groupDict["Resources"]; ok {
		resolved, err := resolveIfRef(in.resolver, resObj)
		if err != nil {
			return nil, err
		}
		if resDict, ok := resolved.(syntax.Dictionary); ok {
			resources = resDict
		}
	}

	bboxClip, err := formBBoxClip(in.resolver, groupDict, maskCTM)
	if err != nil {
		return nil, err
	}

	samples, err := in.resolver.DecodeStream(stream)
	if err != nil {
		return nil, err
	}
	ops, err := Parse(samples)
	if err != nil {
		return nil, err
	}
	nested, err := interpretAtDepth(ops, maskCTM, resources, in.resolver, in.fontCache, in.formDepth+1)
	if err != nil {
		return nil, err
	}
	for i := range nested {
		nested[i].Clips = mergeClips(bboxClip, nested[i].Clips)
	}

	rendered := raster.RenderTransparent(nested, maskW, maskH)

	backdrop := graphics.Color{} // black - the specification's default when /BC is absent.
	if luminosity {
		if bcVals, found, err := floatArrayEntry(in.resolver, dict, "BC"); err != nil {
			return nil, err
		} else if found {
			backdrop = backdropColorFromComponents(bcVals)
		}
	}

	values := make([]byte, maskW*maskH)
	for i := 0; i < maskW*maskH; i++ {
		r, g, b, a := rendered.At(i%maskW, i/maskW)
		if luminosity {
			values[i] = toByte(luminosityValue(r, g, b, a, backdrop))
		} else {
			values[i] = toByte(a)
		}
	}

	return &graphics.SoftMask{Width: maskW, Height: maskH, Values: values, DeviceToMask: deviceToMask}, nil
}

// luminosityValue implements a /Luminosity soft mask's per-pixel
// reduction (11.6.5.2): the mask group's own rendered pixel (r,g,b,a),
// not yet composited against anything, is first composited "over" a
// fully opaque backdrop color (black, unless /BC said otherwise) -
// exactly the specification's "the group shall be composited with a
// fully opaque backdrop" - and *then* reduced to a single brightness
// number. Compositing before reducing (rather than just taking the
// rendered color's own luminosity and ignoring a) matters for any pixel
// the mask group's own content did not fully cover: without this step,
// a half-transparent pixel over black would be measured at its own
// (bright) painted color's luminosity instead of correctly reading as
// half as bright.
//
// The brightness weights (0.3/0.59/0.11) are the standard ITU-R BT.601
// luma coefficients - the same "eyes are most sensitive to green, least
// to blue" weighting used throughout image and video processing - not a
// PDF-specific formula; the specification itself only says "luminosity"
// without mandating one exact set of coefficients, so this project uses
// the conventional ones rather than inventing its own.
func luminosityValue(r, g, b, a float64, backdrop graphics.Color) float64 {
	outR := r*a + backdrop.R*(1-a)
	outG := g*a + backdrop.G*(1-a)
	outB := b*a + backdrop.B*(1-a)
	return 0.3*outR + 0.59*outG + 0.11*outB
}

// backdropColorFromComponents interprets /BC's numeric array as a color
// in whatever Device color space matches its component count (1 ->
// DeviceGray, 3 -> DeviceRGB) - the same by-component-count convention
// interpret.go's colorFromComponents already applies to a bare "sc"/
// "scn" operand with no resolvable named color space, reused here rather
// than duplicated with different rules, since /BC has exactly the same
// "just numbers, no way to know the real color space without further
// resolution this project does not attempt" shape. Any other component
// count (including 0, an empty array) is not a shape colorFromComponents
// recognizes either - falls back to black, matching the specification's
// own default for when /BC is absent, since there is no better default
// to fall back to for a `/BC` this project cannot interpret.
func backdropColorFromComponents(vals []float64) graphics.Color {
	switch len(vals) {
	case 1:
		return grayColor(vals[0])
	case 3:
		return graphics.Color{R: vals[0], G: vals[1], B: vals[2]}
	default:
		return graphics.Color{}
	}
}

// toByte converts a [0,1] float (clamped first, so a value already
// slightly out of range from floating-point arithmetic does not wrap or
// panic) to the 0-255 byte scale graphics.SoftMask.Values uses - the
// same rounding convention (+0.5 before truncation) internal/raster's
// own to8 helper uses for exactly the same reason, duplicated here
// rather than exported from internal/raster solely for this one caller.
func toByte(v float64) byte {
	v = clamp01(v)
	return byte(v*255 + 0.5)
}

// clampSoftMaskDimension converts a device-space length (in pixels) to a
// mask buffer pixel dimension of at least 1 (nothing smaller can be
// rasterized) and at most maxSoftMaskDimension - see that constant's own
// doc comment. Mirrors tilingpattern.go's clampTileDimension exactly,
// including guarding against a non-finite input (NaN or Inf) by falling
// back to 1 rather than producing an invalid slice allocation size -
// duplicated rather than shared because the two live in different files
// with otherwise no reason to depend on each other, and the function is
// three lines long.
func clampSoftMaskDimension(v float64) int {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 1 {
		return 1
	}
	n := int(v + 0.5)
	if n > maxSoftMaskDimension {
		return maxSoftMaskDimension
	}
	return n
}
