package main

import "fmt"

// This file builds Phase 4 (text and fonts) fixtures. Every one of them
// embeds the same tiny synthetic TrueType program (buildTestFontProgram,
// in truetype.go) rather than a real font file - see that file's doc
// comment, and tools/genfixtures' own package doc comment, for why: a
// hand-built font program keeps this project's whole test corpus free
// of any externally sourced binary with its own license and provenance
// question, exactly like every other fixture here.
//
// The embedded test font has exactly one visible glyph: glyph index 1,
// a square occupying glyph-space [150,850]x[150,850] (out of a 1000-unit
// em - see glyphSquareMin/Max in truetype.go), reachable as character
// code 'A' (0x41) through the font's cmap. At a chosen font size Tfs and
// text position (tx,ty), that glyph's rendered device-space bounding box
// works out to a simple, exactly computable rectangle: with a page whose
// MediaBox starts at (0,0) and Render's default scale of 1 device pixel
// per point, showing 'A' at "tx ty Td" with size Tfs paints the square
// [tx + 0.15*Tfs, tx + 0.85*Tfs] horizontally, and (after the page's
// standard y-axis flip - see page.go's pageDeviceGeometry) [pageHeight -
// ty - 0.85*Tfs, pageHeight - ty - 0.15*Tfs] vertically. Each fixture
// below documents its own worked-out numbers so the corresponding
// pdfviewer_text_test.go assertions are traceable back to this formula
// rather than "trust the magic numbers."

// buildTextSimpleTrueType returns a single 100x100-point page showing
// 'A' in red at font size 100 with the text origin at the page origin
// ("0 0 Td"), using a simple (/Subtype /TrueType) font with an embedded
// TrueType program - Phase 4's baseline rendering fixture, the text
// counterpart to Phase 2's buildFilledRect. Working through this file's
// doc comment formula with Tfs=100, tx=ty=0, pageHeight=100: the
// rendered square should span device x:[15,85], y:[15,85] (this
// particular size/position happens to make the y-flipped box coincide
// with its own un-flipped bounds, since the glyph's [0.15,0.85] extent
// is symmetric about the page's own center).
func buildTextSimpleTrueType() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nBT\n/F1 100 Tf\n0 0 Td\n(A) Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /TrueType /BaseFont /GenfixturesSquare "+
		"/FirstChar 65 /LastChar 65 /Widths [1000] /Encoding /WinAnsiEncoding "+
		"/FontDescriptor 6 0 R >>", nil)
	b.addObject(6, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 7 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(7, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	return b.finish(1)
}

// buildTextScaled returns a single 200x100-point page showing two 'A's
// with the same embedded font at two different sizes ("Tf" changed mid
// text object) and repositioned with "Td" between them - exercising
// font-size scaling and text-position tracking together in one fixture:
//
//   - First 'A' at size 40, text origin (0,0): square spans
//     text-space [6,34]x[6,34], i.e. device x:[6,34], y:[66,94] (after
//     the y-flip against this fixture's 100-point-tall page).
//   - "60 0 Td" then moves the *line* origin to (60,0) (not
//     relative to wherever the first glyph's advance left the text
//     matrix - Td always moves relative to the line origin, see
//     internal/content/text.go's moveTextLine); the second 'A' at size
//     80 spans text-space [72,128]x[72,128], i.e. device x:[72,128],
//     y:[-28,28] - deliberately extending above the page's own top edge
//     (y<0), which is a perfectly valid thing for a content stream to
//     do (PDF content is not confined to the page's box) and exercises
//     that this project's rasterizer clips it correctly rather than
//     misbehaving on an out-of-canvas coordinate.
func buildTextScaled() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("0 0 1 rg\nBT\n/F1 40 Tf\n0 0 Td\n(A) Tj\n/F1 80 Tf\n60 0 Td\n(A) Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /TrueType /BaseFont /GenfixturesSquare "+
		"/FirstChar 65 /LastChar 65 /Widths [1000] /Encoding /WinAnsiEncoding "+
		"/FontDescriptor 6 0 R >>", nil)
	b.addObject(6, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 7 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(7, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	return b.finish(1)
}

// buildTextType0Identity returns a single 100x100-point page showing the
// same embedded font's glyph 1 (the square), but reached through a
// composite (/Subtype /Type0, /Encoding /Identity-H) font instead of a
// simple one: the shown string is the 2-byte hex string "<0001>" (CID 1,
// which /CIDToGIDMap /Identity maps directly to glyph index 1 - the
// square). This is the Phase 4 counterpart to buildTextSimpleTrueType
// for the *other* way PDF text can reference glyphs, and - showing "A"
// at the same size and position - should render an identical image to
// buildTextSimpleTrueType's, letting a test assert the two fixtures
// produce the same pixels despite going through entirely different
// internal/fonts code paths (simple.go vs. cid.go).
func buildTextType0Identity() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nBT\n/F1 100 Tf\n0 0 Td\n<0001> Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /Type0 /BaseFont /GenfixturesSquare-Identity "+
		"/Encoding /Identity-H /DescendantFonts [6 0 R] >>", nil)
	b.addObject(6, 0, "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /GenfixturesSquare "+
		"/DW 1000 /W [1 [1000]] /CIDToGIDMap /Identity /FontDescriptor 7 0 R >>", nil)
	b.addObject(7, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 8 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(8, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	return b.finish(1)
}

// buildTextType0EmbeddedCMap returns a single 100x100-point page
// structurally identical to buildTextType0Identity above - same
// embedded TrueType program, same rendered square, same size and
// position - except its /Encoding is an embedded CMap *stream* (object
// 6) instead of the literal name "Identity-H", exercising Phase 9's
// CMap parsing end to end: the shown string is the 2-byte hex string
// "<1234>", an arbitrary code this fixture's own CMap maps to CID 1 via
// a single "begincidrange" entry (which /CIDToGIDMap /Identity then
// maps directly to glyph index 1, the square - same as
// buildTextType0Identity's CID 1). Rendering this fixture should
// therefore produce pixel-identical output to buildTextType0Identity's,
// despite going through cid.go's embedded-CMap path (loadType0Encoding,
// cidcmap.go's parseCMap) rather than its Identity-H fast path.
func buildTextType0EmbeddedCMap() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nBT\n/F1 100 Tf\n0 0 Td\n<1234> Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /Type0 /BaseFont /GenfixturesSquare-CMap "+
		"/Encoding 6 0 R /DescendantFonts [7 0 R] >>", nil)

	cmap := []byte("/CIDSystemInfo << /Registry (Genfixtures) /Ordering (Custom) /Supplement 0 >> def\n" +
		"/CMapName /Genfixtures-Custom def\n" +
		"/CMapType 1 def\n" +
		"1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n" +
		"1 begincidrange\n<1234> <1234> 1\nendcidrange\n" +
		"endcmap\n")
	b.addObject(6, 0, fmt.Sprintf("<< /Length %d >>", len(cmap)), cmap)

	b.addObject(7, 0, "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /GenfixturesSquare "+
		"/DW 1000 /W [1 [1000]] /CIDToGIDMap /Identity /FontDescriptor 8 0 R >>", nil)
	b.addObject(8, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 9 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(9, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	return b.finish(1)
}

// buildTextType0PredefinedEncoding returns a single 100x100-point page
// structurally identical to buildTextType0Identity and
// buildTextType0EmbeddedCMap above - same embedded TrueType program,
// same descendant font width table - except its /Encoding is the bare
// *name* "UniGB-UCS2-H" (a real predefined CJK encoding this project
// does not bundle data for - see internal/fonts/predefined_cmap.go and
// pdfviewer.WithPredefinedCMaps) rather than an embedded CMap stream or
// "Identity-H".
//
// This fixture is deliberately used two different ways by two different
// tests:
//
//   - Rendered with no option configured at all (this project's default
//     for every document), the shown code 0x0041 cannot be resolved to a
//     CID at all (this package has no bundled UniGB-UCS2-H data, and the
//     descendant font's /W table only assigns a width to CID 1 - not
//     code/CID 0x41), so it falls back to internal/fonts' notdefGlyph
//     placeholder box, exactly like text-notdef-fallback.pdf - see
//     TestRenderType0PredefinedEncodingFallsBackToNotdefByDefault.
//   - Rendered with pdfviewer.WithPredefinedCMaps pointed at a directory
//     containing a real "UniGB-UCS2-H" file that a test itself writes
//     (mapping code 0x0041 to CID 1 - never any of Adobe's actual
//     licensed data, matching this project's fixture-independence
//     policy), it renders identically to buildTextType0Identity's square -
//     see TestRenderType0PredefinedEncodingWithConfiguredCMapSource.
func buildTextType0PredefinedEncoding() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nBT\n/F1 100 Tf\n0 0 Td\n<0041> Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /Type0 /BaseFont /GenfixturesSquare-Predefined "+
		"/Encoding /UniGB-UCS2-H /DescendantFonts [6 0 R] >>", nil)
	b.addObject(6, 0, "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /GenfixturesSquare "+
		"/DW 1000 /W [1 [1000]] /CIDToGIDMap /Identity /FontDescriptor 7 0 R >>", nil)
	b.addObject(7, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 8 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(8, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	return b.finish(1)
}

// buildTextNotdefFallback returns a single 100x100-point page showing
// 'A' at font size 100, text origin (0,0), in a simple font declaring
// /BaseFont /Helvetica with *no* /FontFile* entry at all - exercising
// this project's documented missing-glyph fallback (internal/fonts'
// notdefGlyph): since Helvetica is not embedded and this project ships
// no standard-14 font metrics or system font lookup (see
// internal/fonts' package doc comment), 'A' should render as a small
// hollow rectangle rather than either nothing or a solid block.
//
// Working through notdefGlyph's fixed geometry (margin=60,
// capHeight=660, inset=100, all in glyph space) at this fixture's
// /Widths [700] and font size 100: the outer rectangle spans text-space
// x:[6,64] y:[0,66], and the inner (hole) rectangle spans text-space
// x:[10,60] y:[10,56] - both converted to device space by this page's
// 100-point height (x unchanged, y flipped as "100 - y"): outer device
// x:[6,64] y:[34,100], inner device x:[10,60] y:[44,90].
func buildTextNotdefFallback() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nBT\n/F1 100 Tf\n0 0 Td\n(A) Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica "+
		"/FirstChar 65 /LastChar 65 /Widths [700] >>", nil)

	return b.finish(1)
}

// buildTextRotatedPage returns a page structurally identical to
// buildRotatedPage (a 100x200-point /MediaBox with /Rotate 90 - see
// that function's doc comment for the general rotation setup this
// reuses), but instead of a filled rectangle, its content shows the
// embedded square-glyph font's 'A' at font size 80 near the unrotated
// page's own origin. This is Phase 4's end-to-end confirmation that
// text painting composes correctly with page rotation (established in
// Phase 2 - see page.go's pageDeviceGeometry).
//
// Working through pageDeviceGeometry's own math by hand, the same way
// buildRotatedPage's doc comment does: with this fixture's 100x200
// MediaBox and /Rotate 90, the resulting content-transformation matrix
// works out to (x_device, y_device) = (y_user, x_user) - a coordinate
// swap (see pdfviewer_text_test.go for the full derivation). The glyph
// square this content shows (font size 80, "0 0 Td") spans user-space
// x:[12,68] and y:[12,68] - the *same* range on both axes, since the
// embedded test glyph is itself a symmetric square and the text
// position is the origin - so swapping x and y leaves the expected
// device-space bounding box unchanged at x:[12,68] y:[12,68], letting
// the test assert exact pixels despite the rotation, not just "some ink
// landed somewhere."
// buildTextToUnicodeSimple returns a single 100x100-point page showing
// three characters ("ABC") in a simple TrueType font at font size 10,
// text origin (0,0) - Phase 10's simple-font text-extraction fixture.
// Only code 65 ('A') actually reaches a real glyph through the embedded
// test font's cmap (buildTestFontProgram only maps 'A' - see this file's
// own package doc comment); codes 66/67 ('B','C') render as
// internal/fonts' notdefGlyph placeholder. That does not matter here,
// since this fixture exists to exercise Page.Text, not Render: text
// extraction never looks at a glyph's outline at all (see
// internal/content's ExtractText doc comment), only at /Widths and
// /ToUnicode - both of which this fixture defines for all three codes.
//
// Its /ToUnicode CMap deliberately exercises both of the beginbfrange
// shapes tounicode.go's Phase 10 parser supports, plus a plain bfchar,
// so a single fixture's worth of end-to-end coverage touches every
// grammar shape internal/fonts/tounicode_test.go otherwise only tests
// in isolation:
//
//   - beginbfchar: code 65 ('A') -> "A" directly.
//   - beginbfrange (array form): codes 66-67 ('B','C') -> an explicit
//     two-element destination array, rather than an arithmetic
//     increment.
//
// Each glyph is 1000 units wide (1 em) per its own /Widths entry, so at
// font size 10 each advances the pen by exactly 10 page-space units:
// 'A' at x=0, 'B' at x=10, 'C' at x=20, all with baseline y=0 (this
// fixture's "0 0 Td").
func buildTextToUnicodeSimple() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("BT\n/F1 10 Tf\n0 0 Td\n(ABC) Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /TrueType /BaseFont /GenfixturesSquare "+
		"/FirstChar 65 /LastChar 67 /Widths [1000 1000 1000] /Encoding /WinAnsiEncoding "+
		"/FontDescriptor 6 0 R /ToUnicode 9 0 R >>", nil)
	b.addObject(6, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 7 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(7, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	toUnicode := []byte("1 beginbfchar\n<41> <0041>\nendbfchar\n" +
		"1 beginbfrange\n<42> <43> [<0042> <0043>]\nendbfrange\n")
	b.addObject(9, 0, fmt.Sprintf("<< /Length %d >>", len(toUnicode)), toUnicode)

	return b.finish(1)
}

// buildTextToUnicodeType0 is buildTextToUnicodeSimple's Type0/CID
// counterpart - Phase 10's other required exit-criteria fixture
// (docs/PLAN2.md's Phase 10: "a fixture with known text content (both a
// simple-font and a Type0/CID fixture)"). Structurally identical to
// buildTextType0Identity (same embedded TrueType program, same
// descendant font, same Identity-H encoding, same "0 0 Td" at font size
// 100 showing CID 1), plus a /ToUnicode CMap on the Type0 font
// dictionary itself mapping code 1 - the same raw code Identity-H also
// happens to use as the CID, though /ToUnicode is keyed by the *code*,
// never the CID (see internal/fonts/tounicode.go's own doc comment) - to
// U+5B57 ("字", a real CJK character), a deliberately non-ASCII,
// non-coincidental destination: nothing about this fixture's rendering
// path could produce this specific rune by accident, so a test asserting
// it proves the /ToUnicode CMap was actually parsed and consulted.
func buildTextToUnicodeType0() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nBT\n/F1 100 Tf\n0 0 Td\n<0001> Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /Type0 /BaseFont /GenfixturesSquare-Identity "+
		"/Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 9 0 R >>", nil)
	b.addObject(6, 0, "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /GenfixturesSquare "+
		"/DW 1000 /W [1 [1000]] /CIDToGIDMap /Identity /FontDescriptor 7 0 R >>", nil)
	b.addObject(7, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 8 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(8, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	toUnicode := []byte("1 beginbfchar\n<0001> <5B57>\nendbfchar\n")
	b.addObject(9, 0, fmt.Sprintf("<< /Length %d >>", len(toUnicode)), toUnicode)

	return b.finish(1)
}

func buildTextRotatedPage() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 200] /Rotate 90 "+
		"/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nBT\n/F1 80 Tf\n0 0 Td\n(A) Tj\nET\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	b.addObject(5, 0, "<< /Type /Font /Subtype /TrueType /BaseFont /GenfixturesSquare "+
		"/FirstChar 65 /LastChar 65 /Widths [1000] /Encoding /WinAnsiEncoding "+
		"/FontDescriptor 6 0 R >>", nil)
	b.addObject(6, 0, "<< /Type /FontDescriptor /FontName /GenfixturesSquare /Flags 32 /FontFile2 7 0 R >>", nil)

	program := buildTestFontProgram()
	b.addObject(7, 0, fmt.Sprintf("<< /Length %d >>", len(program)), program)

	return b.finish(1)
}
