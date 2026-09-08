// Package raster will implement a deterministic, pure-Go software
// rasterizer that turns a display list (produced by internal/content,
// driving internal/graphics) into a standard library image.Image — the
// primary output of this whole project, per the README's "Decisions So
// Far" section.
//
// "Deterministic" matters here specifically for testing: Phase 2's exit
// criteria call for comparing rendered output against checked-in
// reference images with a documented tolerance, which only works if
// rendering the same input twice on the same platform produces the same
// (or reliably near-identical) pixels. This package must not depend on
// CGO, native graphics libraries, or platform-specific rendering
// backends — see the "Dependency and safety policy" section of the
// README.
//
// This package is planned to begin in Phase 2 ("Content streams and a
// minimal raster backend") of the project's phased plan and will gain
// image decoding in Phase 3, text/glyph rendering in Phase 4, and
// transparency/pattern/shading support in Phase 5. It intentionally
// contains no code yet — Phase 0 only establishes the package skeleton
// and its place in the pipeline described in the README's "Proposed
// Internal Layout" section.
package raster
