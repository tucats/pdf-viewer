package fonts

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// This file implements Phase 2 of the font-substitution work described in
// docs/FONTS.md: reading a candidate font *file* (something found on disk
// - as opposed to an embedded PDF font program, which is what the rest of
// this package's sfnt reading in truetype.go and cmap.go was originally
// written for) well enough to describe it as a FontCharacteristics value,
// the same shape Phase 1's descriptor.go produces from a PDF font
// dictionary. A later phase (Phase 4, not yet built) will compare the two
// shapes against each other to pick a substitute font for a PDF font this
// package cannot extract real outlines for.
//
// Two things make a font *file* different from an embedded /FontFile2
// stream, and are exactly what this file adds on top of truetype.go's
// existing parsing:
//
//  1. A file found on disk almost always carries "name" and "OS/2"
//     tables (this package's Font.Glyph never needed to read either one
//     for an *embedded* font, since /BaseFont and /FontDescriptor already
//     say everything the PDF itself wants known about the font). These
//     tables are how a real font file states its own family name,
//     weight, and bold/italic style - see parseNameTable and
//     parseOS2Table below.
//  2. A file found on disk is often a TrueType Collection (a ".ttc" file,
//     common on macOS and Windows for CJK fonts and whole font families
//     bundled together) rather than a single bare font - see
//     parseTTCHeader and ProbeFontFile below.
//
// # If you are new to Go: why so many "(x, bool)" return values here
//
// Nearly every parsing function in this file returns a plain value
// together with an "ok bool" (or, for ProbeFontFile itself, an "error"),
// following the same convention already used throughout this package
// (see, for example, truetype.go's parseSfnt or cmap.go's parseCmap).
// This lets a caller distinguish "successfully parsed, and here is the
// zero-value/empty result" from "could not parse this at all" without
// needing Go's exception-like panic/recover mechanism, which this
// project deliberately avoids for anything but truly unrecoverable
// programmer errors. A malformed or truncated table is always the
// *former* kind of failure here - see each function's own doc comment.

// FontFace describes one usable "face" (a single, selectable font) found
// while probing a candidate font file - see ProbeFontFile. An ordinary
// TrueType/OpenType file (".ttf"/".otf") has exactly one face; a
// TrueType Collection (".ttc") can bundle several (for example, regular
// and bold weights of the same family, or several distinct CJK familes
// sharing one set of tables).
type FontFace struct {
	// Characteristics is this face's best-effort description of its own
	// family name, weight, and bold/italic/serif/fixed-pitch traits -
	// see characterizeFace below for exactly how it is derived from the
	// face's own "name" and "OS/2" tables. It is in the same shape
	// Phase 1's Characterize (descriptor.go) produces from a PDF font
	// dictionary, so a later matching phase can compare the two
	// directly.
	Characteristics FontCharacteristics

	// HasOutlines reports whether Outline (below) can currently succeed
	// for this face: true for a TrueType-outline ("glyf") face or an
	// OpenType/CFF ("OTTO") face that actually carries a "CFF " table
	// (see cffTable below and cff.go's Phase 3 CFF support), false for
	// anything else this package cannot extract outlines from (for
	// instance, a structurally valid sfnt face missing both tables). A
	// false value here does not mean this face is unusable forever, only
	// that this package cannot read its outlines - a caller (Phase 4's
	// matcher) should still be able to see and characterize this face,
	// it just cannot select it as a usable substitute source.
	HasOutlines bool

	// data is the entire file's bytes this face was probed from (shared
	// across every face of a TrueType Collection, since a TTC's faces
	// commonly share tables - see parseTableDirectory's doc comment in
	// truetype.go for why table offsets always address this whole slice
	// rather than being local to one face).
	data []byte

	// dirOffset is the byte offset, within data, of this face's own
	// table directory - 0 for a plain (non-collection) file's single
	// face, or one of parseTTCHeader's returned offsets for a face
	// within a ".ttc" file.
	dirOffset int

	// cffTable, when non-nil, is this face's own "CFF " table bytes
	// (already sliced out at probe time by probeFace, at negligible
	// cost - a CFF table's own internal structure is not touched until
	// Outline is actually called). Its presence, rather than
	// HasOutlines alone, is what tells Outline which parser to use: a
	// glyf-outline face has this nil and is read via parseSfntAt
	// instead - see Outline below.
	cffTable []byte
}

// Outline fully parses this face's glyph outline data on demand -
// either "head"/"maxp"/"loca"/"glyf" for a TrueType-outline face, or the
// "CFF " table alone for a CFF-outline one (see the cffTable field's doc
// comment for how Outline tells the two apart) - returning the same
// glyphOutlineSource interface font.go's Font type already uses for an
// embedded font program (satisfied by both *sfntFont and *cffFont), so
// once a later phase selects a FontFace as a substitute, everything
// downstream (glyph lookup, scaling, drawing) works identically
// regardless of which outline format the face actually uses or whether
// it came from an embedded font program or a file found on disk. ok is
// false if HasOutlines is false, or if the face's outline data turns out
// to be malformed despite having looked structurally promising during
// ProbeFontFile - Outline is intentionally not called eagerly for every
// candidate (see docs/FONTS.md's Phase 2 goal), only once a candidate is
// actually chosen.
func (f FontFace) Outline() (glyphOutlineSource, bool) {
	if !f.HasOutlines {
		return nil, false
	}
	if f.cffTable != nil {
		cff, ok := parseCFFFont(f.cffTable)
		if !ok {
			return nil, false
		}
		return &cff, true
	}
	sfnt, ok := parseSfntAt(f.data, f.dirOffset)
	if !ok {
		return nil, false
	}
	return &sfnt, true
}

// maxTTCFaces bounds how many faces ProbeFontFile will ever try to read
// out of a single TrueType Collection header, defending against a
// hostile or corrupt file claiming an absurd face count that would
// otherwise cause a huge, useless slice allocation before any real
// parsing has even happened - the same "bounded work against hostile
// input" policy truetype.go's maxCompositeDepth documents for composite
// glyphs. No real-world font collection bundles anywhere near this many
// faces.
const maxTTCFaces = 4096

// ProbeFontFile examines data - the complete, already-read bytes of one
// candidate font file (a ".ttf", ".otf", or ".ttc") - and returns one
// FontFace per face it contains: exactly one for an ordinary single-face
// file, or however many a TrueType Collection's own header says it
// bundles. It never reads glyph outline data itself (see FontFace.Outline
// for that, called lazily later) - only whatever is needed to fill in
// each face's Characteristics and HasOutlines fields, which is
// deliberately cheap enough to do for every candidate file a later
// phase's directory scan finds, without needing to fully parse every
// candidate's glyph tables just to decide whether it is even worth
// considering.
//
// A non-nil error means data could not be recognized as a font file (or
// collection) at all - callers are expected to simply skip such a file
// (this package's font-loading code never treats "could not find/parse a
// substitute" as a hard failure - see Font's own never-fail contract in
// font.go), not to propagate the error as a fatal condition.
func ProbeFontFile(data []byte) ([]FontFace, error) {
	if len(data) >= 4 && binary.BigEndian.Uint32(data[0:4]) == sfntVersionTTC {
		offsets, ok := parseTTCHeader(data)
		if !ok {
			return nil, fmt.Errorf("fonts: malformed TrueType Collection header")
		}
		faces := make([]FontFace, 0, len(offsets))
		for _, offset := range offsets {
			face, ok := probeFace(data, int(offset))
			if ok {
				faces = append(faces, face)
			}
			// A single bad face's table directory does not doom the
			// whole collection - other faces in the same file may still
			// be perfectly readable, so a bad one is simply skipped
			// rather than failing ProbeFontFile outright.
		}
		if len(faces) == 0 {
			return nil, fmt.Errorf("fonts: no usable faces found in TrueType Collection")
		}
		return faces, nil
	}

	face, ok := probeFace(data, 0)
	if !ok {
		return nil, fmt.Errorf("fonts: not a recognized sfnt font file")
	}
	return []FontFace{face}, nil
}

// probeFace reads just enough of one face's table directory (starting at
// dirOffset within data - see parseTableDirectory's doc comment) to build
// a FontFace: whether it has a usable "glyf" or "CFF " outline table,
// and its FontCharacteristics as derived from whatever "name"/"OS/2"
// tables it has. ok is false only when dirOffset does not even point at
// a structurally valid sfnt table directory (see parseTableDirectory) -
// a valid directory that merely lacks a "name" or "OS/2" table still
// succeeds, just with a less confident Characteristics value (see
// characterizeFace); likewise, a valid directory lacking both "glyf"
// and "CFF " still succeeds, just with HasOutlines false.
func probeFace(data []byte, dirOffset int) (FontFace, bool) {
	tables, _, ok := parseTableDirectory(data, dirOffset)
	if !ok {
		return FontFace{}, false
	}
	_, hasGlyf := tables["glyf"]
	cffTable, hasCFF := tables["CFF "]

	var names []nameTableEntry
	if nameRaw, ok := tables["name"]; ok {
		names, _ = parseNameTable(nameRaw)
	}

	os2, hasOS2 := os2Table{}, false
	if os2Raw, ok := tables["OS/2"]; ok {
		os2, hasOS2 = parseOS2Table(os2Raw)
	}

	face := FontFace{
		Characteristics: characterizeFace(names, os2, hasOS2),
		HasOutlines:     hasGlyf || hasCFF,
		data:            data,
		dirOffset:       dirOffset,
	}
	if hasCFF {
		face.cffTable = cffTable
	}
	return face, true
}

// parseTTCHeader parses a TrueType Collection's own header - four bytes
// of "ttcf" version tag (already checked by ProbeFontFile before calling
// this), a format version, a face count, and then that many big-endian
// uint32 byte offsets, one per bundled face's own table directory (each
// suitable as parseTableDirectory's dirOffset parameter). ok=false for a
// header that is truncated, claims zero faces, or claims an unreasonably
// large face count (see maxTTCFaces) - all treated the same as any other
// malformed-input case in this package: reported, not panicked on.
func parseTTCHeader(data []byte) ([]uint32, bool) {
	const headerLen = 12 // "ttcf" tag + version + numFonts, before the offset array
	if len(data) < headerLen {
		return nil, false
	}
	numFonts := binary.BigEndian.Uint32(data[8:12])
	if numFonts == 0 || numFonts > maxTTCFaces {
		return nil, false
	}
	need := headerLen + int(numFonts)*4
	if need > len(data) {
		return nil, false
	}
	offsets := make([]uint32, numFonts)
	for i := range offsets {
		offsets[i] = binary.BigEndian.Uint32(data[headerLen+i*4 : headerLen+i*4+4])
	}
	return offsets, true
}

// nameTableEntry is one decoded record from an sfnt "name" table -
// see parseNameTable.
type nameTableEntry struct {
	platformID, encodingID, languageID, nameID uint16
	value                                      string
}

// sfnt "name" table name IDs this file reads - see the OpenType/TrueType
// "name" table specification for the complete list, most of which
// (copyright notices, trademark strings, license URLs, and so on) this
// package has no use for.
const (
	nameIDFamily     = 1 // e.g. "Arial", "Times New Roman"
	nameIDSubfamily  = 2 // e.g. "Regular", "Bold Italic"
	nameIDFullName   = 4 // e.g. "Arial Bold"
	nameIDPostScript = 6 // e.g. "Arial-BoldMT" - the same style of name /BaseFont uses
)

// parseNameTable decodes an sfnt "name" table into its individual
// records, without yet picking which one is "the" family name or
// PostScript name for a given nameID - that selection happens in
// selectName below, since a "name" table can legally carry the same
// nameID more than once (once per platform/encoding/language it wants to
// support, e.g. one English record and one German record).
//
// # The "name" table's on-disk shape, for readers new to font formats
//
// After a small fixed header (format, record count, and the byte offset
// of a "string storage" area that follows all the fixed-size records),
// the table lists one 12-byte record per name: which (platform,
// encoding, language) this particular string is for, which nameID it is
// (see the constants above), and where in the string storage area to
// find its raw bytes. The strings themselves are stored back to back
// after every record, addressed by (storage area start + this record's
// own offset) - very similar in spirit to cmap.go's format 4 subtable,
// which also separates "here is where to look" records from a shared
// data area referenced by relative offsets.
func parseNameTable(data []byte) ([]nameTableEntry, bool) {
	const headerLen = 6
	if len(data) < headerLen {
		return nil, false
	}
	count := int(binary.BigEndian.Uint16(data[2:4]))
	stringAreaStart := int(binary.BigEndian.Uint16(data[4:6]))

	const recordSize = 12
	recordsEnd := headerLen + count*recordSize
	if count < 0 || recordsEnd > len(data) || stringAreaStart > len(data) {
		return nil, false
	}

	entries := make([]nameTableEntry, 0, count)
	for i := 0; i < count; i++ {
		rec := data[headerLen+i*recordSize : headerLen+(i+1)*recordSize]
		platformID := binary.BigEndian.Uint16(rec[0:2])
		encodingID := binary.BigEndian.Uint16(rec[2:4])
		languageID := binary.BigEndian.Uint16(rec[4:6])
		nameID := binary.BigEndian.Uint16(rec[6:8])
		length := int(binary.BigEndian.Uint16(rec[8:10]))
		offset := int(binary.BigEndian.Uint16(rec[10:12]))

		start := stringAreaStart + offset
		end := start + length
		if start < 0 || end < start || end > len(data) {
			// One bad record's string bytes do not doom every other
			// record in the table - skipped, mirroring
			// parseTableDirectory's own "skip one bad entry" precedent.
			continue
		}

		entries = append(entries, nameTableEntry{
			platformID: platformID,
			encodingID: encodingID,
			languageID: languageID,
			nameID:     nameID,
			value:      decodeNameBytes(platformID, data[start:end]),
		})
	}
	return entries, true
}

// decodeNameBytes turns one "name" table record's raw string bytes into a
// Go string, using the text encoding its platformID implies. The
// Windows (3) and full Unicode (0) platforms always encode "name" table
// strings as UTF-16BE (two bytes per character, most significant byte
// first) - by far the common case in real-world fonts, since those are
// the platform IDs modern font tools write. The classic Macintosh
// platform (1) instead used a single byte per character in an
// old Mac-specific encoding ("Mac Roman"); this package does not
// implement that encoding's few dozen non-ASCII code points (accented
// letters and symbols above byte value 0x7F) and instead passes such
// bytes through as raw Latin-1-ish text, which is exactly correct for
// the plain-ASCII family/style names this package actually looks up
// (e.g. "Arial", "Bold Italic") and only wrong for a font whose declared
// name happens to use an accented character - a narrow, documented gap
// in the same spirit as encoding.go's own documented StandardEncoding
// limitation.
func decodeNameBytes(platformID uint16, raw []byte) string {
	if platformID == 3 || platformID == 0 {
		return utf16BEToString(raw)
	}
	return string(raw)
}

// utf16BEToString decodes raw as big-endian UTF-16 code units into a Go
// string. An odd trailing byte (which should never happen in a
// well-formed "name" table record, since UTF-16 code units are always 2
// bytes) is simply dropped rather than causing a failure - consistent
// with this file's general "degrade gracefully on a slightly malformed
// candidate file" approach, since a probe result is never more than a
// best-effort description of a font this package didn't create.
func utf16BEToString(raw []byte) string {
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(raw[i*2 : i*2+2])
	}
	return string(utf16.Decode(units))
}

// selectName picks the single best string for a given nameID out of
// entries (which may contain several records for that same nameID, one
// per platform/encoding/language a font's author chose to support - see
// parseNameTable), using the same "prefer the most broadly-compatible
// modern encoding" precedence cmap.go's selectCmapSubtable already
// establishes for cmap subtables: a Windows Unicode BMP record first,
// then any other Unicode-platform record, then any other Windows record,
// then finally a classic Mac Roman record. ok=false means entries
// contains no record at all for the requested nameID.
func selectName(entries []nameTableEntry, nameID uint16) (string, bool) {
	var best string
	bestScore := -1
	for _, e := range entries {
		if e.nameID != nameID {
			continue
		}
		score := -1
		switch {
		case e.platformID == 3 && e.encodingID == 1:
			score = 3
		case e.platformID == 0:
			score = 2
		case e.platformID == 3:
			score = 1
		case e.platformID == 1 && e.encodingID == 0:
			score = 0
		}
		if score > bestScore {
			bestScore = score
			best = e.value
		}
	}
	return best, bestScore >= 0
}

// os2Table holds the handful of sfnt "OS/2" table fields this package
// reads - see parseOS2Table and characterizeFace. The real "OS/2" table
// has many more fields (subscript/superscript metrics, Unicode/code-page
// coverage bitmaps, typographic ascent/descent, and so on); none of the
// others matter for this package's purpose of guessing a candidate
// font's weight/style/serif-ness.
type os2Table struct {
	// usWeightClass is the font's declared weight on the same 100-900
	// scale FontCharacteristics.Weight and PDF's own /FontWeight use
	// (400 = regular, 700 = bold, and so on).
	usWeightClass uint16

	// fsSelection is a bitmask of style flags - this file only reads its
	// bold (bit 5) and italic (bit 0) bits, see the fsSelection*
	// constants below.
	fsSelection uint16

	// sFamilyClass classifies the font's overall design style; its high
	// byte is the class ID this file checks in characterizeFace to guess
	// serif-ness (its low byte, a finer "subclass" this package has no
	// use for, is left untouched).
	sFamilyClass int16
}

// fsSelection bit values this file reads (OS/2 table specification,
// "fsSelection" field) - only the two bits relevant to
// FontCharacteristics.Bold/Italic; the table defines several others
// (underline, strikeout, "regular" itself as bit 6) this package never
// needs.
const (
	fsSelectionItalic = 1 << 0
	fsSelectionBold   = 1 << 5
)

// os2MinLen is the smallest "OS/2" table this file can read from: large
// enough to reach fsSelection at byte offset 62 (see parseOS2Table). Real
// "OS/2" tables are almost always considerably larger (later table
// versions append more fields at the end), but this package never reads
// anything past this point, so a shorter-than-usual but still
// spec-conformant version 0 table (which the original 1990s "OS/2"
// table specification only guaranteed up to a smaller size than modern
// versions) is intentionally still accepted here rather than rejected
// for lacking fields nothing in this file looks at.
const os2MinLen = 64

// parseOS2Table decodes the handful of "OS/2" table fields this package
// cares about - see os2Table's own doc comment for which fields and why.
// ok=false only when data is shorter than os2MinLen, i.e. too truncated
// to even reach fsSelection; every "OS/2" table version in real-world use
// is at least this long.
func parseOS2Table(data []byte) (os2Table, bool) {
	if len(data) < os2MinLen {
		return os2Table{}, false
	}
	return os2Table{
		usWeightClass: binary.BigEndian.Uint16(data[4:6]),
		sFamilyClass:  int16(binary.BigEndian.Uint16(data[30:32])),
		fsSelection:   binary.BigEndian.Uint16(data[62:64]),
	}, true
}

// characterizeFace builds a FontCharacteristics for one probed face from
// its decoded "name" table records and (if present) its "OS/2" table -
// converging on the exact same FontCharacteristics shape Phase 1's
// Characterize (descriptor.go) builds from a PDF font dictionary, so a
// later matching phase can compare a PDF font's *wanted* characteristics
// against a candidate file's *actual* ones without needing to know which
// side of the comparison came from a PDF and which came from a file.
//
// Family name preference: the "name" table's own family record (nameID
// 1) is preferred, since it is already exactly the plain human-readable
// name a person would expect (e.g. "Arial"); if that is missing, the
// full name (nameID 4, e.g. "Arial Bold") is used instead; if even that
// is missing, the PostScript name (nameID 6, e.g. "Arial-BoldMT") is
// run through Phase 1's own ParsePostScriptName - the exact same
// family/style extraction this package already applies to a PDF
// /BaseFont value, reused here because a font file's PostScript name
// follows the same naming conventions a PDF's /BaseFont does (unsurprising,
// since /BaseFont is conventionally copied from a font's own PostScript
// name in the first place).
//
// Bold/italic/weight preference: an "OS/2" table, when present, is a
// direct machine-readable statement of these traits and is always
// preferred over guessing from a name string. Only when a face has no
// "OS/2" table at all (uncommon, but seen in some older or minimal
// TrueType-only files) does this function fall back to looking for
// style keywords ("Bold", "Italic", ...) in the subfamily or PostScript
// name, via the same styleTokens keyword list descriptor.go's
// ParsePostScriptName already uses (see detectStyleTokens in
// descriptor.go).
//
// Serif detection uses "OS/2"'s sFamilyClass field when present - a
// direct classification from the font itself, not a guess from its name
// (compare Phase 1's Characterize, which deliberately never guesses
// serif-ness from a PDF font's name alone - see descriptor.go's own doc
// comment on FontCharacteristics.Serif). FixedPitch is left at its zero
// value (false) here: that trait lives in a different table ("post")
// this phase does not parse - see docs/FONTS.md's Phase 2 non-goals.
func characterizeFace(names []nameTableEntry, os2 os2Table, hasOS2 bool) FontCharacteristics {
	family, _ := selectName(names, nameIDFamily)
	if family == "" {
		family, _ = selectName(names, nameIDFullName)
	}

	psName, _ := selectName(names, nameIDPostScript)
	if family == "" {
		family, _, _ = ParsePostScriptName(psName)
	}

	var bold, italic, serif bool
	weight := 400

	if hasOS2 {
		bold = os2.fsSelection&fsSelectionBold != 0 || os2.usWeightClass >= 600
		italic = os2.fsSelection&fsSelectionItalic != 0
		if os2.usWeightClass > 0 {
			weight = int(os2.usWeightClass)
		} else if bold {
			weight = 700
		}
		// sFamilyClass's high byte is an IBM/OpenType "font class" ID;
		// classes 1 through 5 and 7 are the specification's various
		// flavors of serif design ("Oldstyle Serifs" through "Slab
		// Serifs", plus "Freeform Serifs") - class 6 is reserved/unused
		// by the specification and deliberately excluded here alongside
		// every non-serif class (8 "Sans Serif", 9 "Ornamentals", 10
		// "Scripts", 12 "Symbolic", and 0 "No Classification").
		switch os2.sFamilyClass >> 8 {
		case 1, 2, 3, 4, 5, 7:
			serif = true
		}
	} else {
		subfamily, _ := selectName(names, nameIDSubfamily)
		subBold, subItalic := detectStyleTokens(subfamily)
		psBold, psItalic := detectStyleTokens(psName)
		bold = subBold || psBold
		italic = subItalic || psItalic
		if bold {
			weight = 700
		}
	}

	return FontCharacteristics{
		Family: family,
		Bold:   bold,
		Italic: italic,
		Serif:  serif,
		Weight: weight,
	}
}
