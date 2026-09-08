// Package graphics will implement the PDF graphics state machine: the
// current transformation matrix and its stack (PDF's "q"/"Q" save and
// restore operators), path construction, fill and stroke styles, line
// width/cap/join/dash, clipping regions, and solid color handling in the
// color spaces supported at the time.
//
// This package sits between internal/content (which decodes *what*
// operators a content stream contains) and internal/raster (which turns
// fully-resolved paths and fills into actual pixels): internal/content
// drives this package's state machine by replaying operators against
// it, and the resulting paths/state are what internal/raster consumes.
// Keeping graphics state tracking separate from both parsing and pixel
// output means the coordinate math and state stack can be tested with
// plain geometric assertions, independent of any particular page's
// content stream or rasterization details.
//
// This package is planned to begin in Phase 2 ("Content streams and a
// minimal raster backend") of the project's phased plan and will gain
// transparency groups, blend modes, and patterns in Phase 5. It
// intentionally contains no code yet — Phase 0 only establishes the
// package skeleton and its place in the pipeline described in the
// README's "Proposed Internal Layout" section.
package graphics
