package fonts

import (
	"bytes"
	"encoding/binary"
	"sort"
	"testing"
	"unicode/utf16"
)

// This file tests probe.go's candidate-font-file reading (Phase 2 of
// docs/FONTS.md's font-substitution plan): the sfnt "name"/"OS/2" table
// parsing, the TrueType Collection ("ttcf") header parsing, and the
// ProbeFontFile entry point that ties them together. Like
// truetype_test.go (whose assembleSfnt/sfntTable helpers this file
// reuses), every fixture here is a small, hand-built synthetic byte
// stream rather than a real font file - see truetype_test.go's own doc
// comment for the reasoning (no external font file, with its own
// separate license and provenance question, needs to be vendored just to
// exercise this package's own parsing logic).

// utf16BEEncode encodes s as big-endian UTF-16 bytes - the inverse of
// probe.go's own utf16BEToString, used here to build realistic "name"
// table string data the way a real font-authoring tool would (this
// package only ever needs to *read* UTF-16BE "name" strings, never write
// them, so this encoder exists only for tests).
func utf16BEEncode(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, len(units)*2)
	for i, u := range units {
		binary.BigEndian.PutUint16(buf[i*2:i*2+2], u)
	}
	return buf
}

// buildNameTable builds a complete sfnt "name" table (format 0) with one
// Windows-platform (3), Unicode BMP (1), US English (0x0409) record per
// entry in names (keyed by name ID - see probe.go's nameID* constants).
// This is the single (platform, encoding) combination selectName scores
// highest, so tests using this helper don't need to worry about
// selectName's fallback precedence unless they are specifically testing
// it.
func buildNameTable(names map[uint16]string) []byte {
	// A deterministic iteration order keeps this helper's output (and
	// any test asserting on exact bytes) reproducible - Go deliberately
	// randomizes plain map iteration order, so the name IDs are sorted
	// first.
	ids := make([]uint16, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var storage bytes.Buffer
	type placedRecord struct {
		nameID         uint16
		offset, length uint16
	}
	placements := make([]placedRecord, 0, len(ids))
	for _, id := range ids {
		encoded := utf16BEEncode(names[id])
		placements = append(placements, placedRecord{
			nameID: id,
			offset: uint16(storage.Len()),
			length: uint16(len(encoded)),
		})
		storage.Write(encoded)
	}

	var out bytes.Buffer
	_ = binary.Write(&out, binary.BigEndian, uint16(0))                    // format 0
	_ = binary.Write(&out, binary.BigEndian, uint16(len(placements)))      // count
	_ = binary.Write(&out, binary.BigEndian, uint16(6+len(placements)*12)) // stringOffset
	for _, p := range placements {
		_ = binary.Write(&out, binary.BigEndian, uint16(3))      // platformID: Windows
		_ = binary.Write(&out, binary.BigEndian, uint16(1))      // encodingID: Unicode BMP
		_ = binary.Write(&out, binary.BigEndian, uint16(0x0409)) // languageID: en-US
		_ = binary.Write(&out, binary.BigEndian, p.nameID)
		_ = binary.Write(&out, binary.BigEndian, p.length)
		_ = binary.Write(&out, binary.BigEndian, p.offset)
	}
	out.Write(storage.Bytes())
	return out.Bytes()
}

// buildOS2Table builds a minimal (exactly os2MinLen bytes) sfnt "OS/2"
// table carrying only the three fields parseOS2Table reads.
func buildOS2Table(weightClass, fsSelection uint16, familyClass int16) []byte {
	data := make([]byte, os2MinLen)
	binary.BigEndian.PutUint16(data[4:6], weightClass)
	binary.BigEndian.PutUint16(data[30:32], uint16(familyClass))
	binary.BigEndian.PutUint16(data[62:64], fsSelection)
	return data
}

// buildTestTTC assembles a complete TrueType Collection file from
// faces, a list of already-encoded table sets (one per bundled face -
// see assembleSfnt/sfntTable in truetype_test.go). Each face is written
// as its own self-contained, non-overlapping run of bytes immediately
// following the last one, with the TTC header's own offset array
// pointing at where each face's table directory begins - see
// assembleSfnt's "base" parameter doc comment for why each face must be
// built with that face's own starting position as its base, rather than
// concatenating independently-built (base=0) blobs.
func buildTestTTC(faces [][]sfntTable) []byte {
	headerLen := 12 + len(faces)*4
	offsets := make([]uint32, len(faces))
	blobs := make([][]byte, len(faces))
	pos := uint32(headerLen)
	for i, tables := range faces {
		blob := assembleSfnt(sfntVersionTrueType, tables, pos)
		offsets[i] = pos
		blobs[i] = blob
		pos += uint32(len(blob))
	}

	var out bytes.Buffer
	out.WriteString("ttcf")
	_ = binary.Write(&out, binary.BigEndian, uint32(0x00010000)) // TTC header version
	_ = binary.Write(&out, binary.BigEndian, uint32(len(faces)))
	for _, off := range offsets {
		_ = binary.Write(&out, binary.BigEndian, off)
	}
	for _, blob := range blobs {
		out.Write(blob)
	}
	return out.Bytes()
}

func TestProbeFontFile_PlainTrueType(t *testing.T) {
	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmap := buildCmapFormat0Table(map[rune]uint16{'A': 1})
	name := buildNameTable(map[uint16]string{
		nameIDFamily:     "Arial",
		nameIDSubfamily:  "Regular",
		nameIDFullName:   "Arial",
		nameIDPostScript: "ArialMT",
	})
	os2 := buildOS2Table(400, 0x0040, 0x0800) // regular weight, fsSelection REGULAR bit, "Sans Serif" class

	// head/maxp/loca/glyf need real, consistent content - simplest to
	// reuse buildTestSfnt's own construction for those four tables and
	// splice in the extra "name"/"OS/2" tables this test cares about,
	// rather than duplicating loca/glyf layout logic here.
	base := buildTestSfnt(t, glyphs, cmap)
	baseTables, version, ok := parseTableDirectory(base, 0)
	if !ok || version != sfntVersionTrueType {
		t.Fatalf("buildTestSfnt produced an unparsable fixture")
	}
	tables := []sfntTable{
		{"head", baseTables["head"]},
		{"maxp", baseTables["maxp"]},
		{"loca", baseTables["loca"]},
		{"glyf", baseTables["glyf"]},
		{"cmap", baseTables["cmap"]},
		{"name", name},
		{"OS/2", os2},
	}
	data := assembleSfnt(sfntVersionTrueType, tables, 0)

	faces, err := ProbeFontFile(data)
	if err != nil {
		t.Fatalf("ProbeFontFile failed: %v", err)
	}
	if len(faces) != 1 {
		t.Fatalf("got %d faces, want 1", len(faces))
	}
	face := faces[0]

	if !face.HasOutlines {
		t.Errorf("HasOutlines = false, want true for a glyf-flavored face")
	}
	want := FontCharacteristics{Family: "Arial", Weight: 400}
	if face.Characteristics != want {
		t.Errorf("Characteristics = %+v, want %+v", face.Characteristics, want)
	}

	outline, ok := face.Outline()
	if !ok {
		t.Fatalf("Outline() failed for a valid glyf-flavored face")
	}
	glyph, ok := outline.GlyphOutline(1) // glyphs[1] is unitSquareGlyph.
	if !ok {
		t.Fatalf("Outline().GlyphOutline(1) failed")
	}
	minX, minY, maxX, maxY := pathBounds(t, glyph)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

// TestProbeFontFile_OTTOWithoutCFFTableHasNoOutlines exercises an OTTO
// (OpenType/CFF) face missing its own "CFF " table - a structurally odd
// but not impossible file (or, in this test, simply a fixture that
// omits it on purpose): still fully characterized via "name"/"OS/2" (see
// probeFace), but with no outline data of either kind, so HasOutlines
// must stay false and Outline must report ok=false rather than guessing.
func TestProbeFontFile_OTTOWithoutCFFTableHasNoOutlines(t *testing.T) {
	name := buildNameTable(map[uint16]string{
		nameIDFamily: "Times New Roman",
	})
	os2 := buildOS2Table(700, fsSelectionBold, 0x0200) // bold weight+flag, "Transitional Serifs" class
	tables := []sfntTable{
		{"name", name},
		{"OS/2", os2},
	}
	data := assembleSfnt(sfntVersionOTTO, tables, 0)

	faces, err := ProbeFontFile(data)
	if err != nil {
		t.Fatalf("ProbeFontFile failed on a structurally valid OTTO file: %v", err)
	}
	if len(faces) != 1 {
		t.Fatalf("got %d faces, want 1", len(faces))
	}
	face := faces[0]

	if face.HasOutlines {
		t.Errorf("HasOutlines = true for an OTTO face with no \"CFF \" table, want false")
	}
	if face.Characteristics.Family != "Times New Roman" {
		t.Errorf("Family = %q, want %q", face.Characteristics.Family, "Times New Roman")
	}
	if !face.Characteristics.Bold {
		t.Errorf("Bold = false, want true (OS/2 fsSelection bold bit was set)")
	}
	if !face.Characteristics.Serif {
		t.Errorf("Serif = false, want true (OS/2 sFamilyClass was Transitional Serifs)")
	}

	if _, ok := face.Outline(); ok {
		t.Errorf("Outline() succeeded for an OTTO face with no \"CFF \" table, want ok=false")
	}
}

// TestProbeFontFile_OTTOWithCFFTableHasOutlines is this file's
// counterpart to TestProbeFontFile_OTTOWithoutCFFTableHasNoOutlines,
// this time with a real "CFF " table present (the common real-world
// shape for an OTTO/OpenType-CFF font file) - Phase 3's cff.go support
// should make this face's outlines fully extractable via Outline, just
// like a glyf-flavored face already is.
func TestProbeFontFile_OTTOWithCFFTableHasOutlines(t *testing.T) {
	cffData := buildTestCFF(t, [][]byte{{}, squareCharstring()}, nil, nil, nil)
	name := buildNameTable(map[uint16]string{nameIDFamily: "Test CFF"})
	tables := []sfntTable{
		{"name", name},
		{"CFF ", cffData},
	}
	data := assembleSfnt(sfntVersionOTTO, tables, 0)

	faces, err := ProbeFontFile(data)
	if err != nil {
		t.Fatalf("ProbeFontFile failed: %v", err)
	}
	if len(faces) != 1 {
		t.Fatalf("got %d faces, want 1", len(faces))
	}
	face := faces[0]

	if !face.HasOutlines {
		t.Errorf("HasOutlines = false for an OTTO face with a \"CFF \" table, want true")
	}
	outline, ok := face.Outline()
	if !ok {
		t.Fatalf("Outline() failed for a valid CFF-flavored face")
	}
	glyph, ok := outline.GlyphOutline(1)
	if !ok {
		t.Fatalf("Outline().GlyphOutline(1) failed")
	}
	minX, minY, maxX, maxY := pathBounds(t, glyph)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

func TestProbeFontFile_TrueTypeCollection(t *testing.T) {
	regularName := buildNameTable(map[uint16]string{nameIDFamily: "Cousine", nameIDSubfamily: "Regular"})
	boldName := buildNameTable(map[uint16]string{nameIDFamily: "Cousine", nameIDSubfamily: "Bold"})

	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmap := buildCmapFormat0Table(map[rune]uint16{'A': 1})

	// Build each face's glyf/loca/head/maxp independently (a TTC's faces
	// need not share any tables - this test just happens to give them
	// identical outlines, which is fine, since only the "name"/"OS/2"
	// data is under test here).
	regularBase := buildTestSfnt(t, glyphs, cmap)
	regularBaseTables, _, ok := parseTableDirectory(regularBase, 0)
	if !ok {
		t.Fatalf("buildTestSfnt produced an unparsable fixture")
	}
	boldBase := buildTestSfnt(t, glyphs, cmap)
	boldBaseTables, _, ok := parseTableDirectory(boldBase, 0)
	if !ok {
		t.Fatalf("buildTestSfnt produced an unparsable fixture")
	}

	faces := [][]sfntTable{
		{
			{"head", regularBaseTables["head"]},
			{"maxp", regularBaseTables["maxp"]},
			{"loca", regularBaseTables["loca"]},
			{"glyf", regularBaseTables["glyf"]},
			{"cmap", regularBaseTables["cmap"]},
			{"name", regularName},
			{"OS/2", buildOS2Table(400, 0, 0)},
		},
		{
			{"head", boldBaseTables["head"]},
			{"maxp", boldBaseTables["maxp"]},
			{"loca", boldBaseTables["loca"]},
			{"glyf", boldBaseTables["glyf"]},
			{"cmap", boldBaseTables["cmap"]},
			{"name", boldName},
			{"OS/2", buildOS2Table(700, fsSelectionBold, 0)},
		},
	}
	data := buildTestTTC(faces)

	got, err := ProbeFontFile(data)
	if err != nil {
		t.Fatalf("ProbeFontFile failed on a well-formed TTC: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d faces, want 2", len(got))
	}
	if got[0].Characteristics.Bold {
		t.Errorf("face 0 Bold = true, want false (regular weight)")
	}
	if !got[1].Characteristics.Bold {
		t.Errorf("face 1 Bold = false, want true (bold weight)")
	}
	for i, face := range got {
		if !face.HasOutlines {
			t.Errorf("face %d HasOutlines = false, want true", i)
		}
		if _, ok := face.Outline(); !ok {
			t.Errorf("face %d Outline() failed", i)
		}
	}
}

func TestProbeFontFile_RejectsGarbage(t *testing.T) {
	if _, err := ProbeFontFile([]byte("not a font file at all")); err == nil {
		t.Errorf("ProbeFontFile accepted unrecognized garbage input")
	}
	if _, err := ProbeFontFile(nil); err == nil {
		t.Errorf("ProbeFontFile accepted empty input")
	}
}

func TestProbeFontFile_RejectsTruncatedTTC(t *testing.T) {
	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmap := buildCmapFormat0Table(map[rune]uint16{'A': 1})
	base := buildTestSfnt(t, glyphs, cmap)
	baseTables, _, _ := parseTableDirectory(base, 0)
	tables := []sfntTable{
		{"head", baseTables["head"]},
		{"maxp", baseTables["maxp"]},
		{"loca", baseTables["loca"]},
		{"glyf", baseTables["glyf"]},
		{"cmap", baseTables["cmap"]},
	}
	full := buildTestTTC([][]sfntTable{tables})
	for cut := 0; cut < len(full); cut += 11 {
		// The only real assertion is the absence of a panic, regardless
		// of where the file is cut - matching
		// TestParseSfnt_RejectsTruncatedInput's precedent in
		// truetype_test.go.
		ProbeFontFile(full[:cut]) //nolint:errcheck // only absence of a panic is under test
	}
}

func TestParseTTCHeader_RejectsUnreasonableFaceCount(t *testing.T) {
	data := make([]byte, 12)
	copy(data, "ttcf")
	binary.BigEndian.PutUint32(data[8:12], maxTTCFaces+1)
	if _, ok := parseTTCHeader(data); ok {
		t.Errorf("parseTTCHeader accepted a face count above maxTTCFaces")
	}
}

func TestParseTTCHeader_RejectsZeroFaces(t *testing.T) {
	data := make([]byte, 12)
	copy(data, "ttcf")
	binary.BigEndian.PutUint32(data[8:12], 0)
	if _, ok := parseTTCHeader(data); ok {
		t.Errorf("parseTTCHeader accepted a header claiming zero faces")
	}
}

func TestSelectName_PrefersWindowsUnicodeOverMacRoman(t *testing.T) {
	entries := []nameTableEntry{
		{platformID: 1, encodingID: 0, nameID: nameIDFamily, value: "MacRomanName"},
		{platformID: 3, encodingID: 1, nameID: nameIDFamily, value: "WindowsName"},
	}
	got, ok := selectName(entries, nameIDFamily)
	if !ok || got != "WindowsName" {
		t.Errorf("selectName = (%q, %v), want (\"WindowsName\", true)", got, ok)
	}
}

func TestSelectName_NotFound(t *testing.T) {
	if _, ok := selectName(nil, nameIDFamily); ok {
		t.Errorf("selectName reported found on an empty entry list")
	}
}

func TestCharacterizeFace_FallsBackToNameKeywordsWithoutOS2(t *testing.T) {
	names := []nameTableEntry{
		{platformID: 3, encodingID: 1, nameID: nameIDFamily, value: "Verdana"},
		{platformID: 3, encodingID: 1, nameID: nameIDSubfamily, value: "Bold Italic"},
	}
	got := characterizeFace(names, os2Table{}, false)
	if !got.Bold || !got.Italic {
		t.Errorf("characterizeFace without OS/2 = %+v, want Bold=true, Italic=true from subfamily text", got)
	}
	if got.Family != "Verdana" {
		t.Errorf("Family = %q, want %q", got.Family, "Verdana")
	}
}

func TestCharacterizeFace_FamilyFallsBackToPostScriptName(t *testing.T) {
	names := []nameTableEntry{
		{platformID: 3, encodingID: 1, nameID: nameIDPostScript, value: "Arial-BoldMT"},
	}
	got := characterizeFace(names, os2Table{}, false)
	if got.Family != "Arial" {
		t.Errorf("Family = %q, want %q (parsed from PostScript name)", got.Family, "Arial")
	}
	if !got.Bold {
		t.Errorf("Bold = false, want true (parsed from PostScript name)")
	}
}

func TestParseOS2Table_RejectsTooShort(t *testing.T) {
	if _, ok := parseOS2Table(make([]byte, os2MinLen-1)); ok {
		t.Errorf("parseOS2Table accepted a table one byte shorter than os2MinLen")
	}
	if _, ok := parseOS2Table(make([]byte, os2MinLen)); !ok {
		t.Errorf("parseOS2Table rejected a table exactly os2MinLen bytes long")
	}
}

func TestParseNameTable_RejectsTruncated(t *testing.T) {
	full := buildNameTable(map[uint16]string{nameIDFamily: "Arial"})
	for cut := 0; cut < len(full); cut++ {
		// Must never panic regardless of where the table is cut, and a
		// cut version must never report more usable entries than the
		// full table - the real assertion is the absence of a panic
		// (which would fail the test on its own), matching
		// TestParseSfnt_RejectsTruncatedInput's precedent in
		// truetype_test.go.
		parseNameTable(full[:cut]) //nolint:errcheck // only absence of a panic is under test
	}
}

func TestDecodeNameBytes_MacRomanPassesThroughAsIs(t *testing.T) {
	// Platform 1 (Macintosh) strings are single-byte, not UTF-16BE - a
	// plain ASCII string should come back unchanged.
	got := decodeNameBytes(1, []byte("Arial"))
	if got != "Arial" {
		t.Errorf("decodeNameBytes(platform 1) = %q, want %q", got, "Arial")
	}
}

func TestUtf16BEToString_DropsOddTrailingByte(t *testing.T) {
	// "A" (0x0041) followed by one stray trailing byte, which is not a
	// complete UTF-16 code unit and must be dropped rather than causing
	// a panic or a corrupted decode of the valid part before it.
	got := utf16BEToString([]byte{0x00, 0x41, 0xFF})
	if got != "A" {
		t.Errorf("utf16BEToString with an odd trailing byte = %q, want %q", got, "A")
	}
}
