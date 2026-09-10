package main

import "github.com/tucats/pdf-viewer/internal/fonts"

// This file hand-builds a tiny, entirely synthetic Type 1 font program
// via internal/fonts's own exported test/fixture-only encoder
// (fonts.EncodeType1FontProgram - see that package's type1_encode.go
// doc comment for why it exists and why it is exported at all), the
// same "construct exactly the bytes this project's own code needs,
// with no externally sourced binary and its own license/provenance
// question" approach truetype.go's buildTestFontProgram uses for its
// own synthetic TrueType program - see that file's doc comment.
//
// Like buildTestFontProgram, this font has exactly two glyphs:
// ".notdef" (empty) and a filled square occupying glyph-space
// [150,850]x[150,850] out of a 1000-unit em (the same glyphSquareMin/
// Max truetype.go's own square glyph uses - see that file), reachable
// by the glyph name "A" via this package's small built-in
// glyph-name-to-Unicode-rune table (internal/fonts/encoding.go's
// glyphNameToRune) once a PDF font dictionary's own /Encoding resolves
// character code 0x41 to the rune 'A' - see buildTextSimpleType1 in
// text.go.
//
// Type 1 charstring operator byte values used below, named for
// readability at each call site (see internal/fonts/type1.go's exec
// for the authoritative operator table these mirror - not exported
// from that package, since only this file needs them, so they are
// simply repeated here as this file's own small local constants).
const (
	t1OpHsbw      = 13
	t1OpEndchar   = 14
	t1OpRlineto   = 5
	t1OpClosepath = 9
	t1OpRmoveto   = 21
)

// t1num encodes v using Type 1 Charstring's own compact integer operand
// encoding - a thin wrapper around fonts.EncodeType1Int purely so the
// charstring-building code below reads as a flat byte-slice append
// chain instead of repeated fonts.EncodeType1Int(...) calls.
func t1num(v int) []byte { return fonts.EncodeType1Int(v) }

// buildTestType1FontProgram returns a complete, minimal Type 1 font
// program (suitable for a PDF /FontFile stream) with the specification
// default of 1000 units per em and one visible glyph (named "A", the
// same square shape and glyph-space extent buildTestFontProgram's
// TrueType glyph 1 draws), plus its own /Length1 and /Length2 - see
// simple.go's readType1FontFileStream in internal/fonts for how a real
// /FontFile stream dictionary carries those two lengths alongside the
// font program bytes themselves.
func buildTestType1FontProgram() (data []byte, length1, length2 int) {
	notdef := append(append(t1num(0), t1num(0)...), t1OpHsbw, t1OpEndchar)

	var square []byte
	square = append(square, t1num(glyphSquareMin)...)
	square = append(square, t1num(700)...) // wx (advance width) - unused by this project, see opHsbw's own doc comment
	square = append(square, t1OpHsbw)
	square = append(square, t1num(0)...)
	square = append(square, t1num(glyphSquareMin)...)
	square = append(square, t1OpRmoveto)
	square = append(square, t1num(glyphSquareMax-glyphSquareMin)...)
	square = append(square, t1num(0)...)
	square = append(square, t1OpRlineto)
	square = append(square, t1num(0)...)
	square = append(square, t1num(glyphSquareMax-glyphSquareMin)...)
	square = append(square, t1OpRlineto)
	square = append(square, t1num(-(glyphSquareMax - glyphSquareMin))...)
	square = append(square, t1num(0)...)
	square = append(square, t1OpRlineto)
	square = append(square, t1OpClosepath, t1OpEndchar)

	return fonts.EncodeType1FontProgram([]fonts.Type1TestGlyph{
		{Name: ".notdef", Charstring: notdef},
		{Name: "A", Charstring: square},
	}, nil, 0)
}
