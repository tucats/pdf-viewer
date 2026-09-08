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
// As of Phase 0, this package contains no PDF parsing or rendering logic
// yet. Phase 0 is scaffolding: the Go module, the error conventions used
// throughout the codebase, the internal package skeleton described in the
// README, a hand-authored test fixture corpus, a capability matrix, and a
// CI check that keeps the build free of CGO and other native dependencies.
// The public API sketched in the README (Document, Page, Open, OpenFile,
// RenderOptions, and so on) will be implemented starting in Phase 1.
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
// Once page access is implemented (Phase 1), pages will be indexed
// zero-based, matching Go slice conventions and the page-oriented APIs of
// the comparable projects surveyed in the README.
package pdfviewer
