package fonts

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// This file implements docs/FONTS.md's Phase 3: enough of Adobe's "CFF"
// (Compact Font Format) file format - and, within it, the "Type 2
// Charstring" instruction set that actually draws each glyph's outline -
// to extract real glyph outlines from a CFF font program. This is the
// format an embedded /FontFile3 stream contains (for a simple font, the
// PDF specification calls this a "Type1C" or bare "CFF" program; for a
// Type0/CID font's descendant, a "CIDFontType0C" program), and it is
// also the outline format an "OTTO"-flavored OpenType font uses inside
// its "CFF " table (see truetype.go's doc comment on sfntVersionOTTO,
// and probe.go, which characterizes but - before this phase - could not
// extract outlines from such a file).
//
// # If you are new to font formats: why CFF needs a completely different
// parser than truetype.go's
//
// truetype.go reads TrueType's "glyf" outline format: each glyph is a
// list of (x, y) points connected by straight lines and quadratic Bézier
// curves, stored almost literally as coordinate deltas. CFF is a
// different, older lineage (it descends from Adobe's PostScript Type 1
// font format) with two big differences that make it a separate parser
// rather than an extension of truetype.go's:
//
//  1. A glyph's outline is not stored as a table of points at all. It is
//     a tiny bytecode *program* - a "charstring" - built from operators
//     like "move the pen by (dx, dy)" or "draw a curve through these
//     four control points" (both relative to wherever the pen currently
//     is). Extracting an outline means *running* this program (see
//     charstringInterp and its exec method below), not just decoding a
//     fixed record layout. Real-world charstrings also commonly call
//     into small shared "subroutines" (see doCallsubr and
//     doCallgsubr) - reusable snippets of charstring bytecode, a lot
//     like a PDF content stream's own operator sequence, or like calling
//     a function in an ordinary programming language, right down to
//     needing its own call stack.
//  2. CFF's curves are *cubic* Bézier curves (four points: start, two
//     control points, end - see graphics.Path.CurveTo, the exact method
//     PDF's own "c" content-stream operator uses), not TrueType's
//     quadratic ones (three points - graphics.Path.QuadTo). This is
//     convenient here: it means this file's curveTo helper (below) can
//     reuse CurveTo exactly as-is, with no new graphics.Path method
//     needed (contrast truetype.go, which had to add QuadTo specifically
//     for its own quadratic curves).
//
// # Scope of what this file implements
//
// A CFF font program is built from a handful of reusable building
// blocks, each with its own parsing function below:
//
//   - "INDEX": CFF's basic repeated-variable-length-record container,
//     used for several different purposes throughout the format (glyph
//     names, dictionaries, strings, subroutines, and - the one that
//     matters most here - each glyph's own charstring). See cffIndex and
//     parseCFFIndex.
//   - "DICT": a compact binary key/value structure (not to be confused
//     with a PDF dictionary) naming a font's own metadata and, crucially,
//     the byte offsets of its other structures within the file. See
//     parseCFFDict.
//   - "charset": a table naming each glyph, either by a PostScript glyph
//     name (an ordinary font) or by CID (a CID-keyed font, used by a
//     Type0/CIDFontType0C PDF font - see cid.go) - see parseCFFCharset
//     and parseCustomCFFCharset.
//   - "FDArray"/"FDSelect": present only in a CID-keyed CFF font, these
//     let different ranges of glyphs use different font-wide settings
//     (in particular, different local subroutines) - see
//     parseCFFFDSelect.
//
// Deliberately out of scope, matching truetype.go's own precedent of
// implementing outline extraction without hinting or rendering-quality
// features:
//
//   - Hinting: CFF's own stem-hint operators (hstem/vstem/hstemhm/
//     vstemhm/hintmask/cntrmask) are parsed only as far as needed to
//     correctly skip their operands and any associated hintmask/cntrmask
//     bytes (see doStems and doMask) - the hints themselves are never
//     applied. This mirrors truetype.go's own "no hinting" scope.
//   - CFF2 (the variable-font-flavored successor format): not read at
//     all - see docs/FONTS.md's Phase 3 non-goals.
//   - The deprecated implicit "seac" accented-character composition some
//     older charstrings encode via a 4-argument endchar: recognized (so
//     it fails this one glyph gracefully rather than misinterpreting the
//     arguments as path data) but not composed - see doEndchar's doc
//     comment for the full rationale.
//   - A CFF font's own built-in Encoding table (DICT operator 16, a
//     second, separate code-to-glyph mapping from the charset): not
//     read. simple.go's use of this file instead follows the same path
//     PDF's own specification describes for a non-symbolic simple font -
//     a character code maps to a glyph *name* via the PDF font
//     dictionary's own /Encoding (encoding.go), and that name is then
//     looked up in this file's charset - which this package already
//     needs for CID-keyed fonts regardless, and which covers the common
//     case (an ordinary, non-symbolic embedded Latin-text font).

// cffEscapeOperator is added to a DICT or Type 2 Charstring operator's
// second byte when its first byte is 12 (the shared "this is actually a
// two-byte operator" escape prefix both formats use - see parseCFFDict
// and charstringInterp.exec) - chosen well above the largest possible
// one-byte operator value (31 for a charstring operator, 21 for a DICT
// operator) so that a lookup keyed by "operator + cffEscapeOperator"
// (e.g. this file's topDict map, or exec's switch statement) can never
// collide with an ordinary one-byte operator's own key.
const cffEscapeOperator = 1000

// cffIndex is a parsed CFF "INDEX" (CFF specification section 5): an
// ordered list of variable-length byte records. This one on-disk
// structure is reused throughout a CFF file for several different kinds
// of data - see parseCFFFont for the specific INDEXes this package
// reads (Name, Top DICT, String, Global Subr, CharStrings, and each
// font's own Local Subr INDEX). Each element is a subslice of the
// original file's bytes, not a copy - the same "views into the original
// buffer, not allocations" approach truetype.go's parseTableDirectory
// already uses.
type cffIndex [][]byte

// parseCFFIndex reads one CFF INDEX starting at byte offset pos within
// data, returning the parsed items, the byte offset immediately
// following the INDEX (so a caller can chain several INDEX reads back to
// back, exactly as a CFF file lays them out), and ok=false for any
// INDEX this package cannot trust (a count, offset size, or individual
// item range that does not fit within data). Like every other parser in
// this file, this fails closed rather than panicking on hostile or
// truncated input - see this file's doc comment.
func parseCFFIndex(data []byte, pos int) (items cffIndex, next int, ok bool) {
	if pos < 0 || pos+2 > len(data) {
		return nil, 0, false
	}
	count := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2
	if count == 0 {
		// An empty INDEX is just its own 2-byte item count - no offSize
		// byte, no offset array, no data at all (CFF specification
		// section 5, "Data Types").
		return cffIndex{}, pos, true
	}

	if pos >= len(data) {
		return nil, 0, false
	}
	offSize := int(data[pos])
	pos++
	if offSize < 1 || offSize > 4 {
		return nil, 0, false
	}

	offArrayLen := (count + 1) * offSize
	if offArrayLen < 0 || pos+offArrayLen > len(data) {
		return nil, 0, false
	}
	offsets := make([]uint32, count+1)
	for i := range offsets {
		var v uint32
		for _, b := range data[pos+i*offSize : pos+(i+1)*offSize] {
			v = v<<8 | uint32(b)
		}
		offsets[i] = v
	}
	pos += offArrayLen

	// Offsets are 1-based, counted from the byte immediately before the
	// data area (so offsets[0] is always 1, pointing at the data area's
	// very first byte) - CFF specification section 5.
	dataStart := pos - 1
	items = make(cffIndex, count)
	for i := 0; i < count; i++ {
		start := int64(dataStart) + int64(offsets[i])
		end := int64(dataStart) + int64(offsets[i+1])
		if start < 0 || end < start || end > int64(len(data)) {
			return nil, 0, false
		}
		items[i] = data[start:end]
	}
	return items, dataStart + int(offsets[count]), true
}

// parseCFFDict decodes a CFF "DICT" (CFF specification section 4) - a
// compact binary sequence of operand(s)-followed-by-operator entries,
// unrelated to (and not to be confused with) a PDF syntax.Dictionary.
// The result maps each operator to its own operand list: a one-byte
// operator (0-21) is keyed by its own value; a two-byte operator (first
// byte 12, "escape") is keyed by cffEscapeOperator plus its second byte,
// so both kinds of operator share one map without collision - see
// cffEscapeOperator's own doc comment.
//
// A DICT operand is a number encoded in one of several variable-length
// forms (see the switch below) - the same encoding a Type 2 Charstring
// uses for its own operands (decodeCharstringNumber), except a DICT
// operand additionally allows a nibble-encoded arbitrary-precision "real
// number" form (operand lead byte 30 - see parseCFFReal) that a
// charstring never uses, and never allows lead byte 255 (which a
// charstring instead uses for a 16.16 fixed-point number - see
// decodeCharstringNumber) - encountering byte 255 here therefore aborts
// the whole DICT rather than guessing how many bytes it might have meant
// to consume.
func parseCFFDict(data []byte) (map[int][]float64, bool) {
	const maxDictOperands = 48 // generous - no real DICT entry needs more than a handful (FontMatrix's 6 is the largest this file reads).

	dict := make(map[int][]float64)
	var operands []float64
	pos := 0
	for pos < len(data) {
		b0 := data[pos]
		switch {
		case b0 <= 21:
			op := int(b0)
			pos++
			if b0 == 12 {
				if pos >= len(data) {
					return nil, false
				}
				op = cffEscapeOperator + int(data[pos])
				pos++
			}
			dict[op] = operands
			operands = nil

		case b0 == 28:
			if pos+3 > len(data) {
				return nil, false
			}
			operands = append(operands, float64(int16(binary.BigEndian.Uint16(data[pos+1:pos+3]))))
			pos += 3

		case b0 == 29:
			if pos+5 > len(data) {
				return nil, false
			}
			operands = append(operands, float64(int32(binary.BigEndian.Uint32(data[pos+1:pos+5]))))
			pos += 5

		case b0 == 30:
			v, newPos, ok := parseCFFReal(data, pos+1)
			if !ok {
				return nil, false
			}
			operands = append(operands, v)
			pos = newPos

		case b0 >= 32 && b0 <= 246:
			operands = append(operands, float64(int(b0)-139))
			pos++

		case b0 >= 247 && b0 <= 250:
			if pos+2 > len(data) {
				return nil, false
			}
			operands = append(operands, float64((int(b0)-247)*256+int(data[pos+1])+108))
			pos += 2

		case b0 >= 251 && b0 <= 254:
			if pos+2 > len(data) {
				return nil, false
			}
			operands = append(operands, float64(-(int(b0)-251)*256-int(data[pos+1])-108))
			pos += 2

		default: // 255: not a valid DICT operand lead byte (see this function's doc comment) - fail closed rather than guess how many bytes to skip.
			return nil, false
		}
		if len(operands) > maxDictOperands {
			return nil, false
		}
	}
	return dict, true
}

// parseCFFReal decodes a DICT "real number" operand (CFF specification
// section 4, Table 3, operand lead byte 30): a sequence of 4-bit
// "nibbles" (two per byte, most significant first), each either a
// decimal digit, a decimal point, an exponent marker, a minus sign, or
// (nibble 0xf) the terminator - assembled here into an ordinary decimal
// string and handed to strconv.ParseFloat, rather than this package
// implementing its own decimal parsing logic redundantly. pos is the
// byte offset of the first nibble pair, immediately after the lead byte
// 30 itself. ok=false if the terminator nibble is never reached before
// data runs out, or if the assembled string is not a valid number
// (which should not happen for a well-formed CFF file, but a hostile one
// could construct a nibble sequence like "reserved nibble only" that
// decodes to an empty or malformed string).
func parseCFFReal(data []byte, pos int) (float64, int, bool) {
	var s strings.Builder
	for {
		if pos >= len(data) {
			return 0, 0, false
		}
		b := data[pos]
		pos++
		for _, nibble := range [2]byte{b >> 4, b & 0x0f} {
			switch {
			case nibble <= 9:
				s.WriteByte('0' + nibble)
			case nibble == 0xa:
				s.WriteByte('.')
			case nibble == 0xb:
				s.WriteByte('E')
			case nibble == 0xc:
				s.WriteString("E-")
			case nibble == 0xd:
				// Reserved/unused - ignored rather than failing the whole
				// number, matching this package's general tolerance for
				// one malformed field (see truetype.go's
				// parseTableDirectory).
			case nibble == 0xe:
				s.WriteByte('-')
			case nibble == 0xf:
				v, err := strconv.ParseFloat(s.String(), 64)
				if err != nil {
					return 0, 0, false
				}
				return v, pos, true
			}
		}
	}
}

// cffFont holds the parsed subset of a CFF font program this package
// uses to extract glyph outlines - the CFF counterpart to truetype.go's
// sfntFont. A zero-value cffFont is never handed to a caller (see
// parseCFFFont, which only ever returns ok=true alongside a fully
// populated value).
type cffFont struct {
	// charStrings is the font's CharStrings INDEX: charStrings[gid] is
	// glyph index gid's own Type 2 Charstring program. GID 0 is always
	// ".notdef" by convention (CFF specification section 16).
	charStrings cffIndex

	// globalSubrs is shared by every glyph in the file, regardless of
	// isCID - see charstringInterp.doCallgsubr.
	globalSubrs cffIndex

	// localSubrs is used for glyph outline extraction when isCID is
	// false (an ordinary, non-CID-keyed font has exactly one Private
	// DICT, and therefore one local subroutine INDEX, shared by every
	// glyph) - see charstringInterp.doCallsubr. Left nil (and unused;
	// see fdLocalSubrs/fdSelect instead) when isCID is true.
	localSubrs cffIndex

	// isCID reports whether this font is CID-keyed (its Top DICT
	// contains a ROS operator - CFF specification section 19) - the CFF
	// counterpart to a Type0 PDF font's CIDFontType0C descendant font
	// program (see cid.go). A CID-keyed font's charset (see charsetIDs)
	// holds each glyph's CID rather than a glyph-name SID, and its local
	// subroutines are selected per glyph via fdSelect/fdLocalSubrs
	// instead of one shared localSubrs.
	isCID bool

	// fdLocalSubrs holds one Local Subr INDEX per "Font DICT" (CFF
	// specification section 19) - only populated when isCID is true.
	// fdSelect names, for each GID, which element of this slice to use.
	fdLocalSubrs []cffIndex

	// fdSelect maps a GID to an index into fdLocalSubrs - only populated
	// (to len(charStrings) entries) when isCID is true.
	fdSelect []byte

	// charsetIDs maps a GID to its charset entry: a String ID (SID),
	// resolvable to a glyph name via sidToName, for an ordinary font; a
	// CID, for a CID-keyed one. len(charsetIDs) == len(charStrings).
	// charsetIDs[0] is always 0 (".notdef"'s SID, which also doubles
	// harmlessly as "CID 0" for a CID-keyed font, since CID 0 is
	// likewise conventionally .notdef).
	charsetIDs []uint16

	// strings is the font's String INDEX: custom (non-standard) strings
	// referenced by a SID of len(cffStandardStrings) or higher - see
	// sidToName.
	strings cffIndex

	// unitsPerEm is derived from the Top DICT's FontMatrix (default
	// 1000, matching FontMatrix's own default of 0.001 - see
	// parseCFFFont) - the CFF counterpart to sfntFont.unitsPerEm, letting
	// font.go's scaleGlyph treat a CFF-backed Font exactly like a
	// TrueType-backed one.
	unitsPerEm uint16

	// runeToGID resolves a Unicode rune to a GID by way of this font's
	// own charset names (see parseCFFFont's construction of it, and this
	// file's doc comment on why a name, not a rune, is CFF's own native
	// key) - populated only when isCID is false. simple.go uses this the
	// same way it uses an embedded TrueType program's cmap subtable (see
	// simpleGlyphLookup), letting both font kinds share one glyph-lookup
	// strategy for a simple font's /Encoding-resolved text.
	runeToGID map[rune]uint16

	// cidToGID inverts charsetIDs for a CID-keyed font (GID -> CID
	// becomes CID -> GID) - populated only when isCID is true. cid.go
	// uses this instead of a CIDFontType2 descendant's /CIDToGIDMap
	// stream, since a CIDFontType0 descendant has no such entry at all
	// (the specification says CID-to-GID mapping for this font kind
	// comes from the CFF program's own charset - see PDF specification
	// 9.7.4.2).
	cidToGID map[uint16]uint16
}

// identityCharset returns a charset mapping GID directly to itself as
// its own SID - correct for CFF's predefined "ISOAdobe" charset (offset
// value 0, CFF specification section 13), which by construction assigns
// glyph i the standard string SID i for every i covered by
// cffStandardStrings' own first 229 entries (the ISOAdobe character
// set's names, listed in exactly SID order - see this file's standard
// strings table). This is by far the most common charset for a simple
// (non-CID) Latin-text font that does not bother writing out its own
// explicit charset table.
func identityCharset(numGlyphs int) []uint16 {
	ids := make([]uint16, numGlyphs)
	for i := range ids {
		ids[i] = uint16(i)
	}
	return ids
}

// parseCFFCharset resolves a Top DICT "charset" operand (CFF
// specification section 13) into a GID-indexed slice of SIDs (or, for a
// CID-keyed font, CIDs - the two share the same on-disk encoding, and
// the caller, parseCFFFont, is the one that knows which interpretation
// applies). offset is the charset operand's own raw value: 0, 1, or 2
// select one of CFF's three *predefined* charsets rather than pointing
// into the file at all; any other value is a real byte offset to a
// charset table.
//
// Only predefined charset 0 (ISOAdobe) is actually implemented (via
// identityCharset) - predefined charsets 1 ("Expert") and 2 ("Expert
// Subset") are a rare, essentially obsolete companion-font convention
// for old-style figures and fraction glyphs that this package has never
// had any use for elsewhere (compare docs/capability-matrix.md's several
// other "recognized subset, not the whole specification" gaps). Rather
// than fabricate wrong names by treating them as identity too, this
// function reports every non-.notdef glyph's SID as 0 for those two
// values - which is indistinguishable from ".notdef" to every later SID
// lookup (sidToName) and rune/CID map builder (parseCFFFont), so those
// glyphs simply end up unreachable by name or CID rather than reachable
// under a wrong one. A glyph's outline is still extractable by GID
// directly wherever a caller already has one (for instance, a
// CIDFontType2-style direct GID reference would never go through this
// path anyway); only name/CID-based lookup is affected.
func parseCFFCharset(data []byte, offset, numGlyphs int) []uint16 {
	switch offset {
	case 0:
		return identityCharset(numGlyphs)
	case 1, 2:
		return make([]uint16, numGlyphs)
	default:
		if ids, ok := parseCustomCFFCharset(data, offset, numGlyphs); ok {
			return ids
		}
		// A malformed custom charset table degrades to "no names known"
		// (see this function's doc comment on the predefined-1/2 case
		// above) rather than failing the whole font.
		return make([]uint16, numGlyphs)
	}
}

// parseCustomCFFCharset decodes an explicit charset table (CFF
// specification section 13, formats 0, 1, and 2) starting at byte offset
// pos within data. GID 0 (always ".notdef") is never itself listed in
// the table on disk - only GIDs 1..numGlyphs-1 are - so the returned
// slice's index 0 is always left at its zero value.
//
// Formats 1 and 2 both encode the same idea - a run of nLeft+1
// consecutively-numbered GIDs starting at "first"'s SID/CID and counting
// up by one - differing only in whether nLeft is a 1-byte (format 1) or
// 2-byte (format 2) field; format 2 exists for fonts with enough glyphs
// that format 1's 256-per-range limit (a 1-byte nLeft) would need an
// impractical number of ranges, which is common for large CID-keyed CJK
// fonts.
func parseCustomCFFCharset(data []byte, pos, numGlyphs int) ([]uint16, bool) {
	if pos < 0 || pos >= len(data) {
		return nil, false
	}
	format := data[pos]
	pos++

	ids := make([]uint16, numGlyphs)
	gid := 1
	switch format {
	case 0:
		for gid < numGlyphs {
			if pos+2 > len(data) {
				return nil, false
			}
			ids[gid] = binary.BigEndian.Uint16(data[pos : pos+2])
			pos += 2
			gid++
		}
	case 1, 2:
		nLeftSize := 1
		if format == 2 {
			nLeftSize = 2
		}
		for gid < numGlyphs {
			if pos+2+nLeftSize > len(data) {
				return nil, false
			}
			first := binary.BigEndian.Uint16(data[pos : pos+2])
			pos += 2
			var nLeft int
			if format == 1 {
				nLeft = int(data[pos])
				pos++
			} else {
				nLeft = int(binary.BigEndian.Uint16(data[pos : pos+2]))
				pos += 2
			}
			for i := 0; i <= nLeft && gid < numGlyphs; i++ {
				ids[gid] = first + uint16(i)
				gid++
			}
		}
	default:
		return nil, false
	}
	return ids, true
}

// parseCFFFDSelect decodes a CID-keyed font's FDSelect table (CFF
// specification section 19), mapping each GID to which "Font DICT" (and,
// through it, which Local Subr INDEX - see cffFont.fdLocalSubrs) it
// belongs to. Only the two formats real-world font generators actually
// emit are implemented:
//
//   - Format 0: one raw byte per GID, in GID order - the simplest
//     possible encoding, used by smaller CID fonts.
//   - Format 3: a sorted list of (first GID, FD index) ranges plus a
//     final "sentinel" GID marking the end of the last range - more
//     compact for a large font whose glyphs are grouped into
//     contiguous runs sharing the same FD (typical of CJK fonts grouped
//     by script or weight).
func parseCFFFDSelect(data []byte, offset, numGlyphs int) ([]byte, bool) {
	if offset < 0 || offset >= len(data) {
		return nil, false
	}
	format := data[offset]
	pos := offset + 1

	fds := make([]byte, numGlyphs)
	switch format {
	case 0:
		if pos+numGlyphs > len(data) {
			return nil, false
		}
		copy(fds, data[pos:pos+numGlyphs])

	case 3:
		if pos+2 > len(data) {
			return nil, false
		}
		numRanges := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		pos += 2

		prevFirst, prevFD := -1, byte(0)
		fillRange := func(end int) bool {
			if prevFirst < 0 {
				return true
			}
			if end < prevFirst || end > numGlyphs {
				return false
			}
			for g := prevFirst; g < end; g++ {
				fds[g] = prevFD
			}
			return true
		}
		for i := 0; i < numRanges; i++ {
			if pos+3 > len(data) {
				return nil, false
			}
			first := int(binary.BigEndian.Uint16(data[pos : pos+2]))
			fd := data[pos+2]
			pos += 3
			if !fillRange(first) {
				return nil, false
			}
			prevFirst, prevFD = first, fd
		}
		if pos+2 > len(data) {
			return nil, false
		}
		sentinel := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if !fillRange(sentinel) {
			return nil, false
		}

	default:
		return nil, false
	}
	return fds, true
}

// dictOperand returns dict's index'th operand for operator op, and
// ok=false if op was never seen or did not carry that many operands -
// the small helper every Top-DICT/Private-DICT field lookup in
// parseCFFFont goes through.
func dictOperand(dict map[int][]float64, op, index int) (float64, bool) {
	vals, ok := dict[op]
	if !ok || index >= len(vals) {
		return 0, false
	}
	return vals[index], true
}

// parsePrivateSubrs reads a Private DICT's local subroutines: privDict
// is the already-parsed Private DICT itself, base is the byte offset
// (within the whole CFF data) where that Private DICT's own bytes began,
// and data is the whole CFF file. A Private DICT's Subrs operand (19),
// when present, is a byte offset for the Local Subr INDEX *relative to
// the Private DICT's own start* (CFF specification section 15) - unlike
// every other offset this file reads, which is relative to the whole
// file - hence needing base passed in separately here.
func parsePrivateSubrs(data []byte, privDict map[int][]float64, base int) cffIndex {
	subrOff, ok := dictOperand(privDict, 19, 0)
	if !ok {
		return nil
	}
	subrs, _, ok := parseCFFIndex(data, base+int(subrOff))
	if !ok {
		return nil
	}
	return subrs
}

// parsePrivateDict reads and parses one Private DICT given its Top-DICT-
// or Font-DICT-style [size, offset] operand pair (dictOperand(dict, 18,
// 0) and (dict, 18, 1) respectively - CFF specification section 14),
// returning ok=false if the operand pair is absent or its byte range
// does not fit within data.
func parsePrivateDict(data []byte, dict map[int][]float64) (privDict map[int][]float64, base int, ok bool) {
	size, okSize := dictOperand(dict, 18, 0)
	offset, okOffset := dictOperand(dict, 18, 1)
	if !okSize || !okOffset {
		return nil, 0, false
	}
	start, end := int(offset), int(offset)+int(size)
	if start < 0 || end < start || end > len(data) {
		return nil, 0, false
	}
	privDict, ok = parseCFFDict(data[start:end])
	return privDict, start, ok
}

// parseCFFFont parses data (an already filter-decoded /FontFile3
// stream's bytes for a bare CFF/Type1C or CIDFontType0C program, or the
// bytes of an sfnt "CFF " table for an OpenType/CFF font - see probe.go
// and simple.go/cid.go for the two ways a caller reaches this function)
// into a cffFont, returning ok=false for any program this package
// cannot use. Like parseSfnt (truetype.go), this deliberately never
// returns an error: an unusable embedded font program is this package's
// documented "no outlines available" case, handled by falling back to
// notdefGlyph, not a fatal one - see font.go's doc comment.
//
// This package supports exactly one Top DICT per file (CFF technically
// allows a "FontSet" of several via a Name INDEX with more than one
// entry, a rarely-used feature for bundling unrelated fonts together in
// one file - out of scope here, matching this package's existing
// "one usable face per embedded font program" assumption elsewhere).
func parseCFFFont(data []byte) (cffFont, bool) {
	if len(data) < 4 {
		return cffFont{}, false
	}
	hdrSize := int(data[2])
	if hdrSize < 4 || hdrSize > len(data) {
		return cffFont{}, false
	}
	pos := hdrSize

	// Name INDEX: this package has no use for the font's own PostScript
	// name (that comes from the PDF's /BaseFont instead - see
	// descriptor.go), only for skipping past this INDEX to reach the Top
	// DICT INDEX right after it.
	_, pos, ok := parseCFFIndex(data, pos)
	if !ok {
		return cffFont{}, false
	}

	topDicts, pos, ok := parseCFFIndex(data, pos)
	if !ok || len(topDicts) == 0 {
		return cffFont{}, false
	}
	topDict, ok := parseCFFDict(topDicts[0])
	if !ok {
		return cffFont{}, false
	}

	strs, pos, ok := parseCFFIndex(data, pos)
	if !ok {
		return cffFont{}, false
	}

	globalSubrs, _, ok := parseCFFIndex(data, pos)
	if !ok {
		return cffFont{}, false
	}

	charStringsOffset, ok := dictOperand(topDict, 17, 0)
	if !ok {
		return cffFont{}, false
	}
	charStrings, _, ok := parseCFFIndex(data, int(charStringsOffset))
	if !ok || len(charStrings) == 0 {
		return cffFont{}, false
	}
	numGlyphs := len(charStrings)

	// CharstringType (12 6, default 2): this file's charstringInterp only
	// understands Type 2 charstrings. Type 1 charstrings nested inside a
	// CFF wrapper are allowed by the specification but essentially never
	// seen in practice (real Type 1 fonts are always the older, entirely
	// separate PostScript Type 1 format this package already does not
	// read - see simple.go's doc comment on /FontFile) - fail closed
	// rather than misinterpret Type 1 bytecode as Type 2.
	if v, ok := dictOperand(topDict, cffEscapeOperator+6, 0); ok && v != 2 {
		return cffFont{}, false
	}

	// FontMatrix (12 7, default [0.001 0 0 0.001 0 0]): this package
	// only ever derives a single "unitsPerEm" scalar from it (matching
	// sfntFont's own single-scalar-per-em model - see truetype.go), using
	// just the horizontal scale term. A font with a genuinely
	// non-uniform or skewed FontMatrix (vanishingly rare in practice -
	// virtually every real CFF font uses the default) would not be
	// scaled quite correctly by this approximation, the same kind of
	// documented simplification this package already makes elsewhere
	// (see, for example, parseCompositeGlyph's point-matching gap).
	unitsPerEm := uint16(1000)
	if a, ok := dictOperand(topDict, cffEscapeOperator+7, 0); ok && a > 0 {
		if per := 1 / a; per > 0 && per < (1<<16) {
			unitsPerEm = uint16(per + 0.5)
		}
	}

	_, isCID := topDict[cffEscapeOperator+30] // ROS operand: presence alone marks a CID-keyed font (its own operand values - registry/ordering/supplement SIDs - are metadata this package has no use for).

	charsetOffset := 0
	if v, ok := dictOperand(topDict, 15, 0); ok {
		charsetOffset = int(v)
	}
	charsetIDs := parseCFFCharset(data, charsetOffset, numGlyphs)

	font := cffFont{
		charStrings: charStrings,
		globalSubrs: globalSubrs,
		isCID:       isCID,
		charsetIDs:  charsetIDs,
		strings:     strs,
		unitsPerEm:  unitsPerEm,
	}

	if isCID {
		if fdaOffset, ok := dictOperand(topDict, cffEscapeOperator+36, 0); ok {
			if fdArray, _, ok := parseCFFIndex(data, int(fdaOffset)); ok {
				font.fdLocalSubrs = make([]cffIndex, len(fdArray))
				for i, raw := range fdArray {
					fd, ok := parseCFFDict(raw)
					if !ok {
						continue
					}
					if privDict, base, ok := parsePrivateDict(data, fd); ok {
						font.fdLocalSubrs[i] = parsePrivateSubrs(data, privDict, base)
					}
				}
			}
		}
		if fdsOffset, ok := dictOperand(topDict, cffEscapeOperator+37, 0); ok {
			font.fdSelect, _ = parseCFFFDSelect(data, int(fdsOffset), numGlyphs)
		}
		if font.fdSelect == nil {
			// FDSelect missing or malformed: fall back to "every glyph
			// belongs to Font DICT 0" rather than failing the whole font
			// - the same graceful-degradation policy this file applies
			// to a malformed charset table above.
			font.fdSelect = make([]byte, numGlyphs)
		}

		font.cidToGID = make(map[uint16]uint16, numGlyphs)
		for gid := 1; gid < len(charsetIDs); gid++ {
			cid := charsetIDs[gid]
			if _, exists := font.cidToGID[cid]; !exists {
				font.cidToGID[cid] = uint16(gid)
			}
		}
	} else {
		if privDict, base, ok := parsePrivateDict(data, topDict); ok {
			font.localSubrs = parsePrivateSubrs(data, privDict, base)
		}

		font.runeToGID = make(map[rune]uint16, numGlyphs)
		for gid := 1; gid < len(charsetIDs); gid++ {
			name := font.sidToName(charsetIDs[gid])
			if name == "" {
				continue
			}
			if r, ok := glyphNameToRune(name); ok {
				if _, exists := font.runeToGID[r]; !exists {
					font.runeToGID[r] = uint16(gid)
				}
			}
		}
	}

	return font, true
}

// sidToName resolves a String ID to its glyph name: one of the 391
// predefined names in cffStandardStrings for sid < 391, or - for any
// larger value - an entry from this font's own String INDEX (CFF
// specification section 10). Returns "" for a SID this font's String
// INDEX does not actually have an entry for (a malformed or truncated
// font, since a well-formed one only ever references SIDs its String
// INDEX covers).
func (f *cffFont) sidToName(sid uint16) string {
	if int(sid) < len(cffStandardStrings) {
		return cffStandardStrings[sid]
	}
	idx := int(sid) - len(cffStandardStrings)
	if idx < 0 || idx >= len(f.strings) {
		return ""
	}
	return string(f.strings[idx])
}

// UnitsPerEm reports how many font design units make up one em in this
// font's own outline coordinate space - see this type's unitsPerEm field
// doc comment. It exists (mirroring sfntFont's identically-named method,
// added alongside this one - see font.go) so that font.go's Font type
// can scale a glyph outline from either kind of embedded font program
// through the same code path, without needing to know which one
// produced it.
func (f *cffFont) UnitsPerEm() uint16 {
	return f.unitsPerEm
}

// GIDForRune looks up a GID by Unicode rune via this font's own charset
// names - see the runeToGID field's doc comment. Always reports
// ok=false for a CID-keyed font (whose charset holds CIDs, not names -
// see GIDForCID instead).
func (f *cffFont) GIDForRune(r rune) (uint16, bool) {
	gid, ok := f.runeToGID[r]
	return gid, ok
}

// GIDForCID looks up a GID by CID via this font's inverted charset - see
// the cidToGID field's doc comment. Always reports ok=false for a
// non-CID-keyed font.
func (f *cffFont) GIDForCID(cid uint16) (uint16, bool) {
	gid, ok := f.cidToGID[cid]
	return gid, ok
}

// GlyphOutline returns glyph index gid's outline as a graphics.Path in
// the font's own native design-units coordinate space (i.e. not yet
// scaled by UnitsPerEm - see Font.Glyph in font.go, which does that
// scaling once the caller also knows what to scale to), and ok=false if
// gid is out of range or interpreting its charstring failed - see
// charstringInterp.exec. This is the CFF counterpart to sfntFont's
// identically-shaped GlyphOutline method (truetype.go); font.go's Font
// type calls whichever one its glyphSource actually is through a shared
// interface, without needing to know which.
func (f *cffFont) GlyphOutline(gid uint16) (*graphics.Path, bool) {
	if int(gid) >= len(f.charStrings) {
		return nil, false
	}

	local := f.localSubrs
	if f.isCID {
		fd := 0
		if int(gid) < len(f.fdSelect) {
			fd = int(f.fdSelect[gid])
		}
		if fd < 0 || fd >= len(f.fdLocalSubrs) {
			local = nil
		} else {
			local = f.fdLocalSubrs[fd]
		}
	}

	interp := newCharstringInterp(f.globalSubrs, local)
	if _, ok := interp.exec(f.charStrings[gid]); !ok {
		return nil, false
	}
	// endchar (the normal, well-formed way a charstring finishes) already
	// closes whatever subpath was open - this is a harmless no-op in that
	// case, and a safety net for a charstring that happened to run out of
	// bytes without ever reaching endchar (see doEndchar's doc comment).
	interp.path.Close()
	return interp.path, true
}

// maxCharstringStack, maxCharstringCallDepth, and maxStemHints bound a
// Type 2 Charstring interpreter's resource usage against a hostile or
// corrupted program, mirroring maxCompositeDepth's role for TrueType
// composite glyphs (truetype.go). These specific values are the Type 2
// Charstring Format specification's own limits (Adobe Technical Note
// #5177), not arbitrary choices: a conforming charstring never exceeds
// them, so a program that does is either corrupt or hostile, and safely
// rejected rather than trusted.
const (
	maxCharstringStack     = 48
	maxCharstringCallDepth = 10
	maxStemHints           = 256

	// maxCharstringSteps bounds the total number of operators a single
	// glyph's interpretation may execute, across every subroutine call
	// combined. This is not part of the Type 2 specification itself, but
	// this package's own defense against a pathological (though already
	// depth- and stack-bounded) charstring that still runs for a very
	// long time by repeatedly calling short subroutines many times over
	// - the same "bounded work against hostile input" policy
	// maxCompositeDepth documents for TrueType.
	maxCharstringSteps = 1 << 16
)

// charstringInterp is a Type 2 Charstring virtual machine: it holds
// everything exec (below) needs to run one glyph's charstring program
// (and, recursively, whatever local/global subroutines it calls into)
// and accumulate the resulting outline into path. A fresh
// charstringInterp is created per glyph (see cffFont.GlyphOutline) - it
// carries no state that should ever persist across two different
// glyphs.
//
// # If you are new to bytecode interpreters: what "operand stack" means
// here
//
// A Type 2 Charstring is a sequence of numbers ("operands") followed by
// an operator that consumes them, much like reverse-Polish-notation
// calculator input (e.g. "3 4 add" rather than "3 + 4"). stack holds
// whatever operands have been pushed since the last operator consumed
// them; x and y track the "pen" position outline operators draw
// relative to (every coordinate in a charstring is a *delta* from
// wherever the pen currently is, never an absolute position - see
// moveTo/lineTo/curveTo below).
type charstringInterp struct {
	globalSubrs, localSubrs cffIndex
	globalBias, localBias   int32

	stack []float64
	x, y  float64
	path  *graphics.Path

	// nStems counts every stem hint declared so far (via hstem/vstem/
	// hstemhm/vstemhm, or implicitly via hintmask/cntrmask - see
	// doStems/doMask), needed only to know how many bytes a hintmask or
	// cntrmask operator's own mask occupies ((nStems+7)/8 - one bit per
	// stem, packed into whole bytes). The hints themselves are never
	// applied - see this file's doc comment on scope.
	nStems int

	// widthParsed becomes true the first time this glyph's charstring
	// reaches any of the operators the Type 2 specification allows to
	// carry an optional leading glyph-width operand (see takeWidth) -
	// after which no later operator ever checks for one again, per the
	// specification's "first stack-clearing operator" rule.
	widthParsed bool

	// depth counts subroutine call nesting (see doCallsubr/doCallgsubr),
	// bounded by maxCharstringCallDepth. steps counts total operators
	// executed across the whole call tree, bounded by
	// maxCharstringSteps.
	depth int
	steps int

	// transient backs the `put`/`get` escape operators' small scratch
	// array (Type 2 Charstring specification's "transient array") - see
	// doArithmetic.
	transient [32]float64

	// randState is this glyph's own pseudorandom generator state for the
	// `random` escape operator - see nextRandom's doc comment for why
	// this is a small fixed-seed generator rather than Go's math/rand.
	randState uint32
}

// newCharstringInterp creates a fresh interpreter for one glyph, ready
// to run its top-level charstring via exec.
func newCharstringInterp(globalSubrs, localSubrs cffIndex) *charstringInterp {
	return &charstringInterp{
		globalSubrs: globalSubrs,
		localSubrs:  localSubrs,
		globalBias:  subrBias(len(globalSubrs)),
		localBias:   subrBias(len(localSubrs)),
		path:        &graphics.Path{},
		randState:   1,
	}
}

// subrBias returns the index bias a callsubr/callgsubr operand must be
// adjusted by before it is a usable index into subroutines (Type 2
// Charstring specification section 4.7, "Subroutine Operators") - a
// historical accommodation for 8-bit-clean data transport that, by the
// specification's own admission, "is not, in retrospect, a wise design
// decision" but is nonetheless required to correctly index into a real
// font's subroutine INDEX.
func subrBias(numSubroutines int) int32 {
	switch {
	case numSubroutines < 1240:
		return 107
	case numSubroutines < 33900:
		return 1131
	default:
		return 32768
	}
}

// decodeCharstringNumber decodes one Type 2 Charstring operand starting
// at instr[pos]. The caller (exec) is expected to have already checked
// that instr[pos] is a valid operand lead byte (28, or 32 through 255 -
// every other value below 32 is an operator, not the start of a
// number). This encoding is almost the same as a DICT operand's (compare
// parseCFFDict), except a charstring never uses DICT's nibble-encoded
// "real number" form (lead byte 30 there is instead the *hvcurveto*
// operator here - see exec), and gives lead byte 255 its own charstring-
// specific meaning: a 16.16 fixed-point number, the form charstring
// coordinate deltas use whenever they need fractional precision (a DICT
// operand never needs this, since DICT values like FontMatrix's entries
// use the nibble-encoded real form instead - see parseCFFDict).
func decodeCharstringNumber(instr []byte, pos int) (float64, int, bool) {
	b0 := instr[pos]
	switch {
	case b0 == 28:
		if pos+3 > len(instr) {
			return 0, 0, false
		}
		return float64(int16(binary.BigEndian.Uint16(instr[pos+1 : pos+3]))), pos + 3, true
	case b0 == 255:
		if pos+5 > len(instr) {
			return 0, 0, false
		}
		return float64(int32(binary.BigEndian.Uint32(instr[pos+1:pos+5]))) / 65536, pos + 5, true
	case b0 >= 32 && b0 <= 246:
		return float64(int(b0) - 139), pos + 1, true
	case b0 >= 247 && b0 <= 250:
		if pos+2 > len(instr) {
			return 0, 0, false
		}
		return float64((int(b0)-247)*256 + int(instr[pos+1]) + 108), pos + 2, true
	case b0 >= 251 && b0 <= 254:
		if pos+2 > len(instr) {
			return 0, 0, false
		}
		return float64(-(int(b0)-251)*256 - int(instr[pos+1]) - 108), pos + 2, true
	default:
		return 0, 0, false
	}
}

// exec interprets instr - either a glyph's own top-level charstring, or,
// recursively, a local/global subroutine's body (see doCallsubr/
// doCallgsubr) - appending whatever outline it draws to c.path. It
// returns done=true once endchar has been reached (signalling every
// enclosing exec call, all the way back up to GlyphOutline, to stop
// immediately without interpreting any further bytes at any level), and
// ok=false for any charstring this interpreter cannot make sense of:
// malformed operand encoding, an out-of-range subroutine index,
// exceeding one of this file's safety bounds, or an operator receiving
// an operand count the Type 2 specification's grammar does not allow for
// it. Like every other parser in this package, this fails closed rather
// than panicking - see this file's doc comment.
//
// A plain `return` operator (11) is handled simply by this function
// returning normally (done=false, ok=true): the recursive call this
// exec invocation is nested inside (see doCallsubr/doCallgsubr) then
// naturally resumes its own loop right where it left off, which is
// exactly Go's own call-stack behaviour and needs no explicit
// "resume position" bookkeeping of this file's own.
func (c *charstringInterp) exec(instr []byte) (done bool, ok bool) {
	pos := 0
	for pos < len(instr) {
		c.steps++
		if c.steps > maxCharstringSteps {
			return false, false
		}

		b0 := instr[pos]
		if b0 == 28 || b0 >= 32 {
			v, newPos, ok := decodeCharstringNumber(instr, pos)
			if !ok {
				return false, false
			}
			if len(c.stack) >= maxCharstringStack {
				return false, false
			}
			c.stack = append(c.stack, v)
			pos = newPos
			continue
		}

		pos++
		op := int(b0)
		if b0 == 12 {
			if pos >= len(instr) {
				return false, false
			}
			op = cffEscapeOperator + int(instr[pos])
			pos++
		}

		switch op {
		case 1, 3, 18, 23: // hstem, vstem, hstemhm, vstemhm
			if !c.doStems() {
				return false, false
			}
		case 19, 20: // hintmask, cntrmask
			skip, ok := c.doMask()
			if !ok || pos+skip > len(instr) {
				return false, false
			}
			pos += skip
		case 21: // rmoveto
			if !c.doRmoveto() {
				return false, false
			}
		case 22: // hmoveto
			if !c.doHmoveto() {
				return false, false
			}
		case 4: // vmoveto
			if !c.doVmoveto() {
				return false, false
			}
		case 5: // rlineto
			if !c.doRlineto() {
				return false, false
			}
		case 6: // hlineto
			if !c.doAltLineto(true) {
				return false, false
			}
		case 7: // vlineto
			if !c.doAltLineto(false) {
				return false, false
			}
		case 8: // rrcurveto
			if !c.doRrcurveto() {
				return false, false
			}
		case 24: // rcurveline
			if !c.doRcurveline() {
				return false, false
			}
		case 25: // rlinecurve
			if !c.doRlinecurve() {
				return false, false
			}
		case 26: // vvcurveto
			if !c.doVVHHcurveto(true) {
				return false, false
			}
		case 27: // hhcurveto
			if !c.doVVHHcurveto(false) {
				return false, false
			}
		case 30: // vhcurveto
			if !c.doVHHVcurveto(true) {
				return false, false
			}
		case 31: // hvcurveto
			if !c.doVHHVcurveto(false) {
				return false, false
			}
		case 10: // callsubr
			d, ok := c.callSubr(c.localSubrs, c.localBias)
			if !ok {
				return false, false
			}
			if d {
				return true, true
			}
		case 29: // callgsubr
			d, ok := c.callSubr(c.globalSubrs, c.globalBias)
			if !ok {
				return false, false
			}
			if d {
				return true, true
			}
		case 11: // return
			return false, true
		case 14: // endchar
			return true, c.doEndchar()
		case cffEscapeOperator + 34: // hflex
			if !c.doHflex() {
				return false, false
			}
		case cffEscapeOperator + 35: // flex
			if !c.doFlex() {
				return false, false
			}
		case cffEscapeOperator + 36: // hflex1
			if !c.doHflex1() {
				return false, false
			}
		case cffEscapeOperator + 37: // flex1
			if !c.doFlex1() {
				return false, false
			}
		case cffEscapeOperator + 3, cffEscapeOperator + 4, cffEscapeOperator + 5,
			cffEscapeOperator + 9, cffEscapeOperator + 10, cffEscapeOperator + 11,
			cffEscapeOperator + 12, cffEscapeOperator + 14, cffEscapeOperator + 15,
			cffEscapeOperator + 18, cffEscapeOperator + 20, cffEscapeOperator + 21,
			cffEscapeOperator + 22, cffEscapeOperator + 23, cffEscapeOperator + 24,
			cffEscapeOperator + 26, cffEscapeOperator + 27, cffEscapeOperator + 28,
			cffEscapeOperator + 29, cffEscapeOperator + 30:
			if !c.doArithmetic(op - cffEscapeOperator) {
				return false, false
			}
		default:
			// A Reserved opcode, or an escape sub-operator outside the
			// Type 2 specification's defined set: cleared and ignored
			// rather than failing the whole glyph - the same tolerance
			// this project applies to an unrecognized PDF content stream
			// operator (see internal/content's own doc comment on that).
			c.stack = c.stack[:0]
		}
	}
	return false, true
}

// callSubr implements callsubr (local subroutines) and callgsubr
// (global subroutines) - both pop a subroutine index off the top of the
// operand stack, adjust it by bias (see subrBias), bounds-check it
// against subrs, and recursively interpret that subroutine's own bytes.
func (c *charstringInterp) callSubr(subrs cffIndex, bias int32) (done bool, ok bool) {
	if len(c.stack) == 0 {
		return false, false
	}
	idx := int32(c.stack[len(c.stack)-1]) + bias
	c.stack = c.stack[:len(c.stack)-1]
	if idx < 0 || int(idx) >= len(subrs) {
		return false, false
	}

	c.depth++
	if c.depth > maxCharstringCallDepth {
		return false, false
	}
	done, ok = c.exec(subrs[idx])
	c.depth--
	return done, ok
}

// takeWidth removes a charstring's optional leading glyph-width operand
// from the bottom of the operand stack, at most once per glyph (the
// Type 2 specification's "first stack-clearing operator" rule: only the
// very first of hstem/vstem/hstemhm/vstemhm/cntrmask/hintmask/hmoveto/
// vmoveto/rmoveto/endchar that a charstring actually reaches may carry
// one). This package has no use for the width value itself - a glyph's
// advance width always comes from the PDF font dictionary's own
// /Widths or /W array (see font.go's Width and docs/FONTS.md's "Widths
// vs. outlines" discussion, which treats reading it from a substitute
// font as optional future work, not something this phase needs) - so
// the value, once identified, is simply discarded rather than stored
// anywhere.
//
// expectedArgs is how many operands the calling operator expects once
// any width has been removed: a fixed count for hmoveto/vmoveto (1),
// rmoveto (2), or endchar (0) - or -1 for a stem operator, whose
// expected count is always even rather than one fixed number (see
// hasLeadingWidth).
func (c *charstringInterp) takeWidth(expectedArgs int) {
	if c.widthParsed {
		return
	}
	c.widthParsed = true
	if hasLeadingWidth(len(c.stack), expectedArgs) {
		c.stack = c.stack[1:]
	}
}

func hasLeadingWidth(nArgs, expectedArgs int) bool {
	if expectedArgs < 0 {
		return nArgs%2 == 1
	}
	return nArgs == expectedArgs+1
}

// moveTo starts a new contour at (c.x+dx, c.y+dy) - the shared
// implementation behind rmoveto/hmoveto/vmoveto (see doRmoveto,
// doHmoveto, doVmoveto). It always closes whatever contour was open
// beforehand first: graphics.Path.Close is documented as a harmless
// no-op when no subpath is open yet (the very first moveto in a
// charstring), so this needs no separate "is a contour already open"
// bookkeeping of its own.
func (c *charstringInterp) moveTo(dx, dy float64) {
	c.path.Close()
	c.x += dx
	c.y += dy
	c.path.MoveTo(graphics.Point{X: c.x, Y: c.y})
}

// lineTo appends a straight segment from the current point to
// (c.x+dx, c.y+dy).
func (c *charstringInterp) lineTo(dx, dy float64) {
	c.x += dx
	c.y += dy
	c.path.LineTo(graphics.Point{X: c.x, Y: c.y})
}

// curveTo appends a cubic Bézier curve from the current point, through
// two control points, to an end point - all three given as (dx, dy)
// deltas chained from the current point in turn (control point 1 is
// relative to the point before this call; control point 2 is relative
// to control point 1; the end point is relative to control point 2 -
// exactly how every CFF curve operator's own operands are defined,
// letting each of this file's doXxxCurveto helpers pass their operands
// straight through without first converting to absolute coordinates
// themselves).
func (c *charstringInterp) curveTo(dx1, dy1, dx2, dy2, dx3, dy3 float64) {
	c1 := graphics.Point{X: c.x + dx1, Y: c.y + dy1}
	c2 := graphics.Point{X: c1.X + dx2, Y: c1.Y + dy2}
	end := graphics.Point{X: c2.X + dx3, Y: c2.Y + dy3}
	c.path.CurveTo(c1, c2, end)
	c.x, c.y = end.X, end.Y
}

// doStems implements hstem, vstem, hstemhm, and vstemhm: each declares
// zero or more stem hints as pairs of operands (see this type's nStems
// field doc comment for why only the *count* matters to this package,
// never the hints' own values).
func (c *charstringInterp) doStems() bool {
	c.takeWidth(-1)
	if len(c.stack)%2 != 0 {
		return false
	}
	c.nStems += len(c.stack) / 2
	if c.nStems > maxStemHints {
		return false
	}
	c.stack = c.stack[:0]
	return true
}

// doMask implements the shared logic behind hintmask and cntrmask: both
// optionally declare one final batch of vstem hints "for free" (as an
// implicit vstemhm, if any operands are still on the stack when the
// operator is reached - CFF specification section 4.3), then are
// followed in the charstring byte stream by a mask of (nStems+7)/8 bytes
// (one bit per declared stem) that this package has no use for beyond
// knowing how many bytes to skip over. The returned int is that skip
// count; the caller (exec) is the one that actually advances past those
// bytes, since only it has access to the surrounding instruction slice.
func (c *charstringInterp) doMask() (int, bool) {
	if len(c.stack) > 0 {
		if !c.doStems() {
			return 0, false
		}
	} else if !c.widthParsed {
		// An empty stack can never itself carry a width value, but
		// reaching hintmask/cntrmask with no pending stem operands still
		// counts as this charstring's first stack-clearing operator for
		// width-parsing purposes (matching every other stack-clearing
		// operator's behaviour - see takeWidth).
		c.widthParsed = true
	}
	return (c.nStems + 7) / 8, true
}

func (c *charstringInterp) doRmoveto() bool {
	c.takeWidth(2)
	if len(c.stack) != 2 {
		return false
	}
	c.moveTo(c.stack[0], c.stack[1])
	c.stack = c.stack[:0]
	return true
}

func (c *charstringInterp) doHmoveto() bool {
	c.takeWidth(1)
	if len(c.stack) != 1 {
		return false
	}
	c.moveTo(c.stack[0], 0)
	c.stack = c.stack[:0]
	return true
}

func (c *charstringInterp) doVmoveto() bool {
	c.takeWidth(1)
	if len(c.stack) != 1 {
		return false
	}
	c.moveTo(0, c.stack[0])
	c.stack = c.stack[:0]
	return true
}

func (c *charstringInterp) doRlineto() bool {
	if !c.widthParsed || len(c.stack) < 2 || len(c.stack)%2 != 0 {
		return false
	}
	for i := 0; i+1 < len(c.stack); i += 2 {
		c.lineTo(c.stack[i], c.stack[i+1])
	}
	c.stack = c.stack[:0]
	return true
}

// doAltLineto implements both hlineto (startHorizontal=true) and
// vlineto (startHorizontal=false): a sequence of single-axis line
// segments that alternate which axis each one moves along, starting
// with whichever axis the operator's own name says.
func (c *charstringInterp) doAltLineto(startHorizontal bool) bool {
	if !c.widthParsed || len(c.stack) < 1 {
		return false
	}
	horizontal := startHorizontal
	for _, v := range c.stack {
		if horizontal {
			c.lineTo(v, 0)
		} else {
			c.lineTo(0, v)
		}
		horizontal = !horizontal
	}
	c.stack = c.stack[:0]
	return true
}

func (c *charstringInterp) doRrcurveto() bool {
	if !c.widthParsed || len(c.stack) < 6 || len(c.stack)%6 != 0 {
		return false
	}
	for i := 0; i+5 < len(c.stack); i += 6 {
		c.curveTo(c.stack[i], c.stack[i+1], c.stack[i+2], c.stack[i+3], c.stack[i+4], c.stack[i+5])
	}
	c.stack = c.stack[:0]
	return true
}

// doRcurveline implements rcurveline: one or more full 6-operand curves
// followed by one final 2-operand line (total operand count 6k+2 for
// k>=1) - a compact encoding for the common case of a curved shape
// ending in one straight edge.
func (c *charstringInterp) doRcurveline() bool {
	n := len(c.stack)
	if !c.widthParsed || n < 8 || n%6 != 2 {
		return false
	}
	i := 0
	for ; i+6 <= n-2; i += 6 {
		c.curveTo(c.stack[i], c.stack[i+1], c.stack[i+2], c.stack[i+3], c.stack[i+4], c.stack[i+5])
	}
	c.lineTo(c.stack[i], c.stack[i+1])
	c.stack = c.stack[:0]
	return true
}

// doRlinecurve implements rlinecurve: one or more 2-operand lines
// followed by one final 6-operand curve (total operand count 2k+6 for
// k>=1) - rcurveline's mirror image, for a shape made of straight edges
// ending in one curve.
func (c *charstringInterp) doRlinecurve() bool {
	n := len(c.stack)
	if !c.widthParsed || n < 8 || n%2 != 0 {
		return false
	}
	i := 0
	for ; i+2 <= n-6; i += 2 {
		c.lineTo(c.stack[i], c.stack[i+1])
	}
	c.curveTo(c.stack[i], c.stack[i+1], c.stack[i+2], c.stack[i+3], c.stack[i+4], c.stack[i+5])
	c.stack = c.stack[:0]
	return true
}

// doVVHHcurveto implements vvcurveto (vertical=true) and hhcurveto
// (vertical=false): a sequence of curves whose tangents are all
// vertical (vvcurveto) or all horizontal (hhcurveto), except that the
// very first curve may additionally carry one leading cross-axis offset
// (an operand count of 4k+1 rather than a clean 4k, for exactly one
// extra value at the very front of the operand list) - the Type 2
// Charstring specification's compact way to encode a nearly-but-not-
// quite-single-axis piecewise curve (common for the curved sides of
// letterforms like "O" or "S") without spending 6 operands per segment
// the way a general rrcurveto would.
func (c *charstringInterp) doVVHHcurveto(vertical bool) bool {
	n := len(c.stack)
	if !c.widthParsed || n < 4 {
		return false
	}

	i, lead := 0, 0.0
	switch {
	case n%4 == 1:
		lead = c.stack[0]
		i = 1
	case n%4 != 0:
		return false
	}

	first := true
	for ; i+3 < n; i += 4 {
		a, b, cc, d := c.stack[i], c.stack[i+1], c.stack[i+2], c.stack[i+3]
		cross := 0.0
		if first {
			cross = lead
		}
		if vertical {
			c.curveTo(cross, a, b, cc, 0, d)
		} else {
			c.curveTo(a, cross, b, cc, d, 0)
		}
		first = false
	}
	c.stack = c.stack[:0]
	return true
}

// doVHHVcurveto implements vhcurveto (startVertical=true) and hvcurveto
// (startVertical=false): a sequence of curves that *alternate* between
// vertical-tangent-start and horizontal-tangent-start each segment
// (rather than vvcurveto/hhcurveto's "always the same axis"), with an
// optional single trailing cross-axis offset for the very last curve
// only (an operand count of 4k+1, the extra value at the very *end*
// this time, unlike doVVHHcurveto's leading one).
func (c *charstringInterp) doVHHVcurveto(startVertical bool) bool {
	n := len(c.stack)
	if !c.widthParsed || n < 4 {
		return false
	}

	vertical := startVertical
	i := 0
	for n-i >= 4 {
		extra := n-i == 5
		a, b, cc, d := c.stack[i], c.stack[i+1], c.stack[i+2], c.stack[i+3]
		i += 4

		cross := 0.0
		if extra {
			cross = c.stack[i]
			i++
		}
		if vertical {
			c.curveTo(0, a, b, cc, d, cross)
		} else {
			c.curveTo(a, 0, b, cc, cross, d)
		}
		vertical = !vertical
	}
	if i != n {
		// A malformed operand count (not fully consumed by the loop
		// above, e.g. 4k+2 or 4k+3 operands) - the Type 2 grammar only
		// allows 4k or 4k+1.
		return false
	}
	c.stack = c.stack[:0]
	return true
}

// doHflex, doFlex, doHflex1, and doFlex1 implement the four "flex"
// escape operators (Type 2 Charstring specification section 4.5): each
// draws two cubic curves forming one smooth S-shaped or C-shaped
// feature (commonly used for the gentle curve where a stem joins a
// serif, or a symbol's rounded corner). Their names encode how many of
// each curve's coordinate deltas are actually stored in the charstring
// versus implied to be zero (a plain "flex" stores every delta
// explicitly; "hflex"/"hflex1" additionally assume several deltas are
// zero because the whole feature is known in advance to start and end
// at the same height) - purely a space-saving encoding choice on the
// font author's tool's part, not a different visual result than the
// equivalent pair of rrcurveto/vvcurveto calls would produce, so this
// package draws all four exactly the same way (two ordinary cubic
// curves via curveTo) rather than treating them as a distinct kind of
// path segment.
//
// This package deliberately ignores each operator's own "flex height"
// operand (present on flex and hflex1; a hinting-quality-only value
// about when a renderer should treat the two curves as flat instead) -
// matching truetype.go's "no hinting" precedent for TrueType outlines.
func (c *charstringInterp) doHflex() bool {
	if !c.widthParsed || len(c.stack) != 7 {
		return false
	}
	s := c.stack
	c.curveTo(s[0], 0, s[1], s[2], s[3], 0)
	c.curveTo(s[4], 0, s[5], -s[2], s[6], 0)
	c.stack = c.stack[:0]
	return true
}

func (c *charstringInterp) doFlex() bool {
	if !c.widthParsed || len(c.stack) != 13 {
		return false
	}
	s := c.stack
	c.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
	c.curveTo(s[6], s[7], s[8], s[9], s[10], s[11])
	// s[12] is the flex height operand - see this function group's doc
	// comment on why it is ignored.
	c.stack = c.stack[:0]
	return true
}

func (c *charstringInterp) doHflex1() bool {
	if !c.widthParsed || len(c.stack) != 9 {
		return false
	}
	s := c.stack
	dy1, dy2, dy5 := s[1], s[3], s[7]
	dy6 := -(dy1 + dy2 + dy5) // the second curve's endpoint returns to the same height (y) the first curve started at.
	c.curveTo(s[0], dy1, s[2], dy2, s[4], 0)
	c.curveTo(s[5], 0, s[6], dy5, s[8], dy6)
	c.stack = c.stack[:0]
	return true
}

func (c *charstringInterp) doFlex1() bool {
	if !c.widthParsed || len(c.stack) != 11 {
		return false
	}
	s := c.stack
	dxSum := s[0] + s[2] + s[4] + s[6] + s[8]
	dySum := s[1] + s[3] + s[5] + s[7] + s[9]
	c.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
	// flex1's final operand, s[10], is whichever axis moved *less* over
	// the first five delta pairs combined (dxSum vs dySum): the other
	// axis is inferred to return the pen to that same coordinate it
	// started this whole two-curve feature at, keeping the overall shape
	// aligned to whichever axis dominates its motion.
	if math.Abs(dxSum) > math.Abs(dySum) {
		c.curveTo(s[6], s[7], s[8], s[9], s[10], -dySum)
	} else {
		c.curveTo(s[6], s[7], s[8], s[9], -dxSum, s[10])
	}
	c.stack = c.stack[:0]
	return true
}

// doEndchar implements endchar: the operator that always ends a glyph's
// charstring interpretation, at whatever subroutine nesting level it is
// reached from (see exec's own handling, which propagates done=true all
// the way back up regardless of call depth).
func (c *charstringInterp) doEndchar() bool {
	if !c.widthParsed && (len(c.stack) == 1 || len(c.stack) == 5) {
		c.stack = c.stack[1:]
	}
	c.widthParsed = true

	switch len(c.stack) {
	case 0:
		c.path.Close()
		return true
	case 4:
		// A deprecated implicit "seac" (standard-encoding accented
		// character) composition: adx ady bchar achar endchar, composing
		// this glyph from two others (a base letter plus a combining
		// accent) named by their Adobe StandardEncoding code points.
		// Modern font tools essentially never emit this form
		// (precomposed accented glyphs are used instead), and correctly
		// implementing it would need this package to also carry a full
		// StandardEncoding-code-to-glyph-name table it has no other use
		// for - so, like Type 1 charstring support itself, this is a
		// documented non-goal (see docs/capability-matrix.md). This one
		// glyph is reported as unavailable (falls back to notdefGlyph
		// via GlyphOutline's ok=false), rather than the whole font
		// failing to load, or - worse - this operand data being
		// misinterpreted as path-drawing coordinates.
		return false
	default:
		return false
	}
}

// doArithmetic implements the small set of Type 2 Charstring escape
// operators (12 3 through 12 30, excluding the flex operators handled
// separately above) that manipulate the operand stack or this glyph's
// transient array numerically, rather than drawing anything - sub is
// the operator's own second byte (already stripped of cffEscapeOperator
// by the caller, exec). These exist so a charstring can encode a small
// amount of conditional logic (historically intended for
// programmatically-generated multiple-master fonts); real-world fonts
// essentially never use them, but this package implements them anyway
// rather than merely tolerating and ignoring them (exec's default case,
// this file's usual policy for a genuinely unrecognized operator),
// since silently mishandling one would corrupt the operand stack for
// whatever path-drawing operator comes next - a worse failure mode than
// simply not supporting the feature at all.
func (c *charstringInterp) doArithmetic(sub int) bool {
	pop := func() (float64, bool) {
		if len(c.stack) == 0 {
			return 0, false
		}
		v := c.stack[len(c.stack)-1]
		c.stack = c.stack[:len(c.stack)-1]
		return v, true
	}
	push := func(v float64) bool {
		if len(c.stack) >= maxCharstringStack {
			return false
		}
		c.stack = append(c.stack, v)
		return true
	}
	boolVal := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	pop2 := func() (a, b float64, ok bool) {
		b, ok1 := pop()
		a, ok2 := pop()
		return a, b, ok1 && ok2
	}

	switch sub {
	case 3: // and
		a, b, ok := pop2()
		return ok && push(boolVal(a != 0 && b != 0))
	case 4: // or
		a, b, ok := pop2()
		return ok && push(boolVal(a != 0 || b != 0))
	case 5: // not
		a, ok := pop()
		return ok && push(boolVal(a == 0))
	case 9: // abs
		a, ok := pop()
		return ok && push(math.Abs(a))
	case 10: // add
		a, b, ok := pop2()
		return ok && push(a+b)
	case 11: // sub
		a, b, ok := pop2()
		return ok && push(a-b)
	case 12: // div
		a, b, ok := pop2()
		if !ok {
			return false
		}
		if b == 0 {
			return push(0)
		}
		return push(a / b)
	case 14: // neg
		a, ok := pop()
		return ok && push(-a)
	case 15: // eq
		a, b, ok := pop2()
		return ok && push(boolVal(a == b))
	case 18: // drop
		_, ok := pop()
		return ok
	case 20: // put: val i put - stores val into the transient array at index i.
		i, val, ok := pop2()
		if !ok {
			return false
		}
		if idx := int(i); idx >= 0 && idx < len(c.transient) {
			c.transient[idx] = val
		}
		return true
	case 21: // get: i get - pushes the transient array's value at index i.
		i, ok := pop()
		if !ok {
			return false
		}
		idx := int(i)
		if idx < 0 || idx >= len(c.transient) {
			return push(0)
		}
		return push(c.transient[idx])
	case 22: // ifelse: s1 s2 v1 v2 ifelse - pushes s1 if v1<=v2, else s2.
		if len(c.stack) < 4 {
			return false
		}
		v2, v1, s2, s1 := c.stack[len(c.stack)-1], c.stack[len(c.stack)-2], c.stack[len(c.stack)-3], c.stack[len(c.stack)-4]
		c.stack = c.stack[:len(c.stack)-4]
		if v1 <= v2 {
			return push(s1)
		}
		return push(s2)
	case 23: // random: pushes a pseudorandom number in [0, 1).
		return push(c.nextRandom())
	case 24: // mul
		a, b, ok := pop2()
		return ok && push(a*b)
	case 26: // sqrt
		a, ok := pop()
		if !ok || a < 0 {
			return false
		}
		return push(math.Sqrt(a))
	case 27: // dup
		a, ok := pop()
		return ok && push(a) && push(a)
	case 28: // exch
		a, b, ok := pop2()
		return ok && push(b) && push(a)
	case 29: // index: n index - pushes a copy of the element n back from the top (0 duplicates the top element; a negative n is treated as 0, per the specification).
		n, ok := pop()
		if !ok {
			return false
		}
		idx := int(n)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(c.stack) {
			return false
		}
		return push(c.stack[len(c.stack)-1-idx])
	case 30: // roll: n j roll - circularly shifts the top n stack elements by j positions.
		j, nf, ok := pop2()
		if !ok {
			return false
		}
		count := int(nf)
		if count < 0 || count > len(c.stack) {
			return false
		}
		if count == 0 {
			return true
		}
		shift := ((int(j) % count) + count) % count
		top := c.stack[len(c.stack)-count:]
		rolled := make([]float64, count)
		for i, v := range top {
			rolled[(i+shift)%count] = v
		}
		copy(top, rolled)
		return true
	default:
		// Reserved/unassigned escape sub-operator - see exec's default
		// case for why this clears the stack instead of failing.
		c.stack = c.stack[:0]
		return true
	}
}

// nextRandom returns this glyph's next value for the `random` Type 2
// Charstring operator (escape 23): a pseudorandom number in [0, 1). The
// Type 2 specification only requires "a pseudo random number" with no
// particular algorithm or seed, so this package uses a small, fixed-
// seed xorshift generator rather than Go's math/rand: the same
// charstring must always produce the same glyph outline on every run
// (see graphics.Path's package-level precedent of deterministic Bézier
// flattening, for exactly this project-wide reason), which a process- or
// time-seeded random source would violate for the vanishingly rare font
// that actually uses this operator to affect its own path geometry.
func (c *charstringInterp) nextRandom() float64 {
	x := c.randState
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	c.randState = x
	return float64(x) / float64(1<<32)
}

// cffStandardStrings is CFF's fixed table of 391 predefined glyph names
// (CFF specification Appendix A) - a String ID (SID) below this table's
// length names cffStandardStrings[sid] directly; a SID of 391 or higher
// instead indexes into this particular font's own String INDEX (see
// cffFont.sidToName). Every CFF font in existence agrees on this exact
// list and ordering, since it is baked into the file format itself
// (unlike, say, TrueType's "name" table, where every font supplies its
// own strings from scratch - see probe.go's parseNameTable) - this
// package's copy is transcribed from the reference list maintained by
// the widely used fontTools Python library (Lib/fontTools/cffLib,
// itself transcribing the same Adobe specification), which is easier to
// verify byte-for-byte against than the original PDF specification
// document.
//
// Its first 229 entries (indices 0-228) are also exactly CFF's
// predefined "ISOAdobe" charset, in order - see identityCharset's doc
// comment for why that fact matters to this file's charset handling.
var cffStandardStrings = []string{
	".notdef", "space", "exclam", "quotedbl", "numbersign", "dollar",
	"percent", "ampersand", "quoteright", "parenleft", "parenright",
	"asterisk", "plus", "comma", "hyphen", "period", "slash", "zero", "one",
	"two", "three", "four", "five", "six", "seven", "eight", "nine", "colon",
	"semicolon", "less", "equal", "greater", "question", "at", "A", "B", "C",
	"D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P", "Q", "R",
	"S", "T", "U", "V", "W", "X", "Y", "Z", "bracketleft", "backslash",
	"bracketright", "asciicircum", "underscore", "quoteleft", "a", "b", "c",
	"d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r",
	"s", "t", "u", "v", "w", "x", "y", "z", "braceleft", "bar", "braceright",
	"asciitilde", "exclamdown", "cent", "sterling", "fraction", "yen",
	"florin", "section", "currency", "quotesingle", "quotedblleft",
	"guillemotleft", "guilsinglleft", "guilsinglright", "fi", "fl", "endash",
	"dagger", "daggerdbl", "periodcentered", "paragraph", "bullet",
	"quotesinglbase", "quotedblbase", "quotedblright", "guillemotright",
	"ellipsis", "perthousand", "questiondown", "grave", "acute",
	"circumflex", "tilde", "macron", "breve", "dotaccent", "dieresis",
	"ring", "cedilla", "hungarumlaut", "ogonek", "caron", "emdash", "AE",
	"ordfeminine", "Lslash", "Oslash", "OE", "ordmasculine", "ae",
	"dotlessi", "lslash", "oslash", "oe", "germandbls", "onesuperior",
	"logicalnot", "mu", "trademark", "Eth", "onehalf", "plusminus", "Thorn",
	"onequarter", "divide", "brokenbar", "degree", "thorn", "threequarters",
	"twosuperior", "registered", "minus", "eth", "multiply", "threesuperior",
	"copyright", "Aacute", "Acircumflex", "Adieresis", "Agrave", "Aring",
	"Atilde", "Ccedilla", "Eacute", "Ecircumflex", "Edieresis", "Egrave",
	"Iacute", "Icircumflex", "Idieresis", "Igrave", "Ntilde", "Oacute",
	"Ocircumflex", "Odieresis", "Ograve", "Otilde", "Scaron", "Uacute",
	"Ucircumflex", "Udieresis", "Ugrave", "Yacute", "Ydieresis", "Zcaron",
	"aacute", "acircumflex", "adieresis", "agrave", "aring", "atilde",
	"ccedilla", "eacute", "ecircumflex", "edieresis", "egrave", "iacute",
	"icircumflex", "idieresis", "igrave", "ntilde", "oacute", "ocircumflex",
	"odieresis", "ograve", "otilde", "scaron", "uacute", "ucircumflex",
	"udieresis", "ugrave", "yacute", "ydieresis", "zcaron", "exclamsmall",
	"Hungarumlautsmall", "dollaroldstyle", "dollarsuperior",
	"ampersandsmall", "Acutesmall", "parenleftsuperior",
	"parenrightsuperior", "twodotenleader", "onedotenleader",
	"zerooldstyle", "oneoldstyle", "twooldstyle", "threeoldstyle",
	"fouroldstyle", "fiveoldstyle", "sixoldstyle", "sevenoldstyle",
	"eightoldstyle", "nineoldstyle", "commasuperior",
	"threequartersemdash", "periodsuperior", "questionsmall", "asuperior",
	"bsuperior", "centsuperior", "dsuperior", "esuperior", "isuperior",
	"lsuperior", "msuperior", "nsuperior", "osuperior", "rsuperior",
	"ssuperior", "tsuperior", "ff", "ffi", "ffl", "parenleftinferior",
	"parenrightinferior", "Circumflexsmall", "hyphensuperior",
	"Gravesmall", "Asmall", "Bsmall", "Csmall", "Dsmall", "Esmall",
	"Fsmall", "Gsmall", "Hsmall", "Ismall", "Jsmall", "Ksmall", "Lsmall",
	"Msmall", "Nsmall", "Osmall", "Psmall", "Qsmall", "Rsmall", "Ssmall",
	"Tsmall", "Usmall", "Vsmall", "Wsmall", "Xsmall", "Ysmall", "Zsmall",
	"colonmonetary", "onefitted", "rupiah", "Tildesmall", "exclamdownsmall",
	"centoldstyle", "Lslashsmall", "Scaronsmall", "Zcaronsmall",
	"Dieresissmall", "Brevesmall", "Caronsmall", "Dotaccentsmall",
	"Macronsmall", "figuredash", "hypheninferior", "Ogoneksmall",
	"Ringsmall", "Cedillasmall", "questiondownsmall", "oneeighth",
	"threeeighths", "fiveeighths", "seveneighths", "onethird", "twothirds",
	"zerosuperior", "foursuperior", "fivesuperior", "sixsuperior",
	"sevensuperior", "eightsuperior", "ninesuperior", "zeroinferior",
	"oneinferior", "twoinferior", "threeinferior", "fourinferior",
	"fiveinferior", "sixinferior", "seveninferior", "eightinferior",
	"nineinferior", "centinferior", "dollarinferior", "periodinferior",
	"commainferior", "Agravesmall", "Aacutesmall", "Acircumflexsmall",
	"Atildesmall", "Adieresissmall", "Aringsmall", "AEsmall",
	"Ccedillasmall", "Egravesmall", "Eacutesmall", "Ecircumflexsmall",
	"Edieresissmall", "Igravesmall", "Iacutesmall", "Icircumflexsmall",
	"Idieresissmall", "Ethsmall", "Ntildesmall", "Ogravesmall",
	"Oacutesmall", "Ocircumflexsmall", "Otildesmall", "Odieresissmall",
	"OEsmall", "Oslashsmall", "Ugravesmall", "Uacutesmall",
	"Ucircumflexsmall", "Udieresissmall", "Yacutesmall", "Thornsmall",
	"Ydieresissmall", "001.000", "001.001", "001.002", "001.003", "Black",
	"Bold", "Book", "Light", "Medium", "Regular", "Roman", "Semibold",
}
