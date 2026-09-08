package fonts

import (
	"strconv"
	"strings"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements PDF's "simple font" character encodings: the
// mapping from a single-byte character *code* (0-255) in a Tj/TJ string
// to a Unicode code point ("rune", Go's name for one - see the comment on
// runeTable below if you are new to Go and have not seen this term
// before), which internal/fonts then uses to look a glyph up in an
// embedded TrueType font's own "cmap" table (see truetype.go). PDF calls
// this second step "encoding" because historically (going back to
// PostScript) it described how a font's *native* single-byte codes map
// to named glyphs; this package skips the "named glyph" step for
// TrueType fonts specifically and goes straight to Unicode, since a
// TrueType font's cmap table is itself keyed by Unicode code point (for
// the common case - see truetype.go's cmap subtable selection) rather
// than by Adobe glyph name.
//
// # If you are new to Go: what a "rune" is
//
// Go's `rune` type is an alias for int32, used by convention to hold one
// Unicode code point (a single character, in the everyday sense - 'A',
// 'é', '中', and so on). A Go string is really just a sequence of bytes,
// not characters, so code that needs to talk about "one character" uses
// rune instead - exactly what this package needs, since PDF text codes
// ultimately need to become Unicode characters to find the right glyph.

// runeTable is a full 256-entry lookup from an 8-bit PDF character code
// to the Unicode rune it represents under one particular PDF encoding.
// An entry of 0 means "this code has no defined character in this
// encoding" (rune 0, the null character, never appears as real text, so
// it doubles safely as a sentinel for "no mapping"); looking such a code
// up later falls back to this package's documented missing-glyph policy
// (see font.go's notdefGlyph).
type runeTable [256]rune

// asciiRange fills in codes 0x20 ("space") through 0x7E ("~") of a
// runeTable with plain ASCII, which is identical across every PDF
// encoding this package implements - StandardEncoding, WinAnsiEncoding,
// and MacRomanEncoding all agree on the ordinary Latin letters, digits,
// and common punctuation (see this file's package-level doc comment
// section on scope below for the handful of typographic-quote
// distinctions this project deliberately does not model).
func asciiRange() runeTable {
	var t runeTable
	for c := 0x20; c <= 0x7E; c++ {
		t[c] = rune(c)
	}
	return t
}

// standardEncoding returns Adobe StandardEncoding (the default simple
// font encoding when a font dictionary has no /Encoding entry at all -
// PDF specification Annex D). This project models only its ASCII range
// (0x20-0x7E) exactly; codes above 0x7E are deliberately left unmapped
// (rune 0) rather than transcribed from Annex D's less common,
// rarely-used-in-practice upper range - real-world PDF producers
// targeting non-ASCII Latin text overwhelmingly use WinAnsiEncoding (see
// below) or an explicit /Differences array instead, both of which this
// package does implement fully. A code in this gap falls back to this
// package's documented missing-glyph policy rather than silently
// rendering the wrong character, so this is a "renders nothing" gap, not
// a "renders wrong" one.
func standardEncoding() runeTable {
	return asciiRange()
}

// winAnsiEncoding returns PDF's WinAnsiEncoding (PDF specification Annex
// D), which matches Windows code page 1252 closely enough that this
// project treats the two as identical. Codes 0xA0-0xFF match Unicode's
// Latin-1 Supplement block exactly (both were designed to align with
// ISO 8859-1), so those are filled in directly rather than transcribed
// digit by digit; only the 0x80-0x9F range - where CP1252 substitutes
// "smart" typographic punctuation (curly quotes, em/en dashes, the euro
// sign, and so on) for the C1 control codes ISO 8859-1 leaves undefined
// there - needs its own explicit table.
func winAnsiEncoding() runeTable {
	t := asciiRange()
	// 0x80-0x9F: CP1252's substitutions for the C1 control range.
	upper := [32]rune{
		0x00: '€', 0x02: '‚', 0x03: 'ƒ', 0x04: '„',
		0x05: '…', 0x06: '†', 0x07: '‡',
		0x08: 'ˆ', 0x09: '‰', 0x0A: 'Š',
		0x0B: '‹', 0x0C: 'Œ', 0x0E: 'Ž',
		0x11: '‘', 0x12: '’', 0x13: '“',
		0x14: '”', 0x15: '•', 0x16: '–',
		0x17: '—', 0x18: '˜', 0x19: '™',
		0x1A: 'š', 0x1B: '›', 0x1C: 'œ',
		0x1E: 'ž', 0x1F: 'Ÿ',
	}
	for i, r := range upper {
		if r != 0 {
			t[0x80+i] = r
		}
	}
	// 0xA0-0xFF: identical to Unicode code points U+00A0-U+00FF.
	for c := 0xA0; c <= 0xFF; c++ {
		t[c] = rune(c)
	}
	return t
}

// macRomanEncoding returns PDF's MacRomanEncoding (PDF specification
// Annex D), the classic Mac OS 8-bit Latin text encoding. Unlike
// WinAnsiEncoding, its upper range (0x80-0xFF) does not align with any
// Unicode block directly, so it is transcribed as its own table below.
func macRomanEncoding() runeTable {
	t := asciiRange()
	upper := [128]rune{
		'Ä', 'Å', 'Ç', 'É', 'Ñ', 'Ö', 'Ü', 'á',
		'à', 'â', 'ä', 'ã', 'å', 'ç', 'é', 'è',
		'ê', 'ë', 'í', 'ì', 'î', 'ï', 'ñ', 'ó',
		'ò', 'ô', 'ö', 'õ', 'ú', 'ù', 'û', 'ü',
		'†', '°', '¢', '£', '§', '•', '¶', 'ß',
		'®', '©', '™', '´', '¨', '≠', 'Æ', 'Ø',
		'∞', '±', '≤', '≥', '¥', 'µ', '∂', '∑',
		'∏', 'π', '∫', 'ª', 'º', 'Ω', 'æ', 'ø',
		'¿', '¡', '¬', '√', 'ƒ', '≈', '∆', '«',
		'»', '…', ' ', 'À', 'Ã', 'Õ', 'Œ', 'œ',
		'–', '—', '“', '”', '‘', '’', '÷', '◊',
		'ÿ', 'Ÿ', '⁄', '€', '‹', '›', 'ﬁ', 'ﬂ',
		'‡', '·', '‚', '„', '‰', 'Â', 'Ê', 'Á',
		'Ë', 'È', 'Í', 'Î', 'Ï', 'Ì', 'Ó', 'Ô',
		'', 'Ò', 'Ú', 'Û', 'Ù', 'ı', 'ˆ', '˜',
		'¯', '˘', '˙', '˚', '¸', '˝', '˛', 'ˇ',
	}
	for i, r := range upper {
		t[0x80+i] = r
	}
	return t
}

// baseEncodingByName returns the runeTable named by name (one of PDF's
// three predefined simple-font encodings), and ok=false for any other
// name - which includes "MacExpertEncoding" (a rare, symbol-font-like
// encoding this project does not implement) and any typo or
// non-predefined name, both of which fall back to StandardEncoding at
// the call site (see BuildSimpleEncoding).
func baseEncodingByName(name syntax.Name) (runeTable, bool) {
	switch name {
	case "WinAnsiEncoding":
		return winAnsiEncoding(), true
	case "MacRomanEncoding":
		return macRomanEncoding(), true
	case "StandardEncoding":
		return standardEncoding(), true
	default:
		return runeTable{}, false
	}
}

// BuildSimpleEncoding resolves a simple font's (Type1/TrueType/MMType1 -
// i.e. not Type0) /Encoding entry into a full 256-entry code-to-rune
// table, per the PDF specification's rules (9.6.6): /Encoding may be
// absent (StandardEncoding), a bare Name (one of the three predefined
// encodings), or a Dictionary naming a /BaseEncoding (itself optional,
// defaulting to StandardEncoding) plus a /Differences array that
// overrides individual codes on top of the base table.
//
// A /Differences array alternates integers and names: each integer sets
// "the next code to override", and each name following it assigns that
// glyph to the current code before advancing to the next one - e.g.
// "[24 /breve /caron 32 /space]" assigns code 24 to "breve", code 25 to
// "caron" (25 = 24+1, no new integer needed), then jumps to code 32 for
// "space". Each glyph name is converted to the rune it represents via
// glyphNameToRune below; a name this package does not recognize leaves
// that one code unmapped (falling back to this package's missing-glyph
// policy - see font.go) rather than aborting the whole font, consistent
// with this project's general tolerance for one bad field not spoiling
// an otherwise-usable document (see, for example, internal/model's
// handling of a malformed /Rotate).
func BuildSimpleEncoding(dict syntax.Dictionary) runeTable {
	encObj, ok := dict["Encoding"]
	if !ok {
		return standardEncoding()
	}

	if name, ok := encObj.(syntax.Name); ok {
		if t, ok := baseEncodingByName(name); ok {
			return t
		}
		return standardEncoding()
	}

	encDict, ok := encObj.(syntax.Dictionary)
	if !ok {
		// /Encoding is present but neither a Name nor a Dictionary - not
		// valid PDF, but not worth failing the whole font over; fall back
		// to the specification's own default.
		return standardEncoding()
	}

	base := standardEncoding()
	if baseName, ok := encDict["BaseEncoding"].(syntax.Name); ok {
		if t, ok := baseEncodingByName(baseName); ok {
			base = t
		}
	}

	diffs, _ := encDict["Differences"].(syntax.Array)
	code := 0
	for _, item := range diffs {
		switch v := item.(type) {
		case syntax.Integer:
			code = int(v)
		case syntax.Real:
			code = int(v)
		case syntax.Name:
			if code >= 0 && code <= 255 {
				if r, ok := glyphNameToRune(string(v)); ok {
					base[code] = r
				}
			}
			code++
		}
	}
	return base
}

// glyphNameToRune converts an Adobe-style PostScript glyph name (as used
// in a /Differences array, or - for a Type 1 font's built-in encoding,
// which this package does not otherwise model - nowhere else in this
// package) to the Unicode rune it represents, and ok=false for a name
// this package does not recognize.
//
// This implements three of the Adobe Glyph List's naming conventions,
// covering the overwhelming majority of names real-world PDF producers
// actually emit for Latin text:
//
//  1. A small explicit table (glyphNames below) for the common named
//     punctuation and Latin-1 glyphs - the names that do not spell their
//     own character directly (digits are spelled out as words, e.g.
//     "seven" not "7"; many punctuation marks have names unrelated to
//     their printed form, e.g. "ampersand" for '&').
//  2. A single-character name that is itself an ASCII letter or digit
//     denotes exactly that character - this is how the Adobe Glyph List
//     names the 52 basic Latin letters ("A" is capital A, "z" is
//     lowercase z), and this package extends the same convenient rule to
//     bare digit names non-standard producers sometimes emit (a
//     forgiving extension beyond the strict Adobe Glyph List, in the
//     same spirit as this project's lexer tolerating minor real-world
//     deviations elsewhere - see internal/syntax.Lexer's doc comment).
//  3. The "uniXXXX" convention (exactly four uppercase hexadecimal
//     digits after the literal prefix "uni") names a glyph by its
//     Unicode code point directly - extremely common in
//     programmatically generated /Differences arrays and font subsets.
func glyphNameToRune(name string) (rune, bool) {
	if r, ok := glyphNames[name]; ok {
		return r, true
	}
	if len(name) == 1 {
		c := name[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return rune(c), true
		}
	}
	if strings.HasPrefix(name, "uni") && len(name) == 7 {
		if v, err := strconv.ParseUint(name[3:], 16, 32); err == nil {
			return rune(v), true
		}
	}
	return 0, false
}

// glyphNames is a deliberately small subset of the Adobe Glyph List:
// just the punctuation, symbol, and accented-Latin names common enough
// to plausibly appear in a real /Differences array for Latin-script
// text. It is not a complete AGL implementation (the full list has
// thousands of entries covering scripts this project does not otherwise
// support rendering text in - see internal/fonts' package doc comment on
// scope) - an unrecognized name simply falls back to this package's
// missing-glyph policy, the same graceful degradation every other
// "recognized subset, not the whole specification" table in this
// project uses (see, for example, internal/image/colorspace.go's
// documented color-space subset).
var glyphNames = map[string]rune{
	"space": ' ', "exclam": '!', "quotedbl": '"', "numbersign": '#',
	"dollar": '$', "percent": '%', "ampersand": '&', "quotesingle": '\'',
	"quoteright": '\'', "quoteleft": '`', "parenleft": '(', "parenright": ')',
	"asterisk": '*', "plus": '+', "comma": ',', "hyphen": '-', "minus": '-',
	"period": '.', "slash": '/',
	"zero": '0', "one": '1', "two": '2', "three": '3', "four": '4',
	"five": '5', "six": '6', "seven": '7', "eight": '8', "nine": '9',
	"colon": ':', "semicolon": ';', "less": '<', "equal": '=', "greater": '>',
	"question": '?', "at": '@',
	"bracketleft": '[', "backslash": '\\', "bracketright": ']',
	"asciicircum": '^', "underscore": '_', "grave": '`',
	"braceleft": '{', "bar": '|', "braceright": '}', "asciitilde": '~',
	"exclamdown": '¡', "cent": '¢', "sterling": '£',
	"currency": '¤', "yen": '¥', "section": '§',
	"dieresis": '¨', "copyright": '©', "ordfeminine": 'ª',
	"guillemotleft": '«', "logicalnot": '¬', "registered": '®',
	"macron": '¯', "degree": '°', "plusminus": '±',
	"acute": '´', "mu": 'µ', "paragraph": '¶',
	"periodcentered": '·', "cedilla": '¸', "ordmasculine": 'º',
	"guillemotright": '»', "questiondown": '¿',
	"Agrave": 'À', "Aacute": 'Á', "Acircumflex": 'Â',
	"Atilde": 'Ã', "Adieresis": 'Ä', "Aring": 'Å', "AE": 'Æ',
	"Ccedilla": 'Ç', "Egrave": 'È', "Eacute": 'É',
	"Ecircumflex": 'Ê', "Edieresis": 'Ë', "Igrave": 'Ì',
	"Iacute": 'Í', "Icircumflex": 'Î', "Idieresis": 'Ï',
	"Ntilde": 'Ñ', "Ograve": 'Ò', "Oacute": 'Ó',
	"Ocircumflex": 'Ô', "Otilde": 'Õ', "Odieresis": 'Ö',
	"Oslash": 'Ø', "Ugrave": 'Ù', "Uacute": 'Ú',
	"Ucircumflex": 'Û', "Udieresis": 'Ü', "germandbls": 'ß',
	"agrave": 'à', "aacute": 'á', "acircumflex": 'â',
	"atilde": 'ã', "adieresis": 'ä', "aring": 'å', "ae": 'æ',
	"ccedilla": 'ç', "egrave": 'è', "eacute": 'é',
	"ecircumflex": 'ê', "edieresis": 'ë', "igrave": 'ì',
	"iacute": 'í', "icircumflex": 'î', "idieresis": 'ï',
	"ntilde": 'ñ', "ograve": 'ò', "oacute": 'ó',
	"ocircumflex": 'ô', "otilde": 'õ', "odieresis": 'ö',
	"oslash": 'ø', "ugrave": 'ù', "uacute": 'ú',
	"ucircumflex": 'û', "udieresis": 'ü', "yacute": 'ý',
	"ydieresis": 'ÿ',
	"bullet":    '•', "endash": '–', "emdash": '—',
	"quotedblleft": '“', "quotedblright": '”',
	"quoteleftdbl": '“', "ellipsis": '…', "dagger": '†',
	"daggerdbl": '‡', "perthousand": '‰', "trademark": '™',
	"fi": 'ﬁ', "fl": 'ﬂ', "florin": 'ƒ', "Euro": '€',
}
