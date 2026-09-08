// Package pdferror holds the ErrMalformed and ErrUnsupported sentinel
// error values shared by every layer of this module, from the
// lowest-level internal packages up through the public pdfviewer
// package.
//
// Why this lives in its own tiny internal package rather than directly
// in the root pdfviewer package: internal packages such as
// internal/source and internal/syntax need to be able to return
// genuinely-identical error values to what the public package exposes,
// so that a caller's errors.Is(err, pdfviewer.ErrMalformed) check works
// no matter how deep in the call stack the error actually originated.
// But an internal package cannot import the root pdfviewer package to
// get at those values, because the root package needs to import the
// internal packages to do its work — that would be an import cycle,
// which Go's compiler forbids. Putting the shared sentinel values in
// their own leaf package (one that imports nothing from this module)
// lets both "directions" import it safely. The root package's
// errors.go re-exports these same values under the pdfviewer.ErrXxx
// names described in the package documentation there; the two are
// identical values, not merely equal-looking copies, so errors.Is works
// either way a caller happens to reach for the sentinel.
package pdferror

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformed indicates that input claiming to be a PDF file (or a
	// PDF construct within one) violates the structural rules of the
	// PDF specification badly enough that it cannot be interpreted.
	ErrMalformed = errors.New("pdfviewer: malformed PDF input")

	// ErrUnsupported indicates that the input is structurally valid PDF
	// but uses a feature this module does not implement (yet, or ever).
	ErrUnsupported = errors.New("pdfviewer: unsupported PDF feature")

	// ErrEncrypted indicates that a document's trailer declares an
	// /Encrypt dictionary - i.e. the file uses one of PDF's security
	// handlers (Standard or public-key) to encrypt some or all of its
	// contents. This project implements no security handler at all (see
	// the root package's doc comment for the "Password handling"
	// decision and rationale), so opening such a file always fails.
	//
	// ErrEncrypted is built by wrapping ErrUnsupported directly (see its
	// declaration below), not by declaring an independent error value:
	// an encrypted document is a specific, common case of "structurally
	// valid PDF, unsupported feature", so any caller already checking
	// errors.Is(err, ErrUnsupported) keeps working unchanged after this
	// error was introduced, while a caller that wants to react
	// specifically to "this file needs a password we cannot supply"
	// (for example, to show a distinct "password required" message
	// rather than a generic "unsupported PDF feature" one) can check
	// errors.Is(err, ErrEncrypted) instead.
	ErrEncrypted = fmt.Errorf("pdfviewer: encrypted PDF documents are not supported: %w", ErrUnsupported)
)

// Malformedf builds an error wrapping ErrMalformed with a formatted
// detail message. See the root package's MalformedErrorf, which this
// function backs, for the full rationale and a usage example.
func Malformedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, args...))
}

// Unsupportedf builds an error wrapping ErrUnsupported with a formatted
// detail message. See the root package's UnsupportedErrorf, which this
// function backs, for the full rationale and a usage example.
func Unsupportedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// Encryptedf builds an error wrapping ErrEncrypted (and, transitively,
// ErrUnsupported - see ErrEncrypted's doc comment) with a formatted
// detail message. Used by internal/parser when a document's trailer
// declares an /Encrypt dictionary.
func Encryptedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrEncrypted, fmt.Sprintf(format, args...))
}
