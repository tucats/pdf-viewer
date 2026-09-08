package filter

import "github.com/tucats/pdf-viewer/internal/pdferror"

// decodeRunLength reverses RunLengthDecode, a simple byte-oriented
// run-length encoding (unrelated to LZW or Flate): the data is a
// sequence of "length byte, then payload" records. A length byte in
// [0,127] means "copy the next length+1 bytes literally"; a length byte
// in [129,255] means "repeat the single byte that follows (257-length)
// times"; and the length byte 128 marks end-of-data. Per the PDF
// specification every RunLengthDecode stream must end with a 128 byte,
// but this tolerates data that simply runs out first (the stream's
// /Length already marks the true end in that case), matching this
// package's other filters' tolerance of a missing explicit terminator.
func decodeRunLength(data []byte) ([]byte, error) {
	var out []byte
	i := 0
	for i < len(data) {
		n := data[i]
		i++
		switch {
		case n == 128:
			return out, nil
		case n < 128:
			count := int(n) + 1
			if i+count > len(data) {
				return nil, pdferror.Malformedf("RunLength: literal run of %d bytes runs past the end of the data", count)
			}
			out = append(out, data[i:i+count]...)
			i += count
		default: // 129-255
			if i >= len(data) {
				return nil, pdferror.Malformedf("RunLength: repeat run has no byte to repeat")
			}
			count := 257 - int(n)
			b := data[i]
			i++
			for j := 0; j < count; j++ {
				out = append(out, b)
			}
		}
		if len(out) > maxDecodedSize {
			return nil, pdferror.Malformedf("RunLength output exceeds %d bytes", maxDecodedSize)
		}
	}
	return out, nil
}
