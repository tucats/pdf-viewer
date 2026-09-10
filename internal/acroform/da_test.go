package acroform

import "testing"

func TestParseDASimple(t *testing.T) {
	got, ok := ParseDA([]byte("/Helv 12 Tf 0 g"))
	if !ok {
		t.Fatalf("ParseDA reported ok=false for valid /DA")
	}
	want := DefaultAppearance{FontName: "Helv", FontSize: 12}
	if got != want {
		t.Fatalf("ParseDA = %+v, want %+v", got, want)
	}
}

func TestParseDAAutoSizeZero(t *testing.T) {
	got, ok := ParseDA([]byte("/Helv 0 Tf 0 g"))
	if !ok {
		t.Fatalf("ParseDA reported ok=false")
	}
	if got.FontSize != 0 {
		t.Fatalf("FontSize = %v, want 0 (auto)", got.FontSize)
	}
}

// TestParseDAColorOperatorOrderDoesNotMatter confirms ParseDA only cares
// about "Tf", regardless of whether the color operator precedes or
// follows it - real producers vary in which order they emit these.
func TestParseDAColorOperatorOrderDoesNotMatter(t *testing.T) {
	got, ok := ParseDA([]byte("0 0 1 rg /Arial,Bold 14.5 Tf"))
	if !ok {
		t.Fatalf("ParseDA reported ok=false")
	}
	want := DefaultAppearance{FontName: "Arial,Bold", FontSize: 14.5}
	if got != want {
		t.Fatalf("ParseDA = %+v, want %+v", got, want)
	}
}

// TestParseDALastTfWins confirms a malformed /DA with more than one "Tf"
// resolves to the last one, matching sequential operator application.
func TestParseDALastTfWins(t *testing.T) {
	got, _ := ParseDA([]byte("/Helv 12 Tf /Times 20 Tf"))
	if got.FontName != "Times" || got.FontSize != 20 {
		t.Fatalf("ParseDA = %+v, want the second Tf", got)
	}
}

func TestParseDANoTfReportsNotOK(t *testing.T) {
	if _, ok := ParseDA([]byte("0 g")); ok {
		t.Fatalf("ParseDA with no Tf operator reported ok=true")
	}
}

func TestParseDAEmptyReportsNotOK(t *testing.T) {
	if _, ok := ParseDA(nil); ok {
		t.Fatalf("ParseDA(nil) reported ok=true")
	}
}

func TestParseDAMalformedReportsNotOK(t *testing.T) {
	// An unterminated literal string is not valid content-stream syntax.
	if _, ok := ParseDA([]byte("(unterminated /Helv 12 Tf")); ok {
		t.Fatalf("ParseDA with malformed syntax reported ok=true")
	}
}

// TestParseDAWrongOperandTypesIgnored confirms a "Tf" whose operands are
// not (name, number) - not valid per the specification - is simply
// skipped rather than accepted with garbage values.
func TestParseDAWrongOperandTypesIgnored(t *testing.T) {
	if _, ok := ParseDA([]byte("12 /Helv Tf")); ok {
		t.Fatalf("ParseDA accepted swapped Tf operand types")
	}
	// A well-formed Tf following the malformed one should still be found.
	got, ok := ParseDA([]byte("12 /Helv Tf /Helv 10 Tf"))
	if !ok || got.FontName != "Helv" || got.FontSize != 10 {
		t.Fatalf("ParseDA = %+v, ok=%v", got, ok)
	}
}
