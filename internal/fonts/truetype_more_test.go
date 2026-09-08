package fonts

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestParseLoca_ShortFormat exercises indexToLocFormat 0 (buildTestSfnt,
// used by every other test in this package, always uses the "long"
// format 1 - see its own doc comment for why) - the "short" format
// packs each offset as a big-endian uint16 that must be doubled to
// recover the real byte offset.
func TestParseLoca_ShortFormat(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	for _, halved := range []uint16{0, 5, 100} { // real offsets 0, 10, 200
		_ = binary.Write(&raw, binary.BigEndian, halved)
	}
	loca, ok := parseLoca(raw.Bytes(), 2, 0)
	if !ok {
		t.Fatalf("parseLoca failed")
	}
	want := []uint32{0, 10, 200}
	for i, w := range want {
		if loca[i] != w {
			t.Errorf("loca[%d] = %d, want %d", i, loca[i], w)
		}
	}
}

func TestParseLoca_RejectsTruncated(t *testing.T) {
	t.Parallel()
	if _, ok := parseLoca([]byte{0, 1}, 5, 0); ok {
		t.Errorf("parseLoca accepted a table too short for the declared glyph count")
	}
	if _, ok := parseLoca([]byte{0, 1, 2, 3}, 5, 7); ok {
		t.Errorf("parseLoca accepted an unrecognized format value")
	}
}

// TestReadCoords_ShortDeltaEncoding exercises the "short" per-point
// coordinate encoding (a single delta byte, sign given by a separate
// flag bit) - encodeSimpleGlyph (used by every other glyph-outline test
// in this package) always writes the "full 16-bit delta" form instead,
// so this constructs the flag/byte layout directly.
func TestReadCoords_ShortDeltaEncoding(t *testing.T) {
	t.Parallel()
	const (
		short = 0x02
		same  = 0x10 // "positive" when short is set; see readCoords' doc comment
	)
	// Three points: +5 (short, positive), then -3 (short, negative via
	// same-bit clear), then +0 (short, positive, contributing nothing).
	flags := []byte{short | same, short, short | same}
	data := []byte{5, 3, 0}
	pos := 0
	xs, ok := readCoords(data, &pos, flags, short, same)
	if !ok {
		t.Fatalf("readCoords failed")
	}
	want := []int32{5, 2, 2} // 5, 5-3=2, 2+0=2 (deltas accumulate)
	for i, w := range want {
		if xs[i] != w {
			t.Errorf("xs[%d] = %d, want %d", i, xs[i], w)
		}
	}
	if pos != len(data) {
		t.Errorf("pos = %d, want %d (all 3 delta bytes consumed)", pos, len(data))
	}
}

func TestReadCoords_RejectsTruncated(t *testing.T) {
	t.Parallel()
	const short = 0x02
	pos := 0
	if _, ok := readCoords(nil, &pos, []byte{short}, short, 0x10); ok {
		t.Errorf("readCoords accepted a short-delta flag with no byte to read")
	}
	pos = 0
	if _, ok := readCoords(nil, &pos, []byte{0}, short, 0x10); ok {
		t.Errorf("readCoords accepted a full-delta flag with no bytes to read")
	}
}

// TestParseCompositeGlyph_2x2Transform exercises a composite glyph
// component with a full 2x2 transform (flagHave2x2 - see
// parseCompositeGlyph), the richest of the three optional-transform
// encodings that section also handles (flagHaveScale and
// flagHaveXYScale are simpler subsets of the same code path) - and,
// through it, f2dot14's fixed-point decoding.
func TestParseCompositeGlyph_2x2Transform(t *testing.T) {
	t.Parallel()
	square := unitSquareGlyph()
	// Component: glyph 1, no offset, transform = [[2 0][0 1]] (double
	// width, unchanged height) via f2dot14-encoded 2.0 = 0x8000... 2.0
	// does not fit in a signed 2.14 value (max just under 2.0), so use
	// 1.5 (0x6000) instead, which does.
	const (
		flagArgsAreWords = 0x0001
		flagArgsAreXY    = 0x0002
		flagHave2x2      = 0x0080
	)
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, int16(-1))
	_ = binary.Write(&buf, binary.BigEndian, [4]int16{})
	flags := uint16(flagArgsAreWords | flagArgsAreXY | flagHave2x2)
	_ = binary.Write(&buf, binary.BigEndian, flags)
	_ = binary.Write(&buf, binary.BigEndian, uint16(1)) // glyphIndex
	_ = binary.Write(&buf, binary.BigEndian, int16(0))  // dx
	_ = binary.Write(&buf, binary.BigEndian, int16(0))  // dy
	scale15 := int16(0x6000)                            // 1.5 in 2.14 fixed point
	_ = binary.Write(&buf, binary.BigEndian, scale15)   // a
	_ = binary.Write(&buf, binary.BigEndian, int16(0))  // b
	_ = binary.Write(&buf, binary.BigEndian, int16(0))  // c
	_ = binary.Write(&buf, binary.BigEndian, int16(16384/1))
	// d = 1.0 in 2.14 fixed point (16384/16384)

	glyphs := [][]byte{{}, square, buf.Bytes()}
	data := buildTestSfnt(t, glyphs, buildCmapFormat0Table(map[rune]uint16{'S': 2}))
	sfnt, ok := parseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	outline, ok := sfnt.GlyphOutline(2)
	if !ok {
		t.Fatalf("GlyphOutline failed")
	}
	minX, minY, maxX, maxY := pathBounds(t, outline)
	// unitSquareGlyph spans [100,800] on both axes; scaling X by 1.5
	// gives [150,1200], leaving Y untouched at [100,800].
	if minX != 150 || maxX != 1200 {
		t.Errorf("X bounds = [%v,%v], want [150,1200] (1.5x horizontal scale)", minX, maxX)
	}
	if minY != 100 || maxY != 800 {
		t.Errorf("Y bounds = [%v,%v], want [100,800] (unscaled)", minY, maxY)
	}
}

func TestF2Dot14(t *testing.T) {
	t.Parallel()
	if v := f2dot14([]byte{0x40, 0x00}); v != 1.0 {
		t.Errorf("f2dot14(0x4000) = %v, want 1.0", v)
	}
	if v := f2dot14([]byte{0xC0, 0x00}); v != -1.0 {
		t.Errorf("f2dot14(0xC000) = %v, want -1.0", v)
	}
}

// TestParseCompositeGlyph_RejectsTruncated confirms a malformed
// composite glyph entry (declaring more components than there are bytes
// for) fails closed rather than panicking.
func TestParseCompositeGlyph_RejectsTruncated(t *testing.T) {
	t.Parallel()
	sfnt := sfntFont{loca: []uint32{0, 4}, glyfData: []byte{0xFF, 0xFF, 0, 0}} // numContours=-1, then nothing
	if _, ok := sfnt.glyphOutline(0, 0); ok {
		t.Errorf("glyphOutline accepted a truncated composite glyph")
	}
}

func TestGlyphOutline_OutOfRangeGID(t *testing.T) {
	t.Parallel()
	var sfnt sfntFont
	if _, ok := sfnt.GlyphOutline(0); ok {
		t.Errorf("GlyphOutline accepted an out-of-range glyph index on a zero-value sfntFont")
	}
}
