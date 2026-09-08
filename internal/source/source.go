package source

import (
	"fmt"
	"io"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// Reader wraps an io.ReaderAt of known size and provides small,
// bounds-checked helpers for reading byte ranges out of it. Every method
// on Reader validates that the requested range actually falls within the
// file before touching the underlying io.ReaderAt, and returns an error
// rather than panicking when it does not.
//
// This matters specifically because a PDF file is untrusted input: PDF
// structure is built entirely out of byte offsets (cross-reference
// tables record "object 7 starts at byte 4213"), and a malformed or
// deliberately hostile file can claim any offset or length it likes,
// including ones far beyond the end of the actual file, or negative, or
// large enough to overflow naive arithmetic. Every later package in this
// module (internal/syntax, internal/parser, ...) is expected to read
// bytes exclusively through a Reader rather than holding onto the raw
// io.ReaderAt directly, so that this bounds-checking is applied
// uniformly instead of being re-implemented (or forgotten) at every call
// site.
//
// If you are new to Go: io.ReaderAt is the standard library interface
// for reading at an arbitrary offset without disturbing any other notion
// of "current position" (unlike io.Reader, which always reads from
// wherever it left off). It is implemented by *os.File, so a Reader
// wrapping an opened file works out of the box, but it is also
// implemented by anything else that supports random-access reads (an
// in-memory byte slice via bytes.NewReader, for example), which is why
// the public pdfviewer.Open function accepts an io.ReaderAt rather than
// requiring a real file.
type Reader struct {
	ra   io.ReaderAt
	size int64
}

// New wraps ra as a Reader of the given size. size must be the exact,
// already-known length of the data ra reads from; New does not attempt
// to discover it (for example there is no reliable, portable way to ask
// an arbitrary io.ReaderAt "how big are you", which is exactly why the
// public API asks the caller for the size up front — see pdfviewer.Open).
//
// New returns an error if size is negative, since a negative size can
// never correspond to real data and every bounds check in this package
// assumes size is a valid, non-negative upper bound.
func New(ra io.ReaderAt, size int64) (*Reader, error) {
	if ra == nil {
		return nil, fmt.Errorf("pdfviewer/internal/source: nil io.ReaderAt")
	}
	if size < 0 {
		return nil, fmt.Errorf("pdfviewer/internal/source: negative size %d", size)
	}
	return &Reader{ra: ra, size: size}, nil
}

// Size returns the total number of bytes available to read.
func (r *Reader) Size() int64 {
	return r.size
}

// ReadAt reads exactly len(p) bytes starting at offset off, matching the
// semantics of io.ReaderAt.ReadAt: it returns an error if it could not
// fill p completely (including reaching the end of the file early).
//
// Unlike calling r.ra.ReadAt directly, this method first checks that the
// requested range [off, off+len(p)) actually lies within [0, r.size)
// and returns a classified pdfviewer.ErrMalformed-wrapped error instead
// of forwarding whatever the underlying reader happens to do with an
// out-of-range offset (which, for many io.ReaderAt implementations,
// would itself just be an error — but this package must not depend on
// every possible implementation behaving safely for out-of-range
// offsets, since a caller can plug in any io.ReaderAt).
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, malformedf("negative read offset %d", off)
	}
	n := int64(len(p))
	if n == 0 {
		return 0, nil
	}
	if off > r.size || n > r.size-off {
		return 0, malformedf("read of %d bytes at offset %d exceeds file size %d", n, off, r.size)
	}
	read, err := r.ra.ReadAt(p, off)
	if err != nil {
		return read, fmt.Errorf("pdfviewer/internal/source: %w", err)
	}
	return read, nil
}

// Bytes reads and returns exactly n bytes starting at offset off, as a
// freshly allocated slice. It is a convenience wrapper around ReadAt for
// the common case of "give me this whole range as a []byte" rather than
// filling a caller-provided buffer.
func (r *Reader) Bytes(off, n int64) ([]byte, error) {
	if n < 0 {
		return nil, malformedf("negative read length %d", n)
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, off); err != nil {
		return nil, err
	}
	return buf, nil
}

// Tail reads the last n bytes of the file (or the whole file, if it is
// shorter than n bytes). This is specifically for locating the PDF
// trailer: per the PDF specification, a conforming reader is expected to
// find the "startxref" keyword by reading backward from the end of the
// file, since that is the one fixed, reliable anchor point in a PDF's
// otherwise offset-addressed structure.
func (r *Reader) Tail(n int64) ([]byte, error) {
	if n < 0 {
		return nil, malformedf("negative tail length %d", n)
	}
	if n > r.size {
		n = r.size
	}
	return r.Bytes(r.size-n, n)
}

// SectionFrom returns an io.Reader that sequentially reads the bytes
// from offset off through the end of the file. This is what
// internal/syntax's tokenizer reads from: tokenizing is inherently
// sequential (read one byte, decide what kind of token that starts,
// consume more bytes accordingly), so a plain io.Reader positioned at
// the right starting offset is a better fit for that layer than the
// random-access ReadAt/Bytes methods above.
//
// The returned reader can never read past the end of the file (attempts
// to do so report io.EOF), which is what keeps a runaway tokenizer from
// reading unbounded amounts of data on a malformed file that never
// supplies an expected terminator.
func (r *Reader) SectionFrom(off int64) (io.Reader, error) {
	if off < 0 {
		return nil, malformedf("negative section offset %d", off)
	}
	if off > r.size {
		return nil, malformedf("section offset %d exceeds file size %d", off, r.size)
	}
	return io.NewSectionReader(r.ra, off, r.size-off), nil
}

func malformedf(format string, args ...any) error {
	return pdferror.Malformedf(format, args...)
}
