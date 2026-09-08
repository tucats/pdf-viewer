// Package fonts will implement PDF font decoding: reading embedded font
// programs (Type 1, TrueType, and Type 0/CID composite fonts, added
// incrementally), character encodings and code-to-glyph mappings, glyph
// widths, and this project's fallback policy for missing glyphs or fonts
// that are referenced but not embedded.
//
// A hard constraint carried over from the "Dependency and safety policy"
// section of the README applies specifically to this package: it must
// not assume a system font is available and must never silently invoke
// a platform font service (fontconfig, Core Text, DirectWrite, and so
// on), because doing so would make rendering output depend on what
// happens to be installed on the machine running the code — exactly the
// kind of non-portable, hard-to-reproduce behavior this project exists
// to avoid. Any fallback must be an explicit, documented, in-package
// decision.
//
// This package is planned for Phase 4 ("Text and fonts") of the
// project's phased plan. It intentionally contains no code yet —
// Phase 0 only establishes the package skeleton and its place in the
// pipeline described in the README's "Proposed Internal Layout" section.
package fonts
