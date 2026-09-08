// Package pdfviewer is an embeddable PDF renderer written entirely in Go.
//
// # Project status
//
// This package is under active, phased development. The full design
// rationale, the phased implementation plan, and the internal package
// layout are documented in the repository's README.md file — read that
// first if you are trying to understand *why* the code is organized the
// way it is. This doc comment only covers what is true of the code that
// actually exists right now.
//
// As of Phase 1, this package can open a PDF document (Open, OpenFile),
// report its page count, and return each page's box in PDF points
// (Page.Bounds) - see internal/parser and internal/model for how that
// is implemented. Page.Render and Page.Thumbnail exist, matching the
// README's Draft Public API, but are not implemented yet: they always
// return an error wrapping ErrUnsupported until Phase 2 and Phase 3,
// respectively, add a rasterizer. Phase 1 also only supports the
// classic, table-based cross-reference format, not PDF 1.5+
// cross-reference streams - see internal/parser's package doc comment
// and docs/capability-matrix.md for what is and is not supported yet.
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
