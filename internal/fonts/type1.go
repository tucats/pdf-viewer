package fonts

import (
	"bytes"
	"encoding/binary"
	"strconv"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// This file implements docs/PLAN2.md's Phase 11: enough of Adobe's
// original "Type 1" font format - and, within it, the "Type 1
// Charstring" instruction set that actually draws each glyph's outline -
// to extract real glyph outlines from a Type 1 font program. This is the
// format a simple font's embedded /FontFile stream contains (see
// simple.go's loadEmbeddedType1) - distinct from, and despite the
// confusingly similar name not read by, cff.go's CFF/"Type1C" support:
// a PDF /FontFile3 stream whose own /Subtype happens to be "Type1C" is
// actually a *CFF* program using *Type 2* charstrings (see cff.go's doc
// comment), not this format at all. Type 1 is the older of the two -
// PostScript's original outline font format, predating CFF - and is
// still what many older or print/publishing-origin PDF producers emit
// (classic LaTeX/dvips output being the most common example still seen
// today).
//
// # If you are new to font formats: what makes Type 1 different
//
// Like CFF's Type 2 charstrings (see cff.go's own doc comment for the
// general "outline as a tiny bytecode program" idea, which applies here
// too), a Type 1 glyph's outline is a charstring: a short sequence of
// operators like "move the pen by (dx, dy)" or "draw a curve through
// these four points" that this file's interpreter (type1Interp, below)
// runs to build a graphics.Path. Type 1's own charstring operator set is
// different from Type 2's (different byte values for some operators,
// simpler in some ways, e.g. no implicit "leading width" ambiguity to
// resolve - see hsbw's doc comment - and no local/global subroutine
// index bias to apply), but the same general shape of interpreter.
//
// Two things specific to Type 1 add real complexity beyond "different
// opcode numbers", though:
//
//  1. Encryption. A Type 1 font program is not stored as plain
//     bytecode: everything past its cleartext header (font name,
//     encoding, and similar public metadata) is run through a simple,
//     historically-motivated (not cryptographically strong - this is
//     obfuscation, not security) stream cipher called "eexec"
//     (decryptType1, decodeType1PFAHex), and each individual charstring
//     and subroutine is *separately* encrypted a second time with the
//     same cipher under a different key (decryptType1Charstring). See
//     parseType1Font and splitType1EexecSection for where this happens.
//  2. No fixed binary layout for the encrypted portion. Where CFF's
//     DICT/INDEX structures (cff.go) are precisely specified binary
//     records at known offsets, a Type 1 font's decrypted "Private"
//     dictionary and CharStrings/Subrs data are themselves written as
//     ordinary PostScript source text with embedded raw binary spans
//     (each charstring's own bytes) - this file's tokenizer
//     (type1Scanner) has to scan that text structurally, the same kind
//     of job internal/content's own PDF content-stream tokenizer does,
//     rather than just reading fixed-width fields.
//
// # Scope of what this file implements
//
//   - Charstring operators: hsbw/sbw (the width/sidebearing operators
//     that make Type 1 charstrings *not* need Type 2's "maybe there's a
//     leading width operand" ambiguity - see decodeType1Number and
//     type1Interp's own doc comment), all path-construction operators
//     (moveto/lineto/curveto in their several fixed-argument-count
//     forms), and the flex feature (a pair of very flat curves a font
//     designer wants smoothed - see opCallothersubr's doc comment for
//     why this needs real support, not just tolerance).
//   - Local subroutines (callsubr/return) - Type 1 has no global
//     subroutine INDEX or subroutine-index bias the way CFF does (see
//     cff.go's subrBias); every subroutine reference is a small,
//     unadjusted index into this one font's own Subrs array.
//   - Hint operators (hstem/vstem/vstem3/hstem3/dotsection) are parsed
//     only as far as needed to correctly clear their operands - like
//     cff.go's doStems, the hints themselves are never applied.
//
// Deliberately out of scope, matching this package's existing
// precedent (see cff.go's doc comment on its own CFF-Encoding-table and
// implicit-seac gaps) of recognizing a rarely-used feature well enough
// to fail one glyph gracefully rather than either implementing it or
// silently misinterpreting its bytes as path data:
//
//   - seac ("standard encoding accented character"): composes an
//     accented glyph (e.g. "eacute") from two other glyphs named by
//     their Adobe StandardEncoding code point, which this package has
//     no table for (see cff.go's identical rationale for its own
//     implicit-seac case). Recognized and reported as "this one glyph
//     unavailable" (GlyphOutline's ok=false), never composed.
//   - A Type 1 font program's own built-in /Encoding array (mapping
//     character code directly to glyph name, inside the *cleartext*
//     header): not read. Exactly the same scope decision cff.go's doc
//     comment documents for CFF's own built-in Encoding table, and for
//     the same reason - simple.go's non-symbolic lookup path (a
//     character code resolves to a glyph name via the PDF font
//     dictionary's own /Encoding, then that name is looked up here by
//     rune - see runeToGID) already covers the common case.
//   - PFB (the MS-DOS-oriented "segmented binary" container format,
//     0x80-tagged chunks): a PDF /FontFile stream is defined by the PDF
//     specification itself to hold the *unwrapped* clear+encrypted
//     concatenation (with /Length1/Length2/Length3 replacing PFB's own
//     segment-length header fields), so no real-world PDF should ever
//     need this - see splitType1EexecSection's doc comment for the one
//     related accommodation this file does make (a PFA-style,
//     hex-encoded encrypted portion, which real producers do
//     occasionally emit even inside a PDF).

// type1EexecKey and type1CharstringKey are the two fixed starting "R"
// values (Type 1 Font Format specification, section 7.3) the eexec
// stream cipher this file implements is seeded with: 55665 for the
// font program's own top-level eexec-encrypted section, and 4330 for
// each individual charstring or subroutine's own second, independent
// layer of encryption within that section (see decryptType1 and
// decryptType1Charstring). type1C1/type1C2 are the cipher's two fixed
// multiplier/increment constants - the same for both keys, part of the
// algorithm itself rather than something either key varies.
const (
	type1EexecKey      uint16 = 55665
	type1CharstringKey uint16 = 4330
	type1C1            uint16 = 52845
	type1C2            uint16 = 22719

	// type1DefaultLenIV is the number of leading decrypted bytes every
	// individual charstring/subroutine discards before its real
	// operator bytes begin (Type 1 Font Format specification, section
	// 7.3's "lenIV") - a small amount of scrambling padding, distinct
	// from (and always present in addition to) the top-level eexec
	// section's own fixed 4-byte discard (see splitType1EexecSection).
	// A font's own Private dictionary may override this via an explicit
	// /lenIV entry (see parseType1Private) - this is only the default
	// used when it does not.
	type1DefaultLenIV = 4
)

// decryptType1 runs Adobe's eexec stream cipher (Type 1 Font Format
// specification, Program 7.1) over cipher, seeded with the starting key
// r, returning the same number of decrypted bytes. This one small
// function implements *both* of this file's two encryption layers (the
// whole eexec section, and each individual charstring) - they differ
// only in which key seeds r and how many leading decrypted bytes the
// caller then discards (see decryptType1Charstring and
// splitType1EexecSection), not in the cipher itself.
//
// # If you are new to this: how the cipher works
//
// This is a simple additive stream cipher with a running key r that
// updates after every byte, seeded from the *ciphertext* byte just
// produced (not the plaintext byte) - which is what makes decryption
// and encryption (see EncodeType1FontProgram's encryptType1) literally
// the same procedure, just choosing which of plaintext/ciphertext is
// the input versus the output at each step. r is deliberately a Go
// uint16: its arithmetic wraps at 2^16 the same way the specification's
// own "mod 65536" requirement does, so no explicit masking is needed
// anywhere in this function.
func decryptType1(cipher []byte, r uint16) []byte {
	plain := make([]byte, len(cipher))
	for i, c := range cipher {
		plain[i] = c ^ byte(r>>8)
		r = (uint16(c)+r)*type1C1 + type1C2
	}
	return plain
}

// decryptType1Charstring decrypts one already-eexec-section-extracted
// charstring or subroutine's raw bytes (raw, still in its own
// second-layer-encrypted form - see parseType1CharStrings/
// parseType1Subrs, which is where raw comes from) using the charstring
// key, then discards its leading lenIV bytes (arbitrary padding that
// exists only to make the cipher's first few output bytes - which are
// the most predictable, since r starts from a fixed known value - never
// actually carry real charstring data - see type1DefaultLenIV's doc
// comment). Returns nil if lenIV is not a sane value for the decrypted
// length actually available (a malformed or hostile font, not a real
// one - GlyphOutline treats a nil charstring as "this glyph has no
// outline available" rather than panicking on it).
func decryptType1Charstring(raw []byte, lenIV int) []byte {
	plain := decryptType1(raw, type1CharstringKey)
	if lenIV < 0 || lenIV > len(plain) {
		return nil
	}
	return plain[lenIV:]
}

// isType1WhitespaceByte reports whether b is one of PostScript's
// whitespace characters (Type 1 Font Format specification's own
// tokenizing rules follow PostScript's) - used both by
// splitType1EexecSection's hex-armored-data detection and by
// type1Scanner's tokenizer.
func isType1WhitespaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', '\f', 0:
		return true
	default:
		return false
	}
}

// isType1HexDigit reports whether b is an ASCII hexadecimal digit
// (either case) - see looksLikeType1PFAHex.
func isType1HexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// looksLikeType1PFAHex reports whether enc - the raw bytes of a Type 1
// font program's eexec-encrypted section, before this function's caller
// decrypts anything - is written out in PFA ("Printer Font ASCII")
// style: plain hexadecimal text (optionally split across lines) rather
// than raw binary ciphertext bytes. Real Type 1 font *files* commonly
// come in either form (PFA for maximum portability through text-only
// channels, PFB for compactness), and although the PDF specification
// says an embedded /FontFile stream should always use the raw-binary
// form, some real-world producers embed a PFA-style font program
// unchanged anyway - so this package checks rather than assumes.
//
// The check itself is a widely used heuristic (the same one other
// open-source Type 1 readers use): if the first handful of non-
// whitespace bytes are all valid hex digits, treat the whole section as
// hex text. Genuine binary ciphertext is, for any real encrypted
// payload, exceedingly unlikely to happen to start with several
// consecutive ASCII-hex-digit byte values by chance (a 1-in-16 chance
// per byte, compounding).
func looksLikeType1PFAHex(enc []byte) bool {
	checked := 0
	for _, b := range enc {
		if isType1WhitespaceByte(b) {
			continue
		}
		if !isType1HexDigit(b) {
			return false
		}
		checked++
		if checked >= 4 {
			return true
		}
	}
	return false
}

// decodeType1PFAHex decodes hex-encoded text (see looksLikeType1PFAHex)
// into the raw ciphertext bytes it represents, skipping whitespace
// between hex digits (real PFA text wraps at a fixed line length) and
// stopping cleanly - not failing - at the first byte that is neither a
// hex digit nor whitespace, which is exactly what happens when this
// section's trailing all-zero-digit PFB/PFA "cleartomark" trailer (also
// valid hex, since '0' is a hex digit) eventually gives way to that
// trailer's own "cleartomark" keyword. Everything decoded before that
// point is still correct; splitType1EexecSection never needed the
// trailer's own bytes anyway.
func decodeType1PFAHex(enc []byte) []byte {
	out := make([]byte, 0, len(enc)/2)
	var hi byte
	haveHi := false
	for _, b := range enc {
		var v byte
		switch {
		case b >= '0' && b <= '9':
			v = b - '0'
		case b >= 'a' && b <= 'f':
			v = b - 'a' + 10
		case b >= 'A' && b <= 'F':
			v = b - 'A' + 10
		case isType1WhitespaceByte(b):
			continue
		default:
			return out
		}
		if !haveHi {
			hi, haveHi = v, true
			continue
		}
		out = append(out, hi<<4|v)
		haveHi = false
	}
	return out
}

// splitType1EexecSection locates data's eexec-encrypted section (using
// length1/length2 - the /FontFile stream dictionary's own /Length1 and
// /Length2 entries, see simple.go's readType1FontFileStream - when
// either looks usable, or falling back to searching for the literal
// "eexec" keyword otherwise, the same recovery this package's other
// parsers apply when a stream's own declared structure cannot be
// trusted), decodes it from PFA hex if needed, and eexec-decrypts it -
// discarding, per the specification, the first 4 bytes of the decrypted
// result (unrelated to lenIV, which only applies to the second,
// per-charstring encryption layer inside this already-decrypted data -
// see decryptType1Charstring). Returns the decrypted "private" portion
// (the Private dictionary, Subrs, and CharStrings - what
// parseType1Private goes on to tokenize) and the cleartext prefix
// (everything before the encrypted section, which findType1UnitsPerEm
// reads /FontMatrix from), or ok=false if data does not look like a
// usable Type 1 font program at all.
func splitType1EexecSection(data []byte, length1, length2 int) (private, cleartext []byte, ok bool) {
	encStart := 0
	if length1 > 0 && length1 <= len(data) {
		encStart = length1
	} else {
		idx := bytes.Index(data, []byte("eexec"))
		if idx < 0 {
			return nil, nil, false
		}
		encStart = idx + len("eexec")
	}
	cleartext = data[:encStart]

	for encStart < len(data) && isType1WhitespaceByte(data[encStart]) {
		encStart++
	}

	encEnd := len(data)
	if length2 > 0 && encStart+length2 <= len(data) {
		encEnd = encStart + length2
	}
	if encStart >= encEnd {
		return nil, nil, false
	}

	enc := data[encStart:encEnd]
	if looksLikeType1PFAHex(enc) {
		enc = decodeType1PFAHex(enc)
	}
	if len(enc) < 4 {
		return nil, nil, false
	}

	plain := decryptType1(enc, type1EexecKey)
	return plain[4:], cleartext, true
}

// type1Scanner is a minimal PostScript-source tokenizer over an already
// eexec-decrypted Type 1 Private dictionary's bytes (see
// splitType1EexecSection) - just enough to find the specific
// /Subrs.../CharStrings...end structure every real Type 1 font's
// Private dictionary follows (parseType1Private,
// parseType1Subrs, parseType1CharStrings), without needing a general
// PostScript interpreter. It does not try to understand PostScript
// syntax beyond "whitespace/comment-separated tokens, with {, }, [, ]
// each their own one-character token" - which is all that is needed to
// walk past the procedure bodies (e.g. "/RD {...} executeonly def")
// this data also contains without being confused by them, since this
// package never actually needs to *run* those procedures - see
// isType1RDToken's doc comment for how the one procedure this package
// does care about (whatever reads N raw bytes) is recognized instead.
type type1Scanner struct {
	data []byte
	pos  int
}

// skipSpace advances past whitespace and "%...end of line" PostScript
// comments, positioning pos at the start of the next real token (or at
// len(data) if none remain).
func (s *type1Scanner) skipSpace() {
	for s.pos < len(s.data) {
		b := s.data[s.pos]
		if b == '%' {
			for s.pos < len(s.data) && s.data[s.pos] != '\n' && s.data[s.pos] != '\r' {
				s.pos++
			}
			continue
		}
		if isType1WhitespaceByte(b) {
			s.pos++
			continue
		}
		return
	}
}

// next returns the next whitespace/comment-delimited token, or
// ok=false once no more data remains. "{", "}", "[", and "]" are always
// returned as their own single-character token even with no
// surrounding whitespace (matching real Type 1 font source, which
// routinely writes e.g. "{string...}" with no space after "{").
func (s *type1Scanner) next() (string, bool) {
	s.skipSpace()
	if s.pos >= len(s.data) {
		return "", false
	}
	switch s.data[s.pos] {
	case '{', '}', '[', ']':
		tok := string(s.data[s.pos])
		s.pos++
		return tok, true
	}
	start := s.pos
	for s.pos < len(s.data) {
		b := s.data[s.pos]
		if isType1WhitespaceByte(b) || b == '{' || b == '}' || b == '[' || b == ']' || b == '%' {
			break
		}
		s.pos++
	}
	return string(s.data[start:s.pos]), true
}

// nextInt reads the next token and parses it as a decimal integer,
// ok=false if there is no next token or it is not one - the common case
// throughout parseType1Subrs/parseType1CharStrings, whose structure is
// entirely "name, then a length, then a read-N-bytes token".
func (s *type1Scanner) nextInt() (int, bool) {
	tok, ok := s.next()
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readBinary consumes exactly one delimiter byte (the single mandatory
// space the Type 1 Font Format specification requires between a
// "read N bytes" token and the raw bytes themselves) and then the
// following length raw bytes verbatim - not tokenized at all, since
// charstring bytes are arbitrary binary data that would otherwise
// desynchronize this scanner's whitespace/delimiter-based next().
// ok=false if length is negative or would read past the end of data.
func (s *type1Scanner) readBinary(length int) ([]byte, bool) {
	if s.pos >= len(s.data) || length < 0 {
		return nil, false
	}
	s.pos++ // the mandatory single separating byte, conventionally a space
	if s.pos+length > len(s.data) {
		return nil, false
	}
	b := s.data[s.pos : s.pos+length]
	s.pos += length
	return b, true
}

// isType1RDToken reports whether tok is this file's recognized spelling
// for a Type 1 font's "read N raw bytes from the font program" private
// procedure - conventionally bound to the name "RD" or its common
// alias "-|" (both widely used across real-world font generation
// tools; Adobe's own tools and virtually every dvips/FontForge/etc.
// output this package has seen use one of these two). A font is free to
// bind this procedure to *any* name it likes (it is just an ordinary
// PostScript procedure definition, executed by a real PostScript
// interpreter rather than looked up by fixed name), so a font using a
// genuinely custom name is a narrow, documented gap this package
// accepts - matching this project's precedent elsewhere (see, for
// example, cff.go's charset-1/2 gap or the LZWDecode /EarlyChange 0
// gap in docs/capability-matrix.md) of choosing a simplification that
// covers real-world files rather than the full generality the format
// technically allows. Such a font's charstrings simply fail to parse
// (parseType1CharStrings/parseType1Subrs return ok=false), falling back
// to notdefGlyph for every glyph exactly like any other unusable
// embedded font program - never a corrupted or misinterpreted parse.
func isType1RDToken(tok string) bool {
	return tok == "RD" || tok == "-|"
}

// maxType1Glyphs and maxType1Subrs bound how many CharStrings/Subrs
// entries this package will ever allocate space for, protecting against
// a hostile or corrupted /Subrs or /CharStrings count driving an
// oversized allocation before any actual data has been validated - the
// same defensive bound cff.go's own INDEX parsing and truetype.go's
// composite-glyph depth limit apply for their own hostile-input cases.
// No real Type 1 font (a format whose charstrings are hand- or
// hinting-tool-authored, never holding more than a few thousand glyphs
// even for a large CJK face - which Type 1 was rarely used for anyway,
// CID-keyed CFF being the modern choice there) comes anywhere close to
// either limit.
const (
	maxType1Glyphs = 65535
	maxType1Subrs  = 65535
)

// parseType1Subrs reads a "/Subrs N array dup 0 L1 RD <L1 bytes> NP dup
// 1 L2 RD <L2 bytes> NP ..." sequence (Type 1 Font Format specification,
// section 7.3) starting right after the already-consumed "/Subrs"
// token itself - sc's next token is expected to be N. Each entry's
// index (the operand right after "dup") need not be in order and need
// not cover every slot (a real font always writes them 0..N-1 in order,
// but this package does not require that); an index outside [0,N) is
// silently ignored rather than failing the whole font, the same
// tolerance cff.go's own malformed-charset handling applies. Returns
// once a non-"dup" token is reached - rewinding sc back to just before
// that token, so the caller (parseType1Private) sees it next - which is
// how this function's own end is detected without needing an explicit
// end-of-Subrs keyword (the real format has none; Subrs is simply
// followed immediately by /CharStrings).
func parseType1Subrs(sc *type1Scanner, lenIV int) ([][]byte, bool) {
	n, ok := sc.nextInt()
	if !ok || n < 0 || n > maxType1Subrs {
		return nil, false
	}

	// "array" conventionally follows the count (e.g. "/Subrs 5 array") -
	// consumed unconditionally when present so it can never be mistaken
	// for something else by the loop below; if a font omits it for some
	// reason, sc.pos is rewound so nothing is lost.
	save := sc.pos
	if tok, ok := sc.next(); !ok || tok != "array" {
		sc.pos = save
	}

	subrs := make([][]byte, n)
	for {
		save := sc.pos
		tok, ok := sc.next()
		if !ok {
			return subrs, true
		}
		if tok != "dup" {
			sc.pos = save
			return subrs, true
		}

		idx, ok1 := sc.nextInt()
		length, ok2 := sc.nextInt()
		rd, ok3 := sc.next()
		if !ok1 || !ok2 || !ok3 || !isType1RDToken(rd) {
			return nil, false
		}
		raw, ok := sc.readBinary(length)
		if !ok {
			return nil, false
		}
		if idx >= 0 && idx < len(subrs) {
			subrs[idx] = decryptType1Charstring(raw, lenIV)
		}
	}
}

// parseType1CharStrings reads a "/CharStrings N dict dup begin
// /name1 L1 RD <L1 bytes> ND /name2 L2 RD <L2 bytes> ND ... end"
// sequence (Type 1 Font Format specification, section 7.3) starting
// right after the already-consumed "/CharStrings" token itself.
// Everything between the count and the first "/name" entry ("dict",
// "dup", "begin", or a font's own variation on this boilerplate) is
// simply skipped - this function only acts on a token that either
// starts a new glyph entry (a literal name, i.e. one starting with
// '/') or ends the whole dictionary ("end"), tolerating whatever
// wrapper tokens a real font puts around those.
func parseType1CharStrings(sc *type1Scanner, lenIV int) ([]string, [][]byte, bool) {
	sc.nextInt() // the declared glyph count - informational only, never checked against what is actually found

	var names []string
	var charstrings [][]byte
	for {
		tok, ok := sc.next()
		if !ok {
			return nil, nil, false // truncated before "end" was ever reached
		}
		if tok == "end" {
			return names, charstrings, true
		}
		if len(tok) == 0 || tok[0] != '/' {
			continue
		}

		length, ok1 := sc.nextInt()
		rd, ok2 := sc.next()
		if !ok1 || !ok2 || !isType1RDToken(rd) {
			return nil, nil, false
		}
		raw, ok := sc.readBinary(length)
		if !ok {
			return nil, nil, false
		}
		if len(names) >= maxType1Glyphs {
			return nil, nil, false
		}
		names = append(names, tok[1:])
		charstrings = append(charstrings, decryptType1Charstring(raw, lenIV))
	}
}

// parseType1Private tokenizes the already eexec-decrypted "private"
// portion of a Type 1 font program (splitType1EexecSection's first
// return value) looking for exactly three things this package needs -
// /lenIV (defaulting to type1DefaultLenIV if never set, per the
// specification), /Subrs, and /CharStrings - and ignoring every other
// token in between (the surrounding Private dictionary's own other
// entries: /BlueValues, /StdHW, /RD's and /ND's own procedure
// *definitions*, and similar hinting/plumbing data this package has no
// use for - see this file's doc comment on scope). Returns ok=false
// only if /CharStrings was never found or was itself malformed; a
// missing /Subrs is not an error (a font with no local subroutines at
// all is entirely valid - subrs stays nil, and any charstring that
// tries to call one simply fails that one glyph - see callSubr).
func parseType1Private(private []byte) (names []string, charstrings, subrs [][]byte, ok bool) {
	sc := &type1Scanner{data: private}
	lenIV := type1DefaultLenIV
	haveCharStrings := false

	for {
		tok, ok := sc.next()
		if !ok {
			break
		}
		switch tok {
		case "/lenIV":
			if v, ok := sc.nextInt(); ok {
				lenIV = v
			}
		case "/Subrs":
			s, ok := parseType1Subrs(sc, lenIV)
			if !ok {
				return nil, nil, nil, false
			}
			subrs = s
		case "/CharStrings":
			n, c, ok := parseType1CharStrings(sc, lenIV)
			if !ok {
				return nil, nil, nil, false
			}
			names, charstrings = n, c
			haveCharStrings = true
		}
	}

	if !haveCharStrings || len(charstrings) == 0 {
		return nil, nil, nil, false
	}
	return names, charstrings, subrs, true
}

// findType1UnitsPerEm scans cleartext (the portion of a Type 1 font
// program before its eexec-encrypted section - splitType1EexecSection's
// second return value) for a "/FontMatrix [a b c d e f]" entry, deriving
// this font's own units-per-em the same way parseCFFFont does from
// CFF's own FontMatrix operand (cff.go) - only the horizontal scale
// term a is used, matching that function's identical documented
// simplification for a non-uniform/skewed matrix (vanishingly rare in
// practice). Returns 1000 (the specification's own default, for
// FontMatrix [0.001 0 0 0.001 0 0]) if /FontMatrix is absent,
// malformed, or scales to something outside a uint16's usable range.
func findType1UnitsPerEm(cleartext []byte) uint16 {
	idx := bytes.Index(cleartext, []byte("/FontMatrix"))
	if idx < 0 {
		return 1000
	}
	sc := &type1Scanner{data: cleartext, pos: idx + len("/FontMatrix")}
	if tok, ok := sc.next(); !ok || tok != "[" {
		return 1000
	}
	tok, ok := sc.next()
	if !ok {
		return 1000
	}
	a, err := strconv.ParseFloat(tok, 64)
	if err != nil || a <= 0 {
		return 1000
	}
	per := 1 / a
	if per <= 0 || per >= (1<<16) {
		return 1000
	}
	return uint16(per + 0.5)
}

// type1Font holds the parsed subset of a Type 1 font program this
// package uses to extract glyph outlines - the Type 1 counterpart to
// cff.go's cffFont and truetype.go's sfntFont, implementing the same
// glyphOutlineSource/runeGlyphSource interfaces (font.go) so simple.go
// can use whichever of the three a font dictionary's embedded program
// turns out to be through the same code path.
//
// Unlike CFF (which has a real GID space via its CharStrings INDEX) or
// TrueType (glyph index order defined by the font file itself), a Type
// 1 font's CharStrings dictionary has no inherent numeric ordering at
// all - PostScript dictionaries are unordered. This package simply
// assigns each glyph a "GID" equal to its position in parse order
// (glyphNames[gid]/charstrings[gid]), which is stable for any one
// parsed type1Font but has no meaning beyond it (never compared across
// two different type1Font values, and never exposed outside this
// package - GIDForRune is the only way simple.go ever looks a glyph up,
// exactly as for a CFF-backed font - see simpleRuneGlyphLookup).
type type1Font struct {
	glyphNames  []string
	charstrings [][]byte // decrypted, lenIV-stripped Type 1 charstring bytes - parallel to glyphNames
	subrs       [][]byte // decrypted, lenIV-stripped local subroutines, indexed by their own /Subrs slot number
	unitsPerEm  uint16
	runeToGID   map[rune]uint16
}

// parseType1Font parses data - an already filter-decoded /FontFile
// stream's complete bytes - into a type1Font, using length1/length2
// (that stream's own /Length1/Length2 dictionary entries, or 0 if
// either is unavailable/untrustworthy - see
// simple.go's readType1FontFileStream) to locate the eexec-encrypted
// section. Like parseCFFFont/parseSfnt, this deliberately never returns
// an error: an unusable embedded font program is this package's
// documented "no outlines available" case, handled by falling back to
// notdefGlyph, not a fatal one.
func parseType1Font(data []byte, length1, length2 int) (type1Font, bool) {
	private, cleartext, ok := splitType1EexecSection(data, length1, length2)
	if !ok {
		return type1Font{}, false
	}
	names, charstrings, subrs, ok := parseType1Private(private)
	if !ok {
		return type1Font{}, false
	}

	font := type1Font{
		glyphNames:  names,
		charstrings: charstrings,
		subrs:       subrs,
		unitsPerEm:  findType1UnitsPerEm(cleartext),
		runeToGID:   make(map[rune]uint16, len(names)),
	}
	for gid, name := range names {
		if r, ok := glyphNameToRune(name); ok {
			if _, exists := font.runeToGID[r]; !exists {
				font.runeToGID[r] = uint16(gid)
			}
		}
	}
	return font, true
}

// UnitsPerEm implements glyphOutlineSource - see this type's unitsPerEm
// field doc comment.
func (f *type1Font) UnitsPerEm() uint16 {
	return f.unitsPerEm
}

// GIDForRune implements runeGlyphSource by way of this font's own
// glyph-name-derived rune table - see this type's doc comment and
// glyphNameToRune (encoding.go, shared with cff.go's identically-shaped
// GIDForRune).
func (f *type1Font) GIDForRune(r rune) (uint16, bool) {
	gid, ok := f.runeToGID[r]
	return gid, ok
}

// GlyphOutline implements glyphOutlineSource: it interprets glyph
// index gid's charstring (see type1Interp) and returns the resulting
// path in this font's own native design-units coordinate space (not
// yet scaled by UnitsPerEm - see font.go's scaleGlyph, which does
// that once the caller also knows what to scale to). ok=false if gid
// is out of range, or interpreting its charstring failed - including
// the deliberate seac case (see this file's doc comment on scope).
func (f *type1Font) GlyphOutline(gid uint16) (*graphics.Path, bool) {
	if int(gid) >= len(f.charstrings) || f.charstrings[gid] == nil {
		return nil, false
	}
	interp := newType1Interp(f.subrs)
	done, ok := interp.exec(f.charstrings[gid])
	if !ok {
		return nil, false
	}
	if !done {
		// A charstring that ran out of bytes without ever reaching
		// endchar (malformed, but not hostile enough to have failed any
		// individual operator along the way) - close whatever subpath
		// is open, the same safety net cff.go's GlyphOutline applies for
		// its own charstring interpreter.
		interp.path.Close()
	}
	return interp.path, true
}

// t1EscapeOperator is added to a two-byte (12-prefixed) Type 1
// charstring operator's second byte, the exact counterpart to cff.go's
// cffEscapeOperator for the same reason (letting a switch statement key
// one-byte and two-byte operators without collision) - see that
// constant's own doc comment for the full rationale. Type 1's one-byte
// operators only ever range 0-14 and 21-31 (see type1Interp.exec), well
// below 1000, so the same threshold cff.go uses works here too.
const t1EscapeOperator = 1000

// maxType1Stack, maxType1CallDepth, and maxType1Steps bound a Type 1
// Charstring interpreter's resource usage against a hostile or
// corrupted program - the same purpose cff.go's identically-shaped
// maxCharstringStack/maxCharstringCallDepth/maxCharstringSteps serve for
// Type 2 charstrings (see that file's doc comment on why: these are
// generous bounds no conforming charstring ever approaches, so anything
// that does is corrupt or hostile and safely rejected). The Type 1 Font
// Format specification does not name an exact stack-depth limit the way
// Type 2's own specification does, so these are this package's own
// conservative choices rather than values copied from the format
// itself - generous enough that no real font's charstrings (which,
// being simpler than Type 2's, tend to use noticeably less stack depth
// in practice) would ever be affected.
const (
	maxType1Stack     = 32
	maxType1CallDepth = 10
	maxType1Steps     = 1 << 16
)

// type1Interp is a Type 1 Charstring virtual machine: it holds
// everything exec (below) needs to run one glyph's charstring program
// (and, recursively, whatever local subroutines it calls into) and
// accumulate the resulting outline into path - the Type 1 counterpart
// to cff.go's charstringInterp. A fresh type1Interp is created per
// glyph (see type1Font.GlyphOutline) and carries no state that should
// ever persist across two different glyphs.
//
// Unlike Type 2 charstrings (cff.go), Type 1 has no "maybe the first
// stack-clearing operator's bottom operand is actually a glyph width"
// ambiguity to resolve (see cff.go's takeWidth): a Type 1 charstring
// always states its own width and left sidebearing explicitly, via a
// dedicated hsbw or sbw operator that (per the specification) must be
// the very first operator in the charstring - see opHsbw/opSbw. This
// package has no use for the width value itself, for the same reason
// cff.go's takeWidth does not either (a glyph's real advance width
// always comes from the PDF font dictionary's own /Widths - see
// simple.go), but it does use the left (and, for sbw, bottom)
// sidebearing to correctly initialize the interpreter's starting pen
// position, since every subsequent moveto/lineto/curveto operand is a
// delta relative to wherever the pen currently is.
type type1Interp struct {
	subrs [][]byte

	// stack is the charstring's own operand stack (numbers pushed by
	// decodeType1Number, consumed by whichever operator follows - see
	// cff.go's charstringInterp.stack doc comment for the same "reverse
	// Polish notation" mental model, which applies identically here).
	stack []float64

	// psStack is the small, separate "PostScript operand stack" the
	// callothersubr/pop operator pair uses to pass values back into the
	// charstring's own operand stack - see opCallothersubr's doc
	// comment. Entirely distinct from stack: a real Type 1 interpreter
	// is embedded inside a full PostScript interpreter, and
	// callothersubr's "OtherSubrs" procedures run in *that* outer
	// interpreter's own execution context, with its own separate
	// operand stack - this field is this package's minimal stand-in for
	// that outer stack, existing only to support the small, fixed set
	// of OtherSubrs behaviors (flex and hint replacement) real fonts
	// actually use.
	psStack []float64

	x, y float64
	path *graphics.Path

	// flexing and flexPts implement the "flex" feature - see
	// opCallothersubr's doc comment.
	flexing bool
	flexPts []graphics.Point

	depth int
	steps int
}

// newType1Interp creates a fresh interpreter for one glyph, ready to
// run its top-level charstring via exec.
func newType1Interp(subrs [][]byte) *type1Interp {
	return &type1Interp{
		subrs: subrs,
		path:  &graphics.Path{},
	}
}

// decodeType1Number decodes one Type 1 Charstring operand starting at
// instr[pos] (Type 1 Font Format specification, section 6.2's "number
// encoding" table). The caller (exec) is expected to have already
// checked that instr[pos] is a valid operand lead byte (32 or above -
// every value 0-31 is instead an operator, or the 12-prefix escape
// introducing a two-byte one - see exec). This is almost the same
// encoding as a Type 2 Charstring operand's (compare cff.go's
// decodeCharstringNumber), except Type 1 has no lead-byte-28 short
// form at all, and gives lead byte 255 a different meaning: a plain
// signed 32-bit integer (not Type 2's 16.16 fixed-point number) - Type
// 1 charstring coordinates are always whole numbers.
func decodeType1Number(instr []byte, pos int) (float64, int, bool) {
	b0 := instr[pos]
	switch {
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
	case b0 == 255:
		if pos+5 > len(instr) {
			return 0, 0, false
		}
		return float64(int32(binary.BigEndian.Uint32(instr[pos+1 : pos+5]))), pos + 5, true
	default:
		return 0, 0, false
	}
}

// exec interprets instr - either a glyph's own top-level charstring, or,
// recursively, a local subroutine's body (see callSubr) - appending
// whatever outline it draws to c.path. It returns done=true once
// endchar has been reached (signalling every enclosing exec call, all
// the way back up to GlyphOutline, to stop immediately), and ok=false
// for any charstring this interpreter cannot make sense of: malformed
// operand encoding, an out-of-range subroutine index, exceeding one of
// this file's safety bounds, an operator receiving fewer operands than
// it requires, or a seac operator (see this file's doc comment on
// scope). Like cff.go's identically-shaped exec, this fails closed
// rather than panicking on hostile or corrupted input.
//
// A plain `return` operator (11) is handled simply by this function
// returning normally (done=false, ok=true), letting the recursive
// callSubr invocation this exec call is nested inside resume its own
// loop right where it left off - exactly Go's own call-stack behavior,
// needing no explicit "resume position" bookkeeping (the same design
// cff.go's exec doc comment explains for its own `return` handling).
func (c *type1Interp) exec(instr []byte) (done, ok bool) {
	pos := 0
	for pos < len(instr) {
		c.steps++
		if c.steps > maxType1Steps {
			return false, false
		}

		b0 := instr[pos]
		if b0 >= 32 {
			v, newPos, ok := decodeType1Number(instr, pos)
			if !ok {
				return false, false
			}
			if len(c.stack) >= maxType1Stack {
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
			op = t1EscapeOperator + int(instr[pos])
			pos++
		}

		switch op {
		case 1, 3: // hstem, vstem - hint declarations; never applied, see this file's doc comment
			c.stack = c.stack[:0]
		case 4: // vmoveto
			if !c.opVmoveto() {
				return false, false
			}
		case 21: // rmoveto
			if !c.opRmoveto() {
				return false, false
			}
		case 22: // hmoveto
			if !c.opHmoveto() {
				return false, false
			}
		case 5: // rlineto
			if !c.opRlineto() {
				return false, false
			}
		case 6: // hlineto
			if !c.opHlineto() {
				return false, false
			}
		case 7: // vlineto
			if !c.opVlineto() {
				return false, false
			}
		case 8: // rrcurveto
			if !c.opRrcurveto() {
				return false, false
			}
		case 30: // vhcurveto
			if !c.opVhcurveto() {
				return false, false
			}
		case 31: // hvcurveto
			if !c.opHvcurveto() {
				return false, false
			}
		case 9: // closepath
			c.path.Close()
			c.stack = c.stack[:0]
		case 13: // hsbw
			if !c.opHsbw() {
				return false, false
			}
		case 10: // callsubr
			d, ok := c.callSubr()
			if !ok {
				return false, false
			}
			if d {
				return true, true
			}
		case 11: // return
			return false, true
		case 14: // endchar
			c.path.Close()
			return true, true
		case t1EscapeOperator + 7: // sbw
			if !c.opSbw() {
				return false, false
			}
		case t1EscapeOperator + 6: // seac - recognized, deliberately not composed, see this file's doc comment
			return true, false
		case t1EscapeOperator + 12: // div
			if !c.opDiv() {
				return false, false
			}
		case t1EscapeOperator + 16: // callothersubr
			if !c.opCallothersubr() {
				return false, false
			}
		case t1EscapeOperator + 17: // pop
			if !c.opPop() {
				return false, false
			}
		case t1EscapeOperator + 33: // setcurrentpoint
			if !c.opSetcurrentpoint() {
				return false, false
			}
		case t1EscapeOperator + 0, t1EscapeOperator + 1, t1EscapeOperator + 2: // dotsection, vstem3, hstem3 - hints only
			c.stack = c.stack[:0]
		default:
			// A Reserved opcode, or an escape sub-operator outside the
			// Type 1 specification's defined set: cleared and ignored
			// rather than failing the whole glyph - the same tolerance
			// cff.go's exec applies to its own unrecognized operators.
			c.stack = c.stack[:0]
		}
	}
	return false, true
}

// callSubr implements callsubr: pops a subroutine index off the top of
// the operand stack and recursively interprets that Subrs entry's own
// bytes. Unlike cff.go's callSubr, there is no index bias to apply
// (Type 1's Subrs indices are used exactly as written) and no separate
// global-versus-local subroutine distinction (Type 1 has only one kind
// of subroutine, always local to this one font).
func (c *type1Interp) callSubr() (done, ok bool) {
	if len(c.stack) == 0 {
		return false, false
	}
	idx := int(c.stack[len(c.stack)-1])
	c.stack = c.stack[:len(c.stack)-1]
	if idx < 0 || idx >= len(c.subrs) || c.subrs[idx] == nil {
		return false, false
	}

	c.depth++
	if c.depth > maxType1CallDepth {
		return false, false
	}
	done, ok = c.exec(c.subrs[idx])
	c.depth--
	return done, ok
}

// moveBy advances the pen by (dx, dy) and either starts a new contour
// there (the ordinary case: closes whatever contour was previously open
// first, exactly like cff.go's moveTo - see that method's doc comment
// for why graphics.Path.Close's documented no-op-when-nothing-is-open
// behavior means this never needs its own "is a contour already open"
// bookkeeping) or, while a flex sequence is in progress, simply records
// the point instead of touching the path at all - see opCallothersubr's
// doc comment.
func (c *type1Interp) moveBy(dx, dy float64) {
	c.x += dx
	c.y += dy
	if c.flexing {
		c.flexPts = append(c.flexPts, graphics.Point{X: c.x, Y: c.y})
		return
	}
	c.path.Close()
	c.path.MoveTo(graphics.Point{X: c.x, Y: c.y})
}

func (c *type1Interp) opRmoveto() bool {
	if len(c.stack) < 2 {
		return false
	}
	n := len(c.stack)
	dx, dy := c.stack[n-2], c.stack[n-1]
	c.stack = c.stack[:0]
	c.moveBy(dx, dy)
	return true
}

func (c *type1Interp) opHmoveto() bool {
	if len(c.stack) < 1 {
		return false
	}
	dx := c.stack[len(c.stack)-1]
	c.stack = c.stack[:0]
	c.moveBy(dx, 0)
	return true
}

func (c *type1Interp) opVmoveto() bool {
	if len(c.stack) < 1 {
		return false
	}
	dy := c.stack[len(c.stack)-1]
	c.stack = c.stack[:0]
	c.moveBy(0, dy)
	return true
}

// lineBy appends a straight segment from the current point to
// (c.x+dx, c.y+dy).
func (c *type1Interp) lineBy(dx, dy float64) {
	c.x += dx
	c.y += dy
	c.path.LineTo(graphics.Point{X: c.x, Y: c.y})
}

func (c *type1Interp) opRlineto() bool {
	if len(c.stack) < 2 {
		return false
	}
	n := len(c.stack)
	dx, dy := c.stack[n-2], c.stack[n-1]
	c.stack = c.stack[:0]
	c.lineBy(dx, dy)
	return true
}

func (c *type1Interp) opHlineto() bool {
	if len(c.stack) < 1 {
		return false
	}
	dx := c.stack[len(c.stack)-1]
	c.stack = c.stack[:0]
	c.lineBy(dx, 0)
	return true
}

func (c *type1Interp) opVlineto() bool {
	if len(c.stack) < 1 {
		return false
	}
	dy := c.stack[len(c.stack)-1]
	c.stack = c.stack[:0]
	c.lineBy(0, dy)
	return true
}

// curveBy appends a cubic Bezier curve from the current point, through
// two control points, to an end point - all three given as (dx, dy)
// deltas chained from the current point in turn, exactly like cff.go's
// curveTo (see that method's doc comment for the full explanation of
// why each operand is relative to the *previous* point rather than the
// curve's own start).
func (c *type1Interp) curveBy(dx1, dy1, dx2, dy2, dx3, dy3 float64) {
	c1 := graphics.Point{X: c.x + dx1, Y: c.y + dy1}
	c2 := graphics.Point{X: c1.X + dx2, Y: c1.Y + dy2}
	end := graphics.Point{X: c2.X + dx3, Y: c2.Y + dy3}
	c.path.CurveTo(c1, c2, end)
	c.x, c.y = end.X, end.Y
}

func (c *type1Interp) opRrcurveto() bool {
	if len(c.stack) < 6 {
		return false
	}
	n := len(c.stack)
	dx1, dy1, dx2, dy2, dx3, dy3 := c.stack[n-6], c.stack[n-5], c.stack[n-4], c.stack[n-3], c.stack[n-2], c.stack[n-1]
	c.stack = c.stack[:0]
	c.curveBy(dx1, dy1, dx2, dy2, dx3, dy3)
	return true
}

// opVhcurveto implements vhcurveto: "dy1 dx2 dy2 dx3 vhcurveto" draws a
// curve that starts with a vertical tangent (its first control point is
// straight up/down from the current point, i.e. dx1=0) and ends with a
// horizontal tangent (dy3=0).
func (c *type1Interp) opVhcurveto() bool {
	if len(c.stack) < 4 {
		return false
	}
	n := len(c.stack)
	dy1, dx2, dy2, dx3 := c.stack[n-4], c.stack[n-3], c.stack[n-2], c.stack[n-1]
	c.stack = c.stack[:0]
	c.curveBy(0, dy1, dx2, dy2, dx3, 0)
	return true
}

// opHvcurveto implements hvcurveto: "dx1 dx2 dy2 dy3 hvcurveto" draws a
// curve that starts with a horizontal tangent (dy1=0) and ends with a
// vertical tangent (dx3=0) - the mirror image of opVhcurveto.
func (c *type1Interp) opHvcurveto() bool {
	if len(c.stack) < 4 {
		return false
	}
	n := len(c.stack)
	dx1, dx2, dy2, dy3 := c.stack[n-4], c.stack[n-3], c.stack[n-2], c.stack[n-1]
	c.stack = c.stack[:0]
	c.curveBy(dx1, 0, dx2, dy2, 0, dy3)
	return true
}

// opHsbw implements hsbw: "sbx wx hsbw" must be the first operator in a
// well-formed Type 1 charstring (see this type's own doc comment). wx
// (the glyph's advance width) is discarded - this package always gets
// width from the PDF font dictionary's own /Widths instead, the same
// choice cff.go's takeWidth documents for Type 2 charstrings. sbx (the
// left sidebearing) becomes the interpreter's starting x position, with
// y starting at 0 - every later moveto/lineto/curveto operand is a
// delta from here.
func (c *type1Interp) opHsbw() bool {
	if len(c.stack) < 2 {
		return false
	}
	n := len(c.stack)
	sbx := c.stack[n-2]
	c.stack = c.stack[:0]
	c.x, c.y = sbx, 0
	return true
}

// opSbw implements sbw: "sbx sby wx wy sbw", the two-dimensional
// counterpart to opHsbw (used by a font whose glyphs do not all sit on
// the same baseline sidebearing convention hsbw assumes, e.g. some
// vertical-writing or unusual glyphs) - sets the starting pen position
// to (sbx, sby) instead of (sbx, 0).
func (c *type1Interp) opSbw() bool {
	if len(c.stack) < 4 {
		return false
	}
	n := len(c.stack)
	sbx, sby := c.stack[n-4], c.stack[n-3]
	c.stack = c.stack[:0]
	c.x, c.y = sbx, sby
	return true
}

// opDiv implements div: pops two operands a, b and pushes a/b - a plain
// arithmetic escape operator real charstrings occasionally use to
// express a coordinate as a ratio (e.g. computing a stem-centered
// position) rather than a literal encoded number. ok=false on division
// by zero, which no well-formed charstring would ever actually request.
func (c *type1Interp) opDiv() bool {
	if len(c.stack) < 2 {
		return false
	}
	n := len(c.stack)
	a, b := c.stack[n-2], c.stack[n-1]
	if b == 0 {
		return false
	}
	c.stack = append(c.stack[:n-2], a/b)
	return true
}

// opSetcurrentpoint implements setcurrentpoint: "x y setcurrentpoint"
// sets the pen position directly to an absolute point, rather than by a
// relative delta the way every other moveto/lineto/curveto operator
// does. In practice this only ever appears as the last step of the
// flex idiom (see opCallothersubr), redundantly re-asserting a position
// this interpreter already tracks correctly on its own - implemented
// here mainly so encountering it does not fall through to exec's
// generic "ignore, clear the stack" case and silently discard the
// x/y operands as if they meant something else.
func (c *type1Interp) opSetcurrentpoint() bool {
	if len(c.stack) < 2 {
		return false
	}
	n := len(c.stack)
	c.x, c.y = c.stack[n-2], c.stack[n-1]
	c.stack = c.stack[:0]
	return true
}

// opCallothersubr implements callothersubr: "arg1 ... argN N
// othersubr# callothersubr" invokes one of a small set of
// interpreter-level behaviors ("OtherSubrs") a real Type 1 interpreter
// implements outside the charstring bytecode itself - conceptually
// similar to a PDF content stream's marked-content operators in that
// they are recognized by number rather than by any charstring-visible
// definition. Only OtherSubrs 0-3 (the ones the Type 1 Font Format
// specification, section 8.3, itself documents as the standard
// convention every font-creation tool actually emits) are given real
// behavior:
//
//   - 1 (0 args): begins a "flex" sequence - two very flat, S-shaped
//     curves a font's hinting mechanism may want to replace with a
//     single straight line at small sizes, expressed in the charstring
//     as seven consecutive rmoveto calls (one reference point, plus two
//     curves' worth of three points each) instead of ordinary
//     curveto operators. While flexing is true, moveBy (above) records
//     each rmoveto's resulting absolute point into flexPts instead of
//     touching the path - this package never applies the "replace with
//     a line" hinting optimization itself (matching this package's
//     general no-hinting scope - see cff.go's own doc comment on the
//     same policy for Type 2), so it always draws the two real curves.
//   - 2 (0 args): marks "one flex point was just recorded" - a pure
//     no-op here, since moveBy already recorded the point when the
//     rmoveto immediately before this call ran; this case exists only
//     so callothersubr does not fall into the "unrecognized" default
//     case for it.
//   - 0 (3 args: flex height, final x, final y): ends the flex
//     sequence. flexPts should now hold exactly 7 points - index 0 is
//     the reference point (used by a real hinting engine to judge
//     whether the flex height justifies two curves versus a straight
//     line; this package always draws the curves, so it is otherwise
//     unused), indices 1-3 are the first curve's two control points and
//     endpoint, and indices 4-6 are the second curve's. A count other
//     than 7 means this charstring's flex sequence was truncated or
//     malformed, and this operator fails the whole glyph rather than
//     draw a curve from an incomplete or wrong set of points. The
//     final x/y arguments are pushed onto psStack for the "pop pop
//     setcurrentpoint" sequence real fonts always write immediately
//     after this call to consume (see opPop/opSetcurrentpoint) -
//     otherwise redundant with what this interpreter already computed,
//     but consuming it keeps the charstring's own operand-stack
//     bookkeeping balanced exactly as a real PostScript interpreter's
//     would be.
//   - 3 (1 arg: a subroutine index): hint replacement - lets a font
//     swap in a size-specific alternate set of hint operators mid-glyph
//     via a *different* subroutine than the one currently executing.
//     Since this package never applies hints at all, the specific
//     subroutine named here would only ever contain more hint
//     declarations it would also ignore - but the charstring still
//     expects to retrieve this argument via "pop" and then call it via
//     callsubr immediately afterward (the standard
//     "<idx> 1 3 callothersubr pop callsubr" idiom), so it is pushed
//     onto psStack exactly like case 0's result, letting that
//     subsequent callsubr still run (and safely find nothing but hints
//     to ignore inside it).
//
// Any other OtherSubr index (essentially never seen in real fonts -
// these four are the entire standard convention) pushes its arguments
// back onto psStack unchanged, so a following pop still retrieves
// *something* well-defined instead of starving the operand stack -
// this package's usual "tolerate rather than abort" policy for a
// genuinely unrecognized case, matching cff.go's exec's identical
// choice for an unrecognized escape operator.
func (c *type1Interp) opCallothersubr() bool {
	if len(c.stack) < 2 {
		return false
	}
	n := len(c.stack)
	othersubr := int(c.stack[n-1])
	nArgs := int(c.stack[n-2])
	c.stack = c.stack[:n-2]
	if nArgs < 0 || nArgs > len(c.stack) {
		return false
	}
	args := append([]float64(nil), c.stack[len(c.stack)-nArgs:]...)
	c.stack = c.stack[:len(c.stack)-nArgs]

	switch othersubr {
	case 1:
		c.flexing = true
		c.flexPts = c.flexPts[:0]
	case 2:
		// no-op - see this method's doc comment
	case 0:
		c.flexing = false
		if len(c.flexPts) != 7 {
			return false
		}
		p := c.flexPts
		c.path.CurveTo(p[1], p[2], p[3])
		c.path.CurveTo(p[4], p[5], p[6])
		c.x, c.y = p[6].X, p[6].Y
		if len(args) >= 3 {
			if len(c.psStack)+2 > maxType1Stack {
				return false
			}
			c.psStack = append(c.psStack, args[len(args)-2], args[len(args)-1])
		}
	case 3:
		if len(args) == 1 {
			if len(c.psStack)+1 > maxType1Stack {
				return false
			}
			c.psStack = append(c.psStack, args[0])
		}
	default:
		if len(c.psStack)+len(args) > maxType1Stack {
			return false
		}
		c.psStack = append(c.psStack, args...)
	}
	return true
}

// opPop implements pop: moves one value from the PostScript stack
// (psStack - see opCallothersubr) onto the charstring's own operand
// stack. A pop with nothing on psStack (malformed input, or an
// OtherSubr this interpreter recognizes but that genuinely produces no
// result) pushes 0 instead of failing outright, so whatever operator
// consumes it next - almost always callsubr, for hint replacement -
// still receives a numeric operand rather than an arity error; this
// mirrors this package's general "tolerate rather than abort" policy
// for a case a real font is never expected to actually hit.
func (c *type1Interp) opPop() bool {
	if len(c.stack) >= maxType1Stack {
		return false
	}
	if len(c.psStack) == 0 {
		c.stack = append(c.stack, 0)
		return true
	}
	v := c.psStack[len(c.psStack)-1]
	c.psStack = c.psStack[:len(c.psStack)-1]
	c.stack = append(c.stack, v)
	return true
}
