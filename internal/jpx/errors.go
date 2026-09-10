package jpx

import (
	"errors"
	"fmt"
)

// This package defines its own error values rather than importing
// internal/pdferror, on purpose: see doc.go's "Why this package exists"
// section. internal/filter's adapter (jpx.go there) is the one place
// that translates an error returned from here into this module's own
// pdferror.ErrMalformed / pdferror.ErrUnsupported sentinels.

var (
	// ErrMalformed indicates that the input bytes do not follow the
	// JPEG 2000 codestream or JP2 container syntax closely enough to be
	// decoded at all - a bad marker, an impossible length field, or a
	// declared value outside the range the standard allows.
	ErrMalformed = errors.New("jpx: malformed input")

	// ErrUnsupported indicates that the input is syntactically valid
	// JPEG 2000 but uses a feature this package does not implement - see
	// doc.go's "Scope" section for the full, current list.
	ErrUnsupported = errors.New("jpx: unsupported feature")
)

// malformedf builds an error wrapping ErrMalformed with a formatted
// detail message - the same "sentinel plus errors.Is-compatible detail"
// shape internal/pdferror.Malformedf uses elsewhere in this module.
func malformedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, args...))
}

// unsupportedf builds an error wrapping ErrUnsupported with a formatted
// detail message, naming the specific unimplemented feature so a caller
// (or a developer reading a test failure) knows exactly what was hit,
// not just that something was.
func unsupportedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}
