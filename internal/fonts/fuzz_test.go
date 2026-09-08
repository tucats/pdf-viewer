package fonts

import "testing"

// FuzzParseSfnt feeds arbitrary byte slices into parseSfnt (an embedded
// /FontFile2 program's parser) and every stage past it that a
// successful parse unlocks (cmap lookups and glyph outline extraction
// for every glyph index the fuzzer discovers), matching this project's
// established pattern of a fuzz target alongside any parser that reads
// hostile or corrupted binary input (see, for example,
// internal/filter's FuzzDecode or internal/parser's
// FuzzOpenAndResolveAll). A TrueType font program is exactly this kind
// of input - untrusted bytes with internal offsets and lengths a
// malicious or merely corrupted PDF could point anywhere - so this is
// this package's counterpart to those.
//
// The only property being tested is "never panics, always terminates" -
// parseSfnt and GlyphOutline are documented (see their own doc
// comments) to fail closed (ok=false) rather than error, so there is no
// error-based assertion to make; a hang would be caught by the fuzzer's
// own per-execution timeout, and a panic fails the fuzz run directly.
func FuzzParseSfnt(f *testing.F) {
	seedGlyphs := [][]byte{{}, unitSquareGlyph(), encodeCompositeGlyph([]compositeComponent{{glyphIndex: 1, dx: 10, dy: 10}})}
	seedCmap := buildCmapFormat0Table(map[rune]uint16{'A': 1, 'B': 2})
	f.Add(buildTestSfntForFuzzSeed(seedGlyphs, seedCmap))
	f.Add([]byte{})
	f.Add([]byte{0, 1, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		sfnt, ok := parseSfnt(data)
		if !ok {
			return
		}
		// Exercise every code path a successful parse unlocks, bounded
		// by numGlyphs (parseSfnt itself does not otherwise limit how
		// many glyph indices exist) so a pathological numGlyphs value
		// cannot turn this fuzz target itself into an unbounded loop.
		limit := int(sfnt.numGlyphs)
		if limit > 4096 {
			limit = 4096
		}
		for gid := 0; gid < limit; gid++ {
			sfnt.GlyphOutline(uint16(gid))
		}
		for _, r := range []rune{'A', 'B', ' ', 0, 0xFFFF} {
			sfnt.cmap.Lookup(r)
		}
	})
}

// buildTestSfntForFuzzSeed builds a seed corpus entry without requiring
// a *testing.T (buildTestSfnt in truetype_test.go takes one only to call
// t.Helper() for clearer failure locations in ordinary tests - not
// needed, and not available, for a fuzz seed built at Add-time).
func buildTestSfntForFuzzSeed(glyphs [][]byte, cmapTable []byte) []byte {
	var t testing.T
	return buildTestSfnt(&t, glyphs, cmapTable)
}
