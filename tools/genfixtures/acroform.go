package main

import "fmt"

// This file builds Phase 12 (AcroForm field appearance regeneration -
// docs/PLAN2.md) fixtures: form field widgets with a current value
// (/V) but deliberately *no* /AP at all, exercising internal/acroform's
// generated-appearance path rather than internal/annotation's ordinary
// "paint whatever appearance already exists" path (already covered by
// buildAnnotationAppearance in main.go).
//
// buildFormFilledNoAppearance reuses the same synthetic square-glyph
// TrueType program text.go's fixtures do (buildTestFontProgram,
// truetype.go) as the AcroForm /DR's only font, rather than a real
// Helvetica-shaped program - for the same reason: this project ships no
// external font binaries at all (see text.go's own doc comment), and a
// known, simple glyph shape lets the corresponding render test assert
// exact device-space pixel positions worked out by hand instead of
// merely "some text got painted somewhere."

// buildFormFilledNoAppearance returns a single 100x100-point page with
// three form field widgets, none of which has an /AP entry at all:
//
//   - A text field (/FT /Tx, object 5) at /Rect [10 10 90 30] (80x20)
//     with /V (A) and /DA "/Helv 20 Tf 1 0 0 rg" (red, size exactly
//     matching the field's own height). Per internal/acroform/textfield.
//     go's documented layout formula, a single-line field vertically
//     centers its text ((height-size)/2 - here, (20-20)/2 = 0, i.e. the
//     baseline sits exactly on the field's own bottom edge) and left-
//     quads (the default /Q) with a fixed 2-point left inset. Combined
//     with the embedded font's own glyph shape ([0.15,0.85] of the em
//     square, both axes, per text.go's doc comment) and font size 20,
//     the glyph's local (Form-XObject-space) extent is x:[5,19],
//     y:[3,17] - and since this field's /Rect is exactly the same size
//     as its own generated BBox (80x20), the Form-to-Rect mapping is a
//     pure translation by the /Rect's own origin (10,10), placing the
//     glyph in page space at x:[15,29], y:[13,27]. After the page's
//     standard y-flip (pageHeight=100), that becomes device x:[15,29],
//     y:[73,87].
//   - A checked checkbox (/FT /Btn, object 6) at /Rect [10 40 30 60]
//     (20x20) with /AS /Yes and /V /Yes, no /MK at all (so no border or
//     background - just the checked mark). checkbox.go insets its solid
//     mark by 20% of the box's shorter side (4 points here), so the
//     mark spans local x:[4,16], y:[4,16] - translated by the /Rect
//     origin (10,40) to page space x:[14,26], y:[44,56], and (since
//     this box is vertically close to the page's own center, 44 and 56
//     are each other's y-flip) device x:[14,26], y:[44,56] again.
//   - An unchecked checkbox (/FT /Btn, object 7) at /Rect [40 40 60 60],
//     /AS /Off, /V /Off - painting nothing at all, confirming
//     generation correctly distinguishes checked from unchecked rather
//     than always drawing a mark.
func buildFormFilledNoAppearance() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R /AcroForm 8 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << >> /Contents 4 0 R /Annots [5 0 R 6 0 R 7 0 R] >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	b.addObject(5, 0, "<< /Type /Annot /Subtype /Widget /FT /Tx /Rect [10 10 90 30] "+
		"/V (A) /DA (/Helv 20 Tf 1 0 0 rg) >>", nil)
	b.addObject(6, 0, "<< /Type /Annot /Subtype /Widget /FT /Btn /Rect [10 40 30 60] "+
		"/AS /Yes /V /Yes >>", nil)
	b.addObject(7, 0, "<< /Type /Annot /Subtype /Widget /FT /Btn /Rect [40 40 60 60] "+
		"/AS /Off /V /Off >>", nil)

	b.addObject(8, 0, "<< /Fields [5 0 R 6 0 R 7 0 R] /DR << /Font << /Helv 9 0 R >> >> >>", nil)

	b.addObject(9, 0, "<< /Type /Font /Subtype /TrueType /BaseFont /GenfixturesSquare "+
		"/FirstChar 65 /LastChar 65 /Widths [1000] /Encoding /WinAnsiEncoding "+
		"/FontDescriptor 10 0 R >>", nil)
	b.addObject(10, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 11 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(11, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	return b.finish(1)
}
