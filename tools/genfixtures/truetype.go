package main

import (
	"bytes"
	"encoding/binary"
)

// This file hand-builds a tiny, entirely synthetic TrueType ("sfnt")
// font program byte-for-byte, the same way every other fixture in this
// package builds PDF bytes: mechanically, by code, rather than by
// pulling in a real font file - see this package's doc comment for why
// that matters (byte-exact reproducibility and no external file with
// its own license/provenance question to track). It is deliberately not
// a full TrueType encoder; it only writes the small handful of tables
// internal/fonts's parser (internal/fonts/truetype.go) actually reads:
// "head", "maxp", "loca", "glyf", and "cmap".
//
// The font this file builds has exactly two glyphs: glyph 0 (.notdef,
// conventionally empty) and glyph 1, a simple filled square occupying
// most of the em square, reached via a format-0 "byte encoding table"
// cmap subtable that maps the ASCII code for 'A' (0x41) directly to
// glyph index 1. That single glyph is enough to exercise this project's
// entire Phase 4 text pipeline end to end (font loading, encoding
// resolution, cmap lookup, glyph outline extraction, and glyph
// positioning) without needing more than one distinguishable shape.

// glyphSquareUnits is the square glyph's extent within its 1000-unit
// em, chosen with a visible margin on every side (not spanning the full
// 0-1000 range) so a test sampling a pixel just outside the glyph's
// expected device-space footprint can reliably expect *not* to find
// ink there.
const (
	glyphSquareMin = 150
	glyphSquareMax = 850
)

// buildTestFontProgram returns a complete, minimal TrueType font
// program (suitable for a PDF /FontFile2 stream) with unitsPerEm 1000
// and a single visible glyph (index 1, a filled square - see this
// file's doc comment) reachable both directly by glyph index (for a
// Type0/CIDFontType2 fixture using /CIDToGIDMap /Identity) and via its
// cmap entry for 'A' (for a simple TrueType font fixture).
func buildTestFontProgram() []byte {
	notdef := encodeSimpleTTGlyph(nil)
	square := encodeSimpleTTGlyph([]ttContourPoint{
		{x: glyphSquareMin, y: glyphSquareMin, onCurve: true},
		{x: glyphSquareMax, y: glyphSquareMin, onCurve: true},
		{x: glyphSquareMax, y: glyphSquareMax, onCurve: true},
		{x: glyphSquareMin, y: glyphSquareMax, onCurve: true},
	})
	cmap := buildTTCmapFormat0(map[byte]uint16{'A': 1})
	return assembleSfnt([][]byte{notdef, square}, cmap)
}

// ttContourPoint is one point of a hand-built glyph contour - see
// encodeSimpleTTGlyph.
type ttContourPoint struct {
	x, y    int16
	onCurve bool
}

// encodeSimpleTTGlyph builds a "glyf" table entry for a single-contour
// simple glyph (points is nil for an intentionally empty/blank glyph,
// such as .notdef here), using the simplest on-disk encoding TrueType
// permits: one flag byte per point (no repeat compression) and a full
// signed 16-bit coordinate delta per point (no short-delta encoding).
// internal/fonts's parser (parseSimpleGlyph) must handle the general
// case regardless of which encoding a real font happens to use, so
// there is no need for this generator to exercise the more compact
// encodings.
func encodeSimpleTTGlyph(points []ttContourPoint) []byte {
	if len(points) == 0 {
		return nil
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, int16(1)) // numContours
	_ = binary.Write(&buf, binary.BigEndian, [4]int16{})
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(points)-1)) // endPtsOfContours[0]
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))             // instructionLength

	for _, p := range points {
		var f byte
		if p.onCurve {
			f = 0x01
		}
		buf.WriteByte(f)
	}
	var prevX, prevY int16
	for _, p := range points {
		_ = binary.Write(&buf, binary.BigEndian, p.x-prevX)
		prevX = p.x
	}
	for _, p := range points {
		_ = binary.Write(&buf, binary.BigEndian, p.y-prevY)
		prevY = p.y
	}
	return buf.Bytes()
}

// buildTTCmapFormat0 builds a complete "cmap" table (directory header
// plus one format-0 "byte encoding table" subtable, platform 3
// (Windows) encoding 1 (Unicode BMP) - internal/fonts's
// selectCmapSubtable prefers exactly this (platform, encoding) pair)
// mapping each single-byte code in entries to its glyph index.
func buildTTCmapFormat0(entries map[byte]uint16) []byte {
	var sub bytes.Buffer
	_ = binary.Write(&sub, binary.BigEndian, uint16(0))   // format
	_ = binary.Write(&sub, binary.BigEndian, uint16(262)) // length
	_ = binary.Write(&sub, binary.BigEndian, uint16(0))   // language
	glyphIDs := make([]byte, 256)
	for code, gid := range entries {
		glyphIDs[code] = byte(gid)
	}
	sub.Write(glyphIDs)

	var out bytes.Buffer
	_ = binary.Write(&out, binary.BigEndian, uint16(0))  // version
	_ = binary.Write(&out, binary.BigEndian, uint16(1))  // numTables
	_ = binary.Write(&out, binary.BigEndian, uint16(3))  // platformID: Windows
	_ = binary.Write(&out, binary.BigEndian, uint16(1))  // encodingID: Unicode BMP
	_ = binary.Write(&out, binary.BigEndian, uint32(12)) // offset of the subtable below
	out.Write(sub.Bytes())
	return out.Bytes()
}

// assembleSfnt writes a complete sfnt (TrueType) font program from
// already-encoded glyf entries (glyphs[i] is glyph index i's outline
// data, possibly nil/empty for a blank glyph) and a complete "cmap"
// table, using the "long" loca format (indexToLocFormat 1) so every
// glyph's encoded byte length - even or odd - can be used directly
// without the "short" format's implicit doubling.
func assembleSfnt(glyphs [][]byte, cmapTable []byte) []byte {
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

	// A fixed table order (alphabetical by tag) keeps this generator's
	// output byte-for-byte reproducible, matching the rest of this
	// package's reproducibility guarantee (see the package doc comment).
	type table struct {
		tag  string
		data []byte
	}
	tables := []table{
		{"cmap", cmapTable},
		{"glyf", glyf.Bytes()},
		{"head", head},
		{"loca", locaBuf.Bytes()},
		{"maxp", maxp},
	}

	var out bytes.Buffer
	_ = binary.Write(&out, binary.BigEndian, uint32(0x00010000)) // sfnt version
	_ = binary.Write(&out, binary.BigEndian, uint16(len(tables)))
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // searchRange
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // entrySelector
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // rangeShift

	headerLen := 12 + len(tables)*16
	offset := uint32(headerLen)
	offsets := make([]uint32, len(tables))
	for i, t := range tables {
		offsets[i] = offset
		offset += uint32(len(t.data))
	}
	for i, t := range tables {
		out.WriteString(t.tag)
		_ = binary.Write(&out, binary.BigEndian, uint32(0)) // checksum: unused by this project's reader
		_ = binary.Write(&out, binary.BigEndian, offsets[i])
		_ = binary.Write(&out, binary.BigEndian, uint32(len(t.data)))
	}
	for _, t := range tables {
		out.Write(t.data)
	}
	return out.Bytes()
}
