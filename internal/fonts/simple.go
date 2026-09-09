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
// for a bare CFF/Type1C program, or no /FontFile* at all for a
// non-embedded font expected to be supplied by whatever renders the
// page), this package only parses /FontFile2 (see truetype.go) - Type 1
// and CFF charstring interpretation are a substantially different and
// separately complex format this project defers past Phase 4 (see
// docs/capability-matrix.md), and a non-embedded font's actual outlines
// are simply not available at all without querying a system font
// service, which the README's "Dependency and safety policy" forbids
// this package from ever doing (see this package's own doc comment).
// Every one of those other cases still produces a fully usable Font -
// see font.go's doc comment on Font's fallback policy - just one that
// paints notdefGlyph's placeholder box instead of a real outline.
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

	sfnt, ok := loadEmbeddedTrueType(descriptor, resolver)
	f := &Font{
		widths:       widths,
		defaultWidth: defaultWidth,
		spaceCodes:   spaceCodes,
	}
	if ok {
		f.glyphSource = &sfnt
		f.lookupGID = simpleGlyphLookup(&sfnt, encoding, symbolic)
	} else {
		diag.Note(resolver, "font %v (%v) has no usable embedded TrueType outline data (no /FontFile2, or it failed to parse); its glyphs will render as placeholder boxes", dict["BaseFont"], dict["Subtype"])
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

// loadEmbeddedTrueType reads and parses descriptor's /FontFile2 stream
// (an embedded TrueType/OpenType-with-TrueType-outlines program - see
// truetype.go), returning ok=false if there is none, it cannot be
// resolved, or it fails to parse.
func loadEmbeddedTrueType(descriptor syntax.Dictionary, resolver Resolver) (sfntFont, bool) {
	if descriptor == nil {
		return sfntFont{}, false
	}
	ref, ok := descriptor["FontFile2"]
	if !ok {
		return sfntFont{}, false
	}
	resolved, err := resolveIfRef(resolver, ref)
	if err != nil {
		return sfntFont{}, false
	}
	stream, ok := resolved.(syntax.Stream)
	if !ok {
		return sfntFont{}, false
	}
	data, err := resolver.DecodeStream(stream)
	if err != nil {
		return sfntFont{}, false
	}
	return parseSfnt(data)
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
