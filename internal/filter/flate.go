package filter

import (
	"bytes"
	"compress/zlib"
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
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, pdferror.Malformedf("Flate decode: %v", err)
	}
	defer zr.Close()

	out, err := io.ReadAll(io.LimitReader(zr, maxDecodedSize+1))
	if err != nil {
		return nil, pdferror.Malformedf("Flate decode: %v", err)
	}
	if len(out) > maxDecodedSize {
		return nil, pdferror.Malformedf("Flate-decoded output exceeds %d bytes", maxDecodedSize)
	}

	return applyPredictor(out, parms)
}
