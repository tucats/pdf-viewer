// Package source will provide random-access, bounds-checked reading over
// the raw bytes of a PDF file.
//
// PDF is not a stream format: object locations are found by seeking to
// byte offsets recorded in cross-reference tables, and the file trailer
// is conventionally read from the *end* of the file first. Every layer
// above this one (internal/syntax, internal/parser, and so on) needs to
// read arbitrary byte ranges cheaply and safely, without accidentally
// reading past the end of the underlying data — a malformed or hostile
// PDF will frequently claim offsets and lengths that do not correspond
// to real data, and this package is where that gets caught early rather
// than surfacing as a panic deep in a parser.
//
// This package is planned for Phase 1 ("File structure and safe object
// model") of the project's phased plan; see the repository README for
// the full plan. It intentionally contains no code yet — Phase 0 only
// establishes the package skeleton, naming, and its place in the
// pipeline described in the README's "Proposed Internal Layout" section.
//
// When implemented, this package is expected to wrap an io.ReaderAt plus
// a known size (matching the public pdfviewer.Open signature) and expose
// small, explicit helpers such as reading N bytes at an offset or
// reading the last N bytes of the file, each returning an error — never
// panicking — when the requested range falls outside the file.
package source
