package pdferror

import (
	"errors"
	"strings"
	"testing"
)

// This package is exercised heavily and indirectly by every other
// package's own tests (any error path in internal/source,
// internal/syntax, internal/parser, or internal/model that returns
// ErrMalformed or ErrUnsupported goes through here) - but those tests
// run in different packages, so `go test -cover` does not credit this
// package's own coverage number with any of that. These direct tests
// exist so this package's own correctness does not depend entirely on
// being caught as a side effect elsewhere.

func TestMalformedfWrapsErrMalformed(t *testing.T) {
	err := Malformedf("bad offset %d", 42)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("errors.Is(err, ErrMalformed) = false, want true (err = %v)", err)
	}
	if errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = true, want false (err = %v)", err)
	}
	if want := "bad offset 42"; !strings.Contains(err.Error(), want) {
		t.Errorf("error message %q does not contain %q", err.Error(), want)
	}
}

func TestUnsupportedfWrapsErrUnsupported(t *testing.T) {
	err := Unsupportedf("filter %q", "JBIG2Decode")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = false, want true (err = %v)", err)
	}
	if errors.Is(err, ErrMalformed) {
		t.Fatalf("errors.Is(err, ErrMalformed) = true, want false (err = %v)", err)
	}
	if want := `filter "JBIG2Decode"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error message %q does not contain %q", err.Error(), want)
	}
}

func TestErrMalformedAndErrUnsupportedAreDistinct(t *testing.T) {
	if errors.Is(ErrMalformed, ErrUnsupported) {
		t.Fatal("ErrMalformed and ErrUnsupported compare equal, want distinct sentinel values")
	}
}

func TestEncryptedfWrapsErrEncryptedAndErrUnsupported(t *testing.T) {
	err := Encryptedf("document trailer declares an /Encrypt dictionary")

	if !errors.Is(err, ErrEncrypted) {
		t.Fatalf("errors.Is(err, ErrEncrypted) = false, want true (err = %v)", err)
	}
	// ErrEncrypted is deliberately a specific case of ErrUnsupported (see
	// ErrEncrypted's doc comment), so an Encryptedf error must satisfy
	// both checks - a caller that only knows about the general
	// ErrUnsupported sentinel must not be broken by this more specific
	// one being introduced.
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = false, want true (err = %v)", err)
	}
	if errors.Is(err, ErrMalformed) {
		t.Fatalf("errors.Is(err, ErrMalformed) = true, want false (err = %v)", err)
	}
	if want := "declares an /Encrypt dictionary"; !strings.Contains(err.Error(), want) {
		t.Errorf("error message %q does not contain %q", err.Error(), want)
	}
}
