// Package content interprets a page's content stream - the sequence of
// PDF operators (drawing commands like "m" moveto, "l" lineto, "re"
// rectangle, "f" fill, text-showing operators, and so on) that describes
// what actually appears on a page - and turns it into a
// graphics.DisplayList: an intermediate, already-resolved representation
// of the drawing operations a page requires, in device space.
//
// Parse (operator.go) tokenizes already filter-decoded content stream
// bytes into a sequence of Operators, reusing internal/syntax's Lexer
// and ParseValue for operand syntax but handling operator keywords
// itself, since internal/syntax's object grammar has no notion of them.
// Interpret (interpret.go) then replays those Operators against a
// graphics.Stack, producing the DisplayList.
//
// # Operator coverage (Phase 2)
//
// Graphics state ("q"/"Q"/"cm"/"w"/"J"/"j"/"M"), path construction
// ("m"/"l"/"c"/"v"/"y"/"h"/"re"), path painting and clipping
// ("S"/"s"/"f"/"F"/"f*"/"B"/"B*"/"b"/"b*"/"n"/"W"/"W*"), and solid
// DeviceGray/DeviceRGB/DeviceCMYK color ("g"/"G"/"rg"/"RG"/"k"/"K", plus
// a component-count-based fallback for "sc"/"SC"/"scn"/"SCN") are
// implemented - this is the README's Phase 2 scope ("the PDF graphics
// state, coordinate transforms, paths, fills, strokes, clipping, line
// styles, and solid colors"). Text showing/positioning operators,
// XObject painting ("Do" - forms and images), shading ("sh"), marked
// content, dash patterns, and ExtGState parameters (transparency, blend
// modes) are Phase 3-5 work and are currently either silently skipped
// (Interpret still renders whatever it does understand) or explicitly
// rejected as unsupported (inline images, pattern color spaces) - see
// Interpret's doc comment and docs/capability-matrix.md for the
// authoritative, up-to-date breakdown.
package content
