package filter

import (
	"bytes"
	"compress/lzw"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// encodeLZWForTest compresses data the same way a PDF producer would
// (MSB order, 8-bit literals - see decodeLZW's doc comment on why this
// is exactly what PDF's LZWDecode filter expects), independently of
// this package's decoder, so these tests exercise a real encode/decode
// round trip rather than only checking decodeLZW against itself.
func encodeLZWForTest(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.MSB, 8)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("encoding test LZW data: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing test LZW writer: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeLZWRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"short", []byte("hello, world")},
		{"repetitive", bytes.Repeat([]byte("abcabcabc"), 200)},
		{"binary", func() []byte {
			b := make([]byte, 2000)
			for i := range b {
				b[i] = byte(i * 31)
			}
			return b
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeLZWForTest(t, c.data)
			got, err := decodeLZW(encoded, nil)
			if err != nil {
				t.Fatalf("decodeLZW: %v", err)
			}
			if !bytes.Equal(got, c.data) {
				t.Fatalf("decodeLZW round trip mismatch: got %d bytes, want %d bytes", len(got), len(c.data))
			}
		})
	}
}

func TestDecodeLZWUnsupportedEarlyChange(t *testing.T) {
	encoded := encodeLZWForTest(t, []byte("data"))
	parms := syntax.Dictionary{"EarlyChange": syntax.Integer(0)}
	if _, err := decodeLZW(encoded, parms); err == nil {
		t.Fatal("decodeLZW with /EarlyChange 0: expected an unsupported error, got nil")
	}
}

func TestDecodeLZWWithTIFFPredictor(t *testing.T) {
	// A 3x2 image, 1 gray component, 8 bits per component: rows
	// [10 20 30] and [15 45 5]. The TIFF predictor stores each row as
	// running differences from the previous sample in that row.
	raw := [][]byte{{10, 20, 30}, {15, 45, 5}}
	var predictedBuf bytes.Buffer
	for _, row := range raw {
		predictedBuf.WriteByte(row[0])
		prev := row[0]
		for _, v := range row[1:] {
			predictedBuf.WriteByte(v - prev)
			prev = v
		}
	}

	encoded := encodeLZWForTest(t, predictedBuf.Bytes())
	parms := syntax.Dictionary{
		"Predictor":        syntax.Integer(2),
		"Colors":           syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"Columns":          syntax.Integer(3),
	}
	got, err := decodeLZW(encoded, parms)
	if err != nil {
		t.Fatalf("decodeLZW: %v", err)
	}
	want := append(append([]byte{}, raw[0]...), raw[1]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("decodeLZW with TIFF predictor = %v, want %v", got, want)
	}
}
