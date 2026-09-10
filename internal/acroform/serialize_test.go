package acroform

import (
	"strings"
	"testing"
)

func TestEscapeLiteralEscapesSpecialBytes(t *testing.T) {
	got := string(escapeLiteral([]byte("a(b)c\\d")))
	want := `a\(b\)c\\d`
	if got != want {
		t.Fatalf("escapeLiteral = %q, want %q", got, want)
	}
}

func TestEscapeLiteralOctalEscapesNonPrintable(t *testing.T) {
	got := string(escapeLiteral([]byte{0x01, 0xFF}))
	want := `\001\377`
	if got != want {
		t.Fatalf("escapeLiteral = %q, want %q", got, want)
	}
}

func TestEscapeLiteralPassesThroughPrintableASCII(t *testing.T) {
	got := string(escapeLiteral([]byte("Hello, World! 123")))
	if got != "Hello, World! 123" {
		t.Fatalf("escapeLiteral = %q", got)
	}
}

func TestFormatNumberTrimsTrailingZerosAndDot(t *testing.T) {
	cases := map[float64]string{
		12:     "12",
		12.5:   "12.5",
		0:      "0",
		-0.25:  "-0.25",
		100.00: "100",
	}
	for v, want := range cases {
		if got := formatNumber(v); got != want {
			t.Fatalf("formatNumber(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestNonFontOperatorsSourceDropsTfKeepsColor(t *testing.T) {
	got := string(nonFontOperatorsSource([]byte("/Helv 12 Tf 0 0 1 rg")))
	if strings.Contains(got, "Tf") {
		t.Fatalf("nonFontOperatorsSource kept Tf: %q", got)
	}
	if !strings.Contains(got, "rg") {
		t.Fatalf("nonFontOperatorsSource dropped the color operator: %q", got)
	}
}

func TestNonFontOperatorsSourceOnlyTfIsEmpty(t *testing.T) {
	got := nonFontOperatorsSource([]byte("/Helv 12 Tf"))
	if len(got) != 0 {
		t.Fatalf("nonFontOperatorsSource = %q, want empty", got)
	}
}

func TestNonFontOperatorsSourceMalformedIsEmpty(t *testing.T) {
	got := nonFontOperatorsSource([]byte("(unterminated"))
	if len(got) != 0 {
		t.Fatalf("nonFontOperatorsSource = %q, want empty for malformed input", got)
	}
}
