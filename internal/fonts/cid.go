package fonts

import (
	"encoding/binary"

	"github.com/tucats/pdf-viewer/internal/diag"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements loadType0Font: building a Font from a "Type0"
// (composite, CID-keyed) font dictionary. A Type0 font wraps exactly one
// "descendant font" (PDF specification 9.7.4) - almost always
// /Subtype /CIDFontType2 (a CID-keyed TrueType font, which this package
// can extract real outlines from - see truetype.go) or
// /CIDFontType0 (a CID-keyed CFF font, extracted via cff.go - see
// cidCFFGlyphLookup below) - together with an /Encoding
// naming how a shown string's raw bytes become character codes, and
// each descendant font's own /CIDToGIDMap naming how a character code
// (here, always equal to a CID - see below) becomes a glyph index.
//
// # Scope: only Identity-H/V encodings
//
// PDF allows /Encoding to name any of several dozen predefined CJK
// encodings (for vertical or horizontal Japanese, Chinese, Korean text
// with locale-specific code-to-CID mappings) or an embedded CMap stream
// defining an arbitrary one, each of which can use a different number of
// bytes per character code depending on the code's value (a genuinely
// variable-width encoding, unlike a simple font's fixed one byte). This
// package implements only the two the specification defines directly
// rather than through an external CMap resource: "Identity-H" and
// "Identity-V", under which a code is always exactly two bytes and
// numerically equal to its own CID (hence "Identity") - overwhelmingly
// the most common choice for a PDF produced by embedding a subsetted
// Latin, or indeed any non-CJK-legacy-workflow, font, since it needs no
// separate CMap resource at all. Any other /Encoding value still
// produces a usable Font (assuming, as an approximation, that it is
// still 2-byte-per-code - true for the large majority of the predefined
// CJK encodings this package does not otherwise implement), but with no
// way to know real CIDs from raw codes: it falls back to this package's
// generic default width and notdefGlyph for every code - see
// docs/capability-matrix.md.
func loadType0Font(dict syntax.Dictionary, resolver Resolver) (*Font, error) {
	f := &Font{TwoByteCodes: true, defaultWidth: 1000}

	descendant, ok := firstDescendantFont(dict, resolver)
	if !ok {
		// No usable descendant font dictionary at all - still return a
		// Font (per this package's general "always degrade gracefully"
		// policy - see font.go's doc comment), just one with nothing
		// more specific than the bare defaults set above to go on.
		diag.Note(resolver, "Type0 font %v has no usable /DescendantFonts entry; its glyphs will render as placeholder boxes", dict["BaseFont"])
		return f, nil
	}

	if dw, ok := numberValue(descendant["DW"]); ok {
		f.defaultWidth = dw
	}
	f.widths = parseCIDWidths(descendant["W"], resolver)

	descriptor, _ := dictValue(resolver, descendant, "FontDescriptor")
	if sfnt, ok := loadEmbeddedTrueType(descriptor, resolver); ok {
		f.glyphSource = &sfnt
		f.lookupGID = cidGlyphLookup(descendant, resolver)
		return f, nil
	}
	if cff, ok := loadEmbeddedCFF(descriptor, resolver); ok {
		f.glyphSource = &cff
		f.lookupGID = cidCFFGlyphLookup(&cff)
		return f, nil
	}
	// Unlike loadSimpleFont (simple.go), this does not attempt Phase 4's
	// font substitution (docs/FONTS.md) even when one is configured for
	// the current document - a deliberate scope decision, not an
	// oversight. Substitution needs to know which *Unicode rune* a code
	// means (see simple.go's trySubstitute, which resolves a code to a
	// rune via the PDF font's own /Encoding and looks that rune up in
	// the substitute's own cmap/charset) so it can ask a completely
	// different, substitute font program "what glyph do you have for
	// this character" - but per this file's own package doc comment,
	// this package parses no /ToUnicode CMap, so for a Type0 font a code
	// (here, a CID) has no known Unicode meaning at all. A CID is only
	// ever meaningful as an index into the *specific* font program that
	// originally defined it (its own glyph ordering, or - per this
	// package's Identity-H/V scope - directly as a glyph index into it);
	// reusing that same numeric value against an unrelated substitute
	// font's own, unrelated glyph ordering would not "approximately"
	// work the way a bold/italic mismatch does for a simple font's
	// substitute - it would pick essentially arbitrary, wrong glyphs.
	// notdefGlyph is the honest outcome here until this package parses
	// /ToUnicode (a separate, not-yet-scheduled capability - see
	// doc.go's "Text extraction is a separate, later capability").
	diag.Note(resolver, "Type0 font %v has no usable embedded TrueType or CFF outline data (no /FontFile2 or /FontFile3, or it failed to parse); its glyphs will render as placeholder boxes", dict["BaseFont"])
	return f, nil
}

// firstDescendantFont resolves dict's /DescendantFonts entry - per the
// specification, an array of exactly one indirect reference to the
// descendant CID font dictionary - and returns that dictionary.
func firstDescendantFont(dict syntax.Dictionary, resolver Resolver) (syntax.Dictionary, bool) {
	arr, ok := dict["DescendantFonts"].(syntax.Array)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	resolved, err := resolveIfRef(resolver, arr[0])
	if err != nil {
		return nil, false
	}
	d, ok := resolved.(syntax.Dictionary)
	return d, ok
}

// parseCIDWidths reads a CID font's /W array (PDF specification
// 9.7.4.3), which packs sparse per-CID widths using two alternating
// shapes to avoid one entry per CID for a large font:
//
//   - "c [w1 w2 ... wn]": consecutive CIDs starting at c get widths w1,
//     w2, ..., wn respectively (one width per array element).
//   - "cFirst cLast w": every CID in [cFirst, cLast] gets the same width
//     w.
//
// Which shape a given position uses is only known by looking at the
// *type* of the entry two positions ahead: after reading integer c, the
// next entry is either itself an Array (the first shape) or another
// integer, cLast, followed by a width (the second shape) - so this
// function reads entries with explicit index bookkeeping rather than a
// fixed stride. Any entry that does not fit this grammar is skipped
// (rather than aborting width parsing for the whole font), consistent
// with this project's general tolerance for one malformed field.
func parseCIDWidths(wObj syntax.Object, resolver Resolver) map[int]float64 {
	arr, ok := wObj.(syntax.Array)
	if !ok {
		return nil
	}
	widths := make(map[int]float64)
	i := 0
	for i < len(arr) {
		cVal, ok := numberValue(arr[i])
		if !ok {
			i++
			continue
		}
		c := int(cVal)
		i++
		if i >= len(arr) {
			break
		}

		next, err := resolveIfRef(resolver, arr[i])
		if err != nil {
			break
		}
		if group, ok := next.(syntax.Array); ok {
			for j, wv := range group {
				if w, ok := numberValue(wv); ok {
					widths[c+j] = w
				}
			}
			i++
			continue
		}

		lastVal, ok := numberValue(next)
		if !ok {
			i++
			continue
		}
		last := int(lastVal)
		i++
		if i >= len(arr) {
			break
		}
		w, ok := numberValue(arr[i])
		i++
		if !ok {
			continue
		}
		// last-c can be large in a hostile file (e.g. "0 2000000000 1
		// w"); bound it the same way this project bounds every other
		// input-driven loop (see the README's "Dependency and safety
		// policy") rather than trusting it directly.
		const maxCIDRangeSpan = 1 << 20
		if last < c || last-c > maxCIDRangeSpan {
			continue
		}
		for cid := c; cid <= last; cid++ {
			widths[cid] = w
		}
	}
	return widths
}

// cidGlyphLookup returns the lookupGID function (see Font's doc comment)
// for a CID-keyed TrueType font: it reads descendant's /CIDToGIDMap
// entry, which is either the Name "Identity" (or simply absent, which
// means the same thing per the specification's documented default) -
// meaning a CID *is* its own glyph index, no translation needed - or a
// stream of 2-byte big-endian glyph indices, one per CID, indexed
// directly by CID.
//
// Per this package's scope (see the package-level doc comment above), a
// "code" reaching this function is already known to be a CID (since
// only Identity-H/V encodings are supported, where code == CID by
// definition).
func cidGlyphLookup(descendant syntax.Dictionary, resolver Resolver) func(code int) (uint16, bool) {
	entry, hasEntry := descendant["CIDToGIDMap"]
	if !hasEntry {
		return identityGIDLookup
	}
	if name, ok := entry.(syntax.Name); ok {
		if name == "Identity" {
			return identityGIDLookup
		}
		// Not a recognized name; fall back to Identity, the
		// specification's own default, rather than treating this as
		// fatal.
		return identityGIDLookup
	}

	resolved, err := resolveIfRef(resolver, entry)
	if err != nil {
		return identityGIDLookup
	}
	stream, ok := resolved.(syntax.Stream)
	if !ok {
		return identityGIDLookup
	}
	data, err := resolver.DecodeStream(stream)
	if err != nil {
		return identityGIDLookup
	}
	return func(code int) (uint16, bool) {
		i := code * 2
		if code < 0 || i+2 > len(data) {
			return 0, false
		}
		gid := binary.BigEndian.Uint16(data[i : i+2])
		if gid == 0 {
			return 0, false
		}
		return gid, true
	}
}

// cidCFFGlyphLookup returns the lookupGID function (see Font's doc
// comment) for a CID-keyed font backed by an embedded CFF program (a
// CIDFontType0 descendant's own /FontFile3 - see loadEmbeddedCFF in
// simple.go, reused here unchanged since parsing a CFF program's bytes
// doesn't care which PDF font dictionary embedded them). Per this
// package's scope (see the package-level doc comment above), a "code"
// reaching this function is already known to be a CID (since only
// Identity-H/V encodings are supported, where code == CID by
// definition) - so this simply asks the CFF program's own charset
// (already inverted from CID -> GID by cff.go's parseCFFFont, into
// cffFont.GIDForCID) to resolve it.
//
// Unlike cidGlyphLookup above, this never falls back to treating code as
// its own GID: a CIDFontType0 descendant has no /CIDToGIDMap entry at
// all (PDF specification 9.7.4.2 - that entry only applies to
// CIDFontType2), since CID-to-GID mapping for a CFF-backed CID font is
// defined by the CFF program's own charset instead. A CFF program that
// turns out not to actually be CID-keyed (nonconformant, but not
// impossible in a real-world file) has no cidToGID map at all, so
// GIDForCID simply reports every code as not found - correctly falling
// back to notdefGlyph rather than guessing an identity mapping the
// specification does not describe for this font kind.
func cidCFFGlyphLookup(cff *cffFont) func(code int) (uint16, bool) {
	return func(code int) (uint16, bool) {
		if code < 0 || code > 0xFFFF {
			return 0, false
		}
		return cff.GIDForCID(uint16(code))
	}
}

// identityGIDLookup implements /CIDToGIDMap /Identity: a CID is used
// directly as a glyph index, with no lookup at all - the default, and
// by far the most common case for a CIDFontType2 whose glyphs were
// generated in the same order they were subsetted from a TrueType font
// (which is how most PDF-producing toolchains build one).
func identityGIDLookup(code int) (uint16, bool) {
	if code < 0 || code > 0xFFFF {
		return 0, false
	}
	return uint16(code), true
}
