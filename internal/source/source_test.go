package source

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

func TestNewRejectsNilReaderAt(t *testing.T) {
	if _, err := New(nil, 0); err == nil {
		t.Fatal("New(nil, 0) succeeded, want an error")
	}
}

func TestNewRejectsNegativeSize(t *testing.T) {
	if _, err := New(bytes.NewReader(nil), -1); err == nil {
		t.Fatal("New(reader, -1) succeeded, want an error")
	}
}

func TestSize(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("hello world")), 11)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := r.Size(), int64(11); got != want {
		t.Errorf("Size() = %d, want %d", got, want)
	}
}

func TestReadAtInRange(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("hello world")), 11)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	buf := make([]byte, 5)
	n, err := r.ReadAt(buf, 6)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != 5 {
		t.Fatalf("ReadAt read %d bytes, want 5", n)
	}
	if got, want := string(buf), "world"; got != want {
		t.Errorf("ReadAt read %q, want %q", got, want)
	}
}

func TestReadAtRejectsNegativeOffset(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("hello")), 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := r.ReadAt(buf, -1); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("ReadAt(-1) error = %v, want ErrMalformed", err)
	}
}

// TestReadAtRejectsOutOfRange is the core safety property of this
// package: a request whose offset or length would run past the end of
// the file must be rejected with a classified error rather than being
// forwarded to the underlying io.ReaderAt (which, depending on the
// implementation, might return short reads, garbage, or even panic).
func TestReadAtRejectsOutOfRange(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("hello")), 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		name string
		off  int64
		n    int
	}{
		{"offset past end", 5, 1},
		{"offset way past end", 1000, 1},
		{"length overruns end", 3, 10},
		{"huge length overflow risk", 0, 1 << 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, tt.n)
			if _, err := r.ReadAt(buf, tt.off); !errors.Is(err, pdferror.ErrMalformed) {
				t.Fatalf("ReadAt(off=%d, n=%d) error = %v, want ErrMalformed", tt.off, tt.n, err)
			}
		})
	}
}

func TestReadAtZeroLengthAlwaysSucceeds(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("hello")), 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// A zero-length read at an offset equal to the file size (e.g. the
	// very end of the file) is a degenerate but legitimate case and must
	// not be rejected as "out of range".
	if _, err := r.ReadAt(nil, 5); err != nil {
		t.Fatalf("zero-length ReadAt at EOF offset: %v", err)
	}
}

func TestBytes(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("0123456789")), 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := r.Bytes(2, 4)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if want := "2345"; string(got) != want {
		t.Errorf("Bytes(2, 4) = %q, want %q", got, want)
	}
}

func TestBytesRejectsNegativeLength(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("0123456789")), 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := r.Bytes(0, -1); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Bytes(0, -1) error = %v, want ErrMalformed", err)
	}
}

func TestTail(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("0123456789")), 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := r.Tail(4)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := "6789"; string(got) != want {
		t.Errorf("Tail(4) = %q, want %q", got, want)
	}
}

// TestTailLongerThanFileReturnsWholeFile matters because real code
// (internal/parser, locating "startxref") always asks for a fixed-size
// tail such as the last 1024 bytes without first checking the file's
// size; that must degrade gracefully for small files/fixtures instead of
// erroring.
func TestTailLongerThanFileReturnsWholeFile(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("short")), 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := r.Tail(1024)
	if err != nil {
		t.Fatalf("Tail(1024): %v", err)
	}
	if want := "short"; string(got) != want {
		t.Errorf("Tail(1024) = %q, want %q", got, want)
	}
}

func TestTailRejectsNegativeLength(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("short")), 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := r.Tail(-1); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Tail(-1) error = %v, want ErrMalformed", err)
	}
}

func TestSectionFromReadsToEndOfFileAndNoFurther(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("0123456789")), 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sec, err := r.SectionFrom(7)
	if err != nil {
		t.Fatalf("SectionFrom(7): %v", err)
	}
	got, err := io.ReadAll(sec)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if want := "789"; string(got) != want {
		t.Errorf("SectionFrom(7) read %q, want %q", got, want)
	}
}

func TestSectionFromAtExactEndReturnsEmptyReader(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("hello")), 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sec, err := r.SectionFrom(5)
	if err != nil {
		t.Fatalf("SectionFrom(5): %v", err)
	}
	got, err := io.ReadAll(sec)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("SectionFrom(5) read %q, want empty", got)
	}
}

func TestSectionFromRejectsOutOfRangeOffset(t *testing.T) {
	r, err := New(bytes.NewReader([]byte("hello")), 5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := r.SectionFrom(6); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("SectionFrom(6) error = %v, want ErrMalformed", err)
	}
	if _, err := r.SectionFrom(-1); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("SectionFrom(-1) error = %v, want ErrMalformed", err)
	}
}
