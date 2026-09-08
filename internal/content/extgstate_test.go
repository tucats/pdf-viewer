package content

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestGsSetsFillAndStrokeAlpha(t *testing.T) {
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{
			"GS0": syntax.Dictionary{"ca": syntax.Real(0.5), "CA": syntax.Real(0.25)},
		},
	}
	ops, err := Parse([]byte("/GS0 gs 0 0 10 10 re f 0 0 10 10 re S"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if list[0].Alpha != 0.5 {
		t.Fatalf("fill Alpha = %v, want 0.5", list[0].Alpha)
	}
	if list[1].Alpha != 0.25 {
		t.Fatalf("stroke Alpha = %v, want 0.25", list[1].Alpha)
	}
}

func TestGsSetsBlendMode(t *testing.T) {
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{"GS0": syntax.Dictionary{"BM": syntax.Name("Multiply")}},
	}
	ops, err := Parse([]byte("/GS0 gs 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].BlendMode != graphics.BlendMultiply {
		t.Fatalf("BlendMode = %v, want BlendMultiply", list[0].BlendMode)
	}
}

func TestGsUnrecognizedBlendModeFallsBackToNormal(t *testing.T) {
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{"GS0": syntax.Dictionary{"BM": syntax.Name("Hue")}},
	}
	ops, err := Parse([]byte("/GS0 gs 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].BlendMode != graphics.BlendNormal {
		t.Fatalf("BlendMode = %v, want BlendNormal (Hue is a non-separable mode, not implemented)", list[0].BlendMode)
	}
}

func TestGsBlendModeArrayPicksFirstRecognized(t *testing.T) {
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{
			"GS0": syntax.Dictionary{"BM": syntax.Array{syntax.Name("Hue"), syntax.Name("Screen"), syntax.Name("Normal")}},
		},
	}
	ops, err := Parse([]byte("/GS0 gs 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].BlendMode != graphics.BlendScreen {
		t.Fatalf("BlendMode = %v, want BlendScreen (first recognized in the array)", list[0].BlendMode)
	}
}

func TestGsMissingResourceIsTolerated(t *testing.T) {
	ops, err := Parse([]byte("/NotThere gs 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), nil, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret with a missing ExtGState resource: %v", err)
	}
	if list[0].Alpha != 1 {
		t.Fatalf("Alpha = %v, want 1 (default, unaffected by a missing resource)", list[0].Alpha)
	}
}

// TestGsAlphaAndBlendModeAreSavedAndRestoredByQQ confirms "ca"/"CA"/"BM"
// are part of the graphics state proper - saved by "q" and restored by
// "Q" - exactly like FillColor or LineWidth already are.
func TestGsAlphaAndBlendModeAreSavedAndRestoredByQQ(t *testing.T) {
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{"GS0": syntax.Dictionary{"ca": syntax.Real(0.5), "BM": syntax.Name("Multiply")}},
	}
	ops, err := Parse([]byte("q /GS0 gs Q 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].Alpha != 1 {
		t.Fatalf("Alpha after Q = %v, want 1 (restored to the pre-q default)", list[0].Alpha)
	}
	if list[0].BlendMode != graphics.BlendNormal {
		t.Fatalf("BlendMode after Q = %v, want BlendNormal (restored to the pre-q default)", list[0].BlendMode)
	}
}

func TestGsMalformedBMEntryIsMalformed(t *testing.T) {
	resources := syntax.Dictionary{
		"ExtGState": syntax.Dictionary{"GS0": syntax.Dictionary{"BM": syntax.Integer(5)}},
	}
	ops, err := Parse([]byte("/GS0 gs"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{}); err == nil {
		t.Fatalf("Interpret with /BM as an integer: want an error, got nil")
	}
}
