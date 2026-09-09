package fonts

import (
	"encoding/binary"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// This file implements just enough of the TrueType/OpenType "sfnt" font
// file format (the format an embedded /FontFile2 stream contains) to
// extract glyph outlines and look glyphs up by character code. It is not
// a general-purpose font library: it reads exactly the four tables this
// package's rendering needs ("head", "maxp", "loca", "glyf", "cmap") and
// ignores everything else (hinting instructions, kerning, ligatures,
// OpenType layout features, and so on all matter to a full text-layout
// engine but not to placing glyph outlines this project already knows the
// position and advance width of via /Widths and PDF's own text-showing
// operators - see internal/content's text.go).
//
// # If you are new to font formats: what "sfnt" and "glyf" mean
//
// A TrueType (or OpenType-with-TrueType-outlines) font file is a small
// container format: a fixed-size header naming how many data "tables"
// follow, then a table directory (one fixed-size record per table
// naming a 4-byte tag like "glyf", plus its byte offset and length
// within the file), then the tables themselves in no particular order.
// This is conceptually similar to a ZIP file's central directory, just
// without compression. The tables this package reads:
//
//   - "head": one small fixed-layout table with, among other things, how
//     many font design units make up one "em" (unitsPerEm - almost
//     always 1000 or 2048 in real fonts) and which of two width formats
//     the "loca" table below uses.
//   - "maxp": how many glyphs the font defines (numGlyphs) - used only to
//     bounds-check glyph indices before trusting them as array indices.
//   - "loca" ("location"): an array of numGlyphs+1 byte offsets into
//     "glyf", such that glyph N's outline data spans
//     glyf[loca[N]:loca[N+1]] - an empty range (loca[N] == loca[N+1])
//     means glyph N has no outline at all (this is how TrueType
//     represents a blank glyph, such as space).
//   - "glyf": the actual outline data for every glyph, back to back,
//     addressed via "loca" - see parseGlyf below for the two shapes an
//     entry can take (simple or composite).
//   - "cmap": one or more subtables mapping *character codes* (Unicode
//     code points, for the subtable this package prefers - see
//     selectCmapSubtable) to *glyph indices* - the "N" that "loca"/"glyf"
//     above are addressed by. A character code and a glyph index are
//     different numbering spaces: this package's Font.Glyph (font.go)
//     always goes through this lookup (or, for a Type 0/CID font,
//     through /CIDToGIDMap - see cid.go) rather than assuming they
//     coincide.

// sfntVersion* are the four-byte "version" tags an sfnt font file can
// start with, identifying what kind of font program follows. This
// package reads this tag in two places: parseTableDirectory (below, for
// an ordinary single-face file) and probe.go's TrueType Collection
// handling (for a ".ttc" file bundling several faces, each of which
// starts with one of these same four tags at its own table directory).
//
//   - sfntVersionTrueType and sfntVersionAppleTrue both mean "this font's
//     outlines live in a 'glyf' table" - the only outline format
//     truetype.go's glyf/loca parsing (below) understands. The Apple
//     "true" tag is a historical variant of the same on-disk shape,
//     occasionally still seen from older Mac tools.
//   - sfntVersionOTTO means "this font's outlines live in a 'CFF' table
//     instead" (OpenType with PostScript/CFF outlines) - a completely
//     different outline encoding from this file's own glyf/loca parsing
//     below, read instead by cff.go's separate CFF/Type 2 Charstring
//     support (see docs/FONTS.md's Phase 3 and probe.go's FontFace.Outline,
//     which dispatches to whichever of the two a given face actually
//     has). An OTTO file's other tables ("name", "OS/2") are laid out
//     identically to a TrueType-flavored file's, though, which is why
//     probe.go accepts this tag at the table-directory level regardless
//     of which outline table ends up present.
//   - sfntVersionTTC ("ttcf") means "this is not a single font at all,
//     but a TrueType Collection: a small header listing the byte offset
//     of each bundled face's own table directory" - see probe.go's
//     parseTTCHeader. It is checked by the caller before ever reaching
//     parseTableDirectory, since a TTC header has a different shape than
//     an ordinary table directory (no table records of its own).
const (
	sfntVersionTrueType  = 0x00010000
	sfntVersionAppleTrue = 0x74727565 // the four ASCII bytes "true"
	sfntVersionOTTO      = 0x4F54544F // the four ASCII bytes "OTTO"
	sfntVersionTTC       = 0x74746366 // the four ASCII bytes "ttcf"
)

// sfntFont holds the parsed subset of an embedded TrueType font program
// this package uses. A zero-value sfntFont (as returned by parseSfnt on
// any failure) has no tables and glyf/lookupGID/glyphIndexForRune all
// correctly report "not found" rather than panicking - see this file's
// parsing functions, which are written to fail closed (return ok=false)
// rather than trust a hostile or truncated font program's own internal
// lengths and offsets.
type sfntFont struct {
	unitsPerEm uint16
	numGlyphs  uint16
	loca       []uint32 // numGlyphs+1 entries; glyf offsets
	glyfData   []byte
	cmap       cmapSubtable
}

// parseSfnt parses data (an already filter-decoded /FontFile2 stream's
// bytes) into an sfntFont, returning ok=false for any font program this
// package cannot use - a genuinely corrupt file, one missing a required
// table, or one using an sfnt version this package does not recognize.
// This is deliberately not an error return: an unusable embedded font
// program is this package's documented "no outlines available" case
// (see font.go's notdefGlyph), handled by falling back to this
// project's missing-glyph policy rather than failing the page's render
// - exactly the same tolerance internal/content applies to an
// unrecognized content stream operator.
func parseSfnt(data []byte) (sfntFont, bool) {
	return parseSfntAt(data, 0)
}

// parseSfntAt is parseSfnt's more general form, used by probe.go to parse
// one face out of a TrueType Collection (".ttc") file: dirOffset is the
// byte offset, within data, of that face's own table directory (0 for an
// ordinary single-face file, which is exactly what parseSfnt above
// passes). See parseTableDirectory's doc comment for why every *other*
// offset this function reads is still measured from the start of data
// regardless of dirOffset - a ".ttc" face's tables can be shared with
// other faces in the same file, so they are always addressed relative to
// the whole file, never relative to one face's own directory.
func parseSfntAt(data []byte, dirOffset int) (sfntFont, bool) {
	tables, _, ok := parseTableDirectory(data, dirOffset)
	if !ok {
		return sfntFont{}, false
	}

	head, ok := tables["head"]
	if !ok || len(head) < 54 {
		return sfntFont{}, false
	}
	unitsPerEm := binary.BigEndian.Uint16(head[18:20])
	indexToLocFormat := int16(binary.BigEndian.Uint16(head[50:52]))
	if unitsPerEm == 0 {
		return sfntFont{}, false
	}

	maxp, ok := tables["maxp"]
	if !ok || len(maxp) < 6 {
		return sfntFont{}, false
	}
	numGlyphs := binary.BigEndian.Uint16(maxp[4:6])

	locaRaw, ok := tables["loca"]
	if !ok {
		return sfntFont{}, false
	}
	loca, ok := parseLoca(locaRaw, numGlyphs, indexToLocFormat)
	if !ok {
		return sfntFont{}, false
	}

	glyf, ok := tables["glyf"]
	if !ok {
		return sfntFont{}, false
	}

	var cmap cmapSubtable
	if cmapRaw, ok := tables["cmap"]; ok {
		cmap, _ = parseCmap(cmapRaw)
	}

	return sfntFont{
		unitsPerEm: unitsPerEm,
		numGlyphs:  numGlyphs,
		loca:       loca,
		glyfData:   glyf,
		cmap:       cmap,
	}, true
}

// parseTableDirectory reads one sfnt table directory - the fixed-size
// header naming how many tables follow, then one fixed-size record per
// table - and returns a map from each table's 4-byte tag (like "head" or
// "glyf") to that table's own byte slice (a subslice of data, not a
// copy), plus the four-byte version tag the directory itself started
// with (one of the sfntVersion* constants above; the caller decides what
// that tag means for its purposes - see, for example, probe.go's
// HasOutlines).
//
// dirOffset is where the directory itself begins within data: 0 for an
// ordinary single-face sfnt file (parseSfnt's case), or a nonzero byte
// offset for one face of a TrueType Collection (probe.go's case).
// Crucially, dirOffset only affects where the *directory* is read from -
// every table record's own offset field, once read, is still used as a
// byte position measured from the very start of data (index 0), never
// relative to dirOffset. This matches how the sfnt/TTC file formats
// actually lay things out: a TrueType Collection's individual faces can
// *share* tables (for example, several faces reusing one "glyf" table),
// which is only possible because every table offset in the whole file
// addresses the same shared coordinate space rather than being local to
// one face's own directory.
//
// This function bounds-checks every offset and length against len(data)
// before slicing, so a truncated or hostile file cannot cause an
// out-of-range panic - it is reported as ok=false instead, exactly like
// every other malformed-input case in this file.
func parseTableDirectory(data []byte, dirOffset int) (map[string][]byte, uint32, bool) {
	if dirOffset < 0 || dirOffset+12 > len(data) {
		return nil, 0, false
	}
	version := binary.BigEndian.Uint32(data[dirOffset : dirOffset+4])
	// sfntVersionTrueType and sfntVersionAppleTrue are the two sfnt
	// versions whose outlines this package's glyf/loca parsing (below)
	// can actually read. sfntVersionOTTO (CFF-outline OpenType) is also
	// accepted at *this* level - its table directory has the exact same
	// shape as a TrueType-flavored one, so the tables it does share
	// (like "name" and "OS/2") can still be read out of it by probe.go
	// for characterizing a candidate font file - but parseSfnt/
	// parseSfntAt above will still fail once they look for a "glyf"
	// table an OTTO file never has (see this file's doc comment on
	// outline-source coverage, and probe.go's HasOutlines field).
	switch version {
	case sfntVersionTrueType, sfntVersionAppleTrue, sfntVersionOTTO:
	default:
		return nil, 0, false
	}
	numTables := int(binary.BigEndian.Uint16(data[dirOffset+4 : dirOffset+6]))
	const recordSize = 16
	recordsStart := dirOffset + 12
	dirEnd := recordsStart + numTables*recordSize
	if numTables < 0 || dirEnd > len(data) {
		return nil, 0, false
	}

	tables := make(map[string][]byte, numTables)
	for i := 0; i < numTables; i++ {
		rec := data[recordsStart+i*recordSize : recordsStart+(i+1)*recordSize]
		tag := string(rec[0:4])
		offset := binary.BigEndian.Uint32(rec[8:12])
		length := binary.BigEndian.Uint32(rec[12:16])
		start, end := int64(offset), int64(offset)+int64(length)
		if start < 0 || end < start || end > int64(len(data)) {
			// A single bad table record does not doom the whole font -
			// tables this package doesn't always need (kern, hinting
			// programs, and - depending on the caller - even name/OS/2)
			// having bogus offsets should not prevent reading the tables
			// it does need, so this record is simply skipped rather than
			// failing the whole parse.
			continue
		}
		tables[tag] = data[start:end]
	}
	return tables, version, true
}

// parseLoca reads the "loca" table into numGlyphs+1 plain uint32 glyph
// offsets, expanding the format's two on-disk representations (a
// "short" format packing each offset as a big-endian uint16 that must be
// doubled to get the real byte offset, or a "long" format storing the
// real byte offset directly as a big-endian uint32 - selected by head's
// indexToLocFormat field, 0 or 1 respectively) into one uniform
// in-memory shape so parseGlyf never needs to know which format the
// original file used.
func parseLoca(raw []byte, numGlyphs uint16, format int16) ([]uint32, bool) {
	n := int(numGlyphs) + 1
	out := make([]uint32, n)
	switch format {
	case 0: // short format: 2 bytes per entry, value*2 is the real offset
		if len(raw) < n*2 {
			return nil, false
		}
		for i := 0; i < n; i++ {
			out[i] = uint32(binary.BigEndian.Uint16(raw[i*2:i*2+2])) * 2
		}
	case 1: // long format: 4 bytes per entry, the real offset directly
		if len(raw) < n*4 {
			return nil, false
		}
		for i := 0; i < n; i++ {
			out[i] = binary.BigEndian.Uint32(raw[i*4 : i*4+4])
		}
	default:
		return nil, false
	}
	return out, true
}

// maxCompositeDepth bounds how many levels deep a composite glyph (a
// glyph defined as a set of other, already-defined glyphs placed at an
// offset - see parseCompositeGlyph) may reference further composite
// glyphs, defending against a maliciously or accidentally
// self-referential chain of composites that would otherwise recurse
// forever - the same "bounded work against hostile input" policy this
// project applies throughout (see, for example, internal/image's
// maxMaskRecursionDepth). Real-world fonts essentially never nest
// composites more than one or two levels deep.
const maxCompositeDepth = 8

// UnitsPerEm reports how many font design units make up one em in this
// program's own outline coordinate space - see this type's unitsPerEm
// field doc comment. It exists (alongside cffFont's identically-shaped
// method, cff.go) so that font.go's Font type can scale a glyph outline
// from either kind of embedded font program through one shared
// glyphOutlineSource interface, without needing to know which one
// produced it.
func (f *sfntFont) UnitsPerEm() uint16 {
	return f.unitsPerEm
}

// GIDForRune looks up a GID by Unicode rune via this font's own cmap
// table - see the cmap field's doc comment and cmapSubtable.Lookup.
// Added alongside cffFont's identically-shaped method (cff.go) so that
// font.go's runeGlyphSource interface is satisfied by either kind of
// embedded or substitute font program, letting Phase 4's substitution
// wiring (simple.go's trySubstitute) look a rune up the same way
// regardless of which one it is working with.
func (f *sfntFont) GIDForRune(r rune) (uint16, bool) {
	return f.cmap.Lookup(r)
}

// GlyphOutline returns glyph index gid's outline as a graphics.Path in
// the font's own native "design units" coordinate space (i.e. not yet
// scaled by unitsPerEm - see Font.Glyph in font.go, which does that
// scaling once the caller also knows what to scale *to*), and ok=false
// if gid is out of range or its outline data is malformed. A glyph with
// no contours at all (such as space) is a perfectly valid outcome and
// returns an empty Path with ok=true, distinct from ok=false ("could not
// read this glyph") - see Font.Glyph for why that distinction matters
// (it is what lets this package avoid drawing a fallback box over a
// glyph that is legitimately blank).
func (f *sfntFont) GlyphOutline(gid uint16) (*graphics.Path, bool) {
	return f.glyphOutline(gid, 0)
}

func (f *sfntFont) glyphOutline(gid uint16, depth int) (*graphics.Path, bool) {
	if depth > maxCompositeDepth {
		return nil, false
	}
	if int(gid)+1 >= len(f.loca) {
		return nil, false
	}
	start, end := f.loca[gid], f.loca[gid+1]
	if end < start || int64(end) > int64(len(f.glyfData)) {
		return nil, false
	}
	if start == end {
		// No outline (e.g. space) - a valid, deliberately empty glyph.
		return &graphics.Path{}, true
	}
	data := f.glyfData[start:end]
	if len(data) < 10 {
		return nil, false
	}
	numContours := int16(binary.BigEndian.Uint16(data[0:2]))
	if numContours >= 0 {
		return parseSimpleGlyph(data, numContours)
	}
	return f.parseCompositeGlyph(data, depth)
}

// parseSimpleGlyph decodes an ordinary ("simple", TrueType's own term
// for "not composite") glyph outline: numContours closed contours, each
// a mix of on-curve and off-curve (quadratic Bézier control) points.
//
// # If you are new to font outlines: on-curve vs. off-curve points
//
// A contour's points are not simply "connect the dots" - TrueType
// outlines are made of quadratic Bézier curve segments, and a point can
// be "on-curve" (the curve actually passes through it, like an ordinary
// polygon vertex) or "off-curve" (a control point that pulls the curve
// toward it without the curve passing through it - see
// graphics.Path.QuadTo). Two consecutive off-curve points imply an
// on-curve point exactly halfway between them that the font file does
// not bother storing explicitly (this is what buildContourPath's
// "implied midpoint" handling below reconstructs) - a common
// space-saving convention for smooth curves that alternate direction.
func parseSimpleGlyph(data []byte, numContours int16) (*graphics.Path, bool) {
	pos := 10
	endPts := make([]uint16, numContours)
	for i := range endPts {
		if pos+2 > len(data) {
			return nil, false
		}
		endPts[i] = binary.BigEndian.Uint16(data[pos : pos+2])
		pos += 2
	}
	numPoints := 0
	if numContours > 0 {
		numPoints = int(endPts[numContours-1]) + 1
	}

	if pos+2 > len(data) {
		return nil, false
	}
	instructionLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
	pos += 2 + instructionLen
	if pos > len(data) {
		return nil, false
	}

	// Flags: one byte per point, with a repeat-count mechanism (bit 0x08)
	// so a run of identically-flagged points does not need one byte
	// each.
	const (
		flagOnCurve = 0x01
		flagRepeat  = 0x08
	)
	flags := make([]byte, numPoints)
	for i := 0; i < numPoints; {
		if pos >= len(data) {
			return nil, false
		}
		f := data[pos]
		pos++
		flags[i] = f
		i++
		if f&flagRepeat != 0 {
			if pos >= len(data) {
				return nil, false
			}
			repeat := int(data[pos])
			pos++
			for r := 0; r < repeat && i < numPoints; r++ {
				flags[i] = f
				i++
			}
		}
	}

	xs, ok := readCoords(data, &pos, flags, 0x02, 0x10)
	if !ok {
		return nil, false
	}
	ys, ok := readCoords(data, &pos, flags, 0x04, 0x20)
	if !ok {
		return nil, false
	}

	path := &graphics.Path{}
	start := 0
	for _, end := range endPts {
		end := int(end)
		if end < start || end >= numPoints {
			return nil, false
		}
		buildContourPath(path, flags[start:end+1], xs[start:end+1], ys[start:end+1])
		start = end + 1
	}
	return path, true
}

// coordFlag/shortFlag select, for the X or Y coordinate array
// respectively, which two flag bits readCoords consults: bit `short`
// means "this coordinate is a single delta byte" (its sign given by bit
// `same`, since a short delta has no room for a sign bit of its own),
// and - when `short` is clear - bit `same` instead means "this
// coordinate is identical to the previous point's" (a zero delta,
// stored as no bytes at all) versus "a full signed 16-bit delta
// follows". Both X and Y arrays use this same two-bit-per-point scheme,
// just with different bit positions in the shared flags byte (0x02/0x10
// for X, 0x04/0x20 for Y), which is why this helper takes them as
// parameters rather than being duplicated per axis.
func readCoords(data []byte, pos *int, flags []byte, short, same byte) ([]int32, bool) {
	out := make([]int32, len(flags))
	var v int32
	for i, f := range flags {
		switch {
		case f&short != 0:
			if *pos >= len(data) {
				return nil, false
			}
			d := int32(data[*pos])
			*pos++
			if f&same == 0 {
				d = -d
			}
			v += d
		case f&same == 0:
			if *pos+2 > len(data) {
				return nil, false
			}
			d := int32(int16(binary.BigEndian.Uint16(data[*pos : *pos+2])))
			*pos += 2
			v += d
		// else: f&same != 0 and not short - delta is zero, v unchanged.
		default:
		}
		out[i] = v
	}
	return out, true
}

// contourPoint is one point of a glyph contour together with whether it
// is on-curve, used only while buildContourPath assembles a contour -
// see that function's doc comment.
type contourPoint struct {
	graphics.Point
	onCurve bool
}

// buildContourPath appends one closed contour to path, given its points'
// on-curve flags and absolute (already-delta-accumulated) coordinates,
// implementing the on-curve/off-curve quadratic outline convention
// described on parseSimpleGlyph's doc comment.
//
// This works in two passes, which is what keeps it simple to get right:
//
//  1. "Expand" the raw point list by inserting an implied on-curve
//     point - the same "an on-curve point exactly halfway between them"
//     construction the TrueType format itself defines - between every
//     pair of *consecutive* off-curve points, including the pair that
//     wraps around from the contour's last point back to its first.
//     After this pass, no two consecutive points (including
//     wraparound) are both off-curve, which is what makes pass 2 simple:
//     every off-curve point is now immediately followed by an on-curve
//     one, so it is always exactly the single control point of one
//     quadratic curve segment, never part of an ambiguous longer run.
//  2. Rotate the expanded list to start at an on-curve point (there is
//     always at least one after pass 1, for any contour with 2 or more
//     points - see the loop below), then walk it once: an on-curve
//     point is a straight line from wherever the path currently is; an
//     off-curve point is always followed by an on-curve point (per pass
//     1), so the pair together form one graphics.Path.QuadTo call.
func buildContourPath(path *graphics.Path, flags []byte, xs, ys []int32) {
	n := len(flags)
	if n == 0 {
		return
	}
	raw := make([]contourPoint, n)
	for i := range raw {
		raw[i] = contourPoint{
			Point:   graphics.Point{X: float64(xs[i]), Y: float64(ys[i])},
			onCurve: flags[i]&0x01 != 0,
		}
	}

	expanded := make([]contourPoint, 0, n+1)
	for i := 0; i < n; i++ {
		cur := raw[i]
		expanded = append(expanded, cur)
		next := raw[(i+1)%n]
		if !cur.onCurve && !next.onCurve {
			expanded = append(expanded, contourPoint{Point: midpoint(cur.Point, next.Point), onCurve: true})
		}
	}

	start := -1
	for i, p := range expanded {
		if p.onCurve {
			start = i
			break
		}
	}
	if start == -1 {
		// Degenerate: every point in this contour is off-curve even
		// after inserting implied midpoints, which can only happen for a
		// contour of fewer than 2 points - not a shape worth drawing.
		return
	}
	m := len(expanded)
	ordered := make([]contourPoint, m)
	for i := 0; i < m; i++ {
		ordered[i] = expanded[(start+i)%m]
	}

	path.MoveTo(ordered[0].Point)
	for i := 1; i < m; {
		p := ordered[i]
		if p.onCurve {
			path.LineTo(p.Point)
			i++
			continue
		}
		// p is off-curve; pass 1 guarantees the point after it is
		// on-curve, so this pair is exactly one quadratic segment.
		end := ordered[(i+1)%m]
		path.QuadTo(p.Point, end.Point)
		i += 2
	}
	// The final segment back to ordered[0] (closing the contour) is
	// Close()'s job, not drawn explicitly here - see Path.Close's doc
	// comment. If the loop above already ended exactly on ordered[0]
	// (possible when the last pair's QuadTo lands there), this adds a
	// harmless zero-length closing edge.
	path.Close()
}

func midpoint(a, b graphics.Point) graphics.Point {
	return graphics.Point{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
}

// parseCompositeGlyph decodes a "composite" glyph: one built by placing
// one or more other glyphs (each identified by its own glyph index, and
// possibly itself composite, up to maxCompositeDepth) at a translation
// offset and, optionally, under a 2x2 linear transform. This is how
// accented Latin characters are very commonly built in real TrueType
// fonts (e.g. "Ã‰" as the base glyph "E" plus the combining glyph
// "acute" shifted above it) rather than having a fully independent
// outline of their own.
//
// Only the common ARGS_ARE_XY_VALUES case (component arguments are a
// literal (dx, dy) offset) is implemented; the alternative
// "point-matching" placement (arguments name two point indices, one in
// each glyph, that must coincide) is rare in practice and is treated as
// a zero offset instead of being rejected outright - a documented
// approximation, in the same spirit as this project's round-only stroke
// joins, rather than failing the whole glyph over an unusual placement
// mode.
func (f *sfntFont) parseCompositeGlyph(data []byte, depth int) (*graphics.Path, bool) {
	const (
		flagArgsAreWords   = 0x0001
		flagArgsAreXY      = 0x0002
		flagHaveScale      = 0x0008
		flagMoreComponents = 0x0020
		flagHaveXYScale    = 0x0040
		flagHave2x2        = 0x0080
	)
	path := &graphics.Path{}
	pos := 10
	for {
		if pos+4 > len(data) {
			return nil, false
		}
		flags := binary.BigEndian.Uint16(data[pos : pos+2])
		glyphIndex := binary.BigEndian.Uint16(data[pos+2 : pos+4])
		pos += 4

		var dx, dy float64
		if flags&flagArgsAreWords != 0 {
			if pos+4 > len(data) {
				return nil, false
			}
			a1 := int16(binary.BigEndian.Uint16(data[pos : pos+2]))
			a2 := int16(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
			pos += 4
			if flags&flagArgsAreXY != 0 {
				dx, dy = float64(a1), float64(a2)
			}
		} else {
			if pos+2 > len(data) {
				return nil, false
			}
			a1 := int8(data[pos])
			a2 := int8(data[pos+1])
			pos += 2
			if flags&flagArgsAreXY != 0 {
				dx, dy = float64(a1), float64(a2)
			}
		}

		xform := graphics.Identity()
		switch {
		case flags&flagHave2x2 != 0:
			if pos+8 > len(data) {
				return nil, false
			}
			a := f2dot14(data[pos : pos+2])
			b := f2dot14(data[pos+2 : pos+4])
			c := f2dot14(data[pos+4 : pos+6])
			d := f2dot14(data[pos+6 : pos+8])
			pos += 8
			xform = graphics.Matrix{A: a, B: b, C: c, D: d}
		case flags&flagHaveXYScale != 0:
			if pos+4 > len(data) {
				return nil, false
			}
			sx := f2dot14(data[pos : pos+2])
			sy := f2dot14(data[pos+2 : pos+4])
			pos += 4
			xform = graphics.Matrix{A: sx, D: sy}
		case flags&flagHaveScale != 0:
			if pos+2 > len(data) {
				return nil, false
			}
			s := f2dot14(data[pos : pos+2])
			pos += 2
			xform = graphics.Matrix{A: s, D: s}
		}
		xform.E, xform.F = dx, dy

		child, ok := f.glyphOutline(glyphIndex, depth+1)
		if ok {
			appendTransformed(path, child, xform)
		}
		// A component that fails to decode is skipped rather than
		// failing the whole composite glyph - one bad accent mark should
		// not blank out the base letter it decorates.

		if flags&flagMoreComponents == 0 {
			break
		}
	}
	return path, true
}

// f2dot14 decodes a 2.14 fixed-point number (2 integer bits including
// sign, 14 fractional bits - the format TrueType uses for the small
// scale factors in a composite glyph's optional transform) from its
// big-endian 16-bit encoding.
func f2dot14(b []byte) float64 {
	return float64(int16(binary.BigEndian.Uint16(b))) / 16384
}

// appendTransformed copies every subpath of src into dst with xform
// applied to each point - used to place a composite glyph's component
// outlines (which are decoded in their own, untransformed glyph space)
// at their final position and scale within the composite glyph.
func appendTransformed(dst, src *graphics.Path, xform graphics.Matrix) {
	for _, sp := range src.Subpaths {
		if len(sp.Points) == 0 {
			continue
		}
		x, y := xform.Apply(sp.Points[0].X, sp.Points[0].Y)
		dst.MoveTo(graphics.Point{X: x, Y: y})
		for _, p := range sp.Points[1:] {
			x, y := xform.Apply(p.X, p.Y)
			dst.LineTo(graphics.Point{X: x, Y: y})
		}
		if sp.Closed {
			dst.Close()
		}
	}
}
