package pdfviewer

import (
	"errors"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file defines the error conventions used across the whole
// pdf-viewer module, not just this package. Every internal package
// (internal/source, internal/syntax, internal/parser, and so on) wraps
// its errors using the sentinel values declared here so that a caller
// can always ask "what *kind* of problem was this?" using errors.Is,
// regardless of which internal layer produced the error.
//
// If you are new to Go: errors.Is(err, target) walks the chain of
// wrapped errors (created with fmt.Errorf and the "%w" verb, or with
// errors.Join) and reports whether any error in that chain is equal to
// target. This lets a low-level function attach a specific, human
// readable message (e.g. "xref table missing trailer") while still
// letting calling code make decisions based on a small, stable set of
// sentinel errors (e.g. "was this ErrMalformed?"). See the Go standard
// library "errors" package documentation for more detail.
//
// ErrMalformed and ErrUnsupported are declared in the internal
// internal/pdferror package, not here, and simply re-exported by the two
// variables below. That indirection exists so that internal packages
// (which cannot import this root package without creating an import
// cycle, since this package imports them) can still produce errors that
// wrap the exact same sentinel values a caller checks against here — see
// the comment on internal/pdferror for the full explanation. From the
// perspective of anyone importing pdfviewer, ErrMalformed and
// ErrUnsupported behave exactly as if they had been declared directly in
// this file.

// Sentinel errors classify *why* an operation failed. Callers should use
// errors.Is to test for these rather than comparing strings, and should
// use errors.As if a future error type carries structured detail (for
// example a byte offset) beyond its message.
var (
	// ErrMalformed indicates that input claiming to be a PDF file (or a
	// PDF construct, such as a content stream) violates the structural
	// rules of the PDF specification badly enough that it cannot be
	// interpreted at all. This is the error to return for garbled,
	// truncated, or deliberately hostile input.
	ErrMalformed = pdferror.ErrMalformed

	// ErrUnsupported indicates that the input is structurally valid PDF
	// but uses a feature this package does not implement yet (or ever
	// intends to implement — see the Non-goals section of the README).
	// Because the project is implemented in phases, this error is
	// expected to occur frequently on real-world files until later
	// phases land; it must never be confused with ErrMalformed, since a
	// well-formed-but-unsupported file is not itself invalid.
	ErrUnsupported = pdferror.ErrUnsupported

	// ErrClosed indicates that a method was called on a Document (or a
	// value obtained from one, such as a Page) after Close had already
	// been called on that Document. Pages must not outlive their
	// document; see the package-level Draft Public API notes in the
	// README for the ownership rule this enforces.
	ErrClosed = errors.New("pdfviewer: document is closed")

	// ErrPageIndex indicates that a page index passed to Document.Page
	// was negative or greater than or equal to the document's page
	// count. Page indexing is zero-based.
	ErrPageIndex = errors.New("pdfviewer: page index out of range")
)

// MalformedErrorf builds an error that wraps ErrMalformed with a
// formatted, human-readable message describing exactly what was wrong
// and, wherever practical, where in the input it was found (for example
// a byte offset or object number). Internal packages should use the
// equivalent internal/pdferror.Malformedf instead of constructing ad hoc
// errors, so that ErrMalformed remains reliably detectable with
// errors.Is anywhere in the call stack; this exported copy exists so
// code outside this module (or example code in this module) can build
// consistent errors too.
//
// Example:
//
//	if len(header) < 5 {
//		return MalformedErrorf("truncated header: only %d bytes", len(header))
//	}
func MalformedErrorf(format string, args ...any) error {
	return pdferror.Malformedf(format, args...)
}

// UnsupportedErrorf builds an error that wraps ErrUnsupported with a
// formatted message naming the specific feature that was encountered but
// not implemented. See MalformedErrorf for the rationale; the two
// constructors are kept deliberately parallel so the two failure classes
// are easy to tell apart at a glance in calling code.
//
// Example:
//
//	if filter == "JBIG2Decode" {
//		return UnsupportedErrorf("stream filter %q", filter)
//	}
func UnsupportedErrorf(format string, args ...any) error {
	return pdferror.Unsupportedf(format, args...)
}
