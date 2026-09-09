package fonts

import "testing"

// TestSimpleGlyphLookup_Symbolic exercises the symbolic-font lookup
// order (raw code, then raw code + 0xF000, then the encoding-resolved
// rune - see simpleGlyphLookup's doc comment), which
// TestLoad_SimpleFontWithEmbeddedTrueType (font_test.go) does not cover
// since that test's font declares no /Flags symbolic bit at all.
func TestSimpleGlyphLookup_Symbolic(t *testing.T) {
	t.Parallel()
	// A cmap keyed by raw code 0x41 (not by a resolved Unicode rune, and
	// not by the 0xF000+code convention) - a symbolic font's own private
	// code space.
	sfnt := sfntFont{cmap: cmapSubtable{entries: map[rune]uint16{0x41: 9}}}
	lookup := simpleGlyphLookup(&sfnt, runeTable{}, true)
	if gid, ok := lookup(0x41); !ok || gid != 9 {
		t.Errorf("symbolic lookup(0x41) = (%d,%v), want (9,true)", gid, ok)
	}
}

func TestSimpleGlyphLookup_SymbolicPrivateUseArea(t *testing.T) {
	t.Parallel()
	// A cmap keyed by the Windows "symbol" convention (0xF000 + code) -
	// see simpleGlyphLookup's doc comment on byCode.
	sfnt := sfntFont{cmap: cmapSubtable{entries: map[rune]uint16{0xF041: 3}}}
	lookup := simpleGlyphLookup(&sfnt, runeTable{}, true)
	if gid, ok := lookup(0x41); !ok || gid != 3 {
		t.Errorf("symbolic lookup(0x41) via 0xF000+code = (%d,%v), want (3,true)", gid, ok)
	}
}

// TestSimpleGlyphLookup_NonSymbolicFallsBackToRawCode confirms a
// non-symbolic font still tries the raw-code lookup as a last resort
// (simpleGlyphLookup's doc comment: "an extra, harmless lookup attempt
// costs nothing and occasionally recovers a font whose /Flags entry
// does not accurately describe it").
func TestSimpleGlyphLookup_NonSymbolicFallsBackToRawCode(t *testing.T) {
	t.Parallel()
	sfnt := sfntFont{cmap: cmapSubtable{entries: map[rune]uint16{0x41: 9}}}
	// An encoding table that does not map code 0x41 to anything (rune 0)
	// - so the rune-based attempt must fail, falling back to raw code.
	lookup := simpleGlyphLookup(&sfnt, runeTable{}, false)
	if gid, ok := lookup(0x41); !ok || gid != 9 {
		t.Errorf("non-symbolic lookup(0x41) = (%d,%v), want (9,true) via raw-code fallback", gid, ok)
	}
}

// TestSimpleCFFGlyphLookup exercises simpleRuneGlyphLookup directly
// against a fake cffFont (its runeToGID map set up by hand, rather than
// via a full parseCFFFont round trip - cff_test.go's own tests already
// cover that construction step), confirming it resolves a code through
// the PDF encoding table to a rune and then to a GID.
func TestSimpleCFFGlyphLookup(t *testing.T) {
	t.Parallel()
	cff := cffFont{runeToGID: map[rune]uint16{'A': 7}}
	encoding := standardEncoding() // covers ASCII, so code 0x41 ('A') resolves without an explicit /Differences array.
	lookup := simpleRuneGlyphLookup(&cff, encoding)

	if gid, ok := lookup(0x41); !ok || gid != 7 {
		t.Errorf("lookup('A') = (%d,%v), want (7,true)", gid, ok)
	}
	if _, ok := lookup(0x42); ok {
		t.Errorf("lookup('B') found a glyph, want not-found (not in the fake charset)")
	}
	if _, ok := lookup(-1); ok {
		t.Errorf("lookup(-1) found a glyph, want not-found")
	}
	if _, ok := lookup(9999); ok {
		t.Errorf("lookup(9999) found a glyph, want not-found")
	}
}

func TestSimpleGlyphLookup_NotFound(t *testing.T) {
	t.Parallel()
	sfnt := sfntFont{cmap: cmapSubtable{entries: map[rune]uint16{}}}
	lookup := simpleGlyphLookup(&sfnt, runeTable{}, false)
	if _, ok := lookup(0x41); ok {
		t.Errorf("lookup found a glyph in an empty cmap")
	}
	// An out-of-range code must not panic indexing the 256-entry
	// runeTable.
	if _, ok := lookup(-1); ok {
		t.Errorf("lookup(-1) found a glyph, want not-found")
	}
	if _, ok := lookup(9999); ok {
		t.Errorf("lookup(9999) found a glyph, want not-found")
	}
}
