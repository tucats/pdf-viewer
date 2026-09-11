package fonts

import (
	"encoding/binary"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// TestParseCIDWidths_RangeShape exercises the "cFirst cLast w" packed
// width shape (parseCIDWidths' doc comment's second bullet) - the
// font_test.go Load-level tests only exercise the other shape ("c
// [w1 w2 ...]").
func TestParseCIDWidths_RangeShape(t *testing.T) {
	t.Parallel()
	w := syntax.Array{syntax.Integer(10), syntax.Integer(12), syntax.Integer(500)}
	widths := parseCIDWidths(w, fakeResolver{})
	for cid := 10; cid <= 12; cid++ {
		if widths[cid] != 500 {
			t.Errorf("widths[%d] = %v, want 500", cid, widths[cid])
		}
	}
	if _, ok := widths[13]; ok {
		t.Errorf("widths[13] should not be set (outside the range)")
	}
}

// TestParseCIDWidths_MixedShapes confirms both packed-width shapes can
// appear in the same /W array, one after another, as real-world files
// do.
func TestParseCIDWidths_MixedShapes(t *testing.T) {
	t.Parallel()
	w := syntax.Array{
		syntax.Integer(1), syntax.Array{syntax.Integer(100), syntax.Integer(200)},
		syntax.Integer(50), syntax.Integer(55), syntax.Integer(1000),
	}
	widths := parseCIDWidths(w, fakeResolver{})
	if widths[1] != 100 || widths[2] != 200 {
		t.Errorf("array-shape widths = %v, %v, want 100, 200", widths[1], widths[2])
	}
	if widths[50] != 1000 || widths[55] != 1000 {
		t.Errorf("range-shape widths = %v, %v, want 1000, 1000", widths[50], widths[55])
	}
}

// TestParseCIDWidths_RejectsAbsurdRange confirms a hostile "cFirst
// cLast w" entry with an enormous range does not cause parseCIDWidths
// to attempt to populate billions of map entries - see
// maxCIDRangeSpan's inline doc comment.
func TestParseCIDWidths_RejectsAbsurdRange(t *testing.T) {
	t.Parallel()
	w := syntax.Array{syntax.Integer(0), syntax.Integer(2_000_000_000), syntax.Integer(1)}
	widths := parseCIDWidths(w, fakeResolver{})
	if len(widths) != 0 {
		t.Errorf("len(widths) = %d, want 0 (absurd range should be rejected, not truncated)", len(widths))
	}
}

// TestParseCIDWidths_IndirectReference confirms /W is resolved
// correctly when the PDF writes it as an indirect reference to the
// array object (e.g. "/W 3219 0 R") rather than inline - both are legal
// per the specification, but only the inline form was previously
// handled; parseCIDWidths asserted the un-resolved dictionary value
// directly against syntax.Array, so the indirect form was silently
// treated as "no widths at all" (every code then fell back to /DW, or
// this package's generic default) instead of the font's real,
// per-glyph widths - producing badly overspaced text for any CID font
// whose producer happened to write /W as an indirect object (a real
// PowerPoint-exported PDF does this).
func TestParseCIDWidths_IndirectReference(t *testing.T) {
	t.Parallel()
	resolver := fakeResolver{objects: map[int]syntax.Object{
		99: syntax.Array{syntax.Integer(10), syntax.Array{syntax.Integer(500)}},
	}}
	widths := parseCIDWidths(syntax.Reference{Number: 99}, resolver)
	if widths[10] != 500 {
		t.Errorf("widths[10] = %v, want 500", widths[10])
	}
}

// TestCidGlyphLookup_ExplicitStream exercises /CIDToGIDMap as an
// explicit stream of 2-byte big-endian glyph indices (as opposed to the
// /Identity default, which font_test.go's TestLoad_Type0IdentityH
// already covers).
func TestCidGlyphLookup_ExplicitStream(t *testing.T) {
	t.Parallel()
	// CID 0 -> GID 0 (i.e. "no glyph" - font_test's convention), CID 1
	// -> GID 42, CID 2 -> GID 7.
	data := make([]byte, 6)
	binary.BigEndian.PutUint16(data[0:2], 0)
	binary.BigEndian.PutUint16(data[2:4], 42)
	binary.BigEndian.PutUint16(data[4:6], 7)

	resolver := fakeResolver{objects: map[int]syntax.Object{
		9: syntax.Stream{Raw: data},
	}}
	descendant := syntax.Dictionary{"CIDToGIDMap": syntax.Reference{Number: 9}}
	lookup := cidGlyphLookup(descendant, resolver)

	if gid, ok := lookup(1); !ok || gid != 42 {
		t.Errorf("lookup(1) = (%d,%v), want (42,true)", gid, ok)
	}
	if gid, ok := lookup(2); !ok || gid != 7 {
		t.Errorf("lookup(2) = (%d,%v), want (7,true)", gid, ok)
	}
	if _, ok := lookup(0); ok {
		t.Errorf("lookup(0) found a glyph, want not-found (GID 0 means \"no glyph\")")
	}
	if _, ok := lookup(100); ok {
		t.Errorf("lookup(100) found a glyph, want not-found (out of range)")
	}
}

// TestCidCFFGlyphLookup exercises cidCFFGlyphLookup directly against a
// fake cffFont (its cidToGID map set up by hand - cff_test.go's own
// tests already cover building it via a full parseCFFFont round trip).
func TestCidCFFGlyphLookup(t *testing.T) {
	t.Parallel()
	cff := cffFont{isCID: true, cidToGID: map[uint16]uint16{500: 3}}
	lookup := cidCFFGlyphLookup(&cff)

	if gid, ok := lookup(500); !ok || gid != 3 {
		t.Errorf("lookup(500) = (%d,%v), want (3,true)", gid, ok)
	}
	if _, ok := lookup(1); ok {
		t.Errorf("lookup(1) found a glyph, want not-found (not in the fake charset)")
	}
	if _, ok := lookup(-1); ok {
		t.Errorf("lookup(-1) found a glyph, want not-found")
	}
	if _, ok := lookup(0x10000); ok {
		t.Errorf("lookup(0x10000) found a glyph, want not-found (out of range)")
	}
}

// TestLoad_Type0CIDFontType0CFF is TestLoad_Type0IdentityH's counterpart
// for a CIDFontType0 descendant (a CID-keyed CFF program via
// /FontFile3, rather than CIDFontType2's embedded TrueType via
// /FontFile2): CID 1 must resolve to a real outline via the CFF
// program's own charset (cff.go's GIDForCID), with no /CIDToGIDMap
// involved at all (see cidCFFGlyphLookup's doc comment on why a
// CIDFontType0 descendant never has one).
func TestLoad_Type0CIDFontType0CFF(t *testing.T) {
	cffData := buildTestCIDCFF(t, [][]byte{{}, squareCharstring()}, []uint16{1})

	resolver := fakeResolver{objects: map[int]syntax.Object{
		10: syntax.Stream{Raw: cffData},
		20: syntax.Dictionary{
			"Subtype":        syntax.Name("CIDFontType0"),
			"DW":             syntax.Integer(1000),
			"W":              syntax.Array{syntax.Integer(1), syntax.Array{syntax.Integer(600)}},
			"FontDescriptor": syntax.Dictionary{"FontFile3": syntax.Reference{Number: 10}},
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
	if w := f.Width(1); w != 600 {
		t.Errorf("Width(CID 1) = %v, want 600", w)
	}
	glyph := f.Glyph(1) // CID 1 -> GID 1 via the CFF program's own charset.
	if glyph == nil || len(glyph.Subpaths) == 0 {
		t.Fatalf("Glyph(CID 1) did not resolve the embedded CFF square outline")
	}
	minX, minY, maxX, maxY := pathBounds(t, glyph)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("glyph bounds = (%v,%v)-(%v,%v), want the embedded square (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

// TestFirstDescendantFont_IndirectArray confirms /DescendantFonts is
// resolved correctly when the PDF writes it as an indirect reference to
// the array object (e.g. "/DescendantFonts 30 0 R") rather than inline
// (e.g. "/DescendantFonts [20 0 R]") - both are legal per the
// specification, but only the inline form was previously handled;
// firstDescendantFont asserted the un-resolved dictionary value
// directly against syntax.Array, so the indirect form was silently
// treated as "no descendant font" (see cid.go's doc comment on this
// function).
func TestFirstDescendantFont_IndirectArray(t *testing.T) {
	t.Parallel()
	resolver := fakeResolver{objects: map[int]syntax.Object{
		20: syntax.Dictionary{"Subtype": syntax.Name("CIDFontType2")},
		30: syntax.Array{syntax.Reference{Number: 20}},
	}}
	dict := syntax.Dictionary{"DescendantFonts": syntax.Reference{Number: 30}}

	descendant, ok := firstDescendantFont(dict, resolver)
	if !ok {
		t.Fatalf("firstDescendantFont: ok = false, want true")
	}
	if descendant["Subtype"] != syntax.Name("CIDFontType2") {
		t.Errorf("descendant[Subtype] = %v, want CIDFontType2", descendant["Subtype"])
	}
}

func TestCidGlyphLookup_UnrecognizedNameFallsBackToIdentity(t *testing.T) {
	t.Parallel()
	descendant := syntax.Dictionary{"CIDToGIDMap": syntax.Name("SomeUnknownName")}
	lookup := cidGlyphLookup(descendant, fakeResolver{})
	if gid, ok := lookup(5); !ok || gid != 5 {
		t.Errorf("lookup(5) = (%d,%v), want (5,true) (Identity fallback)", gid, ok)
	}
}
