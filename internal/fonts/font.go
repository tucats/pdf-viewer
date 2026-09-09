package fonts

import (
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file defines Font, the single type internal/content's text.go
// (Phase 4) works with regardless of which of PDF's several font
// flavors actually produced it, and Load, which builds one from a font
// dictionary. simple.go implements loading for "simple" fonts (Type1,
// TrueType, MMType1 - one byte per character code); cid.go implements
// loading for Type0 (composite/CID) fonts (this package supports only
// the Identity-H/V encodings - two bytes per character code, equal to
// the glyph's CID). Both converge on the same Font shape so that
// internal/content never needs to know which kind of font dictionary it
// started from once Load has returned.
//
// # Design note: Font never returns an error for a missing glyph or an
// unusable embedded font program
//
// Once a Font has been built, Width and Glyph never fail: a code with no
// defined width falls back to the font's default width, and a glyph
// this package cannot resolve - because there is no embedded font
// program at all, the embedded program is a format this package does
// not parse (Type 1, CFF/OpenType), or the specific glyph could not be
// found within a program this package can otherwise parse - falls back
// to notdefGlyph's small placeholder box, except for a code this
// package can positively identify as whitespace (see spaceCodes), which
// paints nothing rather than a box. This matches the README's Phase 4
// requirement to "define font fallback and missing-glyph behavior" as
// an explicit policy, and it means internal/content's text-showing code
// never needs its own separate "what if the glyph can't be found" branch
// - see docs/capability-matrix.md's Fonts section for exactly which font
// programs this package can and cannot extract real outlines from.
// glyphOutlineSource is the small interface both an embedded TrueType
// program (*sfntFont, truetype.go) and an embedded CFF program
// (*cffFont, cff.go) satisfy, letting Font (below) extract and scale a
// real glyph outline without needing to know which of PDF's several
// embedded-font-program formats actually produced it - simple.go's
// loadSimpleFont and cid.go's loadType0Font each try a TrueType program
// first (/FontFile2) and fall back to a CFF one (/FontFile3) before
// giving up and leaving glyphSource nil (see Font's own doc comment on
// what happens then).
type glyphOutlineSource interface {
	// GlyphOutline returns glyph index gid's outline in the font
	// program's own native design-units coordinate space, and ok=false
	// if gid has no usable outline (out of range, or malformed program
	// data) - see sfntFont.GlyphOutline and cffFont.GlyphOutline's own
	// doc comments, which this interface method matches exactly.
	GlyphOutline(gid uint16) (*graphics.Path, bool)

	// UnitsPerEm reports how many font design units make up one em in
	// this program's own outline coordinate space - see scaleGlyph,
	// which uses it to rescale an outline into this package's own fixed
	// 1000-units-per-em glyph space (see the widths field's doc comment
	// above), regardless of which kind of program supplied it.
	UnitsPerEm() uint16
}

type Font struct {
	// TwoByteCodes is true for a Type0/CID composite font (this package
	// only supports Identity-H/V encoding - see cid.go - so a "code" for
	// such a font is always exactly 2 bytes, equal to its CID), false for
	// a simple font (1 byte per code). internal/content's text.go uses
	// this to decide how to split a shown string into character codes.
	TwoByteCodes bool

	// widths maps a character code (simple font) or CID (composite font)
	// to its glyph's advance width, in "glyph space" units - PDF's fixed
	// convention of 1000 units per em, regardless of the font program's
	// own internal unitsPerEm (see scaleGlyph) - matching the units
	// /Widths and /W array entries are already expressed in. A code with
	// no entry here uses defaultWidth instead (see Width).
	widths       map[int]float64
	defaultWidth float64

	// glyphSource is the parsed embedded font program backing this
	// font's real glyph outlines - either a TrueType program (*sfntFont,
	// truetype.go, from /FontFile2) or a CFF program (*cffFont, cff.go,
	// from /FontFile3) - or nil if neither is available (see
	// glyphOutlineSource's own doc comment for why both kinds share one
	// interface here) - see Glyph's fallback behavior above.
	glyphSource glyphOutlineSource

	// lookupGID maps a character code to a glyph index within
	// glyphSource, or ok=false if no mapping could be found. It is nil
	// exactly when glyphSource is nil (there being no glyph source, a
	// glyph index would be meaningless) - see simple.go's
	// simpleGlyphLookup and cid.go's cidGlyphLookup for the two ways this
	// function is actually built.
	lookupGID func(code int) (gid uint16, ok bool)

	// spaceCodes marks character codes this package can positively
	// identify as whitespace (via a simple font's resolved Unicode
	// encoding - see BuildSimpleEncoding), so that Glyph paints nothing
	// for them instead of notdefGlyph's placeholder box even when no
	// real outline is available. A composite (Type0) font has no
	// per-code Unicode information without parsing a /ToUnicode CMap -
	// deliberately out of scope for this phase (see this package's
	// top-level doc comment) - so spaceCodes is always empty for one,
	// and a CID font falling back to notdefGlyph may draw a box for a
	// space character; this is a narrow, documented, purely cosmetic gap
	// (see docs/capability-matrix.md), not a positioning error, since
	// Width is unaffected either way.
	spaceCodes map[int]bool
}

// Width returns code's advance width in glyph space (1000 units per
// em - see the widths field's doc comment), the unit PDF's own /Widths
// and /W arrays already use, so a caller multiplies by FontSize/1000 to
// get a text-space advance (see internal/content's text.go).
func (f *Font) Width(code int) float64 {
	if w, ok := f.widths[code]; ok {
		return w
	}
	return f.defaultWidth
}

// Glyph returns code's glyph outline as a graphics.Path in glyph space
// (1000 units per em, y-up, origin at the glyph's own left sidebearing
// baseline origin - i.e. already scaled to the same units Width uses),
// or nil if nothing should be painted for this code (a legitimately
// blank glyph such as space, or a code this package positively knows is
// whitespace even without a real outline - see spaceCodes). Any other
// unresolvable code falls back to notdefGlyph's placeholder box - see
// this type's doc comment for the full policy.
func (f *Font) Glyph(code int) *graphics.Path {
	if f.glyphSource != nil && f.lookupGID != nil {
		if gid, ok := f.lookupGID(code); ok {
			if outline, ok := f.glyphSource.GlyphOutline(gid); ok {
				if len(outline.Subpaths) == 0 {
					// A real, deliberately blank glyph (space is the
					// usual example) - paint nothing, and definitely not
					// a fallback box, since this is not a missing-glyph
					// case at all.
					return nil
				}
				return scaleGlyph(outline, f.glyphSource.UnitsPerEm())
			}
		}
	}
	if f.spaceCodes[code] {
		return nil
	}
	return notdefGlyph(f.Width(code))
}

// scaleGlyph rescales outline (in the embedded font program's own
// "design units", unitsPerEm per em) into glyph space (1000 units per
// em, the fixed convention /Widths and /W values use - see the widths
// field's doc comment), so that Glyph's caller never needs to know a
// particular embedded font's own unitsPerEm value at all. unitsPerEm is
// validated as nonzero by parseSfnt before an sfntFont is ever
// constructed (see truetype.go), so this division is always safe.
func scaleGlyph(outline *graphics.Path, unitsPerEm uint16) *graphics.Path {
	scale := 1000 / float64(unitsPerEm)
	scaled := &graphics.Path{}
	appendTransformed(scaled, outline, graphics.Matrix{A: scale, D: scale})
	return scaled
}

// notdefGlyph builds this package's fallback "missing glyph" indicator:
// a small hollow rectangle (the conventional appearance real-world font
// rendering software uses for a glyph it could not find, sometimes
// called a "tofu" box) sized to fit within width (the glyph's own
// advance width, in glyph space - see the widths field's doc comment),
// so it never visually overlaps a neighboring character. It returns nil
// - painting nothing at all - for a width too narrow to draw a
// recognizable box in, which is a safer fallback than an ill-proportioned
// sliver.
//
// The two rectangles are wound in opposite directions (the outer
// counter-clockwise, the inner clockwise) deliberately: internal/raster
// resolves overlapping subpaths using the nonzero winding rule by
// default (see graphics.NonZero), under which two oppositely-wound
// nested rectangles fill only the ring between them - the same
// "winding cancellation" mechanism internal/raster's own tests exercise
// for stroke-to-fill outlines (see internal/raster/scanline_test.go).
func notdefGlyph(width float64) *graphics.Path {
	const (
		margin    = 60.0
		capHeight = 660.0
		minWidth  = 160.0
	)
	if width < minWidth {
		return nil
	}
	outer := [4]graphics.Point{
		{X: margin, Y: 0},
		{X: width - margin, Y: 0},
		{X: width - margin, Y: capHeight},
		{X: margin, Y: capHeight},
	}
	p := &graphics.Path{}
	p.AppendRect(outer)

	inset := margin + 40
	if width-2*inset > 20 && capHeight-2*inset > 20 {
		// Wound opposite to outer (see doc comment above): start at the
		// bottom-left corner and go up-right-down instead of
		// right-up-left, which is what makes this a clockwise loop
		// against the outer rectangle's counter-clockwise one.
		inner := [4]graphics.Point{
			{X: inset, Y: inset},
			{X: inset, Y: capHeight - inset},
			{X: width - inset, Y: capHeight - inset},
			{X: width - inset, Y: inset},
		}
		p.AppendRect(inner)
	}
	return p
}

// Load builds a Font from dict, a font dictionary already resolved
// enough to read its /Subtype (i.e. dict itself may come straight from
// a page's /Resources /Font entry without having been passed through
// resolver.ResolveDictionary first, since dict's own top-level entries
// are read individually here through resolveIfRef as needed - matching
// how internal/image.Decode's own dict parameter is used).
//
// Load does not fail merely because a font is a kind this package
// cannot extract real glyph outlines from (Type 1, CFF, non-embedded, or
// Type 3 - see docs/capability-matrix.md): it returns a usable Font in
// every case, falling back to notdefGlyph for painting and this
// package's best-effort width information for positioning, per this
// type's doc comment. It returns a non-nil error only when dict itself
// is unusable as a font dictionary at all (not a Dictionary, or missing
// /Subtype).
func Load(dict syntax.Dictionary, resolver Resolver) (*Font, error) {
	subtype, _ := dict["Subtype"].(syntax.Name)
	switch subtype {
	case "Type0":
		return loadType0Font(dict, resolver)
	default:
		// Type1, TrueType, MMType1, Type3, and anything else - all
		// handled as a "simple" (single-byte-code) font; see simple.go's
		// doc comment for how it treats Type3 and any other subtype it
		// cannot extract outlines from.
		return loadSimpleFont(dict, resolver)
	}
}

// numberValue extracts a float64 from a syntax.Integer or syntax.Real,
// the two PDF object types that represent numbers - the same small
// helper every package in this module that walks dictionaries ends up
// needing its own copy of (see, for example, internal/image/decode.go's
// identical function).
func numberValue(obj syntax.Object) (float64, bool) {
	switch v := obj.(type) {
	case syntax.Integer:
		return float64(v), true
	case syntax.Real:
		return float64(v), true
	default:
		return 0, false
	}
}

// dictValue resolves key within dict (following one level of indirect
// reference, if present - see resolveIfRef) and reports whether it is a
// syntax.Dictionary.
func dictValue(resolver Resolver, dict syntax.Dictionary, key syntax.Name) (syntax.Dictionary, bool) {
	v, ok := dict[key]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(resolver, v)
	if err != nil {
		return nil, false
	}
	d, ok := resolved.(syntax.Dictionary)
	return d, ok
}
