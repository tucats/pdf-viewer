package fonts

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildCmapFormat4Subtable hand-builds a minimal format-4 cmap subtable
// with a single non-trivial segment [lo,hi] whose glyph indices are
// gid(code) = code - lo + firstGID (i.e. idDelta = firstGID - lo,
// idRangeOffset = 0 - the common, simplest case a real font's format 4
// subtable uses for a contiguous run of glyphs), followed by the
// mandatory terminating 0xFFFF segment - see parseCmapFormat4's doc
// comment for what each of the four parallel arrays means.
func buildCmapFormat4Subtable(lo, hi uint16, firstGID uint16) []byte {
	segCount := 2 // one real segment + the mandatory terminator
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(4))          // format
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))          // length (unused by this package's parser)
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))          // language
	_ = binary.Write(&buf, binary.BigEndian, uint16(segCount*2)) // segCountX2
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))          // searchRange (unused)
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))          // entrySelector (unused)
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))          // rangeShift (unused)

	// endCode[]
	_ = binary.Write(&buf, binary.BigEndian, hi)
	_ = binary.Write(&buf, binary.BigEndian, uint16(0xFFFF))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // reservedPad
	// startCode[]
	_ = binary.Write(&buf, binary.BigEndian, lo)
	_ = binary.Write(&buf, binary.BigEndian, uint16(0xFFFF))
	// idDelta[]
	_ = binary.Write(&buf, binary.BigEndian, int16(firstGID)-int16(lo))
	_ = binary.Write(&buf, binary.BigEndian, int16(1))
	// idRangeOffset[]
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	return buf.Bytes()
}

func TestParseCmapFormat4(t *testing.T) {
	sub := buildCmapFormat4Subtable('A', 'Z', 1)
	cmap, ok := parseCmapFormat4(sub)
	if !ok {
		t.Fatalf("parseCmapFormat4 failed")
	}
	if gid, ok := cmap.Lookup('A'); !ok || gid != 1 {
		t.Errorf("Lookup('A') = (%d,%v), want (1,true)", gid, ok)
	}
	if gid, ok := cmap.Lookup('Z'); !ok || gid != 26 {
		t.Errorf("Lookup('Z') = (%d,%v), want (26,true)", gid, ok)
	}
	if _, ok := cmap.Lookup('a'); ok {
		t.Errorf("Lookup('a') found a glyph, want not-found (outside the mapped segment)")
	}
}

// TestParseCmapFormat4_IdRangeOffset exercises the idRangeOffset
// indirection path (parseCmapFormat4's inline comment on entryAddr),
// which buildCmapFormat4Subtable above deliberately does not exercise
// (it only builds the simpler idDelta-only case) - this is the
// self-relative-pointer-into-glyphIdArray encoding real fonts use when a
// segment's glyph indices are not evenly spaced.
func TestParseCmapFormat4_IdRangeOffset(t *testing.T) {
	// One segment covering codes 65-66 ('A','B'), mapped through
	// glyphIdArray (not idDelta) to non-contiguous glyph indices 5 and
	// 9.
	segCount := 2
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(4))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(segCount*2))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))

	_ = binary.Write(&buf, binary.BigEndian, uint16(66))     // endCode[0]
	_ = binary.Write(&buf, binary.BigEndian, uint16(0xFFFF)) // endCode[1]
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))      // reservedPad
	_ = binary.Write(&buf, binary.BigEndian, uint16(65))     // startCode[0]
	_ = binary.Write(&buf, binary.BigEndian, uint16(0xFFFF)) // startCode[1]
	_ = binary.Write(&buf, binary.BigEndian, int16(0))       // idDelta[0]: unused when idRangeOffset != 0
	_ = binary.Write(&buf, binary.BigEndian, int16(1))       // idDelta[1]: terminator segment

	// idRangeOffset[0] must point (self-relative from its own storage
	// address) at glyphIdArray[0], which is 4 bytes further on: 2 bytes
	// for its own remaining array slot (idRangeOffset[1]) plus 2 bytes to
	// reach glyphIdArray itself... concretely: idRangeOffset array has 2
	// entries (4 bytes), so from idRangeOffset[0]'s own address, the
	// glyphIdArray starts 4 bytes later (skipping both idRangeOffset
	// entries).
	_ = binary.Write(&buf, binary.BigEndian, uint16(4)) // idRangeOffset[0]
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // idRangeOffset[1]: unused (terminator)

	_ = binary.Write(&buf, binary.BigEndian, uint16(5)) // glyphIdArray[0] -> code 65
	_ = binary.Write(&buf, binary.BigEndian, uint16(9)) // glyphIdArray[1] -> code 66

	cmap, ok := parseCmapFormat4(buf.Bytes())
	if !ok {
		t.Fatalf("parseCmapFormat4 failed")
	}
	if gid, ok := cmap.Lookup('A'); !ok || gid != 5 {
		t.Errorf("Lookup('A') = (%d,%v), want (5,true)", gid, ok)
	}
	if gid, ok := cmap.Lookup('B'); !ok || gid != 9 {
		t.Errorf("Lookup('B') = (%d,%v), want (9,true)", gid, ok)
	}
}

func TestParseCmapFormat6(t *testing.T) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(6))  // format
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))  // length
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))  // language
	_ = binary.Write(&buf, binary.BigEndian, uint16(65)) // firstCode
	_ = binary.Write(&buf, binary.BigEndian, uint16(3))  // entryCount
	_ = binary.Write(&buf, binary.BigEndian, uint16(10))
	_ = binary.Write(&buf, binary.BigEndian, uint16(11))
	_ = binary.Write(&buf, binary.BigEndian, uint16(12))

	cmap, ok := parseCmapFormat6(buf.Bytes())
	if !ok {
		t.Fatalf("parseCmapFormat6 failed")
	}
	if gid, ok := cmap.Lookup('A'); !ok || gid != 10 {
		t.Errorf("Lookup('A') = (%d,%v), want (10,true)", gid, ok)
	}
	if gid, ok := cmap.Lookup('C'); !ok || gid != 12 {
		t.Errorf("Lookup('C') = (%d,%v), want (12,true)", gid, ok)
	}
	if _, ok := cmap.Lookup('Z'); ok {
		t.Errorf("Lookup('Z') found a glyph outside the trimmed range")
	}
}

func TestSelectCmapSubtable_PrefersWindowsUnicode(t *testing.T) {
	records := []cmapRecord{
		{platformID: 1, encodingID: 0, offset: 100}, // Mac Roman
		{platformID: 3, encodingID: 1, offset: 200}, // Windows Unicode BMP - preferred
		{platformID: 3, encodingID: 0, offset: 300}, // Windows Symbol
	}
	offset, ok := selectCmapSubtable(records)
	if !ok || offset != 200 {
		t.Errorf("selectCmapSubtable = (%d,%v), want (200,true)", offset, ok)
	}
}

func TestSelectCmapSubtable_NoRecognizedSubtable(t *testing.T) {
	records := []cmapRecord{{platformID: 99, encodingID: 99, offset: 1}}
	if _, ok := selectCmapSubtable(records); ok {
		t.Errorf("selectCmapSubtable accepted an unrecognized (platform, encoding) pair")
	}
}

// TestParseCmapSubtable_Dispatch confirms parseCmapSubtable routes to
// the correct format-specific parser by reading the subtable's leading
// format field, and rejects a format this package does not implement
// (12, "segmented coverage" - see this file's doc comment) rather than
// misreading its bytes as a different format.
func TestParseCmapSubtable_Dispatch(t *testing.T) {
	t.Parallel()
	if _, ok := parseCmapSubtable(buildCmapFormat0Table(map[rune]uint16{'A': 1})[12:]); !ok {
		t.Errorf("parseCmapSubtable did not dispatch format 0")
	}
	if _, ok := parseCmapSubtable(buildCmapFormat4Subtable('A', 'Z', 1)); !ok {
		t.Errorf("parseCmapSubtable did not dispatch format 4")
	}
	unsupported := []byte{0, 12, 0, 0}
	if _, ok := parseCmapSubtable(unsupported); ok {
		t.Errorf("parseCmapSubtable accepted format 12, which this package does not implement")
	}
}

func TestParseCmapFormat4_RejectsTruncated(t *testing.T) {
	sub := buildCmapFormat4Subtable('A', 'Z', 1)
	for cut := 0; cut < len(sub); cut += 3 {
		// Must not panic on any truncation point.
		parseCmapFormat4(sub[:cut])
	}
}
