package content

import (
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

	dict, found, err := in.lookupShadingDict(name)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	sh, err := in.buildShading(dict, st.CTM)
	if err != nil {
		return err
	}
	in.list = append(in.list, graphics.DrawOp{Shading: sh, Clips: st.Clips})
	return nil
}

// lookupShadingDict resolves name within in.resources's /Shading
// dictionary, reporting found=false (with no error) for every way the
// lookup can come up empty without the content actually being malformed
// - mirroring lookupXObject/lookupFont. A shading object may be either a
// plain dictionary (always true for the axial/radial types this package
// supports) or a stream (required only for the mesh shading types this
// package does not support, which carry per-vertex data - see the
// package doc comment above); either way, its dictionary is what this
// function returns.
func (in *interpreter) lookupShadingDict(name syntax.Name) (syntax.Dictionary, bool, error) {
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
	return in.dictionaryOrStreamDict(entry)
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

// buildShading turns a shading dictionary into a graphics.Shading, using
// shadingToDevice as its device mapping (supplied by each of this file's
// two callers - doShading and resolvePatternPaint - according to their
// own, different rules for what that mapping should be).
func (in *interpreter) buildShading(dict syntax.Dictionary, shadingToDevice graphics.Matrix) (*graphics.Shading, error) {
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

	var kind graphics.ShadingKind
	var wantCoords int
	switch int(typeNum) {
	case 2:
		kind, wantCoords = graphics.AxialShading, 4
	case 3:
		kind, wantCoords = graphics.RadialShading, 6
	default:
		return nil, pdferror.Unsupportedf("shading type %d (only axial [2] and radial [3] shadings are supported)", int(typeNum))
	}

	coordsVals, found, err := floatArrayEntry(in.resolver, dict, "Coords")
	if err != nil {
		return nil, err
	}
	if !found || len(coordsVals) != wantCoords {
		return nil, pdferror.Malformedf("shading /Coords must have %d entries for shading type %d, has %d", wantCoords, int(typeNum), len(coordsVals))
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

// resolvePatternPaint resolves name within in.resources's /Pattern
// dictionary and, if it names a shading pattern (/PatternType 2), builds
// the graphics.Shading it should paint with - see colorspace.go's
// setPaintColor, which calls this for a "sc"/"scn"/"SC"/"SCN" operand
// ending in a Name.
//
// The returned Shading's ShadingToDevice combines the pattern's own
// /Matrix (default identity) with in.initialCTM, *not* the current,
// possibly-"cm"-mutated CTM: per the specification (8.7.3.1), "the
// pattern matrix maps pattern space to the default (initial) coordinate
// system of the pattern's parent content stream" - deliberately
// independent of the graphics state in effect at the moment the pattern
// is selected or later used to paint, which is what makes a pattern look
// the same regardless of what transform happens to be active wherever it
// is used.
//
// Every failure mode - no /Resources, no /Pattern dictionary, no entry
// under name, a tiling pattern (/PatternType 1, not yet implemented), or
// an unsupported/malformed shading - returns an error wrapping
// ErrUnsupported or ErrMalformed as appropriate, matching this
// function's only caller's expectation that a pattern name it cannot
// resolve to a paintable shading is a real error, not a silently
// tolerated missing resource (see setPaintColor's doc comment).
func (in *interpreter) resolvePatternPaint(name syntax.Name) (*graphics.Shading, error) {
	if in.resolver == nil || in.resources == nil {
		return nil, pdferror.Unsupportedf("pattern color space (scn/SCN with a pattern name, but no /Resources)")
	}
	patEntry, ok := in.resources["Pattern"]
	if !ok {
		return nil, pdferror.Unsupportedf("pattern color space (scn/SCN with a pattern name, but /Resources has no /Pattern dictionary)")
	}
	resolvedPatRes, err := resolveIfRef(in.resolver, patEntry)
	if err != nil {
		return nil, err
	}
	patResDict, ok := resolvedPatRes.(syntax.Dictionary)
	if !ok {
		return nil, pdferror.Malformedf("/Resources /Pattern is not a dictionary (found %T)", resolvedPatRes)
	}
	entry, ok := patResDict[name]
	if !ok {
		return nil, pdferror.Unsupportedf("pattern resource /%s not found in /Resources /Pattern", name)
	}
	patternDict, found, err := in.dictionaryOrStreamDict(entry)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, pdferror.Malformedf("pattern resource /%s is neither a dictionary nor a stream", name)
	}

	ptObj, ok := patternDict["PatternType"]
	if !ok {
		return nil, pdferror.Malformedf("pattern dictionary has no /PatternType")
	}
	resolvedPt, err := resolveIfRef(in.resolver, ptObj)
	if err != nil {
		return nil, err
	}
	ptNum, ok := numberValue(resolvedPt)
	if !ok {
		return nil, pdferror.Malformedf("/PatternType is not a number (found %T)", resolvedPt)
	}
	if int(ptNum) != 2 {
		return nil, pdferror.Unsupportedf("pattern type %d (only shading patterns [/PatternType 2] are supported; tiling patterns [/PatternType 1] remain unimplemented)", int(ptNum))
	}

	shadingObj, ok := patternDict["Shading"]
	if !ok {
		return nil, pdferror.Malformedf("shading pattern has no /Shading entry")
	}
	shadingDict, found, err := in.dictionaryOrStreamDict(shadingObj)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, pdferror.Malformedf("shading pattern's /Shading is neither a dictionary nor a stream")
	}

	patternMatrix := graphics.Identity()
	if matrixVals, found, err := floatArrayEntry(in.resolver, patternDict, "Matrix"); err != nil {
		return nil, err
	} else if found {
		if len(matrixVals) != 6 {
			return nil, pdferror.Malformedf("pattern /Matrix must have 6 entries, has %d", len(matrixVals))
		}
		patternMatrix = graphics.Matrix{
			A: matrixVals[0], B: matrixVals[1], C: matrixVals[2],
			D: matrixVals[3], E: matrixVals[4], F: matrixVals[5],
		}
	}

	return in.buildShading(shadingDict, patternMatrix.Mul(in.initialCTM))
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
