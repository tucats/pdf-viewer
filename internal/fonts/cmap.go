package fonts

import "encoding/binary"

// This file implements just enough of a TrueType font's "cmap" table -
// the character-code-to-glyph-index mapping described in truetype.go's
// package-level doc comment - to look up the common case: a Latin-script
// PDF simple font, encoded through one of this package's runeTables
// (encoding.go) into Unicode code points, matched against an embedded
// font's Unicode cmap subtable.
//
// A "cmap" table can contain several subtables, one per (platform,
// encoding) pair the font wants to support lookups for (for example, one
// for Windows Unicode, one for classic Mac OS Roman, one for a symbol
// font's private code space) - selectCmapSubtable below picks the single
// best one for this package's purposes and discards the rest, since this
// package only ever needs one code-to-glyph-index function per font.
//
// Only cmap subtable formats 0 (byte encoding table), 4 (segment mapping
// to delta values - by far the most common format for a Unicode BMP
// subtable in real-world fonts), and 6 (trimmed table mapping) are
// implemented. Format 12 (segmented coverage, needed for code points
// beyond the Basic Multilingual Plane - astral emoji, some CJK
// extensions) and the handful of rarer formats (2, 8, 10, 13, 14) are
// not: a font whose only Unicode subtable uses one of those formats is
// treated as having no usable cmap at all (cmapSubtable's zero value),
// which falls back to this package's documented missing-glyph policy -
// see font.go's notdefGlyph - for every code, rather than misreading the
// subtable's bytes as if they were a supported format.

// cmapSubtable is the parsed form of one cmap subtable, reduced to the
// one operation this package needs from it: rune (Unicode code point) or
// raw code -> glyph index. Its zero value (no entries) correctly reports
// every lookup as "not found" via Lookup below, which is what a font
// with no usable cmap subtable (see this file's doc comment) falls back
// to.
type cmapSubtable struct {
	// entries maps a code point (interpreted as whatever the subtable's
	// own (platform, encoding) pair defines - Unicode for the subtables
	// this package prefers, or a raw single-byte code for a format 0
	// "symbol"-style subtable - see selectCmapSubtable) directly to a
	// glyph index. A plain Go map, rather than the more compact ranged
	// representations formats 4/6 use on disk, is simplest to look up
	// from and is not a memory concern: even a large font's cmap
	// subtable covers at most a few tens of thousands of code points.
	entries map[rune]uint16
}

// Lookup returns the glyph index cmap associates with r, and ok=false if
// r has no entry (including when cmap itself is the zero value, i.e. no
// usable subtable was found - see this file's doc comment).
func (c cmapSubtable) Lookup(r rune) (uint16, bool) {
	gid, ok := c.entries[r]
	return gid, ok
}

// parseCmap parses an sfnt "cmap" table's bytes, selecting and decoding
// the single subtable selectCmapSubtable prefers. ok=false means no
// subtable this package understands could be found or decoded - see
// Lookup's doc comment for what that means downstream.
func parseCmap(data []byte) (cmapSubtable, bool) {
	if len(data) < 4 {
		return cmapSubtable{}, false
	}
	numTables := int(binary.BigEndian.Uint16(data[2:4]))
	records := make([]cmapRecord, 0, numTables)
	for i := 0; i < numTables; i++ {
		pos := 4 + i*8
		if pos+8 > len(data) {
			break
		}
		records = append(records, cmapRecord{
			platformID: binary.BigEndian.Uint16(data[pos : pos+2]),
			encodingID: binary.BigEndian.Uint16(data[pos+2 : pos+4]),
			offset:     binary.BigEndian.Uint32(data[pos+4 : pos+8]),
		})
	}

	offset, ok := selectCmapSubtable(records)
	if !ok || int64(offset) >= int64(len(data)) {
		return cmapSubtable{}, false
	}
	return parseCmapSubtable(data[offset:])
}

// cmapRecord is one entry of a "cmap" table's subtable directory: which
// (platform, encoding) pair a subtable claims to serve, and its byte
// offset (from the start of the "cmap" table) - see parseCmap and
// selectCmapSubtable.
type cmapRecord struct {
	platformID, encodingID uint16
	offset                 uint32
}

// selectCmapSubtable picks the byte offset (within the "cmap" table) of
// the single best subtable for this package's purposes, in the standard
// preference order real-world PDF renderers use: prefer a Unicode BMP
// subtable (Windows platform 3, encoding 1 - by a wide margin the most
// common in real fonts; or the equivalent Unicode platform 0 subtable),
// then fall back to a Windows "symbol" subtable (platform 3, encoding
// 0 - used by symbol/dingbat fonts, whose codes are conventionally
// looked up as 0xF000+code, handled by the caller rather than here - see
// simple.go), then finally a classic Mac Roman subtable (platform 1,
// encoding 0), which is rare in modern fonts but occasionally still
// seen. ok=false means the font's cmap table lists no subtable this
// package recognizes.
func selectCmapSubtable(records []cmapRecord) (uint32, bool) {
	var best uint32
	bestScore := -1
	for _, r := range records {
		score := -1
		switch {
		case r.platformID == 3 && r.encodingID == 1:
			score = 3
		case r.platformID == 0:
			score = 2
		case r.platformID == 3 && r.encodingID == 0:
			score = 1
		case r.platformID == 1 && r.encodingID == 0:
			score = 0
		}
		if score > bestScore {
			bestScore = score
			best = r.offset
		}
	}
	return best, bestScore >= 0
}

// parseCmapSubtable dispatches on data's leading format field (the first
// big-endian uint16 of any cmap subtable) to the matching format-specific
// parser - see this file's doc comment for which formats are supported.
func parseCmapSubtable(data []byte) (cmapSubtable, bool) {
	if len(data) < 2 {
		return cmapSubtable{}, false
	}
	switch binary.BigEndian.Uint16(data[0:2]) {
	case 0:
		return parseCmapFormat0(data)
	case 4:
		return parseCmapFormat4(data)
	case 6:
		return parseCmapFormat6(data)
	default:
		return cmapSubtable{}, false
	}
}

// parseCmapFormat0 decodes format 0 ("byte encoding table"): the
// simplest possible cmap format, a flat array of exactly 256 glyph
// indices, one per possible single-byte code. Common for classic Mac
// Roman (platform 1) subtables and simple symbol fonts.
func parseCmapFormat0(data []byte) (cmapSubtable, bool) {
	const headerLen = 6
	if len(data) < headerLen+256 {
		return cmapSubtable{}, false
	}
	entries := make(map[rune]uint16, 256)
	for code := 0; code < 256; code++ {
		gid := data[headerLen+code]
		if gid != 0 {
			entries[rune(code)] = uint16(gid)
		}
	}
	return cmapSubtable{entries: entries}, true
}

// parseCmapFormat6 decodes format 6 ("trimmed table mapping"): a
// contiguous run of glyph indices for codes [firstCode, firstCode+
// entryCount).
func parseCmapFormat6(data []byte) (cmapSubtable, bool) {
	const headerLen = 10
	if len(data) < headerLen {
		return cmapSubtable{}, false
	}
	firstCode := int(binary.BigEndian.Uint16(data[6:8]))
	entryCount := int(binary.BigEndian.Uint16(data[8:10]))
	if headerLen+entryCount*2 > len(data) {
		return cmapSubtable{}, false
	}
	entries := make(map[rune]uint16, entryCount)
	for i := 0; i < entryCount; i++ {
		gid := binary.BigEndian.Uint16(data[headerLen+i*2 : headerLen+i*2+2])
		if gid != 0 {
			entries[rune(firstCode+i)] = gid
		}
	}
	return cmapSubtable{entries: entries}, true
}

// parseCmapFormat4 decodes format 4 ("segment mapping to delta values"),
// the format essentially every real-world Unicode BMP subtable uses. It
// describes the mapping as a list of contiguous code-point *segments*
// (start/end code pairs), each mapped to glyph indices either by a
// constant offset ("idDelta", added to the code point, modulo 65536) or
// through an explicit per-code lookup array ("idRangeOffset", a
// deliberately awkward indirection through the same 16-bit-word address
// space the format's original 1990s designers used to save table space -
// see the inline comment at its use below for exactly how it is
// dereferenced).
func parseCmapFormat4(data []byte) (cmapSubtable, bool) {
	const headerLen = 14
	if len(data) < headerLen {
		return cmapSubtable{}, false
	}
	segCountX2 := int(binary.BigEndian.Uint16(data[6:8]))
	segCount := segCountX2 / 2
	if segCount <= 0 {
		return cmapSubtable{}, false
	}

	// The four parallel segCount-entry arrays that follow the header, in
	// this fixed order: endCode, a reserved padding uint16, startCode,
	// idDelta, idRangeOffset.
	endCodeOff := headerLen
	startCodeOff := endCodeOff + segCountX2 + 2
	idDeltaOff := startCodeOff + segCountX2
	idRangeOff := idDeltaOff + segCountX2
	glyphArrayOff := idRangeOff + segCountX2
	if glyphArrayOff > len(data) {
		return cmapSubtable{}, false
	}

	entries := make(map[rune]uint16)
	for s := 0; s < segCount; s++ {
		endCode := binary.BigEndian.Uint16(data[endCodeOff+s*2 : endCodeOff+s*2+2])
		startCode := binary.BigEndian.Uint16(data[startCodeOff+s*2 : startCodeOff+s*2+2])
		idDelta := int16(binary.BigEndian.Uint16(data[idDeltaOff+s*2 : idDeltaOff+s*2+2]))
		idRangeOffset := binary.BigEndian.Uint16(data[idRangeOff+s*2 : idRangeOff+s*2+2])

		if startCode > endCode {
			continue
		}
		// 0xFFFF/0xFFFF is the mandatory final "no more segments"
		// sentinel every format 4 subtable ends with; it maps to glyph
		// 0 (.notdef) and is skipped rather than added as a real entry.
		if startCode == 0xFFFF && endCode == 0xFFFF {
			continue
		}

		for code := uint32(startCode); code <= uint32(endCode); code++ {
			var gid uint16
			if idRangeOffset == 0 {
				gid = uint16(int32(code) + int32(idDelta))
			} else {
				// This is format 4's infamous indirection: idRangeOffset
				// is a byte offset, but measured from *its own storage
				// location within the idRangeOffset array* (not from the
				// start of the subtable), specifically so it can double
				// as a self-relative pointer into the glyphIdArray that
				// follows immediately after that array in the file - see
				// the OpenType/TrueType "cmap" specification's format 4
				// section for the historical rationale. Concretely: the
				// address of this segment's idRangeOffset entry is
				// (idRangeOff + s*2); adding idRangeOffset itself lands
				// on that entry's *value*, which - reinterpreted as a
				// byte offset from there - reaches into glyphIdArray at
				// position (code - startCode) uint16 words further in.
				entryAddr := idRangeOff + s*2 + int(idRangeOffset) + int(code-uint32(startCode))*2
				if entryAddr+2 > len(data) {
					continue
				}
				raw := binary.BigEndian.Uint16(data[entryAddr : entryAddr+2])
				if raw == 0 {
					continue
				}
				gid = uint16(int32(raw) + int32(idDelta))
			}
			if gid != 0 {
				entries[rune(code)] = gid
			}
		}
	}
	return cmapSubtable{entries: entries}, true
}
