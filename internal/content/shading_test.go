package content

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// axialShadingDict returns a minimal, valid axial (/ShadingType 2)
// shading dictionary: a black-to-white gradient along the x-axis from
// (0,0) to (100,0), via a Type 2 (exponential interpolation) function -
// used by every test in this file that needs a real, resolvable shading.
func axialShadingDict() syntax.Dictionary {
	tintFn := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": syntax.Array{syntax.Real(0), syntax.Real(1)},
		"C0": syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(0)},
		"C1": syntax.Array{syntax.Real(1), syntax.Real(1), syntax.Real(1)},
	}
	return syntax.Dictionary{
		"ShadingType": syntax.Integer(2),
		"ColorSpace":  syntax.Name("DeviceRGB"),
		"Coords":      syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(100), syntax.Real(0)},
		"Function":    tintFn,
	}
}

func TestDoShadingAppendsShadingDrawOpWithNoPath(t *testing.T) {
	resources := syntax.Dictionary{"Shading": syntax.Dictionary{"Sh0": axialShadingDict()}}
	ops, err := Parse([]byte("/Sh0 sh"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].Shading == nil {
		t.Fatalf("list[0].Shading = nil, want a resolved Shading")
	}
	if list[0].Path != nil {
		t.Fatalf("list[0].Path = %v, want nil (\"sh\" paints the whole canvas - see DrawOp.Shading's doc comment)", list[0].Path)
	}
	col, ok := list[0].Shading.At(50, 0)
	if !ok || col.R < 0.4 || col.R > 0.6 {
		t.Fatalf("Shading.At(50,0) = (%v,%v), want approximately mid-gray (~0.5) at the gradient's midpoint", col, ok)
	}
}

// TestDoShadingUsesCurrentCTMNotInitial confirms "sh" (unlike a shading
// pattern - see TestScnShadingPatternUsesInitialCTMNotCurrent) maps
// shading space through whatever CTM is live at the moment "sh" runs.
func TestDoShadingUsesCurrentCTMNotInitial(t *testing.T) {
	resources := syntax.Dictionary{"Shading": syntax.Dictionary{"Sh0": axialShadingDict()}}
	// Translate by (1000,0) before "sh" - the shading's own (0,0)-(100,0)
	// segment should now be found at device x in [1000,1100], not
	// [0,100], proving "sh" used the post-"cm" CTM.
	ops, err := Parse([]byte("q 1 0 0 1 1000 0 cm /Sh0 sh Q"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if _, ok := list[0].Shading.At(50, 0); ok {
		t.Fatalf("At(50,0) covered - want the gradient to have moved with the CTM to x in [1000,1100]")
	}
	if col, ok := list[0].Shading.At(1050, 0); !ok || col.R < 0.4 || col.R > 0.6 {
		t.Fatalf("At(1050,0) = (%v,%v), want covered and approximately mid-gray", col, ok)
	}
}

func TestDoShadingMissingResourceIsSkipped(t *testing.T) {
	ops, err := Parse([]byte("/NotThere sh"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret with a missing shading resource: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("len(list) = %d, want 0", len(list))
	}
}

func TestDoShadingUnsupportedTypeIsError(t *testing.T) {
	dict := axialShadingDict()
	dict["ShadingType"] = syntax.Integer(1) // function-based: not implemented
	resources := syntax.Dictionary{"Shading": syntax.Dictionary{"Sh0": dict}}
	ops, err := Parse([]byte("/Sh0 sh"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Interpret with /ShadingType 1: got %v, want an error wrapping ErrUnsupported", err)
	}
}

// TestScnShadingPatternPaintsFilledShape confirms a shading pattern
// selected via "scn" is used to paint an actual filled shape (unlike
// "sh", which has no Path at all) with the pattern's own Shading.
func TestScnShadingPatternPaintsFilledShape(t *testing.T) {
	resources := syntax.Dictionary{
		"Pattern": syntax.Dictionary{
			"P0": syntax.Dictionary{"PatternType": syntax.Integer(2), "Shading": axialShadingDict()},
		},
	}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].Shading == nil {
		t.Fatalf("Shading = nil, want the resolved shading pattern")
	}
	if list[0].Path == nil {
		t.Fatalf("Path = nil, want the actual filled rectangle's geometry (unlike \"sh\")")
	}
}

// TestScnShadingPatternUsesInitialCTMNotCurrent confirms a pattern's
// device mapping is built from Interpret's initialCTM, not whatever "cm"
// has done to the current CTM by the time "scn" (or the later painting
// operator) runs - the specification's "pattern space is defined
// relative to the default coordinate system" rule.
func TestScnShadingPatternUsesInitialCTMNotCurrent(t *testing.T) {
	resources := syntax.Dictionary{
		"Pattern": syntax.Dictionary{
			"P0": syntax.Dictionary{"PatternType": syntax.Integer(2), "Shading": axialShadingDict()},
		},
	}
	// A large translation is applied via "cm" before the pattern is even
	// selected - if resolvePatternPaint mistakenly used the current CTM,
	// the shading's (0,0)-(100,0) segment would appear shifted by it.
	ops, err := Parse([]byte("q 1 0 0 1 5000 0 cm /Pattern cs /P0 scn 0 0 10 10 re f Q"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if _, ok := list[0].Shading.At(5050, 0); ok {
		t.Fatalf("At(5050,0) covered - the pattern's shading must not have moved with the live CTM")
	}
	if col, ok := list[0].Shading.At(50, 0); !ok || col.R < 0.4 || col.R > 0.6 {
		t.Fatalf("At(50,0) = (%v,%v), want covered at the shading's own (initial-CTM-relative) position", col, ok)
	}
}

func TestScnTilingPatternIsUnsupported(t *testing.T) {
	resources := syntax.Dictionary{
		"Pattern": syntax.Dictionary{
			"P0": syntax.Dictionary{"PatternType": syntax.Integer(1)},
		},
	}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Interpret with a tiling pattern: got %v, want an error wrapping ErrUnsupported", err)
	}
}

// TestScnPlainColorAfterPatternClearsShading confirms selecting an
// ordinary numeric color after a shading pattern was active clears the
// pattern - a later fill must go back to painting a solid color, not the
// stale shading.
func TestScnPlainColorAfterPatternClearsShading(t *testing.T) {
	resources := syntax.Dictionary{
		"Pattern": syntax.Dictionary{
			"P0": syntax.Dictionary{"PatternType": syntax.Integer(2), "Shading": axialShadingDict()},
		},
	}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 1 0 0 rg 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].Shading != nil {
		t.Fatalf("Shading = %v, want nil (a later \"rg\" must clear the pattern)", list[0].Shading)
	}
	if list[0].Color != (graphics.Color{R: 1}) {
		t.Fatalf("Color = %+v, want red", list[0].Color)
	}
}

// TestRgAfterShadingPatternClearsShading is the "g"/"rg"/"k" counterpart
// to TestScnPlainColorAfterPatternClearsShading: this was a real bug
// caught by writing that test - "rg" (and the other direct
// DeviceGray/RGB/CMYK color operators) originally set FillColor/
// StrokeColor without also clearing a previously-selected shading
// pattern, so a fill after "rg" would still incorrectly paint the stale
// pattern instead of the new solid color.
func TestRgAfterShadingPatternClearsShading(t *testing.T) {
	resources := syntax.Dictionary{
		"Pattern": syntax.Dictionary{
			"P0": syntax.Dictionary{"PatternType": syntax.Integer(2), "Shading": axialShadingDict()},
		},
	}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 0 1 0 rg 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].Shading != nil {
		t.Fatalf("Shading = %v, want nil (\"rg\" must clear the pattern)", list[0].Shading)
	}
	if list[0].Color != (graphics.Color{G: 1}) {
		t.Fatalf("Color = %+v, want green", list[0].Color)
	}
}
