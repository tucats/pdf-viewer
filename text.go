package pdfviewer

// This file implements Phase 10's public text-extraction API: TextGlyph,
// the shape Page.Text (page.go) returns, and the small conversion from
// internal/content's own TextGlyph (deliberately a separate type - see
// that package's text_extract.go doc comment for why internal/content
// cannot import this package, and so cannot return this exported type
// directly). See docs/PLAN2.md's Phase 10 entry for the capability this
// implements, and page.go's Text method for the full documentation of
// what it recovers and how.

// TextGlyph is one glyph of text recovered from a page by Page.Text -
// PDF's own smallest unit of shown text (one character *code* from a
// "Tj"/"TJ"/"'"/"\"" string), not necessarily one Unicode character
// (Text can be empty, or more than one character long - see its own doc
// comment).
type TextGlyph struct {
	// Text is this glyph's decoded Unicode text, recovered from the
	// font's own /ToUnicode CMap where one exists, or (for a simple font
	// with none) its resolved /Encoding - see
	// internal/fonts.Font.TextForCode's doc comment for exactly which of
	// those two answers this can be. It is normally exactly one
	// character, but can be more than one (a ligature glyph, such as one
	// glyph standing for "ffi", whose /ToUnicode entry spells out all
	// three characters at once) or empty (no /ToUnicode CMap and no
	// /Encoding-based answer either - most commonly a Type0/CID font
	// with no /ToUnicode CMap at all, which has no other way to guess a
	// code's meaning). An empty Text is a real, honest "this package
	// does not know what this glyph means", not a bug - the glyph is
	// still present in Page.Text's result with a correct position and
	// width.
	Text string

	// X, Y is this glyph's baseline origin - the point it is drawn
	// relative to - in the same page-default-user-space PDF points
	// Page.Bounds reports: not device pixels, and unaffected by any
	// RenderOptions.Scale a caller might separately use with Render,
	// since text extraction has nothing to do with rendering resolution.
	X, Y float64

	// Width is this glyph's advance width, in the same page-space units
	// as X/Y - how far the pen moves along the text line after this
	// glyph, already including character spacing, word spacing, and
	// horizontal scaling exactly the way Render's own text-positioning
	// math does (see internal/content's showText). Summing consecutive
	// glyphs' Width values (for glyphs on the same line, same text
	// object) reproduces the same line layout Render would have
	// painted.
	Width float64

	// FontSize is the font size (PDF's "Tf" second operand, in unscaled
	// text-space units) active when this glyph was shown - included
	// because neither Width nor a glyph's own metrics alone say how
	// large the glyph actually appears on the page.
	FontSize float64
}
