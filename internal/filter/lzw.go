package filter

import (
	"bytes"
	"compress/lzw"
	"io"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// decodeLZW reverses LZWDecode using the Go standard library's
// compress/lzw package, whose package documentation explicitly states
// that it "implements LZW as used by the GIF and PDF file formats" with
// lzw.MSB bit ordering ("as used in the TIFF and PDF file formats") -
// this is not a coincidental fit, PDF's LZWDecode filter is specified as
// exactly this variant of the algorithm, so no separate implementation
// is needed here.
//
// PDF's /EarlyChange parameter (default 1) controls whether the code
// width increases one code earlier than the "textbook" LZW algorithm
// would - the standard library's implementation always behaves as
// EarlyChange=1 (the default, and by far the common case in practice),
// with no way to select the other behavior; a stream that explicitly
// requests /EarlyChange 0 is reported as unsupported rather than
// silently decoded incorrectly.
func decodeLZW(data []byte, parms syntax.Dictionary) ([]byte, error) {
	early := intParm(parms, "EarlyChange", 1)
	if early != 1 {
		return nil, pdferror.Unsupportedf("LZWDecode with /EarlyChange %d (only the default, 1, is supported)", early)
	}

	r := lzw.NewReader(bytes.NewReader(data), lzw.MSB, 8)
	defer r.Close()

	out, err := io.ReadAll(io.LimitReader(r, maxDecodedSize+1))
	if err != nil {
		return nil, pdferror.Malformedf("LZW decode: %v", err)
	}
	if len(out) > maxDecodedSize {
		return nil, pdferror.Malformedf("LZW-decoded output exceeds %d bytes", maxDecodedSize)
	}

	return applyPredictor(out, parms)
}
