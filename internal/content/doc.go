// Package content will interpret a page's content stream — the sequence
// of PDF operators (drawing commands like "m" moveto, "l" lineto, "re"
// rectangle, "f" fill, text-showing operators, and so on) that describes
// what actually appears on a page — and turn it into a display list: an
// intermediate, already-decoded representation of the drawing operations
// a page requires.
//
// A display list is preferred over drawing directly while parsing the
// content stream (see the README's "Proposed Internal Layout" section)
// because it lets internal/raster, internal/content's consumers, and any
// future alternate backend all work from the same already-validated
// representation without re-parsing the file, and it is what makes
// bounded, cacheable Thumbnail rendering possible later without a second
// interpretation of the PDF.
//
// This package is planned to begin in Phase 2 ("Content streams and a
// minimal raster backend") of the project's phased plan and will be
// extended through later phases as text (Phase 4) and advanced graphics
// (Phase 5) are added. It intentionally contains no code yet — Phase 0
// only establishes the package skeleton and its place in the pipeline
// described in the README's "Proposed Internal Layout" section.
package content
