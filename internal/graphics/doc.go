// Package graphics implements the PDF graphics state machine: the
// current transformation matrix (matrix.go), path construction
// (path.go, including deterministic Bézier flattening), the "q"/"Q"
// save/restore stack and clip accumulation (state.go), stroke-to-fill
// outline geometry (stroke.go), and the solid-color and line-style types
// content operators set (style.go).
//
// This package sits between internal/content (which decodes *what*
// operators a content stream contains) and internal/raster (which turns
// fully-resolved, device-space paths into actual pixels):
// internal/content drives this package's Stack by replaying operators
// against it, building graphics.Path values in device space as it goes,
// and internal/raster consumes the resulting Paths, FillRules, and
// Colors without needing to know anything about content stream syntax.
// Keeping graphics state tracking separate from both parsing and pixel
// output means the coordinate math and state stack can be tested with
// plain geometric assertions, independent of any particular page's
// content stream or rasterization details.
//
// This is Phase 2 ("Content streams and a minimal raster backend") work
// per the project's phased plan; transparency groups, blend modes, and
// patterns are Phase 5 work and are not implemented here yet - see
// docs/capability-matrix.md.
package graphics
