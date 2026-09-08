package content

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// separationResources builds a /Resources dictionary with one
// /ColorSpace entry, "CS0": a Separation space over DeviceRGB whose tint
// transform ramps 0 (tint) -> white, 1 -> pure red - so "1 scn" after
// "/CS0 cs" should paint red, not the DeviceGray-by-component-count guess
// colorFromComponents would otherwise make for a single numeric operand.
func separationResources() syntax.Dictionary {
	tintFn := syntax.Dictionary{
		"FunctionType": syntax.Integer(2), "Domain": syntax.Array{syntax.Real(0), syntax.Real(1)},
		"C0": syntax.Array{syntax.Real(1), syntax.Real(1), syntax.Real(1)},
		"C1": syntax.Array{syntax.Real(1), syntax.Real(0), syntax.Real(0)},
	}
	return syntax.Dictionary{
		"ColorSpace": syntax.Dictionary{
			"CS0": syntax.Array{syntax.Name("Separation"), syntax.Name("Spot1"), syntax.Name("DeviceRGB"), tintFn},
		},
	}
}

func TestInterpretCsScnUsesResolvedSeparationColorSpace(t *testing.T) {
	ops, err := Parse([]byte("/CS0 cs 1 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), separationResources(), &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	want := graphics.Color{R: 1, G: 0, B: 0}
	if list[0].Color != want {
		t.Fatalf("fill color = %+v, want %+v (resolved Separation tint transform)", list[0].Color, want)
	}
}

// TestInterpretScnFallsBackWhenComponentCountMismatches confirms that
// selecting a 1-component color space (Separation) but then supplying 3
// numeric operands (as if it were DeviceRGB - malformed content, but not
// a reason to error out) falls back to colorFromComponents' own
// count-based guess rather than misusing the resolved color space or
// panicking on a length mismatch.
func TestInterpretScnFallsBackWhenComponentCountMismatches(t *testing.T) {
	ops, err := Parse([]byte("/CS0 cs 0.2 0.4 0.6 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), separationResources(), &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	want := graphics.Color{R: 0.2, G: 0.4, B: 0.6}
	if list[0].Color != want {
		t.Fatalf("fill color = %+v, want %+v (component-count fallback)", list[0].Color, want)
	}
}

// TestInterpretCsWithUnresolvableNameLeavesColorSpaceNil confirms an
// unresolvable "cs" name is tolerated (matching this package's general
// "missing resource" policy - see lookupXObject/lookupFont), and that
// sc/scn afterward still falls back to component-count guessing rather
// than erroring or using a stale color space from an earlier "cs".
func TestInterpretCsWithUnresolvableNameLeavesColorSpaceNil(t *testing.T) {
	ops, err := Parse([]byte("/NotThere cs 0.5 0.5 0.5 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	want := graphics.Color{R: 0.5, G: 0.5, B: 0.5}
	if list[0].Color != want {
		t.Fatalf("fill color = %+v, want %+v", list[0].Color, want)
	}
}

// TestInterpretCsPatternDoesNotResolveAsOrdinaryColorSpace confirms
// "cs Pattern" records the pattern marker (not a pdfimage.ColorSpace),
// so a subsequent "scn" with numeric operands (rather than the pattern
// name this package still rejects - see colorFromComponents) falls
// through to the ordinary count-based guess instead of a type-assertion
// panic.
func TestInterpretCsPatternDoesNotResolveAsOrdinaryColorSpace(t *testing.T) {
	ops, err := Parse([]byte("/Pattern cs 1 0 0 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	want := graphics.Color{R: 1, G: 0, B: 0}
	if list[0].Color != want {
		t.Fatalf("fill color = %+v, want %+v", list[0].Color, want)
	}
}

func TestInterpretCsWrongOperandCountIsMalformed(t *testing.T) {
	ops, err := Parse([]byte("/A /B cs"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Interpret(ops, graphics.Identity(), nil, &fakeResolver{}); err == nil {
		t.Fatalf("Interpret with 2 operands to \"cs\": want an error, got nil")
	}
}

// TestInterpretCsAndCSAreIndependent confirms "cs" (fill) and "CS"
// (stroke) select independent color spaces - exactly like FillColor and
// StrokeColor already are - by selecting a Separation space for fill and
// DeviceRGB for stroke and painting both with one "B" (fill-then-stroke)
// operator.
func TestInterpretCsAndCSAreIndependent(t *testing.T) {
	ops, err := Parse([]byte("/CS0 cs 1 scn /DeviceRGB CS 0.2 0.4 0.6 SCN 0 0 10 10 re B"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), separationResources(), &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2 (fill, then stroke)", len(list))
	}
	wantFill := graphics.Color{R: 1, G: 0, B: 0}
	if list[0].Color != wantFill {
		t.Fatalf("fill color = %+v, want %+v (fill color space, Separation)", list[0].Color, wantFill)
	}
	wantStroke := graphics.Color{R: 0.2, G: 0.4, B: 0.6}
	if list[1].Color != wantStroke {
		t.Fatalf("stroke color = %+v, want %+v (stroke color space, DeviceRGB)", list[1].Color, wantStroke)
	}
}
