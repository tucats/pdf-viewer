package acroform

import (
	"strings"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestCheckboxCheckedFromAS(t *testing.T) {
	widget := syntax.Dictionary{"AS": syntax.Name("Yes")}
	checked, ok := checkboxChecked(&fakeResolver{}, widget, Field{})
	if !ok || !checked {
		t.Fatalf("checkboxChecked = %v, %v, want true, true", checked, ok)
	}
}

func TestCheckboxCheckedASOff(t *testing.T) {
	widget := syntax.Dictionary{"AS": syntax.Name("Off")}
	checked, ok := checkboxChecked(&fakeResolver{}, widget, Field{})
	if !ok || checked {
		t.Fatalf("checkboxChecked = %v, %v, want false, true", checked, ok)
	}
}

func TestCheckboxCheckedFallsBackToFieldValue(t *testing.T) {
	checked, ok := checkboxChecked(&fakeResolver{}, syntax.Dictionary{}, Field{V: syntax.Name("Yes")})
	if !ok || !checked {
		t.Fatalf("checkboxChecked = %v, %v, want true, true", checked, ok)
	}
}

func TestCheckboxCheckedNoBasisIsNotOK(t *testing.T) {
	if _, ok := checkboxChecked(&fakeResolver{}, syntax.Dictionary{}, Field{}); ok {
		t.Fatalf("checkboxChecked reported ok=true with no /AS or /V")
	}
}

func TestColorOperatorGrayRGBCMYK(t *testing.T) {
	cases := []struct {
		vals   []float64
		stroke bool
		want   string
	}{
		{[]float64{0}, false, "0 g"},
		{[]float64{1, 0, 0}, false, "1 0 0 rg"},
		{[]float64{0, 0, 0, 1}, false, "0 0 0 1 k"},
		{[]float64{0}, true, "0 G"},
	}
	for _, c := range cases {
		got, ok := colorOperator(c.vals, c.stroke)
		if !ok || got != c.want {
			t.Fatalf("colorOperator(%v, %v) = %q, %v, want %q", c.vals, c.stroke, got, ok, c.want)
		}
	}
}

func TestColorOperatorWrongComponentCountIsNotOK(t *testing.T) {
	if _, ok := colorOperator([]float64{1, 2}, false); ok {
		t.Fatalf("colorOperator accepted a 2-component color")
	}
}

func TestBuildCheckboxAppearanceCheckedPaintsMark(t *testing.T) {
	widget := syntax.Dictionary{"AS": syntax.Name("Yes")}
	content, _, ok := buildCheckboxAppearance(&fakeResolver{}, widget, Field{FT: "Btn"}, 20, 20)
	if !ok {
		t.Fatalf("buildCheckboxAppearance reported ok=false")
	}
	if !strings.Contains(string(content), " re f") {
		t.Fatalf("checked checkbox content has no fill: %q", content)
	}
}

func TestBuildCheckboxAppearanceUncheckedPaintsNoMark(t *testing.T) {
	widget := syntax.Dictionary{"AS": syntax.Name("Off")}
	content, _, ok := buildCheckboxAppearance(&fakeResolver{}, widget, Field{FT: "Btn"}, 20, 20)
	if !ok {
		t.Fatalf("buildCheckboxAppearance reported ok=false")
	}
	if strings.Contains(string(content), " re f") {
		t.Fatalf("unchecked checkbox still painted a fill: %q", content)
	}
}

func TestBuildCheckboxAppearancePushbuttonIsNotOK(t *testing.T) {
	widget := syntax.Dictionary{"AS": syntax.Name("Yes")}
	if _, _, ok := buildCheckboxAppearance(&fakeResolver{}, widget, Field{FT: "Btn", Ff: ffPushbutton}, 20, 20); ok {
		t.Fatalf("buildCheckboxAppearance accepted a push button")
	}
}

func TestBuildCheckboxAppearanceBorderAndBackground(t *testing.T) {
	widget := syntax.Dictionary{
		"AS": syntax.Name("Off"),
		"MK": syntax.Dictionary{
			"BC": syntax.Array{syntax.Integer(0)},
			"BG": syntax.Array{syntax.Integer(1)},
		},
	}
	content, _, ok := buildCheckboxAppearance(&fakeResolver{}, widget, Field{FT: "Btn"}, 20, 20)
	if !ok {
		t.Fatalf("buildCheckboxAppearance reported ok=false")
	}
	src := string(content)
	if !strings.Contains(src, " re S") {
		t.Fatalf("content missing border stroke: %q", src)
	}
	if !strings.Contains(src, " re f") {
		t.Fatalf("content missing background fill: %q", src)
	}
}
