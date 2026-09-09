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
// ErrMalformed, ErrUnsupported, and ErrEncrypted are declared in the
// internal internal/pdferror package, not here, and simply re-exported
// by the variables below. That indirection exists so that internal
// packages (which cannot import this root package without creating an
// import cycle, since this package imports them) can still produce
// errors that wrap the exact same sentinel values a caller checks
// against here — see the comment on internal/pdferror for the full
// explanation. From the perspective of anyone importing pdfviewer,
// these behave exactly as if they had been declared directly in this
// file.
//
// # Error taxonomy (Phase 6 decision)
//
// Every error this package can return classifies into exactly one of
// four sentinels, checked with errors.Is:
//
//   - ErrMalformed: the input is not a well-formed PDF construct (a bad
//     cross-reference table, a truncated stream, an operand of the
//     wrong type for its operator, ...). This is a property of the
//     bytes given to this package, never of what this package happens
//     to implement.
//   - ErrUnsupported: the input is well-formed but uses a PDF feature
//     this package does not implement (a filter, color space, font
//     program format, or shading type it does not decode; see
//     docs/capability-matrix.md for the full, current list). More
//     features are expected to move out of this category over time as
//     later phases land; ErrMalformed inputs never will, since being
//     malformed is not an implementation gap.
//   - ErrEncrypted: a specific case of ErrUnsupported (see its own doc
//     comment below) for a document that declares an /Encrypt
//     dictionary. Split out from the general case because "this file
//     needs a password" is a meaningfully different situation for a
//     caller to react to than "this file uses a graphics feature we
//     don't render."
//   - ErrClosed / ErrPageIndex: caller-misuse errors describing how the
//     public API itself was called (a method invoked after Close, or an
//     out-of-range page index) rather than anything about the PDF file
//     being read. These do not wrap ErrMalformed or ErrUnsupported,
//     since they are not a statement about the file's content at all.
//
// A future error type that carries structured detail (a byte offset, an
// object number) beyond a formatted message should still wrap one of
// these four sentinels and be tested with errors.As, per this section's
// opening paragraph, rather than introducing a fifth top-level category.

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

	// ErrEncrypted indicates that Open or OpenFile was asked to open a
	// document whose trailer declares an /Encrypt dictionary that this
	// package could not open. As of Phase 7 (see docs/PLAN2.md), a
	// document protected with the Standard security handler now opens
	// and decrypts transparently whether its user password is empty -
	// the common "permissions-only, opens freely" case, such as many
	// bank statements, invoices, and print-to-PDF output, needing no
	// option at all - or non-empty, given the correct one via the
	// WithPassword OpenOption. ErrEncrypted is returned when the
	// password tried (the one WithPassword supplied, or the empty
	// string if it was not used at all) does not validate, or when the
	// document names a security handler other than Standard (a
	// public-key handler, with no known demand driving support for
	// one).
	//
	// errors.Is(err, ErrEncrypted) lets a caller react specifically to
	// "this file needs a password" (for example, to prompt for one and
	// retry with WithPassword) rather than a generic "unsupported PDF
	// feature" reaction. ErrEncrypted also always satisfies
	// errors.Is(err, ErrUnsupported), since it is wrapped as a specific
	// case of that broader sentinel - a caller written before
	// ErrEncrypted existed, checking only for ErrUnsupported, keeps
	// working unchanged.
	ErrEncrypted = pdferror.ErrEncrypted

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
