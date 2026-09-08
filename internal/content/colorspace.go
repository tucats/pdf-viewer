package content

import (
	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements "cs"/"CS" (selecting the current fill/stroke
// color space) and the color-space-aware half of "sc"/"scn"/"SC"/"SCN"'s
// operand interpretation - see interpret.go's exec for where each
// operator is dispatched to the functions here, and colorFromComponents
// (also in interpret.go) for the component-count-guessing fallback this
// file's colorForOperandsWithSpace defers to whenever no color space was
// selected, or the selected one cannot explain the given operands.
//
// Before this file existed, "cs"/"CS" were accepted and silently
// ignored entirely (see interpret.go's original doc comment on that
// case, still true of the Device-family/pattern-name paths
// colorFromComponents alone handles): a content stream naming a
// Separation, DeviceN, Lab, or Indexed color space via /Resources
// /ColorSpace had no way to have that reflected in "sc"/"scn" colors,
// only in an *image's* /ColorSpace (which already went through
// internal/image directly). Resolving "cs"/"CS" through the same
// internal/image color-space logic (exported for exactly this purpose -
// see that package's public.go) closes that gap for non-image fills and
// strokes too.

// setColorSpace implements "cs" (fill=true) and "CS" (fill=false):
// resolves operands[0] (a single Name) to a pdfimage.ColorSpace and
// stores it on st, so a later "sc"/"scn"/"SC"/"SCN" can use it (see
// colorForOperandsWithSpace).
//
// An unresolvable name - no /Resources, no /ColorSpace entry, or a color
// space using a feature this project does not implement (a Type 4 tint
// transform function, for instance) - is tolerated exactly like every
// other unresolvable resource name in this package (see, for example,
// interpret.go's lookupXObject or text.go's lookupFont): st's color
// space is simply left nil, and sc/scn's own component-count fallback
// still produces a reasonable color for the overwhelmingly common
// DeviceGray/RGB/CMYK case. "/Pattern" (whether named as a bare literal,
// which the specification permits directly for "cs"/"CS" without a
// /Resources lookup at all, or - much less commonly - as a
// /Resources-registered name resolving to a bare "/Pattern" array) is
// recorded as patternColorSpaceSelected rather than as a resolved
// ColorSpace, since it selects an entirely different mode for
// "scn"/"SCN" (a pattern name operand, not numeric components - see
// interpret.go's own handling of that case) that this package does not
// yet fully support (Phase 5's tiling/shading pattern work).
func (in *interpreter) setColorSpace(st *graphics.State, operands []syntax.Object, fill bool) error {
	if len(operands) != 1 {
		return pdferror.Malformedf("\"cs\"/\"CS\" expects 1 operand, got %d", len(operands))
	}
	name, ok := operands[0].(syntax.Name)
	if !ok {
		return pdferror.Malformedf("\"cs\"/\"CS\" operand must be a name, found %T", operands[0])
	}

	var resolved any
	switch {
	case name == "Pattern":
		resolved = patternColorSpaceSelected
	case in.resolver != nil:
		if cs, err := pdfimage.ResolveColorSpace(in.resolver, name, in.resources); err == nil {
			resolved = cs
		}
	}
	if fill {
		st.FillColorSpace = resolved
	} else {
		st.StrokeColorSpace = resolved
	}
	return nil
}

// patternColorSpaceMarker is the type of patternColorSpaceSelected - see
// setColorSpace's doc comment for why "cs Pattern"/"CS Pattern" is
// recorded distinctly from an ordinary resolved color space rather than
// left as a bare pdfimage.ColorSpace zero value (which would be
// indistinguishable from "not yet selected").
type patternColorSpaceMarker struct{}

var patternColorSpaceSelected = patternColorSpaceMarker{}

// colorForOperandsWithSpace converts operands to a Color for "sc"/"scn"/
// "SC"/"SCN", preferring cs (the State field a prior "cs"/"CS" set - see
// setColorSpace) when it holds a resolved pdfimage.ColorSpace whose
// component count matches operands exactly, and every operand is
// numeric (a trailing pattern-name operand, or any other mismatch, is
// left for colorFromComponents to handle exactly as it already did
// before this file existed - including that function's own pattern-name
// rejection and DeviceGray/RGB/CMYK component-count guessing).
func colorForOperandsWithSpace(cs any, operands []syntax.Object) (graphics.Color, error) {
	if resolved, ok := cs.(pdfimage.ColorSpace); ok && len(operands) == resolved.Components() {
		vals := make([]float64, len(operands))
		allNumeric := true
		for i, o := range operands {
			v, ok := numberValue(o)
			if !ok {
				allNumeric = false
				break
			}
			vals[i] = v
		}
		if allNumeric {
			r, g, b := resolved.ToRGB(vals)
			return graphics.Color{R: r, G: g, B: b}, nil
		}
	}
	return colorFromComponents(operands)
}
