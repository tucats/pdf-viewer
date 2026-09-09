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

// FuzzProbeFontFile is probe.go's counterpart to FuzzParseSfnt above,
// covering this file's Phase 2 addition: reading an arbitrary byte slice
// as a candidate font *file* rather than an embedded PDF font program.
// This exercises the extra parsing surface probe.go adds on top of
// truetype.go/cmap.go's existing sfnt reading - the "ttcf" collection
// header (parseTTCHeader), the "name" table (parseNameTable), and the
// "OS/2" table (parseOS2Table) - none of which FuzzParseSfnt's seed
// corpus (built from a plain single-face TrueType file with no "name" or
// "OS/2" table at all) would ever reach.
//
// As with FuzzParseSfnt, the only property under test is "never panics,
// always terminates" - ProbeFontFile is documented to fail closed (a
// non-nil error) rather than panic on malformed input, and FontFace's
// own Outline method (exercised here for every face a successful probe
// returns) carries the same guarantee via parseSfntAt.
func FuzzProbeFontFile(f *testing.F) {
	seedGlyphs := [][]byte{{}, unitSquareGlyph()}
	seedCmap := buildCmapFormat0Table(map[rune]uint16{'A': 1})
	seedNames := buildNameTable(map[uint16]string{nameIDFamily: "Arial", nameIDPostScript: "Arial-BoldMT"})
	seedOS2 := buildOS2Table(700, fsSelectionBold, 0x0800)

	base := buildTestSfntForFuzzSeed(seedGlyphs, seedCmap)
	baseTables, _, ok := parseTableDirectory(base, 0)
	if ok {
		plainFace := []sfntTable{
			{"head", baseTables["head"]},
			{"maxp", baseTables["maxp"]},
			{"loca", baseTables["loca"]},
			{"glyf", baseTables["glyf"]},
			{"cmap", baseTables["cmap"]},
			{"name", seedNames},
			{"OS/2", seedOS2},
		}
		f.Add(assembleSfnt(sfntVersionTrueType, plainFace, 0))
		f.Add(assembleSfnt(sfntVersionOTTO, []sfntTable{{"name", seedNames}, {"OS/2", seedOS2}}, 0))
		f.Add(buildTestTTC([][]sfntTable{plainFace, plainFace}))
	}
	f.Add([]byte{})
	f.Add([]byte("ttcf"))

	f.Fuzz(func(t *testing.T, data []byte) {
		faces, err := ProbeFontFile(data)
		if err != nil {
			return
		}
		for _, face := range faces {
			face.Outline() //nolint:errcheck // only absence of a panic is under test
		}
	})
}
