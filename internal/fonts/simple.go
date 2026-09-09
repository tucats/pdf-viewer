package fonts

import (
	"github.com/tucats/pdf-viewer/internal/diag"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements loadSimpleFont: building a Font from a "simple"
// font dictionary - /Subtype /Type1, /TrueType, /MMType1, /Type3, or
// anything this package does not specifically recognize - where every
// character code in a shown string is exactly one byte (unlike a Type0
// composite font - see cid.go).
//
// # What this package actually extracts real glyph outlines from
//
// Of the several ways a simple font's glyph program can be embedded
// (/FontFile for a Type 1 program, /FontFile2 for TrueType, /FontFile3
// for a CFF/Type1C or OpenType/CFF program, or no /FontFile* at all for
// a non-embedded font expected to be supplied by whatever renders the
// page), this package parses /FontFile2 (see truetype.go) and
// /FontFile3 (see cff.go and loadEmbeddedCFF below) - tried in that
// order (see loadSimpleFont). Type 1's own charstring format (/FontFile,
// distinct from - and, despite the similar name, not read by - cff.go's
// CFF/Type2 support) remains unimplemented (see docs/capability-matrix.md),
// and a non-embedded font's actual outlines are simply not available at
// all without querying a system font service, which the README's
// "Dependency and safety policy" forbids this package from ever doing
// (see this package's own doc comment). Both of those remaining cases
// still produce a fully usable Font - see font.go's doc comment on
// Font's fallback policy - just one that paints notdefGlyph's
// placeholder box instead of a real outline.
const defaultMissingWidth = 500

// loadSimpleFont builds a Font from a simple font dictionary.
func loadSimpleFont(dict syntax.Dictionary, resolver Resolver) (*Font, error) {
	widths, defaultWidth := simpleWidths(dict, resolver)
	encoding := BuildSimpleEncoding(dict)

	descriptor, _ := dictValue(resolver, dict, "FontDescriptor")
	symbolic := isSymbolic(descriptor)
	if mw, ok := numberValue(descriptor["MissingWidth"]); ok {
		defaultWidth = mw
	}

	spaceCodes := make(map[int]bool)
	for code, r := range encoding {
		if r == ' ' {
			spaceCodes[code] = true
		}
	}

	f := &Font{
		widths:       widths,
		defaultWidth: defaultWidth,
		spaceCodes:   spaceCodes,
	}

	// Try an embedded TrueType program first (/FontFile2, by far the
	// most common case for a simple font this package can extract real
	// outlines from), then an embedded CFF program (/FontFile3 - see
	// loadEmbeddedCFF's doc comment for the two shapes it accepts).
	// Exactly one of these ever succeeds in practice (a font dictionary
	// embeds at most one font program), but trying both in order rather
	// than switching on /Subtype keeps this package tolerant of a
	// dictionary whose /Subtype does not quite match which /FontFile*
	// entry it actually carries - the same "trust what is actually
	// there over what a field merely claims" precedent probeFace/
	// ProbeFontFile already establish for a candidate font file.
	if sfnt, ok := loadEmbeddedTrueType(descriptor, resolver); ok {
		f.glyphSource = &sfnt
		f.lookupGID = simpleGlyphLookup(&sfnt, encoding, symbolic)
	} else if cff, ok := loadEmbeddedCFF(descriptor, resolver); ok {
		f.glyphSource = &cff
		f.lookupGID = simpleCFFGlyphLookup(&cff, encoding)
	} else {
		diag.Note(resolver, "font %v (%v) has no usable embedded TrueType or CFF outline data (no /FontFile2 or /FontFile3, or it failed to parse); its glyphs will render as placeholder boxes", dict["BaseFont"], dict["Subtype"])
	}
	return f, nil
}

// simpleWidths reads a simple font's /Widths array (indexed from
// /FirstChar through /LastChar, per the specification) into a
// code->width map, and determines the default width to use for any code
// outside that range (or when /Widths is absent altogether): the
// font descriptor's /MissingWidth if the caller supplies one later (see
// loadSimpleFont, which overwrites the returned default after this
// function runs, since /MissingWidth lives in a different dictionary),
// or - if even that is unavailable - defaultMissingWidth, a generic
// "typical Latin character" advance width. This last fallback exists
// specifically for a non-embedded font with no /Widths at all (this
// package ships no standard-14 font metrics table - see this file's
// package doc comment on scope - so an exact width for, say, Helvetica
// is simply not available to it): text still advances at a reasonable,
// deterministic rate instead of every character stacking on top of the
// last one.
func simpleWidths(dict syntax.Dictionary, resolver Resolver) (map[int]float64, float64) {
	widths := make(map[int]float64)

	firstChar, hasFirst := numberValue(dict["FirstChar"])
	arr, _ := dict["Widths"].(syntax.Array)
	if hasFirst && len(arr) > 0 {
		for i, v := range arr {
			resolved, err := resolveIfRef(resolver, v)
			if err != nil {
				continue
			}
			if w, ok := numberValue(resolved); ok {
				widths[int(firstChar)+i] = w
			}
		}
	}
	return widths, defaultMissingWidth
}

// flagSymbolic is bit position 3 (value 4) of a font descriptor's
// /Flags entry (PDF specification, Table 123) - "Font contains glyphs
// outside the Adobe standard Latin character set... or the font
// encoding is not Latin text." A symbolic font's character codes are
// meant to be looked up directly in the embedded font program's own
// built-in cmap rather than through one of PDF's named Latin encodings
// - see simpleGlyphLookup.
const flagSymbolic = 1 << 2

// isSymbolic reports whether descriptor's /Flags entry has the symbolic
// bit set. A missing or unreadable /Flags is treated as non-symbolic
// (the common case - most fonts, especially any using WinAnsiEncoding or
// a /Differences array, are ordinary Latin-text fonts).
func isSymbolic(descriptor syntax.Dictionary) bool {
	flags, ok := numberValue(descriptor["Flags"])
	if !ok {
		return false
	}
	return int(flags)&flagSymbolic != 0
}

// readFontFileStream resolves and filter-decodes descriptor's key entry
// - an embedded font program stream, one of PDF's three flavors
// ("/FontFile" for Type 1, "/FontFile2" for TrueType, "/FontFile3" for
// CFF/OpenType) - returning ok=false if key is absent, cannot be
// resolved to an indirect object, is not actually a stream, or fails to
// decode (its /Filter chain applied - almost always Flate-compressed in
// a real file). This is the shared "get me the raw program bytes" step
// behind every embedded-font-program loader in this package
// (loadEmbeddedTrueType and loadEmbeddedCFF below); what differs
// between them is only how those bytes are then parsed.
func readFontFileStream(descriptor syntax.Dictionary, key syntax.Name, resolver Resolver) ([]byte, bool) {
	if descriptor == nil {
		return nil, false
	}
	ref, ok := descriptor[key]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(resolver, ref)
	if err != nil {
		return nil, false
	}
	stream, ok := resolved.(syntax.Stream)
	if !ok {
		return nil, false
	}
	data, err := resolver.DecodeStream(stream)
	if err != nil {
		return nil, false
	}
	return data, true
}

// loadEmbeddedTrueType reads and parses descriptor's /FontFile2 stream
// (an embedded TrueType/OpenType-with-TrueType-outlines program - see
// truetype.go), returning ok=false if there is none, it cannot be
// resolved, or it fails to parse.
func loadEmbeddedTrueType(descriptor syntax.Dictionary, resolver Resolver) (sfntFont, bool) {
	data, ok := readFontFileStream(descriptor, "FontFile2", resolver)
	if !ok {
		return sfntFont{}, false
	}
	return parseSfnt(data)
}

// loadEmbeddedCFF reads and parses descriptor's /FontFile3 stream - see
// cff.go for the format itself. A /FontFile3 stream is one of two
// shapes, per its own /Subtype (PDF specification Table 126): "Type1C"
// or "CIDFontType0C" (a bare CFF program, which parseCFFFont reads
// directly), or "OpenType" (a full sfnt wrapper - the same container
// format truetype.go's parseTableDirectory already reads - whose own
// "CFF " table holds the actual CFF program). Rather than branch on the
// declared /Subtype (which this package does not even bother reading
// here - see loadSimpleFont's doc comment on trusting the data over the
// label), this simply tries the bare-CFF interpretation first and falls
// back to unwrapping an sfnt "CFF " table, so a mislabeled or unusual
// real-world file is still handled correctly either way.
func loadEmbeddedCFF(descriptor syntax.Dictionary, resolver Resolver) (cffFont, bool) {
	data, ok := readFontFileStream(descriptor, "FontFile3", resolver)
	if !ok {
		return cffFont{}, false
	}
	if font, ok := parseCFFFont(data); ok {
		return font, true
	}
	if tables, _, ok := parseTableDirectory(data, 0); ok {
		if cffTable, ok := tables["CFF "]; ok {
			return parseCFFFont(cffTable)
		}
	}
	return cffFont{}, false
}

// simpleGlyphLookup returns the lookupGID function (see Font's doc
// comment) for a simple font backed by sfnt: for each code, it tries -
// in the order this file's package doc comment describes -
// whichever combination of "raw code" and "code's resolved Unicode rune"
// lookups matches this font's declared symbolic-ness, trying the other
// as an unconditional fallback either way (an extra, harmless lookup
// attempt costs nothing and occasionally recovers a font whose /Flags
// entry does not accurately describe it, which happens more often in
// real-world files than the specification would suggest).
func simpleGlyphLookup(sfnt *sfntFont, encoding runeTable, symbolic bool) func(code int) (uint16, bool) {
	byCode := func(code int) (uint16, bool) {
		if gid, ok := sfnt.cmap.Lookup(rune(code)); ok {
			return gid, ok
		}
		// The conventional Windows "symbol" cmap (platform 3, encoding
		// 0) convention of storing codes in the Private Use Area
		// starting at U+F000 - see selectCmapSubtable's doc comment.
		return sfnt.cmap.Lookup(rune(0xF000 + code))
	}
	byRune := func(code int) (uint16, bool) {
		if code < 0 || code > 255 {
			return 0, false
		}
		r := encoding[code]
		if r == 0 {
			return 0, false
		}
		return sfnt.cmap.Lookup(r)
	}

	if symbolic {
		return func(code int) (uint16, bool) {
			if gid, ok := byCode(code); ok {
				return gid, true
			}
			return byRune(code)
		}
	}
	return func(code int) (uint16, bool) {
		if gid, ok := byRune(code); ok {
			return gid, true
		}
		return byCode(code)
	}
}

// simpleCFFGlyphLookup returns the lookupGID function (see Font's doc
// comment) for a simple font backed by an embedded CFF program: for
// each code, it resolves the PDF font dictionary's own /Encoding-
// derived rune (encoding.go's BuildSimpleEncoding) and looks that rune
// up via the CFF program's own charset names (cffFont.GIDForRune) - the
// path PDF's specification (9.6.6.2) describes for a non-symbolic
// simple font backed by a Type 1/CFF program: a character code maps to
// a glyph *name* via /Encoding, and that name is then looked up in the
// font program's own charset to find its GID.
//
// Unlike simpleGlyphLookup's TrueType counterpart above, this has no
// "symbolic, look up by raw code" fallback path: a CFF program's own
// built-in Encoding table (a second, separate code-to-GID table the CFF
// format also defines, distinct from the charset) is not read by this
// package - see cff.go's doc comment on scope. A symbolic CFF font
// whose codes are only meaningful through that built-in table therefore
// still falls back to notdefGlyph for those codes - a narrow, documented
// gap that leaves the overwhelmingly common case (a non-symbolic
// embedded Latin-text font using /Encoding, or relying on its default
// StandardEncoding) unaffected.
func simpleCFFGlyphLookup(cff *cffFont, encoding runeTable) func(code int) (uint16, bool) {
	return func(code int) (uint16, bool) {
		if code < 0 || code > 255 {
			return 0, false
		}
		r := encoding[code]
		if r == 0 {
			return 0, false
		}
		return cff.GIDForRune(r)
	}
}
