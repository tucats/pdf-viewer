// Package fonts implements PDF font decoding: turning a font dictionary
// (and, where present, its embedded font program) into a Font -
// character encodings and code-to-glyph mappings, glyph widths, glyph
// outlines, and this project's documented fallback policy for a glyph
// or font program this package cannot resolve. internal/content's
// text-showing operators (Phase 4, "Tj"/"TJ"/"'"/"\"" and friends - see
// that package's text.go) are the only consumer of this package.
//
// # What this package supports
//
// Load (font.go) dispatches a font dictionary's /Subtype to one of two
// loaders, both converging on the same Font type:
//
//   - simple.go: "simple" fonts (/Type1, /TrueType, /MMType1, /Type3 -
//     one byte per character code). Character encoding is resolved per
//     the specification's own rules for /Encoding (a predefined name,
//     or a dictionary with a /BaseEncoding and a /Differences array -
//     see encoding.go).
//   - cid.go: "composite" (/Type0) fonts, restricted to the
//     "Identity-H"/"Identity-V" encodings (two bytes per character code,
//     numerically equal to the glyph's CID - see cid.go's doc comment
//     for the full rationale on this scope decision).
//
// Real glyph *outlines* are extracted from an embedded TrueType program
// (/FontFile2 on a simple font's own /FontDescriptor, or on a Type0
// font's descendant CIDFontType2's /FontDescriptor - see truetype.go and
// cmap.go) or an embedded CFF program (/FontFile3, including an
// OpenType/CFF wrapper - see cff.go, which also serves a
// CIDFontType0 descendant's own CFF program). A Type 1 program
// (/FontFile) and any non-embedded font (no /FontFile* entry at all -
// there being no system font service this package is permitted to
// query, see below) still produce a fully usable Font, just one whose
// Glyph method paints a small placeholder box (notdefGlyph, in font.go)
// instead of a real outline - see Font's own doc comment for the
// complete missing-glyph policy, and docs/capability-matrix.md for the
// authoritative, up-to-date support matrix.
//
// # A hard constraint: no system font service
//
// This package must never assume a system font is available and must
// never silently invoke a platform font service (fontconfig, Core Text,
// DirectWrite, and so on), because doing so would make rendering output
// depend on what happens to be installed on the machine running the
// code - exactly the kind of non-portable, hard-to-reproduce behavior
// the repository README's "Dependency and safety policy" section exists
// to prevent. Every fallback in this package (see Font's doc comment) is
// an explicit, in-package, documented decision instead.
//
// # Text extraction (Phase 10) is a separate capability from painting
//
// Per the repository README's Phase 4 plan, "text extraction" (getting
// the Unicode *text* a page contains back out, as opposed to painting
// its glyphs) is deliberately kept separate from painting, so it could
// be added later without changing how page rendering itself works - see
// docs/PLAN2.md's Phase 10. It has been added: tounicode.go parses a
// font's /ToUnicode CMap, and Font.TextForCode (font.go) is this
// package's one rune/text-level entry point, answering "what Unicode
// text does character code X mean" - a question Glyph and Width (this
// package's original, painting-oriented API) never ask and never
// answer. internal/content's ExtractText (not the text-painting
// showText) is TextForCode's only caller, keeping the separation this
// section describes intact at the internal/content layer too.
package fonts
