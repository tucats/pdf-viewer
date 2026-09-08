package pdfviewer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestSentinelErrorsAreDistinct guards against a copy-paste mistake where
// two sentinel errors accidentally end up "equal" to each other (which,
// for values created with errors.New, would only happen if the same
// errors.New call were reused). errors.Is relies on these being distinct
// identities.
func TestSentinelErrorsAreDistinct(t *testing.T) {
	sentinels := []error{ErrMalformed, ErrUnsupported, ErrClosed, ErrPageIndex}

	for i, a := range sentinels {
		for j, b := range sentinels {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinel %d (%v) unexpectedly matches sentinel %d (%v)", i, a, j, b)
			}
		}
	}
}

// TestMalformedErrorfWrapsSentinel checks that an error built by
// MalformedErrorf can still be identified as ErrMalformed via
// errors.Is, and that the formatted detail message survives in the
// error's text so a human reading logs can see what actually went wrong.
func TestMalformedErrorfWrapsSentinel(t *testing.T) {
	err := MalformedErrorf("truncated header: only %d bytes", 3)

	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("errors.Is(err, ErrMalformed) = false, want true (err = %v)", err)
	}
	if errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = true, want false (err = %v)", err)
	}
	if got, want := err.Error(), "truncated header: only 3 bytes"; !strings.Contains(got, want) {
		t.Fatalf("error message %q does not contain detail %q", got, want)
	}
}

// TestUnsupportedErrorfWrapsSentinel is the ErrUnsupported analogue of
// TestMalformedErrorfWrapsSentinel above.
func TestUnsupportedErrorfWrapsSentinel(t *testing.T) {
	err := UnsupportedErrorf("stream filter %q", "JBIG2Decode")

	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = false, want true (err = %v)", err)
	}
	if errors.Is(err, ErrMalformed) {
		t.Fatalf("errors.Is(err, ErrMalformed) = true, want false (err = %v)", err)
	}
	if got, want := err.Error(), `stream filter "JBIG2Decode"`; !strings.Contains(got, want) {
		t.Fatalf("error message %q does not contain detail %q", got, want)
	}
}

// TestErrorConstructorsSurviveFurtherWrapping simulates a caller further
// up the stack wrapping one of these errors again with fmt.Errorf and
// "%w" (a normal Go pattern for adding context as an error propagates).
// errors.Is must still find the original sentinel no matter how many
// layers of wrapping were added, since that is the entire point of using
// %w instead of formatting the error into a plain string.
func TestErrorConstructorsSurviveFurtherWrapping(t *testing.T) {
	inner := MalformedErrorf("bad xref offset")
	outer := fmt.Errorf("opening object 7: %w", inner)

	if !errors.Is(outer, ErrMalformed) {
		t.Fatalf("errors.Is(outer, ErrMalformed) = false, want true (outer = %v)", outer)
	}
}
