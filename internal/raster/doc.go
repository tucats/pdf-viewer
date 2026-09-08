// Package raster implements a deterministic, pure-Go software
// rasterizer that turns a graphics.DisplayList (produced by
// internal/content, driving internal/graphics) into a standard library
// *image.RGBA - the primary output of this whole project, per the
// README's "Decisions So Far" section.
//
// Render (render.go) is the package's entry point: it paints every
// DrawOp in a DisplayList onto a Canvas (canvas.go), which composites
// filled, anti-aliased, and clipped paths using rasterizeCoverage
// (scanline.go) - a scanline coverage-accumulation rasterizer
// implementing both of PDF's fill rules, shared by ordinary fills,
// stroke outlines (already converted to fill geometry by
// graphics.StrokeToFill before reaching this package), and clip
// evaluation, so this package only needs one core rasterization
// algorithm.
//
// "Deterministic" matters here specifically for testing: Phase 2's exit
// criteria call for comparing rendered output against checked-in
// reference images with a documented tolerance, which only works if
// rendering the same input twice on the same platform produces the same
// (or reliably near-identical) pixels - see scanline.go's doc comment on
// why every source of variation in the rasterizer itself is a fixed
// constant, never derived from timing, map iteration order, or viewport
// size. This package must not depend on CGO, native graphics libraries,
// or platform-specific rendering backends — see the "Dependency and
// safety policy" section of the README.
//
// This is Phase 2 ("Content streams and a minimal raster backend") work
// per the project's phased plan; image decoding is Phase 3,
// text/glyph rendering is Phase 4, and transparency/pattern/shading
// support is Phase 5 - see docs/capability-matrix.md.
package raster
