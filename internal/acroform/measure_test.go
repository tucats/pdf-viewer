package acroform

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/fonts"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// mustLoadUniformWidthFont builds a simple Type1 font (no embedded
// program - see internal/fonts' documented "non-embedded font"
// fallback) with WinAnsiEncoding and every code from firstChar to
// lastChar given the same advance width, so measure_test.go's
// assertions can be worked out by hand rather than depending on
// internal/fonts' own default-missing-width constant.
func mustLoadUniformWidthFont(t *testing.T, firstChar, lastChar int, width float64) *fonts.Font {
	t.Helper()
	arr := make(syntax.Array, lastChar-firstChar+1)
	for i := range arr {
		arr[i] = syntax.Integer(int(width))
	}
	dict := syntax.Dictionary{
		"Subtype":   syntax.Name("Type1"),
		"BaseFont":  syntax.Name("Helvetica"),
		"Encoding":  syntax.Name("WinAnsiEncoding"),
		"FirstChar": syntax.Integer(firstChar),
		"LastChar":  syntax.Integer(lastChar),
		"Widths":    arr,
	}
	f, err := fonts.Load(dict, &fakeResolver{})
	if err != nil || f == nil {
		t.Fatalf("fonts.Load: %v", err)
	}
	return f
}

func TestMeasureWidth(t *testing.T) {
	font := mustLoadUniformWidthFont(t, 'A', 'Z', 600)
	got := measureWidth(font, []byte("AAA"), 10)
	want := 3 * (600.0 / 1000.0) * 10.0
	if got != want {
		t.Fatalf("measureWidth = %v, want %v", got, want)
	}
}

func TestMeasureWidthEmpty(t *testing.T) {
	font := mustLoadUniformWidthFont(t, 'A', 'Z', 600)
	if got := measureWidth(font, nil, 10); got != 0 {
		t.Fatalf("measureWidth(nil) = %v, want 0", got)
	}
}

func TestClampFloat(t *testing.T) {
	cases := []struct{ v, lo, hi, want float64 }{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
	}
	for _, c := range cases {
		if got := clampFloat(c.v, c.lo, c.hi); got != c.want {
			t.Fatalf("clampFloat(%v,%v,%v) = %v, want %v", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestAutoFontSizeClampsToRange(t *testing.T) {
	if got := autoFontSize(1000); got != 12 {
		t.Fatalf("autoFontSize(1000) = %v, want 12 (max)", got)
	}
	if got := autoFontSize(1); got != 4 {
		t.Fatalf("autoFontSize(1) = %v, want 4 (min)", got)
	}
}

// TestAutoFontSizeProportional confirms the mid-range (neither clamp
// active) case follows the documented 0.65*height formula exactly.
func TestAutoFontSizeProportional(t *testing.T) {
	const height = 10.0
	want := clampFloat(height*0.65, 4, 12)
	if got := autoFontSize(height); got != want {
		t.Fatalf("autoFontSize(%v) = %v, want %v", height, got, want)
	}
}

func TestShrinkToFitWidthNoShrinkNeeded(t *testing.T) {
	font := mustLoadUniformWidthFont(t, 'A', 'Z', 600)
	codes := []byte("AA") // width at size 10 = 2*0.6*10 = 12
	got := shrinkToFitWidth(font, codes, 10, 100)
	if got != 10 {
		t.Fatalf("shrinkToFitWidth = %v, want unchanged 10", got)
	}
}

func TestShrinkToFitWidthExactDivision(t *testing.T) {
	font := mustLoadUniformWidthFont(t, 'A', 'Z', 1000) // 1 em per glyph
	codes := []byte("AAAA")                             // width at size 10 = 4*10 = 40
	got := shrinkToFitWidth(font, codes, 10, 20)
	// Exact: width scales linearly with size, so the largest size that
	// fits exactly halves the original (40 -> 20 at size 5).
	if got != 5 {
		t.Fatalf("shrinkToFitWidth = %v, want 5", got)
	}
}

func TestShrinkToFitWidthClampsToMinimum(t *testing.T) {
	font := mustLoadUniformWidthFont(t, 'A', 'Z', 1000)
	codes := []byte("AAAAAAAAAA")
	got := shrinkToFitWidth(font, codes, 100, 1) // would need an absurdly tiny size
	if got != 4 {
		t.Fatalf("shrinkToFitWidth = %v, want the 4.0 floor", got)
	}
}
