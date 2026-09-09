package fonts

import (
	"encoding/binary"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file tests trySubstitute (simple.go) - the sub-phase 4d wiring
// that actually applies Phase 4's font-substitution matching (substitute.go)
// once a PDF font has no usable embedded outline data at all. Unlike
// substitute_test.go's TestMatchFace_* (which exercise matchFace against
// bare in-memory FontFace values with no real outline data behind them),
// this file builds one small, fully synthetic candidate sfnt file (reusing
// truetype_test.go's and probe_test.go's fixture-building helpers) so
// FontFace.Outline can actually succeed - confirming the whole pipeline
// (characterize the PDF font, match a candidate, extract its outline,
// build a working lookupGID) end to end, still with no real font file or
// filesystem access (see substitute_test.go's own doc comment on why
// that matters for this package's tests).

// buildSubstituteCandidateSfnt assembles a complete, minimal sfnt font
// (unlike directory_source_test.go's buildMinimalCandidateSfnt, which
// only needs to be probeable, not actually outline-extractable) with:
//   - a "name" table reporting family, and an "OS/2" table reporting
//     bold/italic (see probe_test.go's buildNameTable/buildOS2Table)
//   - one real glyph (a small square, gid 1; gid 0 is an empty .notdef)
//   - a "cmap" mapping the rune 'A' to gid 1
//
// enough for ProbeFontFile to characterize it, and for the chosen
// face's Outline method to then fully parse it as a real substitute.
func buildSubstituteCandidateSfnt(family string, bold, italic bool) []byte {
	var fsSelection uint16
	if bold {
		fsSelection |= fsSelectionBold
	}
	if italic {
		fsSelection |= fsSelectionItalic
	}

	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmapTable := buildCmapFormat0Table(map[rune]uint16{'A': 1})

	var glyf []byte
	loca := make([]uint32, len(glyphs)+1)
	for i, g := range glyphs {
		loca[i] = uint32(len(glyf))
		glyf = append(glyf, g...)
	}
	loca[len(glyphs)] = uint32(len(glyf))

	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:20], 1000) // unitsPerEm
	binary.BigEndian.PutUint16(head[50:52], 1)    // indexToLocFormat: long

	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:6], uint16(len(glyphs)))

	locaBuf := make([]byte, len(loca)*4)
	for i, off := range loca {
		binary.BigEndian.PutUint32(locaBuf[i*4:i*4+4], off)
	}

	hhea := buildHhea(2)
	hmtx := buildHmtx([]uint16{250, 1234}, 0) // gid 0 (.notdef): 250; gid 1 ('A' square): 1234

	tables := []sfntTable{
		{"head", head},
		{"maxp", maxp},
		{"loca", locaBuf},
		{"glyf", glyf},
		{"hhea", hhea},
		{"hmtx", hmtx},
		{"cmap", cmapTable},
		{"name", buildNameTable(map[uint16]string{nameIDFamily: family})},
		{"OS/2", buildOS2Table(400, fsSelection, 0)},
	}
	return assembleSfnt(sfntVersionTrueType, tables, 0)
}

func TestTrySubstitute_EndToEnd(t *testing.T) {
	data := buildSubstituteCandidateSfnt("Verdana", false, false)
	faces, err := ProbeFontFile(data)
	if err != nil || len(faces) != 1 {
		t.Fatalf("ProbeFontFile: %d faces, err=%v", len(faces), err)
	}

	resolver := fakeSubstitutionProviderResolver{
		source: fakeFontSource{faces: faces},
	}
	dict := syntax.Dictionary{
		"BaseFont": syntax.Name("Verdana"),
		"Subtype":  syntax.Name("TrueType"),
	}
	encoding := standardEncoding()

	f := &Font{}
	if ok := trySubstitute(f, dict, resolver, encoding); !ok {
		t.Fatal("trySubstitute reported ok=false, want a successful match")
	}
	if f.glyphSource == nil || f.lookupGID == nil {
		t.Fatal("trySubstitute did not set glyphSource/lookupGID")
	}

	gid, ok := f.lookupGID(int('A'))
	if !ok || gid != 1 {
		t.Fatalf("lookupGID('A') = (%d,%v), want (1,true)", gid, ok)
	}

	glyph := f.Glyph(int('A'))
	if glyph == nil || len(glyph.Subpaths) == 0 {
		t.Fatal("Glyph('A') returned no outline, want the substitute's real square glyph")
	}
}

func TestTrySubstitute_NoSourceConfigured(t *testing.T) {
	f := &Font{}
	dict := syntax.Dictionary{"BaseFont": syntax.Name("Verdana")}
	if trySubstitute(f, dict, fakeResolver{}, standardEncoding()) {
		t.Fatal("expected ok=false when no FontSource is configured at all")
	}
	if f.glyphSource != nil {
		t.Fatal("expected f to be left unchanged when trySubstitute fails")
	}
}

func TestTrySubstitute_NoMatchingCandidate(t *testing.T) {
	f := &Font{}
	resolver := fakeSubstitutionProviderResolver{source: fakeFontSource{}} // no candidates at all
	dict := syntax.Dictionary{"BaseFont": syntax.Name("Verdana")}
	if trySubstitute(f, dict, resolver, standardEncoding()) {
		t.Fatal("expected ok=false with no candidates to match against")
	}
}

// TestLoadSimpleFont_UsesSubstituteWhenConfigured confirms the full
// loadSimpleFont integration: a font dictionary with no /FontFile* at
// all (the reported real-world case docs/FONTS.md's audit describes -
// standard-14-style non-embedded fonts) gets a real substitute outline
// once a matching FontSource is attached, instead of always falling
// back to notdefGlyph.
func TestLoadSimpleFont_UsesSubstituteWhenConfigured(t *testing.T) {
	data := buildSubstituteCandidateSfnt("Arial", false, false)
	faces, err := ProbeFontFile(data)
	if err != nil || len(faces) != 1 {
		t.Fatalf("ProbeFontFile: %d faces, err=%v", len(faces), err)
	}

	resolver := fakeSubstitutionProviderResolver{source: fakeFontSource{faces: faces}}
	dict := syntax.Dictionary{
		"Subtype":  syntax.Name("TrueType"),
		"BaseFont": syntax.Name("Arial"),
	}

	f, err := loadSimpleFont(dict, resolver)
	if err != nil {
		t.Fatalf("loadSimpleFont: %v", err)
	}
	glyph := f.Glyph(int('A'))
	if glyph == nil || len(glyph.Subpaths) == 0 {
		t.Fatal("expected loadSimpleFont to use the substitute's real outline for 'A', got no outline")
	}
}

// TestLoadSimpleFont_FallsBackToNotdefWithoutSubstitution confirms
// loadSimpleFont's existing behavior is unchanged for a resolver with no
// SubstitutionProvider at all (every Document opened without
// WithFontSubstitution) - the reported bug's original symptom.
func TestLoadSimpleFont_FallsBackToNotdefWithoutSubstitution(t *testing.T) {
	dict := syntax.Dictionary{
		"Subtype":  syntax.Name("TrueType"),
		"BaseFont": syntax.Name("Arial"),
	}
	f, err := loadSimpleFont(dict, fakeResolver{})
	if err != nil {
		t.Fatalf("loadSimpleFont: %v", err)
	}
	// notdefGlyph draws a placeholder box for a wide-enough default
	// width; defaultMissingWidth (500) is comfortably above notdefGlyph's
	// own minWidth threshold, so this should be non-nil.
	if glyph := f.Glyph(int('A')); glyph == nil {
		t.Fatal("expected the notdefGlyph placeholder box with no substitution configured")
	}
}

// TestLoadSimpleFont_SubstituteWidthUsedWhenDictHasNoWidths confirms
// sub-phase 4e's width-from-substitute wiring end to end: a font
// dictionary with no /Widths array at all gets its advance widths from
// the chosen substitute's own "hmtx" data (buildSubstituteCandidateSfnt
// gives gid 1 - the 'A' square - an advance width of 1234, deliberately
// distinct from defaultMissingWidth's generic 500) instead of the
// generic default.
func TestLoadSimpleFont_SubstituteWidthUsedWhenDictHasNoWidths(t *testing.T) {
	data := buildSubstituteCandidateSfnt("Arial", false, false)
	faces, err := ProbeFontFile(data)
	if err != nil || len(faces) != 1 {
		t.Fatalf("ProbeFontFile: %d faces, err=%v", len(faces), err)
	}

	resolver := fakeSubstitutionProviderResolver{source: fakeFontSource{faces: faces}}
	dict := syntax.Dictionary{
		"Subtype":  syntax.Name("TrueType"),
		"BaseFont": syntax.Name("Arial"),
		// deliberately no /Widths or /FirstChar entry at all
	}

	f, err := loadSimpleFont(dict, resolver)
	if err != nil {
		t.Fatalf("loadSimpleFont: %v", err)
	}
	if w := f.Width(int('A')); w != 1234 {
		t.Errorf("Width('A') = %v, want 1234 (the substitute font's own hmtx advance width)", w)
	}
}

// TestLoadSimpleFont_PDFWidthsAlwaysWinOverSubstitute confirms the other
// half of applySubstituteWidths' precedence rule: when the PDF *does*
// supply /Widths, that data is trusted exactly as before, never
// overridden by a substitute's own advance widths (see
// docs/FONTS.md's "Widths vs. outlines" - the PDF's declared widths
// reflect what the rest of the page was actually laid out against).
func TestLoadSimpleFont_PDFWidthsAlwaysWinOverSubstitute(t *testing.T) {
	data := buildSubstituteCandidateSfnt("Arial", false, false)
	faces, err := ProbeFontFile(data)
	if err != nil || len(faces) != 1 {
		t.Fatalf("ProbeFontFile: %d faces, err=%v", len(faces), err)
	}

	resolver := fakeSubstitutionProviderResolver{source: fakeFontSource{faces: faces}}
	dict := syntax.Dictionary{
		"Subtype":   syntax.Name("TrueType"),
		"BaseFont":  syntax.Name("Arial"),
		"FirstChar": syntax.Integer(65),
		"LastChar":  syntax.Integer(65),
		"Widths":    syntax.Array{syntax.Integer(700)},
	}

	f, err := loadSimpleFont(dict, resolver)
	if err != nil {
		t.Fatalf("loadSimpleFont: %v", err)
	}
	if w := f.Width(int('A')); w != 700 {
		t.Errorf("Width('A') = %v, want 700 (the PDF's own /Widths entry, never overridden by the substitute)", w)
	}
}
