package content

import (
	"github.com/tucats/pdf-viewer/internal/fonts"
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Phase 4's text operators: the "BT"/"ET" text
// object delimiters, the text-state operators ("Tc", "Tw", "Tz", "TL",
// "Tf", "Tr", "Ts"), the text-positioning operators ("Td", "TD", "Tm",
// "T*"), and the text-showing operators ("Tj", "TJ", "'", "\""). See
// interpret.go's exec method for where each of these operator names is
// routed into the functions below, and internal/fonts for how a font
// name resolves to something that can answer "what glyph outline and
// advance width does this character code have".
//
// # PDF text layout, briefly
//
// Showing text involves three coordinate spaces layered on top of each
// other, each with its own transform:
//
//  1. Glyph space: a font's own internal coordinate system, fixed at
//     1000 units per em by PDF convention regardless of what units the
//     underlying font program actually uses internally (see
//     internal/fonts.Font's doc comment on scaleGlyph, which already
//     normalizes to this) - the space /Widths values and Font.Glyph's
//     returned outlines are both expressed in.
//  2. Text space: glyph space scaled down by 1/1000, then further
//     scaled by the current font size, horizontal scaling, and text
//     rise - see textRenderingMatrix below, which builds exactly the
//     matrix the PDF specification's section 9.4.4 defines for this.
//  3. User space: text space transformed by the *text matrix* (Tm - not
//     part of the graphics state; see the interpreter's tm/tlm fields
//     below), which "Td"/"TD"/"Tm"/"T*" update as text is shown, one
//     glyph's advance at a time.
//
// User space is then transformed to device space by the ordinary CTM,
// exactly like every other path this package builds - a shown glyph's
// outline ultimately becomes just another Fill graphics.DrawOp, sharing
// internal/raster's one rasterization code path with ordinary vector
// fills (see interpret.go's fillCurrentPath, which this file's showGlyph
// mirrors).

// textInterpreterState is the subset of interpreter state specific to
// text objects: the text matrix and line matrix (Tm/Tlm), and the
// font cache. Per the PDF specification (9.4.1), Tm and Tlm are *not*
// part of the graphics state a "q" saves and a "Q" restores - they exist
// only within one "BT"/"ET" text object, reset to identity by every
// "BT" - which is why these live directly on the interpreter (like the
// in-progress path - see interpreter's own doc comment) rather than on
// graphics.State, exactly the same reasoning that keeps the in-progress
// path off graphics.State.
type textInterpreterState struct {
	tm, tlm graphics.Matrix

	// fontCache remembers every font this interpreter has already built
	// via internal/fonts.Load, keyed by its /Resources /Font resource
	// name, so that a page showing the same font across many "Tf"
	// operators (the overwhelmingly common case: a font is selected once
	// and reused for many text runs) only pays for parsing its embedded
	// font program - which, for a several-hundred-glyph embedded
	// TrueType program, is real work - once rather than on every "Tf".
	fontCache map[syntax.Name]*fonts.Font
}

// beginText implements "BT": resets the text and line matrices to
// identity, per the specification. Text state parameters (character
// spacing, word spacing, font, ...) are untouched - they live in
// graphics.State, not here (see graphics.State's own doc comment on its
// text-state fields), and persist across a "BT"/"ET" pair exactly like
// FillColor or LineWidth would.
func (in *interpreter) beginText() {
	in.text.tm = graphics.Identity()
	in.text.tlm = graphics.Identity()
}

// setFont implements "Tf": operands are [fontName Name, size number].
// It looks up fontName in /Resources /Font (loading and caching it via
// internal/fonts.Load on first use - see textInterpreterState.fontCache)
// and stores the result on the graphics state along with the requested
// size. A name that cannot be resolved to a font dictionary at all
// leaves st.Font nil (or whatever it previously was) - text shown with
// no font selected is silently skipped by showText below, the same
// "missing resource" tolerance this project applies to an unresolvable
// XObject name (see interpret.go's doXObject).
func (in *interpreter) setFont(st *graphics.State, operands []syntax.Object) error {
	if len(operands) != 2 {
		return pdferror.Malformedf("\"Tf\" expects 2 operands, got %d", len(operands))
	}
	name, ok := operands[0].(syntax.Name)
	if !ok {
		return pdferror.Malformedf("\"Tf\" first operand must be a name, found %T", operands[0])
	}
	size, ok := numberValue(operands[1])
	if !ok {
		return pdferror.Malformedf("\"Tf\" second operand must be a number, found %T", operands[1])
	}
	st.FontSize = size

	if f, ok := in.lookupFont(name); ok {
		st.Font = f
	}
	return nil
}

// lookupFont resolves name in /Resources /Font, building (and caching -
// see fontCache) a *fonts.Font for it via internal/fonts.Load. It
// returns ok=false for every way this can come up empty without the
// content actually being malformed: no /Resources, no /Font dictionary,
// no entry under name, or an entry that does not resolve to a
// dictionary - mirroring interpret.go's lookupXObject.
func (in *interpreter) lookupFont(name syntax.Name) (*fonts.Font, bool) {
	if f, ok := in.text.fontCache[name]; ok {
		return f, true
	}
	if in.resources == nil || in.resolver == nil {
		return nil, false
	}
	fontsEntry, ok := in.resources["Font"]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(in.resolver, fontsEntry)
	if err != nil {
		return nil, false
	}
	fontsDict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return nil, false
	}
	entry, ok := fontsDict[name]
	if !ok {
		return nil, false
	}
	resolvedEntry, err := resolveIfRef(in.resolver, entry)
	if err != nil {
		return nil, false
	}
	dict, ok := resolvedEntry.(syntax.Dictionary)
	if !ok {
		return nil, false
	}

	f, err := fonts.Load(dict, in.resolver)
	if err != nil || f == nil {
		return nil, false
	}
	if in.text.fontCache == nil {
		in.text.fontCache = make(map[syntax.Name]*fonts.Font)
	}
	in.text.fontCache[name] = f
	return f, true
}

// moveTextLine implements "Td" and (via a leading update first) "TD":
// translates the *line* matrix by (tx, ty) in unscaled text space, and
// makes that the new text matrix too - per the specification, every
// text-positioning operator moves relative to the start of the current
// line, not wherever the text matrix happens to be after the last glyph
// shown, which is exactly what maintaining Tlm separately from Tm
// achieves (see textInterpreterState's doc comment).
func (in *interpreter) moveTextLine(tx, ty float64) {
	in.text.tlm = graphics.Translate(tx, ty).Mul(in.text.tlm)
	in.text.tm = in.text.tlm
}

// nextLine implements "T*": move to the start of the next line, using
// the negative of the current leading (Tl) - per the specification,
// "T*" is exactly equivalent to "0 -Tl Td".
func (in *interpreter) nextLine(st *graphics.State) {
	in.moveTextLine(0, -st.Leading)
}

// textRenderingMatrix builds the matrix that maps glyph space (see this
// file's doc comment) all the way to device space, for a glyph shown
// while st and in.text.tm hold their current values: composing (in
// order) the fixed 1/1000 glyph-space-to-text-space scale every font
// this package supports uses, the font-size/horizontal-scaling/rise
// parameters matrix the specification calls Trm's left factor, the text
// matrix, and finally the CTM. See Matrix.Mul's own doc comment for why
// composing left-to-right here means "apply this transform first" -
// exactly the order the specification's own formula, Trm = [Tfs*Th 0; 0
// Tfs; 0 Trise] x Tm x CTM, describes.
func textRenderingMatrix(st *graphics.State, tm graphics.Matrix) graphics.Matrix {
	const glyphSpaceUnitsPerEm = 1000
	glyphToText := graphics.Scale(1/float64(glyphSpaceUnitsPerEm), 1/float64(glyphSpaceUnitsPerEm))
	trm := graphics.Matrix{A: st.FontSize * (st.Hscale / 100), D: st.FontSize, F: st.Rise}
	return glyphToText.Mul(trm).Mul(tm).Mul(st.CTM)
}

// paintsGlyphs reports whether st's current text rendering mode (Tr)
// calls for painting glyph fills at all. Mode 3 ("invisible" - used for
// an OCR text layer placed over a scanned page image, positioned
// correctly but never meant to be seen) and mode 7 ("add to clip path"
// only, no paint) both paint nothing; every other mode (0 fill, 1
// stroke, 2 fill+stroke, 4-6 the same three combined with clipping) is
// treated as an ordinary fill using the current fill color - stroking
// glyph outlines and accumulating a text clip path are both real PDF
// features this package does not implement (see
// docs/capability-matrix.md), so "paint it filled" is this package's
// documented approximation for every mode that paints anything at all.
func paintsGlyphs(st *graphics.State) bool {
	switch st.RenderMode {
	case 3, 7:
		return false
	default:
		return true
	}
}

// showGlyph paints one glyph (already resolved to a device-space
// outline) as a Fill DrawOp, sharing internal/raster's one
// rasterization path with every other filled shape this package
// produces (see interpret.go's fillCurrentPath, which this mirrors) -
// TrueType glyph outlines wind in the nonzero-fill convention (an outer
// contour and any counter-wound inner "hole" contour, such as the bowl
// of a "d" or an "o"), which is exactly graphics.NonZero.
func (in *interpreter) showGlyph(st *graphics.State, glyph *graphics.Path, trm graphics.Matrix) {
	if glyph == nil || len(glyph.Subpaths) == 0 || !paintsGlyphs(st) {
		return
	}
	device := &graphics.Path{}
	for _, sp := range glyph.Subpaths {
		if len(sp.Points) == 0 {
			continue
		}
		x, y := trm.Apply(sp.Points[0].X, sp.Points[0].Y)
		device.MoveTo(graphics.Point{X: x, Y: y})
		for _, p := range sp.Points[1:] {
			x, y := trm.Apply(p.X, p.Y)
			device.LineTo(graphics.Point{X: x, Y: y})
		}
		if sp.Closed {
			device.Close()
		}
	}
	in.list = append(in.list, graphics.DrawOp{
		Path:      device,
		Rule:      graphics.NonZero,
		Color:     st.FillColor,
		Clips:     st.Clips,
		Alpha:     st.FillAlpha,
		BlendMode: st.BlendMode,
	})
}

// showText implements the core of "Tj" (and, through it, "'", "\"", and
// each string element of a "TJ" array): decode s into character codes
// per the current font's code width (see fonts.Font.TwoByteCodes), and
// for each code, paint its glyph (if any - see showGlyph) at the text
// matrix's current position and then advance the text matrix by that
// glyph's width, per the specification's formula (9.4.3):
//
//	tx = ((w0 - Tj/1000) * Tfs + Tc + Tw) * Th
//
// (the "Tj/1000" term is TJ's own per-element numeric adjustment,
// handled separately by showAdjustment below, not by this function) -
// word spacing (Tw) applies only to the single-byte code 32, and never
// to any byte within a multi-byte (Type0) code, exactly as the
// specification requires. Only horizontal writing (the vast majority of
// real-world PDF text) is implemented; a vertical-mode Type0 font
// ("Identity-V" - see internal/fonts/cid.go) still shows text and
// advances correctly along the same horizontal formula rather than
// vertically, a documented simplification rather than a silently wrong
// (as opposed to merely differently laid out) result.
func (in *interpreter) showText(st *graphics.State, s []byte) {
	font, ok := st.Font.(*fonts.Font)
	if !ok || font == nil {
		// No font selected (or something other than a *fonts.Font was
		// somehow stored - never true in practice, since setFont is the
		// only writer of st.Font - see graphics.State.Font's doc
		// comment): nothing can be measured or painted, so this string is
		// silently skipped, exactly like "Do" with an unresolvable
		// XObject name.
		return
	}
	for _, code := range decodeCodes(font, s) {
		trm := textRenderingMatrix(st, in.text.tm)
		in.showGlyph(st, font.Glyph(code), trm)

		w0 := font.Width(code) / 1000
		tw := 0.0
		if !font.TwoByteCodes && code == 32 {
			tw = st.WordSpace
		}
		tx := (w0*st.FontSize + st.CharSpace + tw) * (st.Hscale / 100)
		in.text.tm = graphics.Translate(tx, 0).Mul(in.text.tm)
	}
}

// showAdjustment implements one numeric element of a "TJ" array: per the
// specification, the number is expressed in thousandths of a unit of
// text space and is *subtracted* from the current horizontal (or,
// unimplemented here - see showText's doc comment - vertical) text
// position, so a positive number moves text backward (tightening
// letter-spacing, e.g. for kerning pairs) and a negative number moves it
// forward (adding extra space without a literal space character).
func (in *interpreter) showAdjustment(st *graphics.State, amount float64) {
	tx := -(amount / 1000) * st.FontSize * (st.Hscale / 100)
	in.text.tm = graphics.Translate(tx, 0).Mul(in.text.tm)
}

// decodeCodes splits s into character codes according to font's code
// width: two bytes (big-endian) per code for a Type0/Identity-H(V) font
// (see fonts.Font.TwoByteCodes), one byte per code otherwise. A trailing
// odd byte on a two-byte-code font (malformed content) is simply
// dropped rather than treated as an error, consistent with this
// project's general tolerance for minor real-world content stream
// corruption (see, for example, internal/syntax.Lexer's doc comment).
func decodeCodes(font *fonts.Font, s []byte) []int {
	if !font.TwoByteCodes {
		codes := make([]int, len(s))
		for i, b := range s {
			codes[i] = int(b)
		}
		return codes
	}
	codes := make([]int, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		codes = append(codes, int(s[i])<<8|int(s[i+1]))
	}
	return codes
}

// singleArrayOperand requires operands to contain exactly one
// syntax.Array, returning it - the operand shape "TJ" expects (a single
// array mixing strings to show and numeric position adjustments).
func singleArrayOperand(operands []syntax.Object) (syntax.Array, bool) {
	if len(operands) != 1 {
		return nil, false
	}
	arr, ok := operands[0].(syntax.Array)
	return arr, ok
}

// requireString requires operands to contain exactly one syntax.String,
// returning its raw bytes - the operand shape "Tj" (and, indirectly,
// "'") expects.
func requireString(operands []syntax.Object) ([]byte, error) {
	if len(operands) != 1 {
		return nil, pdferror.Malformedf("expected exactly 1 string operand, got %d", len(operands))
	}
	str, ok := operands[0].(syntax.String)
	if !ok {
		return nil, pdferror.Malformedf("expected a string operand, found %T", operands[0])
	}
	return []byte(str), nil
}
