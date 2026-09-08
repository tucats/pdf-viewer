package filter

import "github.com/tucats/pdf-viewer/internal/pdferror"

// decodeASCII85 reverses ASCII85Decode: PDF's base-85 text encoding for
// binary data (used, for example, to make FlateDecode output safe to
// embed in strictly 7-bit transports). Five ASCII85 characters ('!'
// through 'u', i.e. the 85 printable characters starting at 33) encode
// four bytes as a base-85 big-endian number; the single letter 'z' is
// shorthand for a whole group of four zero bytes; whitespace is
// insignificant and may appear anywhere; and the two-character sequence
// "~>" marks the end of the data (an optional leading "<~" is also
// accepted, matching how ASCII85 is sometimes delimited outside of a PDF
// stream, though within a stream's raw bytes it is not expected to be
// present).
func decodeASCII85(data []byte) ([]byte, error) {
	if len(data) >= 2 && data[0] == '<' && data[1] == '~' {
		data = data[2:]
	}

	var out []byte
	var group [5]byte
	n := 0

	for i := 0; i < len(data); i++ {
		b := data[i]
		switch {
		case b == '~':
			// The "~>" end-of-data marker. Nothing after it is part of
			// the encoded data (the trailing '>' is not itself
			// significant beyond marking where "~" ends).
			return finishASCII85(out, group, n)
		case isASCII85Whitespace(b):
			continue
		case b == 'z':
			if n != 0 {
				return nil, pdferror.Malformedf("ASCII85: 'z' shorthand found in the middle of a group")
			}
			out = append(out, 0, 0, 0, 0)
		case b >= '!' && b <= 'u':
			group[n] = b - '!'
			n++
			if n == 5 {
				val, err := decodeASCII85Group(group)
				if err != nil {
					return nil, err
				}
				out = append(out, byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
				n = 0
			}
		default:
			return nil, pdferror.Malformedf("ASCII85: invalid character %q", b)
		}
		if len(out) > maxDecodedSize {
			return nil, pdferror.Malformedf("ASCII85 output exceeds %d bytes", maxDecodedSize)
		}
	}
	// Reached the end of data with no "~>" terminator; tolerate this
	// (some producers omit it when the stream's /Length already marks
	// the exact end) and decode whatever partial final group remains.
	return finishASCII85(out, group, n)
}

// finishASCII85 handles a partial final group of n (0-4) characters
// accumulated in group, per ASCII85's padding rule: a final group short
// of the full five characters is padded with additional 'u' (the
// highest-valued character, 84) before decoding as if it were a full
// group, and only the first n-1 decoded bytes are kept - a single
// leftover character with nothing to pair it with is invalid, since a
// base-85 encoding cannot represent a final byte alone.
func finishASCII85(out []byte, group [5]byte, n int) ([]byte, error) {
	if n == 0 {
		return out, nil
	}
	if n == 1 {
		return nil, pdferror.Malformedf("ASCII85: truncated final group (a single leftover character cannot decode to any bytes)")
	}
	for i := n; i < 5; i++ {
		group[i] = 84 // 'u' - '!'
	}
	val, err := decodeASCII85Group(group)
	if err != nil {
		return nil, err
	}
	buf := [4]byte{byte(val >> 24), byte(val >> 16), byte(val >> 8), byte(val)}
	return append(out, buf[:n-1]...), nil
}

// decodeASCII85Group interprets five base-85 digits (each already
// reduced to the range [0,84]) as a big-endian 32-bit value. A group
// whose base-85 value exceeds 2^32-1 does not correspond to any valid
// ASCII85 encoding of four bytes (the maximum valid group, "s8W-!",
// encodes exactly 0xFFFFFFFF), so such a group is reported as malformed
// rather than silently truncated by wrapping arithmetic.
func decodeASCII85Group(group [5]byte) (uint32, error) {
	var val uint64
	for _, d := range group {
		val = val*85 + uint64(d)
	}
	if val > 0xFFFFFFFF {
		return 0, pdferror.Malformedf("ASCII85: group value %d exceeds a 32-bit value", val)
	}
	return uint32(val), nil
}

func isASCII85Whitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', '\f', 0:
		return true
	default:
		return false
	}
}
