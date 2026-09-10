package fonts

import (
	"bytes"
	"sort"
	"strconv"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements CMap parsing: turning the bytes of a PDF "CMap"
// object (ISO 32000-1 9.7.5) into a CMap value that can both split a
// shown string into character codes of the right byte width(s) and map
// each code to its CID. cid.go's loadType0Encoding is what actually
// builds one of these from a Type0 font's /Encoding entry, when that
// entry is an embedded CMap stream rather than one of the "Identity-H"/
// "Identity-V" names this package has supported directly since Phase 4.
//
// Do not confuse this file's CMap with cmap.go's cmapSubtable: that one
// is a completely different thing despite the similar name - a
// *TrueType font's own* internal "cmap" table, mapping Unicode code
// points to glyph indices within that one font program. This file's
// CMap is a *PDF-level* object, mapping a Type0 font's raw character
// codes to CIDs (which cid.go's cidGlyphLookup/cidCFFGlyphLookup then
// resolve to a glyph index within whichever font program the descendant
// font actually embeds) - the two happen to share a name only because
// both ultimately trace back to Adobe's original PostScript-era "CMap"
// concept, not because either package reuses the other's code.
//
// # If you are new to Go or to PDF: what a CMap actually looks like
//
// A CMap is not PDF's own object syntax (dictionaries, arrays, and so
// on) - it is a small subset of the PostScript programming language,
// the same textual language PDF itself grew out of. A real one (heavily
// trimmed) looks like this:
//
//	/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> def
//	/CMapName /My-Custom-Encoding def
//	1 begincodespacerange
//	<0000> <FFFF>
//	endcodespacerange
//	2 begincidrange
//	<0000> <00FF> 0
//	<0100> <01FF> 256
//	endcidrange
//	1 begincidchar
//	<0041> 65
//	endcidchar
//	endcmap
//
// Three "begin...end" blocks matter to this package:
//
//   - begincodespacerange/endcodespacerange declares which raw byte
//     sequences are even valid codes under this CMap, and - crucially for
//     decode below - how many bytes long a code is. Every predefined CJK
//     encoding and the overwhelming majority of embedded CMaps declare
//     exactly one length (2, matching Identity-H/V), but the grammar
//     technically allows mixing lengths (some legacy CJK encodings use
//     1-byte codes for ASCII and 2-byte codes for everything else), which
//     decode's general algorithm below handles.
//   - begincidrange/endcidrange maps a contiguous range of codes to a
//     contiguous range of CIDs starting at a given value (so
//     "<0100> <01FF> 256" means code 0x101 is CID 257, and so on).
//   - begincidchar/endcidchar maps one single code to one CID directly,
//     used for the isolated codes not worth spelling out as a range.
//
// Everything else in a real CMap (the /CIDSystemInfo and /CMapName
// declarations above, comments, and any other PostScript this package
// does not need) is simply skipped: parseCMap's outer loop only acts on
// the keywords it specifically recognizes and otherwise discards tokens,
// the same tolerant "skip what you don't understand" approach
// internal/content's own content-stream Parse takes towards operators it
// doesn't implement.
//
// This same grammar (and this same parser) is what Phase 10's
// /ToUnicode parsing is expected to reuse, per docs/PLAN2.md's Phase 9
// entry - a /ToUnicode CMap uses "beginbfrange"/"beginbfchar" instead of
// "begincidrange"/"begincidchar" and maps to Unicode strings instead of
// CIDs, but the codespacerange and usecmap machinery is identical.

// maxCMapEntries bounds how many codespace/cidrange/cidchar entries a
// single parseCMap call will accept, combined, before it simply stops
// reading - defending against a hostile or corrupted CMap stream
// claiming millions of tiny ranges purely to force unbounded memory
// growth, the same "bounded work against untrusted input" policy this
// project applies throughout (see the README's "Dependency and safety
// policy", and cid.go's parseCIDWidths for the same idea applied to a
// /W array). No real predefined or embedded CMap comes anywhere near
// this many entries.
const maxCMapEntries = 1 << 20

// maxCMapRangeSpan bounds how many codes a single begincidrange entry
// may claim to cover (hi-lo), for the same reason maxCMapEntries bounds
// the entry count: "<0000> <FFFFFFFF> 0" would otherwise describe a
// four-billion-entry range from three tokens.
const maxCMapRangeSpan = 1 << 20

// maxCMapCodeBytes bounds how many bytes long a single code (in a
// codespacerange, cidrange, or cidchar hex string) may be. PDF's own
// codespace ranges are never longer than 4 bytes in practice (nothing
// this package's own DecodedCode.Code - a plain int - could not hold
// many times over), and beValue below assumes this bound to fit its
// result in a uint32.
const maxCMapCodeBytes = 4

// maxUseCMapDepth bounds how many "usecmap" links parseCMap will follow
// transitively (via the resolveUseCMap callback) before giving up,
// guarding against a resolver configuration that somehow chains CMaps
// into a cycle. No real CMap resource chain is more than one or two
// links deep.
const maxUseCMapDepth = 8

// codespaceRange is one declared valid-code range from a CMap's
// begincodespacerange block: every code of numBytes bytes whose
// big-endian value falls within [lo,hi] is valid under this CMap, and -
// this is the field decode actually cares about - numBytes bytes long.
type codespaceRange struct {
	numBytes int
	lo, hi   uint32
}

// cidRange is one declared code-to-CID range from a CMap's
// begincidrange block: code lo maps to cid, code lo+1 maps to cid+1, and
// so on up to hi.
type cidRange struct {
	lo, hi uint32
	cid    int
}

// CMap is a parsed CMap (see this file's package doc comment): enough
// information to split a shown string's raw bytes into character codes
// (decode, per the codespace ranges) and to translate each resulting
// code into its CID (CIDForCode, per the cidrange/cidchar tables).
//
// A zero-value *CMap (as returned when nothing at all could be parsed)
// is perfectly usable - every method below treats a nil receiver, and
// every empty/nil field, the same as "this CMap declares nothing",
// falling through to whatever fallback each method's own doc comment
// describes - so callers never need to nil-check a *CMap before using
// it, matching this package's general "Font never fails" design (see
// font.go's doc comment).
type CMap struct {
	codespaces []codespaceRange
	ranges     []cidRange
	chars      map[uint32]int

	// parent is the CMap this one's "usecmap" operator (if any) resolved
	// to - see effectiveCodespaces and CIDForCode, both of which check
	// their own tables first and only then defer to parent, exactly
	// matching ISO 32000-1 9.7.5.2's description of usecmap as
	// "supplementing" (not replacing) the referencing CMap.
	parent *CMap
}

// CIDForCode reports the CID code maps to under m: a direct
// begincidchar entry if one exists, else the first begincidrange whose
// range contains code, else (if m has a usecmap-chained parent) whatever
// that parent itself maps code to, else ok=false. code is looked up as a
// plain numeric value regardless of how many bytes it was decoded from -
// callers get that byte count separately from decode/DecodedCode, since
// CIDForCode itself has no way to know it (the same numeric code could
// validly arise from a 1-byte or a 2-byte codespace range in a CMap that
// declares both).
func (m *CMap) CIDForCode(code uint32) (int, bool) {
	if m == nil {
		return 0, false
	}
	if cid, ok := m.chars[code]; ok {
		return cid, true
	}
	for _, r := range m.ranges {
		if code >= r.lo && code <= r.hi {
			return r.cid + int(code-r.lo), true
		}
	}
	return m.parent.CIDForCode(code)
}

// effectiveCodespaces returns m's own codespace ranges, or - if m
// declares none itself - its usecmap-chained parent's, matching
// CIDForCode's own "check mine, then defer to parent" layering. Most
// embedded CMaps declare their own codespacerange even when they also
// use usecmap (typically to inherit CID mappings from a predefined
// encoding while keeping its own codespace), so falling through to a
// parent's codespace is the less common case in practice, but PDF's
// grammar does not forbid a CMap that omits it entirely.
func (m *CMap) effectiveCodespaces() []codespaceRange {
	if m == nil {
		return nil
	}
	if len(m.codespaces) > 0 {
		return m.codespaces
	}
	return m.parent.effectiveCodespaces()
}

// effectiveLengths returns the distinct byte-lengths m's (or its
// usecmap parent's) codespace ranges declare, sorted ascending - decode
// checks them shortest-first, per ISO 32000-1 9.7.6.2's own matching
// algorithm (a shorter valid match always wins over a longer one).
func (m *CMap) effectiveLengths() []int {
	spaces := m.effectiveCodespaces()
	seen := make(map[int]bool, len(spaces))
	lengths := make([]int, 0, len(spaces))
	for _, r := range spaces {
		if !seen[r.numBytes] {
			seen[r.numBytes] = true
			lengths = append(lengths, r.numBytes)
		}
	}
	sort.Ints(lengths)
	return lengths
}

// DecodedCode is one character code decoded from a shown string's raw
// bytes, together with how many of those raw bytes it was expressed in -
// see CMap.decode and Font.DecodeCodes (font.go), which produce these
// for a composite (Type0/CID) font exactly the way decodeCodes
// (internal/content/text.go) always has for a simple one. The byte
// count is not just bookkeeping: PDF's word-spacing parameter (Tw)
// applies only to a single-byte code with value 32, never to any byte
// within a multi-byte code even if part of it happens to equal 32 (PDF
// specification 9.3.3) - internal/content's showText needs this field
// to tell the two cases apart.
type DecodedCode struct {
	Code  int
	Bytes int
}

// decode splits s into codes per m's own (or usecmap-inherited)
// codespace ranges, following ISO 32000-1 9.7.6.2's algorithm: at each
// position, try the shortest declared code length first, accepting the
// first length whose corresponding byte value actually falls within one
// of that length's declared ranges. A prefix matching no declared range
// at all - malformed content, or a byte sequence outside every declared
// codespace - falls back to the first declared range's own length (the
// specification's own "use the codespace range that is more similar"
// guidance, simplified to "use the first one" the same way this
// project simplifies other rarely-exercised corners), or one byte at a
// time if no codespace was declared at all. This mirrors
// internal/content/text.go's decodeCodes tolerance for trailing/
// malformed bytes: a truncated final code is simply dropped rather than
// treated as an error.
func (m *CMap) decode(s []byte) []DecodedCode {
	lengths := m.effectiveLengths()
	spaces := m.effectiveCodespaces()

	var out []DecodedCode
	for i := 0; i < len(s); {
		n := matchCodeLength(s[i:], lengths, spaces)
		if n <= 0 || i+n > len(s) {
			break
		}
		out = append(out, DecodedCode{Code: int(beValue(s[i : i+n])), Bytes: n})
		i += n
	}
	return out
}

// matchCodeLength implements decode's per-position length choice: see
// that method's doc comment for the algorithm this follows.
func matchCodeLength(s []byte, lengths []int, spaces []codespaceRange) int {
	for _, l := range lengths {
		if l > len(s) {
			continue
		}
		v := beValue(s[:l])
		for _, r := range spaces {
			if r.numBytes == l && v >= r.lo && v <= r.hi {
				return l
			}
		}
	}
	if len(spaces) > 0 {
		l := spaces[0].numBytes
		if l > len(s) {
			l = len(s)
		}
		return l
	}
	if len(s) > 0 {
		return 1
	}
	return 0
}

// beValue interprets b (already bounded to at most maxCMapCodeBytes
// bytes by every caller that builds a codespaceRange/cidRange/cidchar
// entry below) as a big-endian unsigned integer - the same numeric
// interpretation PDF's own multi-byte string operands always use.
func beValue(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return v
}

// parseCMap parses data (an embedded CMap stream's decoded bytes) into a
// *CMap, tolerating anything it does not understand exactly the way the
// rest of this package tolerates malformed font data: a parse problem
// with one entry, or the whole stream, simply means that entry (or
// every entry) is missing from the result, never an error returned to
// the caller - cid.go's loadType0Encoding falls back to this package's
// ordinary notdefGlyph/generic-width behavior for any code a resulting
// CMap cannot resolve, so there is no failure mode here that needs its
// own error path.
//
// resolveUseCMap, if non-nil, is called with the name a "usecmap"
// operator names (e.g. "/UniGB-UCS2-H usecmap" calls it with
// "UniGB-UCS2-H") to resolve that referenced CMap - used by Phase 9c's
// predefined-CMap support (predefined_cmap.go) so an embedded CMap that
// itself builds on a predefined one can inherit its codespace and CID
// mappings; pass nil when no such resolution is available (which simply
// means a "usecmap" operator has no effect, exactly as if the CMap
// omitted it) or when parsing a CMap resolveUseCMap itself resolved to
// (depth guards against a resolver that somehow chains CMaps into a
// cycle - see maxUseCMapDepth).
func parseCMap(data []byte, resolveUseCMap func(name string) (*CMap, bool)) *CMap {
	return parseCMapAtDepth(data, resolveUseCMap, 0)
}

func parseCMapAtDepth(data []byte, resolveUseCMap func(name string) (*CMap, bool), depth int) *CMap {
	cm := &CMap{chars: make(map[uint32]int)}
	if depth > maxUseCMapDepth {
		return cm
	}

	lex := syntax.NewLexer(bytes.NewReader(data))
	var lastName string
	entries := 0

	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return cm
		}
		if tok.Kind == syntax.KindName {
			lastName = string(tok.Bytes)
			continue
		}
		if tok.Kind != syntax.KindKeyword {
			continue
		}

		switch tok.Text {
		case "usecmap":
			if resolveUseCMap != nil && cm.parent == nil && lastName != "" {
				if parent, ok := resolveUseCMap(lastName); ok {
					cm.parent = parent
				}
			}
		case "begincodespacerange":
			parseCodespaceRanges(lex, cm, &entries)
		case "begincidrange":
			parseCIDRanges(lex, cm, &entries)
		case "begincidchar":
			parseCIDChars(lex, cm, &entries)
		}
		if entries > maxCMapEntries {
			return cm
		}
	}
}

// parseCodespaceRanges reads <lo> <hi> hex-string pairs until
// "endcodespacerange" (or EOF/malformed input, tolerated the same way
// every loop in this file tolerates it - see parseCMap's doc comment),
// appending one codespaceRange per well-formed pair.
func parseCodespaceRanges(lex *syntax.Lexer, cm *CMap, entries *int) {
	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return
		}
		if tok.Kind == syntax.KindKeyword && tok.Text == "endcodespacerange" {
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

		cm.codespaces = append(cm.codespaces, codespaceRange{
			numBytes: len(lo),
			lo:       beValue(lo),
			hi:       beValue(hiTok.Bytes),
		})
		*entries++
		if *entries > maxCMapEntries {
			return
		}
	}
}

// parseCIDRanges reads <lo> <hi> cid triples until "endcidrange",
// appending one cidRange per well-formed triple - see parseCMap's doc
// comment for how malformed entries are tolerated (skipped, not fatal).
func parseCIDRanges(lex *syntax.Lexer, cm *CMap, entries *int) {
	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return
		}
		if tok.Kind == syntax.KindKeyword && tok.Text == "endcidrange" {
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

		cidTok, err := lex.Next()
		if err != nil {
			return
		}
		cid, ok := parseCMapInt(cidTok)
		if !ok {
			continue
		}

		loVal, hiVal := beValue(lo), beValue(hiTok.Bytes)
		if hiVal < loVal || uint64(hiVal)-uint64(loVal) > maxCMapRangeSpan {
			continue
		}

		cm.ranges = append(cm.ranges, cidRange{lo: loVal, hi: hiVal, cid: cid})
		*entries++
		if *entries > maxCMapEntries {
			return
		}
	}
}

// parseCIDChars reads <code> cid pairs until "endcidchar", recording
// one chars entry per well-formed pair - see parseCMap's doc comment
// for how malformed entries are tolerated.
func parseCIDChars(lex *syntax.Lexer, cm *CMap, entries *int) {
	for {
		tok, err := lex.Next()
		if err != nil || tok.Kind == syntax.KindEOF {
			return
		}
		if tok.Kind == syntax.KindKeyword && tok.Text == "endcidchar" {
			return
		}
		if tok.Kind != syntax.KindHexString {
			continue
		}
		if len(tok.Bytes) == 0 || len(tok.Bytes) > maxCMapCodeBytes {
			continue
		}
		code := beValue(tok.Bytes)

		cidTok, err := lex.Next()
		if err != nil {
			return
		}
		cid, ok := parseCMapInt(cidTok)
		if !ok {
			continue
		}

		cm.chars[code] = cid
		*entries++
		if *entries > maxCMapEntries {
			return
		}
	}
}

// parseCMapInt reads a non-negative integer operand (a CID value) from
// an already-read Token, ok=false if tok is not a plain integer - a CID
// is never fractional and never large enough to need more than an
// ordinary Go int, so this rejects anything that looks like a real
// number (containing '.') or that overflows a reasonable bound, the same
// defensive parsing this package applies to every other numeric field
// read from untrusted PDF content (see, for example, cid.go's
// numberValue).
func parseCMapInt(tok syntax.Token) (int, bool) {
	if tok.Kind != syntax.KindNumber {
		return 0, false
	}
	n, err := strconv.ParseInt(tok.Text, 10, 64)
	if err != nil || n < 0 || n > (1<<31) {
		return 0, false
	}
	return int(n), true
}
