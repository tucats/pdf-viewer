package content

import (
	"math"
	"testing"

	"github.com/tucats/pdf-viewer/internal/fonts"
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// fakeFontResources builds a minimal /Resources dictionary naming one
// font resource ("F1") backed by dict, for tests that need Interpret to
// resolve a "Tf" operand into an actual internal/fonts.Font - see
// interpret_test.go's fakeResolver, reused here unchanged (both this
// file and image_test.go's tests live in the same package and share it).
func fakeFontResources(fontObjNum int) syntax.Dictionary {
	return syntax.Dictionary{
		"Font": syntax.Dictionary{"F1": syntax.Reference{Number: fontObjNum}},
	}
}

// nonEmbeddedSimpleFontDict is a /Subtype /Type1 font dictionary with no
// /FontFile* entry at all - internal/fonts.Load still returns a usable
// Font for this (see that package's documented fallback policy), one
// whose Glyph always returns a notdefGlyph placeholder box except for
// codes it can identify as whitespace. This is enough to exercise this
// package's text-operator wiring (advance-width math, text matrix
// updates, render modes, ...) without this package's own tests needing
// to embed a real TrueType program - see pdfviewer_text_test.go (root
// package) for full pixel-level end-to-end coverage against real
// embedded fonts.
var nonEmbeddedSimpleFontDict = syntax.Dictionary{
	"Subtype":   syntax.Name("Type1"),
	"BaseFont":  syntax.Name("Helvetica"),
	"FirstChar": syntax.Integer(0),
	"LastChar":  syntax.Integer(255),
	"Widths":    wideWidthsArray(),
}

// wideWidthsArray returns a 256-entry /Widths array, every code set to
// 1000 (1 em) except code 32 (space), left at 0 so a code-32 advance in
// tests is driven entirely by word spacing (Tw), not by the glyph's own
// width - making TestShowTextWordSpacing's assertion unambiguous.
func wideWidthsArray() syntax.Array {
	arr := make(syntax.Array, 256)
	for i := range arr {
		arr[i] = syntax.Integer(1000)
	}
	arr[32] = syntax.Integer(0)
	return arr
}

func mustInterpretWithResources(t *testing.T, src string, resources syntax.Dictionary, resolver *fakeResolver) graphics.DisplayList {
	t.Helper()
	ops, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, resolver)
	if err != nil {
		t.Fatalf("Interpret(%q): %v", src, err)
	}
	return list
}

func TestShowText_PaintsNotdefBoxAndAdvances(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	// Two glyphs ('A','B'), each 1000 units wide (1 em) at font size 10 =>
	// each should advance the text matrix by 10 user-space units, so the
	// second glyph's DrawOp should sit exactly 10 units to the right of
	// the first.
	list := mustInterpretWithResources(t, "BT\n/F1 10 Tf\n0 0 Td\n(AB) Tj\nET\n", fakeFontResources(5), resolver)
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2 (one notdef box per glyph)", len(list))
	}
	firstMinX, _, _, _, ok := list[0].Path.Bounds()
	if !ok {
		t.Fatalf("first glyph has no bounds")
	}
	secondMinX, _, _, _, ok := list[1].Path.Bounds()
	if !ok {
		t.Fatalf("second glyph has no bounds")
	}
	if math.Abs((secondMinX-firstMinX)-10) > 1e-6 {
		t.Errorf("second glyph starts %v units after the first, want 10 (1000-unit-wide glyph at font size 10)", secondMinX-firstMinX)
	}
}

func TestShowText_NoFontSelectedIsANoOp(t *testing.T) {
	list := mustInterpretWithResources(t, "BT\n0 0 Td\n(A) Tj\nET\n", nil, &fakeResolver{})
	if len(list) != 0 {
		t.Errorf("len(list) = %d, want 0 (no font selected)", len(list))
	}
}

func TestShowText_InvisibleRenderModePaintsNothing(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	list := mustInterpretWithResources(t, "BT\n/F1 10 Tf\n3 Tr\n0 0 Td\n(A) Tj\nET\n", fakeFontResources(5), resolver)
	if len(list) != 0 {
		t.Errorf("len(list) = %d, want 0 (render mode 3 is invisible)", len(list))
	}
}

// TestShowText_WordSpacingAppliesOnlyToSingleByteSpace confirms Tw
// (word spacing) advances the text position an extra amount only for
// the single-byte character code 32 - per the PDF specification, never
// for any other code, and never for a byte within a multi-byte
// (Type0) code. wideWidthsArray gives code 32 zero glyph width, so
// with Tw=50 the *entire* advance for a space should be exactly 50.
func TestShowText_WordSpacingAppliesOnlyToSingleByteSpace(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	list := mustInterpretWithResources(t, "BT\n/F1 10 Tf\n50 Tw\n0 0 Td\n( A) Tj\nET\n", fakeFontResources(5), resolver)
	// wideWidthsArray gives every visible code a notdefGlyph box (>=160
	// units wide, per notdefGlyph's minWidth), but code 32 (space) has
	// zero width and is explicitly excluded from a notdef box (see
	// Font.spaceCodes) - so only the second character ('A') should
	// produce a DrawOp at all.
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1 (space paints nothing, only 'A' does)", len(list))
	}
	minX, _, _, _, ok := list[0].Path.Bounds()
	if !ok {
		t.Fatalf("glyph has no bounds")
	}
	// The space (code 32, zero glyph width) should have advanced the
	// text position by exactly Tw (50), since its own width contributes
	// nothing; 'A's notdef box then starts at that offset plus its own
	// left margin (60, from notdefGlyph's fixed margin constant, scaled
	// by font size 10/1000 = 0.6).
	wantMinX := 50.0 + 60.0*10.0/1000.0
	if math.Abs(minX-wantMinX) > 1e-6 {
		t.Errorf("'A' glyph starts at X=%v, want %v", minX, wantMinX)
	}
}

// TestShowAdjustment_TJNumberMovesText confirms a numeric element of a
// "TJ" array shifts the text position by exactly -(amount/1000)*Tfs, in
// the direction the PDF specification defines (a positive number moves
// text backward/tighter, a negative number moves it forward/looser).
func TestShowAdjustment_TJNumberMovesText(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	// Two 'A's with a TJ adjustment of -500 (in thousandths of text
	// space) between them at font size 10: extra advance = -(-500/1000)*10 = 5,
	// on top of the first glyph's own 1000-unit-wide (=10 user units at
	// this size) advance - so the second glyph should start 10+5=15
	// units after the first.
	list := mustInterpretWithResources(t, "BT\n/F1 10 Tf\n0 0 Td\n[(A) -500 (A)] TJ\nET\n", fakeFontResources(5), resolver)
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	firstMinX, _, _, _, _ := list[0].Path.Bounds()
	secondMinX, _, _, _, _ := list[1].Path.Bounds()
	if math.Abs((secondMinX-firstMinX)-15) > 1e-6 {
		t.Errorf("second glyph starts %v units after the first, want 15", secondMinX-firstMinX)
	}
}

// TestQuoteOperator_MovesToNextLineThenShows confirms "'" is exactly
// "T* string Tj" (move to the next line using the current leading, then
// show the string) - so with a nonzero leading (TL) set beforehand, the
// glyph shown should be vertically offset from where a plain "Tj" at
// the same text position would have painted it.
func TestQuoteOperator_MovesToNextLine(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	list := mustInterpretWithResources(t, "BT\n/F1 10 Tf\n12 TL\n0 0 Td\n(A) '\nET\n", fakeFontResources(5), resolver)
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	_, minY, _, _, ok := list[0].Path.Bounds()
	if !ok {
		t.Fatalf("glyph has no bounds")
	}
	// "'" moves down by the leading (12) before showing - graphics.Identity
	// CTM means device Y tracks user Y directly, so the glyph's lower
	// edge should sit near Y = -12 + notdefGlyph's own 0 baseline, i.e.
	// strictly negative.
	if minY >= 0 {
		t.Errorf("minY = %v, want negative (T* should have moved down by the 12-unit leading before painting)", minY)
	}
}

func TestTextRenderingMatrix_Identity(t *testing.T) {
	st := graphics.NewState(graphics.Identity())
	st.FontSize = 10
	m := textRenderingMatrix(st, graphics.Identity())
	x, y := m.Apply(500, 500) // glyph space midpoint of a 1000-unit em
	if math.Abs(x-5) > 1e-9 || math.Abs(y-5) > 1e-9 {
		t.Errorf("Apply(500,500) = (%v,%v), want (5,5) (500/1000 * font size 10)", x, y)
	}
}

// TestTextRenderingMatrix_HscaleAndRise confirms horizontal scaling
// (Tz) and rise (Ts) both apply as the specification's own Trm formula
// dictates: Hscale multiplies only the X axis (as a percentage, so 200
// doubles X), Rise only translates Y.
func TestTextRenderingMatrix_HscaleAndRise(t *testing.T) {
	st := graphics.NewState(graphics.Identity())
	st.FontSize = 10
	st.Hscale = 200
	st.Rise = 3
	m := textRenderingMatrix(st, graphics.Identity())
	x, y := m.Apply(1000, 0) // one full em to the right, no vertical offset
	if math.Abs(x-20) > 1e-9 {
		t.Errorf("X = %v, want 20 (1000/1000 * font size 10 * Hscale 200%%)", x)
	}
	if math.Abs(y-3) > 1e-9 {
		t.Errorf("Y = %v, want 3 (the rise, unaffected by X-only glyph-space input)", y)
	}
}

// TestTextRenderingMatrix_RotatedCTM confirms textRenderingMatrix
// correctly composes with a rotated CTM - this project's "text
// positioning tests cover rotation" requirement (see the repository
// README's Phase 4 exit criteria) verified at the level of the actual
// matrix formula, independent of any particular glyph's shape or a
// full PDF fixture's device geometry (see pdfviewer_text_test.go for
// the complementary full end-to-end rendering coverage).
func TestTextRenderingMatrix_RotatedCTM(t *testing.T) {
	st := graphics.NewState(graphics.Matrix{A: 0, B: 1, C: -1, D: 0}) // 90-degree rotation
	st.FontSize = 10
	m := textRenderingMatrix(st, graphics.Identity())
	// A point 1 em to the right in glyph space (1000,0) becomes (10,0) in
	// text space, which a 90-degree rotation (x,y)->(-y,x) turns into
	// (0,10).
	x, y := m.Apply(1000, 0)
	if math.Abs(x) > 1e-9 || math.Abs(y-10) > 1e-9 {
		t.Errorf("Apply(1000,0) under a 90-degree-rotated CTM = (%v,%v), want (0,10)", x, y)
	}
}

// TestDecodeCodes_TwoByteVsOneByte confirms decodeCodes splits a shown
// string's bytes according to the font's own code width: one byte per
// code for an ordinary simple font, two (big-endian) for a Type0/
// Identity-H composite font - see fonts.Font.TwoByteCodes's doc comment.
func TestDecodeCodes_TwoByteVsOneByte(t *testing.T) {
	simpleFont, err := fonts.Load(nonEmbeddedSimpleFontDict, &fakeResolver{})
	if err != nil {
		t.Fatalf("Load (simple): %v", err)
	}
	if codes := decodeCodes(simpleFont, []byte{0x41, 0x42}); len(codes) != 2 || codes[0] != 0x41 || codes[1] != 0x42 {
		t.Errorf("decodeCodes (simple) = %v, want [65 66]", codes)
	}

	type0Dict := syntax.Dictionary{
		"Subtype":         syntax.Name("Type0"),
		"Encoding":        syntax.Name("Identity-H"),
		"DescendantFonts": syntax.Array{syntax.Reference{Number: 20}},
	}
	resolver := &fakeResolver{objects: map[int]syntax.Object{
		20: syntax.Dictionary{"Subtype": syntax.Name("CIDFontType2")},
	}}
	type0Font, err := fonts.Load(type0Dict, resolver)
	if err != nil {
		t.Fatalf("Load (Type0): %v", err)
	}
	if codes := decodeCodes(type0Font, []byte{0x00, 0x41, 0x01, 0x02}); len(codes) != 2 || codes[0] != 0x0041 || codes[1] != 0x0102 {
		t.Errorf("decodeCodes (Type0) = %v, want [65 258]", codes)
	}
}
