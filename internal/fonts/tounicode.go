package fonts

import (
	"bytes"
	"unicode/utf16"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Phase 10's text-extraction support: parsing a PDF
// font's /ToUnicode CMap (ISO 32000-1 9.10.3) into a ToUnicodeMap that
// answers "what Unicode text does this character code mean", the
// question Font.TextForCode (font.go) exists to answer and neither
// Font.Glyph nor Font.Width has any use for.
//
// # How a /ToUnicode CMap differs from cidcmap.go's CMap
//
// Both are the same small PostScript-like CMap grammar (see cidcmap.go's
// own package doc comment for what that grammar looks like in general),
// parsed with the same syntax.Lexer - but they answer different
// questions and are keyed differently:
//
//   - cidcmap.go's CMap (used for a Type0 font's /Encoding) maps a raw
//     character code to a CID (an integer, "begincidrange"/
//     "begincidchar") - a number meaningful only as an index into a
//     specific font program's own glyph data.
//   - This file's ToUnicodeMap maps a raw character code to actual
//     Unicode text ("beginbfrange"/"beginbfchar", "bf" standing for
//     "base font" in Adobe's own naming) - a hex string interpreted as
//     UTF-16BE, PDF's own convention for a /ToUnicode CMap's destination
//     values (9.10.3) - and, unlike a CID, that text can be more than
//     one character long (a ligature glyph such as "ffi" is commonly
//     given a single /ToUnicode entry mapping it to the three-character
//     string "ffi", since it is one glyph but three real characters).
//
// Critically, a ToUnicodeMap is looked up by the font's raw character
// code - the very same value cidcmap.go's CMap.CIDForCode also takes -
// never by a CID. A Type0 font can have both an /Encoding CMap (code ->
// CID, used to find a glyph to paint) and a /ToUnicode CMap (code ->
// text, used to know what that glyph means) at once, and the two serve
// entirely different purposes from the very same input value - see
// Font.TextForCode's own doc comment for why it must not call cidFor
// first the way Width and Glyph do.
//
// This file deliberately does not implement /ToUnicode's own
// "usecmap" operator (rare in practice for /ToUnicode CMaps
// specifically - real files essentially always spell out their own
// bfchar/bfrange entries directly rather than building on a predefined
// UCS2 resource - unlike cidcmap.go's CMap, where usecmap chaining onto
// a predefined CJK encoding is common enough that Phase 9 built real
// support for it). A /ToUnicode CMap that does use it simply has that
// one operator ignored, exactly like any other keyword this parser does
// not recognize - see parseToUnicodeCMap's own doc comment.

// bfRange is one declared code-to-text range from a /ToUnicode CMap's
// beginbfrange block (ISO 32000-1 9.10.3): every code in [lo,hi] maps to
// some Unicode text, in one of the two shapes the specification allows -
// see textFor below for how each shape is actually evaluated. Exactly
// one of base/array is ever set for a given bfRange (parseBFRanges below
// is what enforces that), never both.
type bfRange struct {
	lo, hi uint32

	// base is set for the common "increment a single destination value"
	// shape: "<lo> <hi> <dstHex>" means code lo maps to dstHex, code
	// lo+1 maps to dstHex with its numeric value incremented by 1, and
	// so on up to hi - see textFor's arithmetic. This is by far the most
	// common shape in real /ToUnicode CMaps, since it is how a tool
	// spells out "this whole contiguous block of codes are the
	// corresponding contiguous block of Unicode characters" in one
	// entry instead of one per code.
	base []byte

	// array is set for the less common "explicit destination per code"
	// shape: "<lo> <hi> [<d0> <d1> ... <dn>]" means code lo+i maps to
	// array[i] directly, with no arithmetic at all - used when the
	// destination text for consecutive codes is not itself a simple
	// numeric run (for example, consecutive glyph codes that mean
	// unrelated characters, or one of them a multi-character ligature).
	array []string
}

// textFor evaluates this range for one code already known (by
// ToUnicodeMap.TextForCode) to satisfy r.lo <= code <= r.hi, returning
// the Unicode text that code maps to.
func (r bfRange) textFor(code uint32) (string, bool) {
	offset := code - r.lo
	if r.array != nil {
		if int(offset) >= len(r.array) {
			// Can only happen if a caller passes a code this range does
			// not actually claim to cover (ToUnicodeMap.TextForCode
			// never does) - defensive, not reachable in practice.
			return "", false
		}
		return r.array[offset], true
	}
	// The increment shape: reinterpret r.base as a big-endian integer
	// (beValue, shared with cidcmap.go - both files bound a code/dst hex
	// string to at most maxCMapCodeBytes bytes, so this is always safe),
	// add offset, and re-encode into the same byte width before decoding
	// as UTF-16BE - so "<0041> <0043> <0041>" produces "A", "B", "C" for
	// codes 0x41, 0x42, 0x43 respectively, exactly the way real-world
	// /ToUnicode CMaps use this shape for a contiguous run of ordinary
	// characters.
	v := beValue(r.base) + offset
	return utf16BEBytesToString(encodeBEBytes(v, len(r.base))), true
}

// ToUnicodeMap is a parsed /ToUnicode CMap: enough information to answer
// "what Unicode text does this raw character code mean" (TextForCode),
// for Font.TextForCode's use. A nil *ToUnicodeMap (a font with no
// /ToUnicode entry at all, or one whose stream failed to parse into
// anything usable) answers every code with ok=false, matching this
// package's general "a Font is always usable, just sometimes without a
// definite answer" policy (see font.go's doc comment) - callers never
// need to nil-check before calling TextForCode.
type ToUnicodeMap struct {
	ranges []bfRange
	chars  map[uint32]string
}

// TextForCode reports the Unicode text code maps to under u: a direct
// beginbfchar entry if one exists, else the first beginbfrange whose
// range contains code, else ok=false - the same "specific entry beats a
// range" precedence cidcmap.go's CMap.CIDForCode gives CID lookups,
// applied here to text instead.
func (u *ToUnicodeMap) TextForCode(code uint32) (string, bool) {
	if u == nil {
		return "", false
	}
	if s, ok := u.chars[code]; ok {
		return s, true
	}
	for _, r := range u.ranges {
		if code >= r.lo && code <= r.hi {
			return r.textFor(code)
		}
	}
	return "", false
}

// utf16BEBytesToString decodes b (raw bytes from a CMap hex string, the
// PDF specification's own convention for a /ToUnicode destination value
// - 9.10.3) as UTF-16BE into a Go string. An odd trailing byte (not
// valid UTF-16 at all - malformed input) is simply dropped rather than
// treated as an error, matching this package's usual tolerance for
// malformed font data.
func utf16BEBytesToString(b []byte) string {
	n := len(b) / 2
	if n == 0 {
		return ""
	}
	units := make([]uint16, n)
	for i := 0; i < n; i++ {
		units[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(units))
}

// encodeBEBytes encodes v as n big-endian bytes (the inverse of
// beValue), truncating to at most maxCMapCodeBytes if n is somehow
// larger - bfRange.textFor's own caller already bounds len(r.base) to
// that many bytes when the range is first parsed, so this cap is purely
// defensive. n <= 0 (an empty base, which parseBFRanges never actually
// produces - see its own bounds check) returns nil, decoding to the
// empty string.
func encodeBEBytes(v uint32, n int) []byte {
	if n <= 0 {
		return nil
	}
	if n > maxCMapCodeBytes {
		n = maxCMapCodeBytes
	}
	b := make([]byte, n)
	for i := n - 1; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
	return b
}

// parseToUnicodeCMap parses data (a /ToUnicode stream's already
// filter-decoded bytes) into a *ToUnicodeMap, tolerating anything it
// does not understand exactly the way cidcmap.go's parseCMap tolerates
// malformed CID CMap data: a parse problem with one entry, or the whole
// stream, simply means that entry (or every entry) is missing from the
// result, never an error returned to the caller. Font.TextForCode
// (font.go) falls back to reporting ok=false for any code a resulting
// ToUnicodeMap cannot resolve, so there is no failure mode here that
// needs its own error path - see loadToUnicode, which is what actually
// calls this after resolving and filter-decoding a font dictionary's
// /ToUnicode entry.
func parseToUnicodeCMap(data []byte) *ToUnicodeMap {
	u := &ToUnicodeMap{chars: make(map[uint32]string)}

	lex := syntax.NewLexer(bytes.NewReader(data))
	entries := 0

	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return u
		}
		if tok.Kind != syntax.KindKeyword {
			continue
		}
		switch tok.Text {
		case "beginbfchar":
			parseBFChars(lex, u, &entries)
		case "beginbfrange":
			parseBFRanges(lex, u, &entries)
		}
		if entries > maxCMapEntries {
			return u
		}
	}
}

// parseBFChars reads <code> <dstHex> pairs until "endbfchar" (or
// EOF/malformed input, tolerated the same way every loop in this file
// tolerates it - see parseToUnicodeCMap's doc comment), recording one
// u.chars entry per well-formed pair. A destination that is not a hex
// string (the specification also technically allows a bare Name here,
// naming a glyph rather than spelling out UTF-16BE bytes directly - rare
// in practice) is simply skipped, the same tolerance this package
// applies to a codespace/cidrange entry cidcmap.go cannot parse.
func parseBFChars(lex *syntax.Lexer, u *ToUnicodeMap, entries *int) {
	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return
		}
		if tok.Kind == syntax.KindKeyword && tok.Text == "endbfchar" {
			return
		}
		if tok.Kind != syntax.KindHexString {
			continue
		}
		if len(tok.Bytes) == 0 || len(tok.Bytes) > maxCMapCodeBytes {
			continue
		}
		code := beValue(tok.Bytes)

		dstTok, err := lex.Next()
		if err != nil {
			return
		}
		if dstTok.Kind != syntax.KindHexString {
			continue
		}

		u.chars[code] = utf16BEBytesToString(dstTok.Bytes)
		*entries++
		if *entries > maxCMapEntries {
			return
		}
	}
}

// parseBFRanges reads <lo> <hi> triples until "endbfrange", where the
// third element is either a hex string (the "increment" shape - see
// bfRange.base's doc comment) or a bracketed array of hex strings (the
// "explicit" shape - see bfRange.array's), appending one bfRange per
// well-formed triple.
func parseBFRanges(lex *syntax.Lexer, u *ToUnicodeMap, entries *int) {
	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return
		}
		if tok.Kind == syntax.KindKeyword && tok.Text == "endbfrange" {
			return
		}
		if tok.Kind != syntax.KindHexString {
			continue
		}
		lo := tok.Bytes

		hiTok, err := lex.Next()
		if err != nil {
			return
		}
		if hiTok.Kind != syntax.KindHexString || len(hiTok.Bytes) != len(lo) ||
			len(lo) == 0 || len(lo) > maxCMapCodeBytes {
			continue
		}
		loVal, hiVal := beValue(lo), beValue(hiTok.Bytes)
		if hiVal < loVal || uint64(hiVal)-uint64(loVal) > maxCMapRangeSpan {
			continue
		}

		dstTok, err := lex.Next()
		if err != nil {
			return
		}
		switch dstTok.Kind {
		case syntax.KindHexString:
			if len(dstTok.Bytes) == 0 || len(dstTok.Bytes) > maxCMapCodeBytes {
				continue
			}
			u.ranges = append(u.ranges, bfRange{
				lo: loVal, hi: hiVal,
				base: append([]byte(nil), dstTok.Bytes...),
			})
		case syntax.KindArrayStart:
			want := int(hiVal-loVal) + 1
			u.ranges = append(u.ranges, bfRange{
				lo: loVal, hi: hiVal,
				array: parseBFRangeArray(lex, want),
			})
		default:
			continue
		}
		*entries++
		if *entries > maxCMapEntries {
			return
		}
	}
}

// loadToUnicode resolves dict's /ToUnicode entry (PDF specification
// 9.10.2 - valid on any font dictionary, simple or Type0) into f's own
// toUnicode field, called by both simple.go's loadSimpleFont and cid.go's
// loadType0Font right after they build f's other fields. A missing
// entry, one that resolves to something other than a stream, or a stream
// whose /Filter chain fails to decode, all leave f.toUnicode nil -
// TextForCode's simpleEncoding fallback (or, for a Type0 font, plain
// ok=false) covers every one of those cases, matching this package's
// general "a Font is always usable, just sometimes without a definite
// answer" policy.
func loadToUnicode(f *Font, dict syntax.Dictionary, resolver Resolver) {
	obj, err := resolveIfRef(resolver, dict["ToUnicode"])
	if err != nil {
		return
	}
	stream, ok := obj.(syntax.Stream)
	if !ok {
		return
	}
	data, err := resolver.DecodeStream(stream)
	if err != nil {
		return
	}
	f.toUnicode = parseToUnicodeCMap(data)
}

// parseBFRangeArray reads hex strings until "]" (KindArrayEnd),
// returning each decoded as UTF-16BE text, in order - the destination
// list for a beginbfrange entry's "explicit" shape (see bfRange.array's
// doc comment). want (the range's own hi-lo+1, already bounded by
// parseBFRanges against maxCMapRangeSpan before this is called) caps how
// many elements are actually kept; any further hex strings before "]"
// are still consumed (so parsing of the surrounding beginbfrange block
// stays correctly positioned) but simply discarded, the same "tolerate,
// don't let a hostile file force unbounded memory" policy
// maxCMapRangeSpan itself exists for.
func parseBFRangeArray(lex *syntax.Lexer, want int) []string {
	var out []string
	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return out
		}
		if tok.Kind == syntax.KindArrayEnd {
			return out
		}
		if tok.Kind != syntax.KindHexString {
			continue
		}
		if len(out) >= want {
			continue
		}
		out = append(out, utf16BEBytesToString(tok.Bytes))
	}
}
