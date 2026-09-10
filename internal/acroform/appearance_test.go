package acroform

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestGenerateAppearanceTextField(t *testing.T) {
	widget := syntax.Dictionary{
		"FT":   syntax.Name("Tx"),
		"Rect": syntax.Array{syntax.Integer(10), syntax.Integer(20), syntax.Integer(110), syntax.Integer(50)},
		"V":    syntax.String("Hi"),
		"DA":   syntax.String("/Helv 12 Tf 0 g"),
	}
	stream, rect, ok := GenerateAppearance(&fakeResolver{}, widget, nil)
	if !ok {
		t.Fatalf("GenerateAppearance reported ok=false")
	}
	if len(rect) != 4 || rect[0] != 10 || rect[2] != 110 {
		t.Fatalf("rect = %v", rect)
	}
	bbox, ok := stream.Dict["BBox"].(syntax.Array)
	if !ok || len(bbox) != 4 {
		t.Fatalf("BBox = %#v", stream.Dict["BBox"])
	}
	if bbox[2] != syntax.Real(100) || bbox[3] != syntax.Real(30) {
		t.Fatalf("BBox = %v, want width 100 height 30", bbox)
	}
	if stream.Dict["Subtype"] != syntax.Name("Form") {
		t.Fatalf("Subtype = %v, want Form", stream.Dict["Subtype"])
	}
	if len(stream.Raw) == 0 {
		t.Fatalf("generated stream has no content")
	}
}

func TestGenerateAppearanceSignatureFieldIsNotOK(t *testing.T) {
	widget := syntax.Dictionary{
		"FT":   syntax.Name("Sig"),
		"Rect": syntax.Array{syntax.Integer(0), syntax.Integer(0), syntax.Integer(10), syntax.Integer(10)},
	}
	if _, _, ok := GenerateAppearance(&fakeResolver{}, widget, nil); ok {
		t.Fatalf("GenerateAppearance accepted a signature field")
	}
}

func TestGenerateAppearanceMalformedRectIsNotOK(t *testing.T) {
	widget := syntax.Dictionary{"FT": syntax.Name("Tx"), "V": syntax.String("x"), "DA": syntax.String("/Helv 12 Tf 0 g")}
	if _, _, ok := GenerateAppearance(&fakeResolver{}, widget, nil); ok {
		t.Fatalf("GenerateAppearance accepted a widget with no /Rect")
	}
}

func TestGenerateAppearanceDegenerateRectIsNotOK(t *testing.T) {
	widget := syntax.Dictionary{
		"FT":   syntax.Name("Tx"),
		"Rect": syntax.Array{syntax.Integer(10), syntax.Integer(10), syntax.Integer(10), syntax.Integer(20)},
		"V":    syntax.String("x"),
		"DA":   syntax.String("/Helv 12 Tf 0 g"),
	}
	if _, _, ok := GenerateAppearance(&fakeResolver{}, widget, nil); ok {
		t.Fatalf("GenerateAppearance accepted a zero-width /Rect")
	}
}

func TestGenerateAppearanceUsesAcroFormDefaults(t *testing.T) {
	widget := syntax.Dictionary{
		"FT":   syntax.Name("Tx"),
		"Rect": syntax.Array{syntax.Integer(0), syntax.Integer(0), syntax.Integer(100), syntax.Integer(20)},
		"V":    syntax.String("Hi"),
	}
	acroForm := syntax.Dictionary{"DA": syntax.String("/Helv 10 Tf 0 g")}
	_, _, ok := GenerateAppearance(&fakeResolver{}, widget, acroForm)
	if !ok {
		t.Fatalf("GenerateAppearance did not fall back to the AcroForm /DA")
	}
}

func TestGenerateAppearanceCheckbox(t *testing.T) {
	widget := syntax.Dictionary{
		"FT":   syntax.Name("Btn"),
		"Rect": syntax.Array{syntax.Integer(0), syntax.Integer(0), syntax.Integer(20), syntax.Integer(20)},
		"AS":   syntax.Name("Yes"),
	}
	_, _, ok := GenerateAppearance(&fakeResolver{}, widget, nil)
	if !ok {
		t.Fatalf("GenerateAppearance reported ok=false for a checkbox")
	}
}
