package content

import (
	"github.com/tucats/pdf-viewer/internal/diag"
	"github.com/tucats/pdf-viewer/internal/function"
	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements the "sh" operator and shading-pattern resolution
// (a "scn"/"SCN" operand naming a /PatternType 2 resource - see
// colorspace.go's setPaintColor, which calls resolvePatternPaint below).
// Both ultimately build a graphics.Shading via buildShading, which reads
// a shading dictionary's /ShadingType, /Coords, /Domain, /Extend,
// /Function (internal/function), and /ColorSpace
// (internal/image.ResolveColorSpace) - the only difference between "sh"
// and a shading pattern is how each establishes the resulting Shading's
// ShadingToDevice matrix (see doShading and resolvePatternPaint
// respectively, and graphics.Shading's own doc comment for why that
// differs).
//
// Only axial (/ShadingType 2) and radial (/ShadingType 3) shadings are
// supported - the two that dominate real-world gradient content and that
// internal/graphics's own shading math (shading.go) implements directly.
// Function-based (Type 1) and mesh (Types 4-7) shadings are rejected
// with an error wrapping ErrUnsupported, naming the type, rather than
// silently misrendering or skipping them.
//
// Tiling patterns (/PatternType 1) remain unimplemented (a further
// Phase 5 sub-phase): resolvePatternPaint returns an error wrapping
// ErrUnsupported for one, exactly as it already did before shading
// patterns existed at all (see colorFromComponents' original, blanket
// pattern-name rejection, which setPaintColor now only falls back to for
// a pattern this file cannot resolve).

// doShading implements "sh": operands must be a single Name naming a
// shading resource in /Resources /Shading. On success, it appends a
// Shading DrawOp with no Path (see graphics.DrawOp.Shading's doc comment
// for why "sh" specifically leaves this nil) using the *current* CTM as
// the shading's device mapping - unlike a shading pattern (see
// resolvePatternPaint), "sh" paints in the content stream's live,
// currently-in-effect coordinate system, exactly as an ordinary fill or
// stroke would.
//
// An unresolvable name (no /Resources, no /Shading dictionary, or no
// entry under name) is tolerated, not an error - this project's usual
// "missing resource" policy (see, for example, doXObject). A shading
// this package cannot build at all - an unsupported shading type, or a
// malformed shading dictionary - propagates as a real error, since
// (unlike a missing resource) the content explicitly asked for something
// this package positively knows it cannot do correctly.
func (in *interpreter) doShading(st *graphics.State, operands []syntax.Object) error {
	if in.resolver == nil {
		return nil
	}
	if len(operands) != 1 {
		return pdferror.Malformedf("\"sh\" expects exactly 1 operand, got %d", len(operands))
	}
	name, ok := operands[0].(syntax.Name)
	if !ok {
		return pdferror.Malformedf("\"sh\" operand must be a name, found %T", operands[0])
	}

	obj, found, err := in.lookupShadingObject(name)
	if err != nil {
		return err
	}
	if !found {
		diag.Note(in.resolver, "shading %q is not in /Resources /Shading; nothing painted for this \"sh\"", name)
		return nil
	}

	sh, err := in.buildShading(obj, st.CTM)
	if err != nil {
		return err
	}
	in.list = append(in.list, graphics.DrawOp{Shading: sh, Clips: st.Clips, Alpha: st.FillAlpha, BlendMode: st.BlendMode, SoftMask: st.SoftMask})
	return nil
}

// lookupShadingObject resolves name within in.resources's /Shading
// dictionary, reporting found=false (with no error) for every way the
// lookup can come up empty without the content actually being malformed
// - mirroring lookupXObject/lookupFont. A shading object may legally be
// either a plain dictionary (the only shape the axial/radial/
// function-based types ever take) or a stream (required for the four
// mesh types, which carry their own per-vertex/per-patch data as the
// stream's bytes - see meshshading.go): unlike dictionaryOrStreamDict,
// this function does *not* discard a stream's raw bytes by reducing it to
// just its dictionary, since buildShading itself needs to tell the two
// shapes apart and read a mesh stream's data.
func (in *interpreter) lookupShadingObject(name syntax.Name) (syntax.Object, bool, error) {
	if in.resources == nil {
		return nil, false, nil
	}
	shadingEntry, ok := in.resources["Shading"]
	if !ok {
		return nil, false, nil
	}
	resolved, err := resolveIfRef(in.resolver, shadingEntry)
	if err != nil {
		return nil, false, err
	}
	shadingResDict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return nil, false, pdferror.Malformedf("/Resources /Shading is not a dictionary (found %T)", resolved)
	}
	entry, ok := shadingResDict[name]
	if !ok {
		return nil, false, nil
	}
	resolvedEntry, err := resolveIfRef(in.resolver, entry)
	if err != nil {
		return nil, false, err
	}
	return resolvedEntry, true, nil
}

// dictionaryOrStreamDict resolves obj and returns its dictionary whether
// obj itself is a plain dictionary or a stream (whose own .Dict is what
// callers of this helper want) - shading and pattern objects may
// legally be either, per the specification (see lookupShadingDict's doc
// comment), and every caller in this file wants the dictionary either
// way.
func (in *interpreter) dictionaryOrStreamDict(obj syntax.Object) (syntax.Dictionary, bool, error) {
	resolved, err := resolveIfRef(in.resolver, obj)
	if err != nil {
		return nil, false, err
	}
	switch v := resolved.(type) {
	case syntax.Dictionary:
		return v, true, nil
	case syntax.Stream:
		return v.Dict, true, nil
	default:
		return nil, false, nil
	}
}

// buildShading turns a resolved shading object - a plain dictionary
// (every shading type can be one) or a stream (required for the four
// mesh types, whose own bytes carry the packed vertex/patch data
// meshshading.go decodes) - into a graphics.Shading, using
// shadingToDevice as its device mapping (supplied by each of this file's
// two callers - doShading and resolvePatternPaint - according to their
// own, different rules for what that mapping should be). This is the one
// place that tells the two possible shapes apart, so every helper it
// calls below can assume whichever shape its own shading type actually
// requires.
func (in *interpreter) buildShading(resolvedObj syntax.Object, shadingToDevice graphics.Matrix) (*graphics.Shading, error) {
	var dict syntax.Dictionary
	var stream syntax.Stream
	isStream := false
	switch v := resolvedObj.(type) {
	case syntax.Dictionary:
		dict = v
	case syntax.Stream:
		dict, stream, isStream = v.Dict, v, true
	default:
		return nil, pdferror.Malformedf("shading is neither a dictionary nor a stream (found %T)", resolvedObj)
	}

	typeObj, ok := dict["ShadingType"]
	if !ok {
		return nil, pdferror.Malformedf("shading dictionary has no /ShadingType")
	}
	resolvedType, err := resolveIfRef(in.resolver, typeObj)
	if err != nil {
		return nil, err
	}
	typeNum, ok := numberValue(resolvedType)
	if !ok {
		return nil, pdferror.Malformedf("/ShadingType is not a number (found %T)", resolvedType)
	}

	switch int(typeNum) {
	case 1:
		return in.buildFunctionBasedShading(dict, shadingToDevice)
	case 4, 5, 6, 7:
		// A mesh shading object must be the stream form (its own bytes
		// carry the packed vertex/patch data meshshading.go decodes) -
		// checked here, once, regardless of which mesh type it turns out
		// to be.
		if !isStream {
			return nil, pdferror.Malformedf("mesh shading (type %d) must be a stream, found a plain dictionary", int(typeNum))
		}
		return in.buildMeshShading(dict, stream, int(typeNum), shadingToDevice)
	}

	return in.buildAxialOrRadialShading(dict, int(typeNum), shadingToDevice)
}

// buildAxialOrRadialShading builds the two geometric shading types
// (axial and radial) - split out from buildShading once that function
// also needed to handle the function-based and mesh types, which share
// none of this parsing (no /Coords, a differently-shaped /Domain, and so
// on - see buildFunctionBasedShading and buildMeshShading).
func (in *interpreter) buildAxialOrRadialShading(dict syntax.Dictionary, typeNum int, shadingToDevice graphics.Matrix) (*graphics.Shading, error) {
	var kind graphics.ShadingKind
	var wantCoords int
	switch typeNum {
	case 2:
		kind, wantCoords = graphics.AxialShading, 4
	case 3:
		kind, wantCoords = graphics.RadialShading, 6
	default:
		return nil, pdferror.Unsupportedf("shading type %d (only types 1-7 are supported)", typeNum)
	}

	coordsVals, found, err := floatArrayEntry(in.resolver, dict, "Coords")
	if err != nil {
		return nil, err
	}
	if !found || len(coordsVals) != wantCoords {
		return nil, pdferror.Malformedf("shading /Coords must have %d entries for shading type %d, has %d", wantCoords, typeNum, len(coordsVals))
	}
	var coords [6]float64
	copy(coords[:], coordsVals)

	domain := [2]float64{0, 1}
	if domainVals, found, err := floatArrayEntry(in.resolver, dict, "Domain"); err != nil {
		return nil, err
	} else if found {
		if len(domainVals) != 2 {
			return nil, pdferror.Malformedf("shading /Domain must have 2 entries, has %d", len(domainVals))
		}
		domain = [2]float64{domainVals[0], domainVals[1]}
	}

	var extend [2]bool
	if extendVals, found, err := boolArrayEntry(in.resolver, dict, "Extend"); err != nil {
		return nil, err
	} else if found {
		if len(extendVals) != 2 {
			return nil, pdferror.Malformedf("shading /Extend must have 2 entries, has %d", len(extendVals))
		}
		extend = [2]bool{extendVals[0], extendVals[1]}
	}

	fnObj, ok := dict["Function"]
	if !ok {
		return nil, pdferror.Malformedf("shading has no /Function")
	}
	fn, err := function.Parse(in.resolver, fnObj)
	if err != nil {
		return nil, err
	}

	csObj, ok := dict["ColorSpace"]
	if !ok {
		return nil, pdferror.Malformedf("shading has no /ColorSpace")
	}
	cs, err := pdfimage.ResolveColorSpace(in.resolver, csObj, in.resources)
	if err != nil {
		return nil, err
	}
	if fn.NumOutputs() != cs.Components() {
		return nil, pdferror.Malformedf("shading /Function produces %d output(s), /ColorSpace needs %d", fn.NumOutputs(), cs.Components())
	}

	return &graphics.Shading{
		Kind: kind, Coords: coords, Domain: domain, Extend: extend,
		ShadingToDevice: shadingToDevice,
		ColorAt: func(t float64) graphics.Color {
			out, err := fn.Eval([]float64{t})
			if err != nil {
				// A well-formed function, evaluated with the single
				// input every shading function takes, cannot fail here
				// in practice - see internal/function's Eval contract -
				// but a defensive fallback to black is cheaper and safer
				// than threading an error through graphics.Shading's
				// ColorAt signature (which every other package
				// constructing one would then also need to handle).
				return graphics.Color{}
			}
			r, g, b := cs.ToRGB(out)
			return graphics.Color{R: r, G: g, B: b}
		},
	}, nil
}

// buildFunctionBasedShading builds a Type 1 (function-based) shading
// (8.7.4.5.2): unlike axial/radial, there is no line or circle - color is
// simply the /Function's own output, evaluated directly at a 2-D (x, y)
// position drawn from a rectangular /Domain and mapped into shading space
// by an optional /Matrix (a *second*, shading-specific transform layered
// underneath shadingToDevice, distinct from anything the content stream
// itself is doing with "cm").
func (in *interpreter) buildFunctionBasedShading(dict syntax.Dictionary, shadingToDevice graphics.Matrix) (*graphics.Shading, error) {
	// Note this /Domain's shape - 4 numbers, [xmin xmax ymin ymax] - is
	// entirely different from axial/radial's 2-number [t0 t1]; they are
	// not interchangeable, which is exactly why this lives in its own
	// function rather than being folded into buildAxialOrRadialShading's
	// existing /Domain handling.
	domain := [4]float64{0, 1, 0, 1}
	if domainVals, found, err := floatArrayEntry(in.resolver, dict, "Domain"); err != nil {
		return nil, err
	} else if found {
		if len(domainVals) != 4 {
			return nil, pdferror.Malformedf("Type 1 shading /Domain must have 4 entries, has %d", len(domainVals))
		}
		domain = [4]float64{domainVals[0], domainVals[1], domainVals[2], domainVals[3]}
	}

	matrix := graphics.Identity()
	if matrixVals, found, err := floatArrayEntry(in.resolver, dict, "Matrix"); err != nil {
		return nil, err
	} else if found {
		if len(matrixVals) != 6 {
			return nil, pdferror.Malformedf("Type 1 shading /Matrix must have 6 entries, has %d", len(matrixVals))
		}
		matrix = graphics.Matrix{
			A: matrixVals[0], B: matrixVals[1], C: matrixVals[2],
			D: matrixVals[3], E: matrixVals[4], F: matrixVals[5],
		}
	}

	fnObj, ok := dict["Function"]
	if !ok {
		return nil, pdferror.Malformedf("shading has no /Function")
	}
	fn, err := function.Parse(in.resolver, fnObj)
	if err != nil {
		return nil, err
	}
	if fn.NumInputs() != 2 {
		return nil, pdferror.Malformedf("Type 1 shading /Function must take 2 inputs (x, y), takes %d", fn.NumInputs())
	}

	csObj, ok := dict["ColorSpace"]
	if !ok {
		return nil, pdferror.Malformedf("shading has no /ColorSpace")
	}
	cs, err := pdfimage.ResolveColorSpace(in.resolver, csObj, in.resources)
	if err != nil {
		return nil, err
	}
	if fn.NumOutputs() != cs.Components() {
		return nil, pdferror.Malformedf("shading /Function produces %d output(s), /ColorSpace needs %d", fn.NumOutputs(), cs.Components())
	}

	return &graphics.Shading{
		Kind: graphics.FunctionBasedShading, Domain2: domain, Matrix: matrix,
		ShadingToDevice: shadingToDevice,
		ColorAt2: func(x, y float64) graphics.Color {
			out, err := fn.Eval([]float64{x, y})
			if err != nil {
				// See buildAxialOrRadialShading's identical fallback: a
				// function validated to take exactly 2 inputs, called with
				// exactly 2 inputs, cannot fail here in practice.
				return graphics.Color{}
			}
			r, g, b := cs.ToRGB(out)
			return graphics.Color{R: r, G: g, B: b}
		},
	}, nil
}

// resolvePatternPaint resolves name within in.resources's /Pattern
// dictionary and builds whichever paint source it names: a
// graphics.Shading for a shading pattern (/PatternType 2 - the returned
// *graphics.TilingPattern is nil), or a *graphics.TilingPattern for a
// tiling pattern (/PatternType 1, built by buildTilingPattern below -
// the returned *graphics.Shading is nil). Exactly one of the two return
// values is non-nil on success; colorspace.go's setPaintColor (this
// function's only caller, for a "sc"/"scn"/"SC"/"SCN" operand ending in
// a Name) assigns whichever one to the matching graphics.State field.
//
// Both pattern types share one rule this function applies once, up
// front: the pattern's own /Matrix (default identity) is combined with
// in.initialCTM, *not* the current, possibly-"cm"-mutated CTM - per the
// specification (8.7.3.1), "the pattern matrix maps pattern space to the
// default (initial) coordinate system of the pattern's parent content
// stream", deliberately independent of the graphics state in effect at
// the moment the pattern is selected or later used to paint, which is
// what makes a pattern look the same regardless of what transform
// happens to be active wherever it is used.
//
// Every failure mode - no /Resources, no /Pattern dictionary, no entry
// under name, an unsupported pattern type, or a malformed/unsupported
// shading or tiling pattern - returns an error wrapping ErrUnsupported
// or ErrMalformed as appropriate, matching this function's only
// caller's expectation that a pattern name it cannot resolve to a
// paintable result is a real error, not a silently tolerated missing
// resource (see setPaintColor's doc comment).
func (in *interpreter) resolvePatternPaint(name syntax.Name) (*graphics.Shading, *graphics.TilingPattern, error) {
	if in.resolver == nil || in.resources == nil {
		return nil, nil, pdferror.Unsupportedf("pattern color space (scn/SCN with a pattern name, but no /Resources)")
	}
	patEntry, ok := in.resources["Pattern"]
	if !ok {
		return nil, nil, pdferror.Unsupportedf("pattern color space (scn/SCN with a pattern name, but /Resources has no /Pattern dictionary)")
	}
	resolvedPatRes, err := resolveIfRef(in.resolver, patEntry)
	if err != nil {
		return nil, nil, err
	}
	patResDict, ok := resolvedPatRes.(syntax.Dictionary)
	if !ok {
		return nil, nil, pdferror.Malformedf("/Resources /Pattern is not a dictionary (found %T)", resolvedPatRes)
	}
	entry, ok := patResDict[name]
	if !ok {
		return nil, nil, pdferror.Unsupportedf("pattern resource /%s not found in /Resources /Pattern", name)
	}
	resolvedEntry, err := resolveIfRef(in.resolver, entry)
	if err != nil {
		return nil, nil, err
	}

	// A pattern object is a dictionary for a shading pattern
	// (/PatternType 2 - no sample data of its own beyond what its
	// /Shading entry separately holds) but a *stream* for a tiling
	// pattern (/PatternType 1 - the stream's own bytes are the
	// pattern cell's content stream, which buildTilingPattern needs in
	// addition to the dictionary) - both shapes are captured here so
	// whichever branch below actually runs has what it needs, without
	// this package's usual dictionaryOrStreamDict helper (which
	// deliberately discards a stream's Raw bytes, useful for every
	// other object this package looks up this way, but not for a
	// tiling pattern specifically).
	var patternDict syntax.Dictionary
	var patternStream syntax.Stream
	isStream := false
	switch v := resolvedEntry.(type) {
	case syntax.Dictionary:
		patternDict = v
	case syntax.Stream:
		patternDict, patternStream, isStream = v.Dict, v, true
	default:
		return nil, nil, pdferror.Malformedf("pattern resource /%s is neither a dictionary nor a stream", name)
	}

	ptObj, ok := patternDict["PatternType"]
	if !ok {
		return nil, nil, pdferror.Malformedf("pattern dictionary has no /PatternType")
	}
	resolvedPt, err := resolveIfRef(in.resolver, ptObj)
	if err != nil {
		return nil, nil, err
	}
	ptNum, ok := numberValue(resolvedPt)
	if !ok {
		return nil, nil, pdferror.Malformedf("/PatternType is not a number (found %T)", resolvedPt)
	}

	patternMatrix := graphics.Identity()
	if matrixVals, found, err := floatArrayEntry(in.resolver, patternDict, "Matrix"); err != nil {
		return nil, nil, err
	} else if found {
		if len(matrixVals) != 6 {
			return nil, nil, pdferror.Malformedf("pattern /Matrix must have 6 entries, has %d", len(matrixVals))
		}
		patternMatrix = graphics.Matrix{
			A: matrixVals[0], B: matrixVals[1], C: matrixVals[2],
			D: matrixVals[3], E: matrixVals[4], F: matrixVals[5],
		}
	}
	// Per the specification, a pattern's own /Matrix is always defined
	// relative to the *default* coordinate system of the content stream
	// that named it (in.initialCTM) - both pattern types share this
	// exact rule; see buildShading's caller here and buildTilingPattern
	// for where each uses it.
	patternToDevice := patternMatrix.Mul(in.initialCTM)

	switch int(ptNum) {
	case 2:
		shadingObj, ok := patternDict["Shading"]
		if !ok {
			return nil, nil, pdferror.Malformedf("shading pattern has no /Shading entry")
		}
		resolvedShading, err := resolveIfRef(in.resolver, shadingObj)
		if err != nil {
			return nil, nil, err
		}
		sh, err := in.buildShading(resolvedShading, patternToDevice)
		return sh, nil, err
	case 1:
		if !isStream {
			return nil, nil, pdferror.Malformedf("tiling pattern (/PatternType 1) is not a stream")
		}
		tiling, err := in.buildTilingPattern(patternDict, patternStream, patternToDevice)
		return nil, tiling, err
	default:
		return nil, nil, pdferror.Unsupportedf("pattern type %d (only tiling [1] and shading [2] patterns are supported)", int(ptNum))
	}
}

// floatArrayEntry resolves dict[key] (following a top-level reference,
// then each element's own reference) into a []float64, reporting
// found=false (with no error) when the key is simply absent - the
// several shading/pattern dictionary arrays this file reads (/Coords,
// /Domain, /Extend is handled separately by boolArrayEntry, /Matrix) are
// all either required (the caller checks found itself) or have a
// documented default the caller substitutes when found is false.
func floatArrayEntry(r pdfimage.Resolver, dict syntax.Dictionary, key syntax.Name) ([]float64, bool, error) {
	obj, ok := dict[key]
	if !ok {
		return nil, false, nil
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, false, err
	}
	arr, ok := resolved.(syntax.Array)
	if !ok {
		return nil, false, pdferror.Malformedf("/%s is not an array (found %T)", key, resolved)
	}
	out := make([]float64, len(arr))
	for i, e := range arr {
		re, err := resolveIfRef(r, e)
		if err != nil {
			return nil, false, err
		}
		v, ok := numberValue(re)
		if !ok {
			return nil, false, pdferror.Malformedf("/%s entry %d is not a number (found %T)", key, i, re)
		}
		out[i] = v
	}
	return out, true, nil
}

// boolArrayEntry is floatArrayEntry's counterpart for a Boolean array -
// used only for /Extend, the one shading dictionary array whose elements
// the specification defines as booleans rather than numbers.
func boolArrayEntry(r pdfimage.Resolver, dict syntax.Dictionary, key syntax.Name) ([]bool, bool, error) {
	obj, ok := dict[key]
	if !ok {
		return nil, false, nil
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, false, err
	}
	arr, ok := resolved.(syntax.Array)
	if !ok {
		return nil, false, pdferror.Malformedf("/%s is not an array (found %T)", key, resolved)
	}
	out := make([]bool, len(arr))
	for i, e := range arr {
		re, err := resolveIfRef(r, e)
		if err != nil {
			return nil, false, err
		}
		b, ok := re.(syntax.Boolean)
		if !ok {
			return nil, false, pdferror.Malformedf("/%s entry %d is not a boolean (found %T)", key, i, re)
		}
		out[i] = bool(b)
	}
	return out, true, nil
}
