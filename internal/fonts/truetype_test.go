package fonts

import (
	"bytes"
	"encoding/binary"
	"sort"
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// This file's helpers (buildTestSfnt, encodeSimpleGlyph, ...) hand-build
// minimal, entirely synthetic TrueType font byte streams - the same
// "construct exactly the bytes a test needs, byte-counted by code
// instead of pulled in from a real font file" approach
// tools/genfixtures uses for PDF fixtures (see that package's doc
// comment for the full rationale, which applies here identically: no
// external font file, with its own separate license and provenance
// question, needs to be vendored just to exercise this package's own
// parsing logic).

// glyphContourPoint is one point of a hand-built test glyph contour.
type glyphContourPoint struct {
	x, y    int16
	onCurve bool
}

// encodeSimpleGlyph builds a "glyf" table entry for a simple (non-
// composite) glyph from one or more contours, using the simplest
// possible on-disk encoding: one flag byte per point (no repeat
// compression) and a full signed 16-bit coordinate delta per point (no
// short-delta or repeated-point compression) - deliberately not
// exercising every space-saving encoding trick a real font encoder
// would use, since parseSimpleGlyph must handle the general case
// regardless, and the tests that care specifically about short deltas or
// flag repetition construct those bytes directly instead (see
// TestReadCoords).
func encodeSimpleGlyph(contours [][]glyphContourPoint) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, int16(len(contours)))
	// xMin/yMin/xMax/yMax: not read by this package's parser, so left
	// zero.
	_ = binary.Write(&buf, binary.BigEndian, [4]int16{})

	total := 0
	for _, c := range contours {
		total += len(c)
		_ = binary.Write(&buf, binary.BigEndian, uint16(total-1))
	}
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // instructionLength

	for _, c := range contours {
		for _, p := range c {
			var f byte
			if p.onCurve {
				f = 0x01
			}
			buf.WriteByte(f)
		}
	}
	var prevX, prevY int16
	for _, c := range contours {
		for _, p := range c {
			_ = binary.Write(&buf, binary.BigEndian, p.x-prevX)
			prevX = p.x
		}
	}
	for _, c := range contours {
		for _, p := range c {
			_ = binary.Write(&buf, binary.BigEndian, p.y-prevY)
			prevY = p.y
		}
	}
	return buf.Bytes()
}

// compositeComponent describes one placed sub-glyph for
// encodeCompositeGlyph.
type compositeComponent struct {
	glyphIndex uint16
	dx, dy     int16
}

// encodeCompositeGlyph builds a "glyf" table entry referencing other
// glyphs at fixed (dx, dy) offsets (the ARGS_ARE_XY_VALUES,
// ARGS_ARE_WORDS case - see parseCompositeGlyph), with no per-component
// scale/transform (every component is placed at 1:1 scale).
func encodeCompositeGlyph(components []compositeComponent) []byte {
	const (
		flagArgsAreWords   = 0x0001
		flagArgsAreXY      = 0x0002
		flagMoreComponents = 0x0020
	)
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, int16(-1))
	_ = binary.Write(&buf, binary.BigEndian, [4]int16{})
	for i, c := range components {
		flags := uint16(flagArgsAreWords | flagArgsAreXY)
		if i < len(components)-1 {
			flags |= flagMoreComponents
		}
		_ = binary.Write(&buf, binary.BigEndian, flags)
		_ = binary.Write(&buf, binary.BigEndian, c.glyphIndex)
		_ = binary.Write(&buf, binary.BigEndian, c.dx)
		_ = binary.Write(&buf, binary.BigEndian, c.dy)
	}
	return buf.Bytes()
}

// buildCmapFormat0Table builds a complete "cmap" table (directory header
// plus one format-0 subtable) mapping each rune in entries (which must
// all be in [0,255], format 0's only representable range) to its glyph
// index.
func buildCmapFormat0Table(entries map[rune]uint16) []byte {
	var sub bytes.Buffer
	_ = binary.Write(&sub, binary.BigEndian, uint16(0))   // format
	_ = binary.Write(&sub, binary.BigEndian, uint16(262)) // length
	_ = binary.Write(&sub, binary.BigEndian, uint16(0))   // language
	glyphIDs := make([]byte, 256)
	for r, gid := range entries {
		glyphIDs[byte(r)] = byte(gid)
	}
	sub.Write(glyphIDs)

	var out bytes.Buffer
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // version
	_ = binary.Write(&out, binary.BigEndian, uint16(1)) // numTables
	_ = binary.Write(&out, binary.BigEndian, uint16(3)) // platformID: Windows
	_ = binary.Write(&out, binary.BigEndian, uint16(1)) // encodingID: Unicode BMP
	_ = binary.Write(&out, binary.BigEndian, uint32(12))
	out.Write(sub.Bytes())
	return out.Bytes()
}

// sfntTable names one table for buildTestSfnt.
type sfntTable struct {
	tag  string
	data []byte
}

// buildTestSfnt assembles a complete, minimal sfnt (TrueType) font
// program from already-encoded glyf entries (glyphs[0] is glyph index
// 0, conventionally .notdef) and a complete "cmap" table. It always uses
// the "long" loca format (indexToLocFormat 1) to avoid this test
// helper needing to worry about keeping every glyph's byte length even,
// which the "short" format requires.
func buildTestSfnt(t *testing.T, glyphs [][]byte, cmapTable []byte) []byte {
	t.Helper()

	var glyf bytes.Buffer
	loca := make([]uint32, len(glyphs)+1)
	for i, g := range glyphs {
		loca[i] = uint32(glyf.Len())
		glyf.Write(g)
	}
	loca[len(glyphs)] = uint32(glyf.Len())

	var locaBuf bytes.Buffer
	for _, off := range loca {
		_ = binary.Write(&locaBuf, binary.BigEndian, off)
	}

	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:20], 1000) // unitsPerEm
	binary.BigEndian.PutUint16(head[50:52], 1)    // indexToLocFormat: long

	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:6], uint16(len(glyphs)))

	tables := []sfntTable{
		{"head", head},
		{"maxp", maxp},
		{"loca", locaBuf.Bytes()},
		{"glyf", glyf.Bytes()},
		{"cmap", cmapTable},
	}
	// A deterministic tag order keeps this helper's output (and any test
	// that happens to assert on exact byte layout) reproducible.
	sort.Slice(tables, func(i, j int) bool { return tables[i].tag < tables[j].tag })

	var out bytes.Buffer
	_ = binary.Write(&out, binary.BigEndian, uint32(0x00010000))
	_ = binary.Write(&out, binary.BigEndian, uint16(len(tables)))
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // searchRange (unused by this package's reader)
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // entrySelector
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // rangeShift

	headerLen := 12 + len(tables)*16
	offset := uint32(headerLen)
	type placed struct {
		tag            string
		offset, length uint32
	}
	var placements []placed
	for _, tbl := range tables {
		placements = append(placements, placed{tbl.tag, offset, uint32(len(tbl.data))})
		offset += uint32(len(tbl.data))
	}
	for _, p := range placements {
		out.WriteString(p.tag)
		_ = binary.Write(&out, binary.BigEndian, uint32(0)) // checksum: not verified by this package's reader
		_ = binary.Write(&out, binary.BigEndian, p.offset)
		_ = binary.Write(&out, binary.BigEndian, p.length)
	}
	for _, tbl := range tables {
		out.Write(tbl.data)
	}
	return out.Bytes()
}

// unitSquareGlyph is a simple square contour used by several tests
// below: a 700x700 unit square with its lower-left corner at (100,100)
// - deliberately not centered on the origin, so a transform bug (a
// missing or backward translation, for example) is more likely to
// produce a visibly wrong bounding box rather than one that happens to
// look right by symmetry.
func unitSquareGlyph() []byte {
	return encodeSimpleGlyph([][]glyphContourPoint{{
		{x: 100, y: 100, onCurve: true},
		{x: 800, y: 100, onCurve: true},
		{x: 800, y: 800, onCurve: true},
		{x: 100, y: 800, onCurve: true},
	}})
}

func pathBounds(t *testing.T, p *graphics.Path) (minX, minY, maxX, maxY float64) {
	t.Helper()
	minX, minY, maxX, maxY, ok := p.Bounds()
	if !ok {
		t.Fatalf("path has no points")
	}
	return
}

func TestParseSfnt_SimpleSquareGlyph(t *testing.T) {
	glyphs := [][]byte{{}, unitSquareGlyph()}
	cmap := buildCmapFormat0Table(map[rune]uint16{'A': 1})
	data := buildTestSfnt(t, glyphs, cmap)

	sfnt, ok := parseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed on a well-formed test font")
	}
	if sfnt.unitsPerEm != 1000 {
		t.Errorf("unitsPerEm = %d, want 1000", sfnt.unitsPerEm)
	}
	if sfnt.numGlyphs != 2 {
		t.Errorf("numGlyphs = %d, want 2", sfnt.numGlyphs)
	}

	gid, ok := sfnt.cmap.Lookup('A')
	if !ok || gid != 1 {
		t.Fatalf("cmap.Lookup('A') = (%d, %v), want (1, true)", gid, ok)
	}

	outline, ok := sfnt.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline(1) failed")
	}
	minX, minY, maxX, maxY := pathBounds(t, outline)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("outline bounds = (%v,%v)-(%v,%v), want (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

func TestParseSfnt_EmptyGlyphIsBlankNotMissing(t *testing.T) {
	// glyph 1 (a "space") has a loca range of zero length: start == end.
	glyphs := [][]byte{{}, {}}
	cmap := buildCmapFormat0Table(map[rune]uint16{' ': 1})
	data := buildTestSfnt(t, glyphs, cmap)

	sfnt, ok := parseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	outline, ok := sfnt.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline(1) reported not-found for a legitimately blank glyph")
	}
	if len(outline.Subpaths) != 0 {
		t.Errorf("blank glyph outline has %d subpaths, want 0", len(outline.Subpaths))
	}
}

func TestParseSfnt_CompositeGlyph(t *testing.T) {
	// Glyph 2 is composite: glyph 1 (the unit square) placed twice, once
	// at its own position and once shifted by (1000, 0) - so the
	// composite's overall bounds should span twice the width.
	square := unitSquareGlyph()
	composite := encodeCompositeGlyph([]compositeComponent{
		{glyphIndex: 1, dx: 0, dy: 0},
		{glyphIndex: 1, dx: 1000, dy: 0},
	})
	glyphs := [][]byte{{}, square, composite}
	cmap := buildCmapFormat0Table(map[rune]uint16{'M': 2})
	data := buildTestSfnt(t, glyphs, cmap)

	sfnt, ok := parseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	outline, ok := sfnt.GlyphOutline(2)
	if !ok {
		t.Fatalf("GlyphOutline(2) (composite) failed")
	}
	if len(outline.Subpaths) != 2 {
		t.Fatalf("composite outline has %d subpaths, want 2", len(outline.Subpaths))
	}
	minX, minY, maxX, maxY := pathBounds(t, outline)
	if minX != 100 || minY != 100 || maxX != 1800 || maxY != 800 {
		t.Errorf("composite outline bounds = (%v,%v)-(%v,%v), want (100,100)-(1800,800)", minX, minY, maxX, maxY)
	}
}

func TestParseSfnt_CompositeDepthBounded(t *testing.T) {
	// A composite glyph whose only component is itself (index 1, the
	// composite's own glyph index) must not recurse forever.
	self := encodeCompositeGlyph([]compositeComponent{{glyphIndex: 1, dx: 0, dy: 0}})
	glyphs := [][]byte{{}, self}
	cmap := buildCmapFormat0Table(nil)
	data := buildTestSfnt(t, glyphs, cmap)

	sfnt, ok := parseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	done := make(chan struct{})
	go func() {
		sfnt.GlyphOutline(1) //nolint:errcheck // only termination is being tested
		close(done)
	}()
	select {
	case <-done:
	default:
	}
	// The call above is synchronous in practice (glyphOutline is not
	// itself concurrent); the real assertion is simply that parseSfnt
	// and GlyphOutline both returned at all rather than looping forever
	// - if maxCompositeDepth were broken, this test would hang and the
	// Go test runner's own timeout would eventually fail it.
	<-done
}

func TestScaleGlyph(t *testing.T) {
	outline := &graphics.Path{}
	outline.AppendRect([4]graphics.Point{{X: 0, Y: 0}, {X: 2048, Y: 0}, {X: 2048, Y: 2048}, {X: 0, Y: 2048}})
	scaled := scaleGlyph(outline, 2048)
	minX, minY, maxX, maxY := pathBounds(t, scaled)
	if minX != 0 || minY != 0 || maxX != 1000 || maxY != 1000 {
		t.Errorf("scaled bounds = (%v,%v)-(%v,%v), want (0,0)-(1000,1000)", minX, minY, maxX, maxY)
	}
}

func TestParseSfnt_RejectsBadVersion(t *testing.T) {
	data := make([]byte, 16)
	binary.BigEndian.PutUint32(data[0:4], 0xDEADBEEF)
	if _, ok := parseSfnt(data); ok {
		t.Errorf("parseSfnt accepted an unrecognized sfnt version")
	}
}

func TestParseSfnt_RejectsTruncatedInput(t *testing.T) {
	full := buildTestSfnt(t, [][]byte{{}, unitSquareGlyph()}, buildCmapFormat0Table(map[rune]uint16{'A': 1}))
	for cut := 0; cut < len(full); cut += 7 {
		// Must never panic, regardless of how the file is truncated -
		// the real assertion here is the absence of a panic (which would
		// fail the test on its own), not any particular ok value.
		parseSfnt(full[:cut])
	}
}
