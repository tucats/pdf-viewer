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
