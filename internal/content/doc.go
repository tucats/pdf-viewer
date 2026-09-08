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
// # Operator coverage
//
// Graphics state ("q"/"Q"/"cm"/"w"/"J"/"j"/"M"), path construction
// ("m"/"l"/"c"/"v"/"y"/"h"/"re"), path painting and clipping
// ("S"/"s"/"f"/"F"/"f*"/"B"/"B*"/"b"/"b*"/"n"/"W"/"W*"), and solid
// DeviceGray/DeviceRGB/DeviceCMYK color ("g"/"G"/"rg"/"RG"/"k"/"K", plus
// a component-count-based fallback for "sc"/"SC"/"scn"/"SCN") were
// Phase 2's scope ("the PDF graphics state, coordinate transforms,
// paths, fills, strokes, clipping, line styles, and solid colors").
//
// Phase 3 adds images: "Do" (image.go's doXObject), when the named
// XObject's /Subtype is /Image, and "BI"/"ID"/"EI" inline images
// (operator.go's Parse calls inlineimage.go's parseInlineImage; the
// resulting Operator's InlineImage is handled by image.go's
// doInlineImage) both decode through internal/image.Decode and append an
// image graphics.DrawOp - see image.go's package-level doc comment. A
// "Do" naming a /Form XObject (forms are not implemented) is silently
// skipped exactly like any other unrecognized operator.
//
// Text showing/positioning operators, shading ("sh"), marked content,
// dash patterns, and ExtGState parameters (transparency, blend modes)
// remain Phase 4-5 work and are silently skipped (Interpret still
// renders whatever it does understand) - see Interpret's doc comment and
// docs/capability-matrix.md for the authoritative, up-to-date breakdown
// of what is implemented versus merely tolerated.
package content
