package fonts

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// fakeResolver is a minimal, in-memory Resolver for tests that need one:
// it holds a fixed set of indirect objects and applies no filters at all
// (DecodeStream just returns the stream's raw bytes unchanged), which is
// sufficient for every test in this package - font programs here are
// embedded as direct (unfiltered) streams, exactly like several of
// tools/genfixtures' own simpler fixtures do for content streams.
type fakeResolver struct {
	objects map[int]syntax.Object
}

func (r fakeResolver) Resolve(num int) (syntax.Object, error) {
	if obj, ok := r.objects[num]; ok {
		return obj, nil
	}
	return syntax.Null{}, nil
}

func (r fakeResolver) ResolveDictionary(dict syntax.Dictionary) (syntax.Dictionary, error) {
	out := make(syntax.Dictionary, len(dict))
	for k, v := range dict {
		if ref, ok := v.(syntax.Reference); ok {
			resolved, err := r.Resolve(ref.Number)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
			continue
		}
		out[k] = v
	}
	return out, nil
}

func (r fakeResolver) DecodeStream(s syntax.Stream) ([]byte, error) {
	return s.Raw, nil
}

func TestNotdefGlyph_HollowBoxForOrdinaryWidth(t *testing.T) {
	t.Parallel()
	p := notdefGlyph(700)
	if p == nil {
		t.Fatalf("notdefGlyph(700) = nil, want a box")
	}
	if len(p.Subpaths) != 2 {
		t.Fatalf("notdefGlyph(700) has %d subpaths, want 2 (outer + inner, forming a hollow box)", len(p.Subpaths))
	}
	minX, minY, maxX, maxY := pathBounds(t, p)
	if minX != 60 || minY != 0 || maxX != 640 || maxY != 660 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (60,0)-(640,660)", minX, minY, maxX, maxY)
	}
}

func TestNotdefGlyph_NilForNarrowWidth(t *testing.T) {
	t.Parallel()
	if p := notdefGlyph(50); p != nil {
		t.Errorf("notdefGlyph(50) = %+v, want nil (too narrow to draw)", p)
	}
}

func TestLoad_SimpleFontWithEmbeddedTrueType(t *testing.T) {
	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmap := buildCmapFormat0Table(map[rune]uint16{'A': 1})
	sfntData := buildTestSfnt(t, glyphs, cmap)

	resolver := fakeResolver{objects: map[int]syntax.Object{
		10: syntax.Stream{Dict: syntax.Dictionary{"Length1": syntax.Integer(len(sfntData))}, Raw: sfntData},
	}}
	dict := syntax.Dictionary{
		"Subtype":   syntax.Name("TrueType"),
		"FirstChar": syntax.Integer(65),
		"LastChar":  syntax.Integer(65),
		"Widths":    syntax.Array{syntax.Integer(750)},
		"Encoding":  syntax.Name("WinAnsiEncoding"),
		"FontDescriptor": syntax.Dictionary{
			"FontFile2": syntax.Reference{Number: 10},
		},
	}

	f, err := Load(dict, resolver)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.TwoByteCodes {
		t.Errorf("simple font reported TwoByteCodes")
	}
	if w := f.Width(65); w != 750 {
		t.Errorf("Width('A') = %v, want 750", w)
	}
	if w := f.Width(66); w != defaultMissingWidth {
		t.Errorf("Width of an unlisted code = %v, want the default %v", w, defaultMissingWidth)
	}

	glyph := f.Glyph(65)
	if glyph == nil || len(glyph.Subpaths) == 0 {
		t.Fatalf("Glyph('A') did not return the embedded outline")
	}
	minX, minY, maxX, maxY := pathBounds(t, glyph)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("glyph bounds = (%v,%v)-(%v,%v), want the embedded square (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

// TestLoad_SimpleFontWithEmbeddedCFF is TestLoad_SimpleFontWithEmbeddedTrueType's
// counterpart for a bare CFF/Type1C /FontFile3 program (cff.go),
// confirming the same square outline comes back through Font.Glyph
// regardless of which embedded font program format produced it.
func TestLoad_SimpleFontWithEmbeddedCFF(t *testing.T) {
	cffData := buildTestCFF(t, [][]byte{{}, squareCharstring()}, []string{"A"}, nil, nil)

	resolver := fakeResolver{objects: map[int]syntax.Object{
		10: syntax.Stream{Raw: cffData},
	}}
	dict := syntax.Dictionary{
		"Subtype":   syntax.Name("Type1"),
		"FirstChar": syntax.Integer(65),
		"LastChar":  syntax.Integer(65),
		"Widths":    syntax.Array{syntax.Integer(750)},
		"FontDescriptor": syntax.Dictionary{
			"FontFile3": syntax.Reference{Number: 10},
		},
	}

	f, err := Load(dict, resolver)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	glyph := f.Glyph(65) // 'A' under the default StandardEncoding.
	if glyph == nil || len(glyph.Subpaths) == 0 {
		t.Fatalf("Glyph('A') did not return the embedded CFF outline")
	}
	minX, minY, maxX, maxY := pathBounds(t, glyph)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("glyph bounds = (%v,%v)-(%v,%v), want the embedded square (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

// TestLoad_SimpleFontWithEmbeddedOpenTypeCFF exercises loadEmbeddedCFF's
// other accepted shape: a /FontFile3 whose bytes are a full sfnt wrapper
// (an "OpenType/CFF" font) rather than a bare CFF program, with the
// actual CFF program inside its "CFF " table - see truetype.go's
// sfntVersionOTTO and loadEmbeddedCFF's own doc comment.
func TestLoad_SimpleFontWithEmbeddedOpenTypeCFF(t *testing.T) {
	cffData := buildTestCFF(t, [][]byte{{}, squareCharstring()}, []string{"A"}, nil, nil)
	wrapped := assembleSfnt(sfntVersionOTTO, []sfntTable{{"CFF ", cffData}}, 0)

	resolver := fakeResolver{objects: map[int]syntax.Object{
		10: syntax.Stream{Raw: wrapped},
	}}
	dict := syntax.Dictionary{
		"Subtype":   syntax.Name("Type1"),
		"FirstChar": syntax.Integer(65),
		"LastChar":  syntax.Integer(65),
		"Widths":    syntax.Array{syntax.Integer(750)},
		"FontDescriptor": syntax.Dictionary{
			"FontFile3": syntax.Reference{Number: 10},
		},
	}

	f, err := Load(dict, resolver)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	glyph := f.Glyph(65)
	if glyph == nil || len(glyph.Subpaths) == 0 {
		t.Fatalf("Glyph('A') did not return the outline embedded inside the sfnt-wrapped \"CFF \" table")
	}
}

func TestLoad_SimpleFontNoEmbeddedProgramFallsBackToNotdef(t *testing.T) {
	dict := syntax.Dictionary{
		"Subtype":   syntax.Name("Type1"),
		"BaseFont":  syntax.Name("Helvetica"),
		"FirstChar": syntax.Integer(65),
		"LastChar":  syntax.Integer(65),
		"Widths":    syntax.Array{syntax.Integer(700)},
	}
	f, err := Load(dict, fakeResolver{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	glyph := f.Glyph(65) // 'A', not a space
	if glyph == nil {
		t.Fatalf("Glyph('A') with no embedded program = nil, want a notdef box")
	}
	if len(glyph.Subpaths) != 2 {
		t.Errorf("fallback glyph has %d subpaths, want 2 (hollow notdef box)", len(glyph.Subpaths))
	}

	if glyph := f.Glyph(32); glyph != nil {
		t.Errorf("Glyph(' ') with no embedded program painted something, want nil (space should never get a notdef box)")
	}
}

func TestLoad_Type0IdentityH(t *testing.T) {
	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmap := buildCmapFormat0Table(nil) // CID fonts don't need a cmap; lookups go through CIDToGIDMap
	sfntData := buildTestSfnt(t, glyphs, cmap)

	resolver := fakeResolver{objects: map[int]syntax.Object{
		10: syntax.Stream{Raw: sfntData},
		20: syntax.Dictionary{
			"Subtype":        syntax.Name("CIDFontType2"),
			"DW":             syntax.Integer(1000),
			"W":              syntax.Array{syntax.Integer(1), syntax.Array{syntax.Integer(600)}},
			"FontDescriptor": syntax.Dictionary{"FontFile2": syntax.Reference{Number: 10}},
			"CIDToGIDMap":    syntax.Name("Identity"),
		},
	}}
	dict := syntax.Dictionary{
		"Subtype":         syntax.Name("Type0"),
		"Encoding":        syntax.Name("Identity-H"),
		"DescendantFonts": syntax.Array{syntax.Reference{Number: 20}},
	}

	f, err := Load(dict, resolver)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !f.TwoByteCodes {
		t.Fatalf("Type0/Identity-H font did not report TwoByteCodes")
	}
	if w := f.Width(1); w != 600 {
		t.Errorf("Width(CID 1) = %v, want 600", w)
	}
	if w := f.Width(2); w != 1000 {
		t.Errorf("Width(CID 2) = %v, want the /DW default 1000", w)
	}
	glyph := f.Glyph(1) // CID 1 -> GID 1 (Identity CIDToGIDMap) -> the unit square
	if glyph == nil || len(glyph.Subpaths) != 1 {
		t.Fatalf("Glyph(CID 1) did not resolve the embedded square outline")
	}
}

// TestLoad_Type0EmbeddedCMap is TestLoad_Type0IdentityH's Phase 9
// counterpart: the same descendant font and embedded TrueType program,
// but reached through an embedded CMap *stream* /Encoding (mapping the
// arbitrary 2-byte code 0x1234 to CID 1) instead of the literal name
// "Identity-H" - confirming loadType0Encoding (cid.go) actually parses
// and attaches a real CMap, and that Font.DecodeCodes/Width/Glyph
// (font.go) correctly translate a raw code through it rather than
// assuming code == CID.
func TestLoad_Type0EmbeddedCMap(t *testing.T) {
	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmapTable := buildCmapFormat0Table(nil)
	sfntData := buildTestSfnt(t, glyphs, cmapTable)

	cmapStream := []byte("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n" +
		"1 begincidrange\n<1234> <1234> 1\nendcidrange\nendcmap\n")

	resolver := fakeResolver{objects: map[int]syntax.Object{
		10: syntax.Stream{Raw: sfntData},
		20: syntax.Dictionary{
			"Subtype":        syntax.Name("CIDFontType2"),
			"DW":             syntax.Integer(1000),
			"W":              syntax.Array{syntax.Integer(1), syntax.Array{syntax.Integer(600)}},
			"FontDescriptor": syntax.Dictionary{"FontFile2": syntax.Reference{Number: 10}},
			"CIDToGIDMap":    syntax.Name("Identity"),
		},
		30: syntax.Stream{Raw: cmapStream},
	}}
	dict := syntax.Dictionary{
		"Subtype":         syntax.Name("Type0"),
		"Encoding":        syntax.Reference{Number: 30},
		"DescendantFonts": syntax.Array{syntax.Reference{Number: 20}},
	}

	f, err := Load(dict, resolver)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	codes := f.DecodeCodes([]byte{0x12, 0x34})
	if len(codes) != 1 || codes[0] != (DecodedCode{Code: 0x1234, Bytes: 2}) {
		t.Fatalf("DecodeCodes(<1234>) = %+v, want one code {0x1234 2}", codes)
	}

	rawCode := codes[0].Code
	if w := f.Width(rawCode); w != 600 {
		t.Errorf("Width(raw code 0x1234, -> CID 1) = %v, want 600", w)
	}
	glyph := f.Glyph(rawCode)
	if glyph == nil || len(glyph.Subpaths) != 1 {
		t.Fatalf("Glyph(raw code 0x1234, -> CID 1 -> GID 1) did not resolve the embedded square outline")
	}

	// A code the CMap declares nothing about at all should fall back to
	// CID 0 (.notdef) rather than being (wrongly) treated as its own CID.
	if w := f.Width(0x9999); w != 1000 {
		t.Errorf("Width(unmapped code) = %v, want the /DW default 1000 (via CID 0)", w)
	}
}

func TestLoad_MalformedDictStillReturnsUsableFont(t *testing.T) {
	// A font dictionary this package cannot recognize at all (missing
	// /Subtype, no descendant fonts, ...) must still degrade to a usable
	// Font per this package's documented policy - see font.go's doc
	// comment - never an error that would abort the whole page.
	f, err := Load(syntax.Dictionary{"Subtype": syntax.Name("Type0")}, fakeResolver{})
	if err != nil {
		t.Fatalf("Load returned an error for a font this package should degrade gracefully instead: %v", err)
	}
	if f.Glyph(1) == nil {
		// No embedded program at all and no known-space code, so this
		// should be the generic notdef box.
		t.Errorf("Glyph(1) = nil, want a notdef box fallback")
	}
}
