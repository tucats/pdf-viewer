package pdfviewer_test

import "testing"

// TestRenderFormXObject exercises Page.Render's Form XObject support end
// to end: tools/genfixtures/main.go's buildFormXObject invokes a Form
// whose content overflows its own /BBox, translated via "cm" - only the
// intersection of the /BBox and that translation (a 40x40 red square
// centered on the page, at device (30,30)-(70,70) - see that function's
// doc comment for the hand-derived geometry) should actually be
// painted, with the rest of the page left as the untouched white
// background.
func TestRenderFormXObject(t *testing.T) {
	img := renderFixture(t, "form-xobject.pdf")
	assertPixel(t, img, 50, 50, 255, 0, 0)     // deep interior of the clipped square: red
	assertPixel(t, img, 31, 31, 255, 0, 0)     // just inside the clip corner: red
	assertPixel(t, img, 10, 10, 255, 255, 255) // outside the form's /BBox: untouched white
	assertPixel(t, img, 90, 90, 255, 255, 255) // outside the form's /BBox: untouched white
}
