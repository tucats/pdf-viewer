package content

import (
	"github.com/tucats/pdf-viewer/internal/filter"
	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements "Do" (painting a referenced XObject) and "BI"
// (painting an already-parsed inline image - see operator.go and
// inlineimage.go for how one of those is read out of the content
// stream). Both ultimately converge on paintImage, since by the time
// either has a resolved dictionary and fully filter-decoded sample
// bytes in hand, there is no remaining difference between "an inline
// image" and "a referenced image XObject" - both are just an image to
// hand to internal/image.Decode.

// doXObject implements "Do": it looks up operands[0] (which must be a
// single Name) in in.resources's /XObject dictionary and, if found,
// decodes and paints it (for /Subtype /Image, via paintImage) or
// recursively interprets it (for /Subtype /Form, via doForm - see
// form.go). Any other outcome - the name is not found, /Resources or
// /XObject is missing, or the XObject's /Subtype is neither /Image nor
// /Form - is silently tolerated rather than treated as an error: a
// missing resource is already how this project treats an unresolvable
// name elsewhere (see, for example, "cs"/"CS").
func (in *interpreter) doXObject(st *graphics.State, operands []syntax.Object) error {
	if in.resolver == nil {
		// No resolver was supplied at all - there is structurally no way
		// to look anything up, so this is treated exactly like a missing
		// resource (see this function's doc comment) rather than an
		// error. In practice the root package's Page.Render always
		// supplies a real resolver; a nil one only occurs in tests (and
		// in fuzzing, deliberately - see FuzzParseAndInterpret), where it
		// must still never cause a nil-interface method call below.
		return nil
	}
	if len(operands) != 1 {
		return pdferror.Malformedf("\"Do\" expects exactly 1 operand, got %d", len(operands))
	}
	name, ok := operands[0].(syntax.Name)
	if !ok {
		return pdferror.Malformedf("\"Do\" operand must be a name, found %T", operands[0])
	}

	stream, found, err := in.lookupXObject(name)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	dict, err := in.resolver.ResolveDictionary(stream.Dict)
	if err != nil {
		return err
	}
	subtype, _ := dict["Subtype"].(syntax.Name)
	switch subtype {
	case "Image":
		samples, err := in.resolver.DecodeStream(stream)
		if err != nil {
			return err
		}
		return in.paintImage(st, dict, samples)
	case "Form":
		return in.doForm(st, dict, stream)
	default:
		// A dictionary with no/wrong /Subtype - unsupported, but not an
		// error; see this function's doc comment.
		return nil
	}
}

// lookupXObject resolves name within in.resources's /XObject dictionary,
// reporting found=false (with no error) for every way the lookup can
// come up empty without the content actually being malformed: no
// /Resources at all, no /XObject dictionary, no entry under name, or an
// entry that does not resolve to a stream.
func (in *interpreter) lookupXObject(name syntax.Name) (syntax.Stream, bool, error) {
	if in.resources == nil {
		return syntax.Stream{}, false, nil
	}
	xobjEntry, ok := in.resources["XObject"]
	if !ok {
		return syntax.Stream{}, false, nil
	}
	resolved, err := resolveIfRef(in.resolver, xobjEntry)
	if err != nil {
		return syntax.Stream{}, false, err
	}
	xobjDict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return syntax.Stream{}, false, pdferror.Malformedf("/Resources /XObject is not a dictionary (found %T)", resolved)
	}
	entry, ok := xobjDict[name]
	if !ok {
		return syntax.Stream{}, false, nil
	}
	resolvedEntry, err := resolveIfRef(in.resolver, entry)
	if err != nil {
		return syntax.Stream{}, false, err
	}
	stream, ok := resolvedEntry.(syntax.Stream)
	if !ok {
		return syntax.Stream{}, false, nil
	}
	return stream, true, nil
}

// doInlineImage implements "BI": inline's Dict is already normalized to
// full key names and its Raw bytes are still filter-encoded (see
// inlineimage.go), so this only needs to run the ordinary filter chain
// (internal/filter.Decode - the same function
// internal/parser.Document.DecodeStream calls for a referenced stream,
// used directly here since an inline image was never wrapped in a
// syntax.Stream in the first place) before handing off to paintImage,
// exactly like an XObject image.
func (in *interpreter) doInlineImage(st *graphics.State, inline *InlineImage) error {
	if inline == nil {
		return pdferror.Malformedf("\"BI\" operator has no parsed inline image data")
	}
	if in.resolver == nil {
		// See doXObject's identical guard: an inline image's /ColorSpace
		// (or, resolving deeper, its /SMask or /Mask) can itself contain
		// an indirect reference - unusual, and forbidden by the
		// specification for inline images specifically, but this
		// package's Parse does not reject it - and internal/image.Decode
		// needs a non-nil Resolver to safely follow one. Without a
		// resolver at all, skipping is the only safe option.
		return nil
	}
	samples, err := filter.Decode(inline.Dict, inline.Raw)
	if err != nil {
		return err
	}
	return in.paintImage(st, inline.Dict, samples)
}

// paintImage decodes one image (dict/samples already fully resolved and
// filter-decoded - the point at which a referenced XObject and an inline
// image become indistinguishable, see this file's own doc comment) via
// internal/image.Decode and appends the resulting graphics.Image as a
// DrawOp painted across the unit square st.CTM currently maps to -
// exactly the region the PDF specification defines for "Do": "the
// image's sample data shall be interpreted as if it were painted onto a
// 1x1 square in user space, ... mapped by the CTM to a region in the
// output device's coordinate space" (8.9.4). No separate image-space
// matrix beyond the CTM is needed.
func (in *interpreter) paintImage(st *graphics.State, dict syntax.Dictionary, samples []byte) error {
	img, err := pdfimage.Decode(dict, samples, pdfimage.Options{
		Resolver:  in.resolver,
		Resources: in.resources,
		FillColor: st.FillColor,
	})
	if err != nil {
		return err
	}
	in.list = append(in.list, graphics.DrawOp{
		Path:          unitSquareQuad(st.CTM),
		Image:         img,
		ImageToDevice: imageSpaceToDevice(st.CTM),
		Clips:         st.Clips,
		// Per the specification, an image XObject is a non-stroking
		// paint operation, so it uses the non-stroking ("ca") alpha
		// constant, exactly like an ordinary fill - see graphics.State.
		// FillAlpha's doc comment.
		Alpha:     st.FillAlpha,
		BlendMode: st.BlendMode,
	})
	return nil
}

// imageSpaceToDevice builds the matrix graphics.DrawOp.ImageToDevice
// documents - one that maps image space directly (top-left origin, y
// increasing downward, matching how internal/image.Decode fills
// graphics.Image.Pix row by row in PDF's own image sample order) to
// device space - from ctm, the content stream's current, ordinary PDF
// (y-up) transformation matrix.
//
// The two coordinate systems are not the same, even though both are
// nominally "the unit square [0,1]x[0,1]": the PDF specification defines
// "Do"'s image painting as mapping image space onto the unit square in
// the *current user space* (y increasing upward, section 8.9.5.1), and
// separately defines that an image's row 0 (its first stored row)
// corresponds to the *top* of that unit square - i.e. y=1, not y=0. So
// converting from "row 0 is v=0" (Image's own convention) to "row 0 is
// t=1" (the convention ctm itself expects to be applied to) requires
// flipping the v axis - v=0 (image top) must land at t=1 (unit square
// top), and v=1 (image bottom) at t=0 - before ctm's own mapping applies.
// flipV (the matrix {1,0,0,-1,0,1}, i.e. (u,v) -> (u, 1-v)) does exactly
// that; composing it with ctm via flipV.Mul(ctm) applies the flip first
// and ctm second, matching Matrix.Mul's own "applies m first, n second"
// convention.
//
// Getting this backward is a real, easy mistake to make (and was, during
// this package's own development - a content stream painting a
// two-quadrant test image rendered with its rows swapped top-to-bottom
// until this function existed): every other coordinate this package and
// internal/graphics handle is already in device space or in ctm's own
// y-up convention, so it is tempting to hand ctm to internal/raster
// unmodified the way every other DrawOp field is. Image is the one
// place in this whole pipeline where a second, different axis
// convention (row-major top-down, like an ordinary raster image file)
// is unavoidable, since that is how PDF itself defines image sample
// storage and how Go's own standard image types work.
func imageSpaceToDevice(ctm graphics.Matrix) graphics.Matrix {
	flipV := graphics.Matrix{A: 1, D: -1, F: 1}
	return flipV.Mul(ctm)
}

// unitSquareQuad returns the device-space quadrilateral that image
// space's unit square [0,1]x[0,1] maps to under ctm, in the corner order
// graphics.Path.AppendRect expects ((0,0), (1,0), (1,1), (0,1) - the same
// "x,y then x+w,y then x+w,y+h then x,y+h" order AppendRect documents,
// which for the unit square is exactly this sequence already).
func unitSquareQuad(ctm graphics.Matrix) *graphics.Path {
	unit := [4][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	var corners [4]graphics.Point
	for i, c := range unit {
		x, y := ctm.Apply(c[0], c[1])
		corners[i] = graphics.Point{X: x, Y: y}
	}
	var p graphics.Path
	p.AppendRect(corners)
	return &p
}

// resolveIfRef returns obj unchanged unless it is itself a
// syntax.Reference, in which case it resolves that reference through r.
// See internal/image's identical helper (resolveIfRef there) for why
// this small pattern is duplicated per package rather than shared.
func resolveIfRef(r pdfimage.Resolver, obj syntax.Object) (syntax.Object, error) {
	ref, ok := obj.(syntax.Reference)
	if !ok {
		return obj, nil
	}
	return r.Resolve(ref.Number)
}
