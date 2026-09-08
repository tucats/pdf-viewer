// Package parser will resolve the object-level structure of a PDF file
// built on top of internal/syntax's tokens: the cross-reference table
// (classic xref tables and cross-reference streams), the trailer,
// incremental updates (a PDF file may contain several trailers layered
// on top of each other as it was incrementally saved), and object
// streams (compressed containers that hold multiple indirect objects).
//
// The key responsibility of this package is turning "indirect object
// reference N generation G" into the object's value *lazily* — i.e.
// without eagerly parsing every object in the file just to open it — and
// distinguishing the different ways that lookup can legitimately fail:
// the reference is missing entirely, it resolves to the PDF null object,
// it resolves to something structurally malformed, or it resolves to
// something this package does not support parsing yet. These are
// different situations for a caller and must not be collapsed into one
// generic "not found" error.
//
// This package is also where resource limits belong: bounded recursion
// when one object refers to another, bounded stream sizes, bounded
// nesting of arrays/dictionaries, and bounded decompression, all so that
// a hostile file cannot force unbounded memory or CPU use. See the
// "Dependency and safety policy" section of the repository README.
//
// This package is planned for Phase 1 ("File structure and safe object
// model") of the project's phased plan. It intentionally contains no
// code yet — Phase 0 only establishes the package skeleton and its place
// in the pipeline described in the README's "Proposed Internal Layout"
// section.
package parser
