package filter

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// decodeFlate reverses FlateDecode: PDF streams are zlib-wrapped
// (RFC 1950 - a two-byte header and Adler-32 checksum trailer around the
// raw DEFLATE data, RFC 1951), which is exactly what the standard
// library's compress/zlib package implements, so no separate DEFLATE
// implementation is needed here.
func decodeFlate(data []byte, parms syntax.Dictionary) ([]byte, error) {
	// A zero-length stream (real producers emit these for, e.g., an
	// intentionally blank annotation appearance) has no zlib header at
	// all, which zlib.NewReader below would otherwise reject as
	// malformed; treat it as decoding to zero bytes instead, the same as
	// every other filter in this package does for empty input.
	if len(data) == 0 {
		return nil, nil
	}

	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, pdferror.Malformedf("Flate decode: %v", err)
	}
	defer zr.Close()

	// A stream that runs out of bytes before its own logical end - a
	// missing/truncated Adler-32 trailer, or a DEFLATE block cut off
	// mid-stream - surfaces here as io.ErrUnexpectedEOF. Real-world PDF
	// producers get this wrong often enough that every mainstream viewer
	// (and this package's other filters - see ascii85.go, asciihex.go,
	// lzw.go, runlength.go for the same "missing terminator" tolerance)
	// uses whatever data decoded successfully rather than rejecting the
	// stream outright. A genuinely corrupt DEFLATE block instead reports
	// flate.CorruptInputError, which is still treated as an error.
	out, err := io.ReadAll(io.LimitReader(zr, maxDecodedSize+1))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, pdferror.Malformedf("Flate decode: %v", err)
	}
	if len(out) > maxDecodedSize {
		return nil, pdferror.Malformedf("Flate-decoded output exceeds %d bytes", maxDecodedSize)
	}

	return applyPredictor(out, parms)
}
