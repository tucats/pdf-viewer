package pdfviewer_test

import "testing"

// TestRenderFormFieldGeneratedTextAppearance exercises Phase 12's
// generated-text-field-appearance path end to end:
// tools/genfixtures/acroform.go's buildFormFilledNoAppearance places a
// text field with /V (A) and no /AP at all - see that function's doc
// comment for the hand-derived device-space geometry this test's
// assertions come from.
func TestRenderFormFieldGeneratedTextAppearance(t *testing.T) {
	img := renderFixture(t, "form-filled-no-appearance.pdf")

	assertPixel(t, img, 22, 80, 255, 0, 0)     // deep interior of the generated glyph: red
	assertPixel(t, img, 12, 80, 255, 255, 255) // inside the field's /Rect but outside the glyph: untouched white
	assertPixel(t, img, 5, 5, 255, 255, 255)   // far outside every field: untouched white
}

// TestRenderFormFieldGeneratedCheckboxAppearance exercises Phase 12's
// generated-checkbox-appearance path: a checked box paints a solid
// inset mark, an unchecked one paints nothing at all - see
// buildFormFilledNoAppearance's doc comment for the geometry.
func TestRenderFormFieldGeneratedCheckboxAppearance(t *testing.T) {
	img := renderFixture(t, "form-filled-no-appearance.pdf")

	assertPixel(t, img, 20, 50, 0, 0, 0)       // deep interior of the checked box's mark: black
	assertPixel(t, img, 11, 41, 255, 255, 255) // corner of the checked box's own /Rect, outside the inset mark: white
	assertPixel(t, img, 50, 50, 255, 255, 255) // interior of the unchecked box: untouched white
}
