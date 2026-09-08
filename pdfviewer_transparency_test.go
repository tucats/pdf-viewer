package pdfviewer_test

import "testing"

// This file tests Page.Render's output for Phase 5's constant-alpha and
// blend-mode work ("gs"'s /ca and /BM ExtGState parameters) - see
// tools/genfixtures/main.go's buildAlphaFill and buildBlendMultiply doc
// comments for exactly what each fixture paints and why.

// TestRenderAlphaFill exercises "gs"'s /ca: a black fill at 50% alpha
// over a white background should land at approximately 50% gray.
func TestRenderAlphaFill(t *testing.T) {
	img := renderFixture(t, "alpha-fill.pdf")
	r, g, b, _ := rgba8(img, 50, 50)
	if r < 100 || r > 155 || g < 100 || g > 155 || b < 100 || b > 155 {
		t.Fatalf("pixel (50,50) = (%d,%d,%d), want approximately (127,127,127)", r, g, b)
	}
}

// TestRenderBlendMultiply exercises "gs"'s /BM: the right half of the
// page (50% gray Multiply-blended onto an already-50%-gray background)
// should be visibly darker than the untouched left half.
func TestRenderBlendMultiply(t *testing.T) {
	img := renderFixture(t, "blend-multiply.pdf")
	leftR, _, _, _ := rgba8(img, 25, 50)
	rightR, _, _, _ := rgba8(img, 75, 50)
	if rightR >= leftR {
		t.Fatalf("right-half R = %d, left-half R = %d; want right darker (Multiply blend)", rightR, leftR)
	}
	if leftR < 115 || leftR > 140 {
		t.Fatalf("left-half R = %d, want approximately 127 (untouched 50%% gray)", leftR)
	}
	if rightR < 50 || rightR > 80 {
		t.Fatalf("right-half R = %d, want approximately 64 (0.5*0.5*255, Multiply-blended)", rightR)
	}
}
