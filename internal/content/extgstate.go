package content

import (
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements "gs" (11.6.4.2): applying a named /Resources
// /ExtGState resource's parameters to the current graphics state. Of the
// several parameters an ExtGState dictionary may carry (line width,
// dash pattern, font, soft mask, ...), /ca, /CA, /BM, and /SMask are
// implemented - the constant alpha, blend mode, and soft mask this
// project's transparency support (graphics.State.FillAlpha/StrokeAlpha/
// BlendMode/SoftMask) actually uses; every other key is silently
// ignored, matching this package's general tolerance for parameters it
// does not yet interpret. /SMask's own dictionary-building logic (
// rendering the mask's transparency group and reducing it to a
// per-pixel luminosity or alpha value) lives in softmask.go, since it is
// substantial enough to deserve its own file - this file just resolves
// the operand and hands the value off.

// applyExtGState implements "gs": operands must be a single Name naming
// an /ExtGState resource. An unresolvable name (no /Resources, no
// /ExtGState dictionary, or no entry under name) is tolerated, not an
// error - this project's usual "missing resource" policy (see, for
// example, doXObject).
func (in *interpreter) applyExtGState(st *graphics.State, operands []syntax.Object) error {
	if len(operands) != 1 {
		return pdferror.Malformedf("\"gs\" expects exactly 1 operand, got %d", len(operands))
	}
	name, ok := operands[0].(syntax.Name)
	if !ok {
		return pdferror.Malformedf("\"gs\" operand must be a name, found %T", operands[0])
	}

	dict, found, err := in.lookupExtGState(name)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	if v, ok := dict["ca"]; ok {
		if f, ok, err := in.resolveNumber(v); err != nil {
			return err
		} else if ok {
			st.FillAlpha = clamp01(f)
		}
	}
	if v, ok := dict["CA"]; ok {
		if f, ok, err := in.resolveNumber(v); err != nil {
			return err
		} else if ok {
			st.StrokeAlpha = clamp01(f)
		}
	}
	if v, ok := dict["BM"]; ok {
		mode, err := in.resolveBlendMode(v)
		if err != nil {
			return err
		}
		st.BlendMode = mode
	}
	if v, ok := dict["SMask"]; ok {
		if err := in.applySoftMask(st, v, name); err != nil {
			return err
		}
	}
	return nil
}

// lookupExtGState resolves name within in.resources's /ExtGState
// dictionary, reporting found=false (with no error) for every way the
// lookup can come up empty without the content actually being malformed
// - mirroring lookupXObject/lookupFont/lookupShadingDict.
func (in *interpreter) lookupExtGState(name syntax.Name) (syntax.Dictionary, bool, error) {
	if in.resources == nil || in.resolver == nil {
		return nil, false, nil
	}
	entry, ok := in.resources["ExtGState"]
	if !ok {
		return nil, false, nil
	}
	resolved, err := resolveIfRef(in.resolver, entry)
	if err != nil {
		return nil, false, err
	}
	extGStateDict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return nil, false, pdferror.Malformedf("/Resources /ExtGState is not a dictionary (found %T)", resolved)
	}
	gsEntry, ok := extGStateDict[name]
	if !ok {
		return nil, false, nil
	}
	return in.dictionaryOrStreamDict(gsEntry)
}

// resolveNumber resolves obj (following an indirect reference) into a
// float64, reporting ok=false rather than an error for a non-numeric
// value - a malformed /ca or /CA entry is tolerated (the alpha simply
// stays whatever it was before this "gs") rather than aborting the
// whole render over one bad ExtGState parameter, consistent with this
// package's general tolerance for one bad field within an otherwise
// usable resource (see, for example, internal/model's identical
// tolerance for a malformed /Rotate).
func (in *interpreter) resolveNumber(obj syntax.Object) (float64, bool, error) {
	resolved, err := resolveIfRef(in.resolver, obj)
	if err != nil {
		return 0, false, err
	}
	v, ok := numberValue(resolved)
	return v, ok, nil
}

// resolveBlendMode resolves obj - a single Name, or (per the
// specification, so a viewer can pick the first one it supports) an
// Array of them - into a graphics.BlendMode. Every recognized name maps
// to the matching separable BlendMode constant (see that type's doc
// comment for which modes this project implements); every unrecognized
// name (including the four non-separable modes, and anything malformed)
// resolves to graphics.BlendNormal, exactly as the specification
// requires ("If the blend mode ... is not supported ..., the
// application shall use Normal") - this function therefore never
// itself returns an error for content that merely names an unsupported
// blend mode, only for a structurally malformed /BM entry (neither a
// name nor an array) or a resolver failure.
func (in *interpreter) resolveBlendMode(obj syntax.Object) (graphics.BlendMode, error) {
	resolved, err := resolveIfRef(in.resolver, obj)
	if err != nil {
		return graphics.BlendNormal, err
	}
	switch v := resolved.(type) {
	case syntax.Name:
		return blendModeByName(v), nil
	case syntax.Array:
		for _, elem := range v {
			resolvedElem, err := resolveIfRef(in.resolver, elem)
			if err != nil {
				return graphics.BlendNormal, err
			}
			if name, ok := resolvedElem.(syntax.Name); ok {
				if mode, ok := recognizedBlendMode(name); ok {
					return mode, nil
				}
			}
		}
		return graphics.BlendNormal, nil
	default:
		return graphics.BlendNormal, pdferror.Malformedf("/BM is neither a name nor an array (found %T)", resolved)
	}
}

// blendModeByName is recognizedBlendMode without the "found" bool - used
// for a bare Name /BM, where an unrecognized name simply means Normal
// (there is no second candidate to fall back to the way an Array of
// names has).
func blendModeByName(name syntax.Name) graphics.BlendMode {
	mode, _ := recognizedBlendMode(name)
	return mode
}

// recognizedBlendMode maps a PDF blend mode name (11.3.5's Table 136) to
// its graphics.BlendMode constant, reporting ok=false for a name this
// project does not implement - the four non-separable modes
// (HSL/Color-based: Hue, Saturation, Color, Luminosity) plus the
// separable modes beyond the six this project chose to implement
// (ColorDodge, ColorBurn, HardLight, SoftLight, Overlay - all
// individually implementable by the same "one formula per channel"
// pattern as the six that are, but omitted so this project's blend mode
// support has a small, clearly documented boundary rather than
// mechanically transcribing the specification's entire table) - see
// graphics.BlendMode's own doc comment.
func recognizedBlendMode(name syntax.Name) (graphics.BlendMode, bool) {
	switch name {
	case "Normal", "Compatible":
		return graphics.BlendNormal, true
	case "Multiply":
		return graphics.BlendMultiply, true
	case "Screen":
		return graphics.BlendScreen, true
	case "Darken":
		return graphics.BlendDarken, true
	case "Lighten":
		return graphics.BlendLighten, true
	case "Difference":
		return graphics.BlendDifference, true
	case "Exclusion":
		return graphics.BlendExclusion, true
	default:
		return graphics.BlendNormal, false
	}
}
