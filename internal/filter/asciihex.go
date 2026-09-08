package filter

import "github.com/tucats/pdf-viewer/internal/pdferror"

// decodeASCIIHex reverses ASCIIHexDecode: pairs of hexadecimal digits
// encode one byte each, whitespace between digits is insignificant, and
// '>' marks the end of the data. If the data ends with an odd number of
// hex digits, the final digit is treated as if followed by an implicit
// '0', per the PDF specification - the same rule internal/syntax's
// Lexer.readHexString applies to hex *string* objects, which this filter
// deliberately mirrors even though it is a separate implementation (a
// stream filter operates on a resolved byte slice, not a live token
// stream, so sharing code with the lexer is not practical).
func decodeASCIIHex(data []byte) ([]byte, error) {
	var digits []byte
	for _, b := range data {
		switch {
		case b == '>':
			return finishASCIIHex(digits)
		case isASCII85Whitespace(b):
			continue
		default:
			v, ok := hexDigitValue(b)
			if !ok {
				return nil, pdferror.Malformedf("ASCIIHex: invalid character %q", b)
			}
			digits = append(digits, v)
		}
		if len(digits)/2 > maxDecodedSize {
			return nil, pdferror.Malformedf("ASCIIHex output exceeds %d bytes", maxDecodedSize)
		}
	}
	// No '>' terminator found; tolerate it, matching decodeASCII85's
	// tolerance of a missing "~>" (the stream's /Length already marks
	// the true end in this case).
	return finishASCIIHex(digits)
}

func finishASCIIHex(digits []byte) ([]byte, error) {
	if len(digits)%2 == 1 {
		digits = append(digits, 0)
	}
	out := make([]byte, len(digits)/2)
	for i := range out {
		out[i] = digits[2*i]<<4 | digits[2*i+1]
	}
	return out, nil
}

func hexDigitValue(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
