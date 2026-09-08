// Package pdfviewer is an embeddable PDF renderer written entirely in Go.
//
// # Project status
//
// This package is under active, phased development. The full design
// rationale, the phased implementation plan, and the internal package
// layout are documented in the repository's README.md file — read that
// first if you are trying to understand *why* the code is organized the
// way it is. docs/capability-matrix.md is the authoritative, row-by-row
// answer to "does this support PDF feature X" - this doc comment
// deliberately does not restate that level of detail, since it would
// only go stale again the way the paragraph it replaced did.
//
// As of Phase 6, this package can open a PDF document (Open, OpenFile),
// inspect it (PageCount, Page.Bounds), render a page to an image.Image
// (Page.Render) or a bounded thumbnail of one (Page.Thumbnail), and
// close it (Document.Close) - the full shape sketched in the README's
// Draft Public API, exercised end to end by the example programs under
// cmd/ (see cmd/pdfpreview, cmd/pdfthumbnails, and cmd/pdfexport).
// Rendering covers vector graphics, images, text with embedded fonts,
// and transparency/patterns/shadings/annotations to the extent recorded
// in docs/capability-matrix.md; a document using an unsupported feature
// this package can positively detect fails with an error wrapping
// ErrUnsupported (see errors.go's "Error taxonomy" section) rather than
// silently misrendering.
//
// See errors.go for this package's four-sentinel error taxonomy and
// Document's own doc comment for this package's concurrency guarantees
// (a single Document is not safe for concurrent use by multiple
// goroutines - open a separate Document per goroutine instead).
//
// # Design constraints that hold from the start
//
// Two constraints apply to every phase of this project, not just the
// finished product, so they are recorded here where any contributor will
// see them immediately:
//
//   - No CGO, no os/exec, no subprocess workers, and no runtime loading of
//     a system PDF library. The renderer must be pure Go so that it can be
//     cross-compiled and embedded without a native toolchain.
//   - No network access and no writes to the filesystem as a side effect
//     of opening or rendering a document. A PDF file is treated as
//     untrusted input: parsing it must not have side effects beyond
//     returning data or an error.
//
// # Page indexing
//
// Pages are indexed zero-based (Document.Page(0) is the first page),
// matching Go slice conventions and the page-oriented APIs of the
// comparable projects surveyed in the README.
package pdfviewer
