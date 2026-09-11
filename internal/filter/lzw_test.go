package filter

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// encodeLZWForTest compresses data as a real PDF producer would (MSB
// order, 8-bit literals, a Clear code up front and an EOD code at the
// end), independently of decodeLZW, so these tests exercise a real
// encode/decode round trip rather than only checking decodeLZW against
// itself.
//
// This is a from-scratch encoder rather than a use of the standard
// library's compress/lzw on purpose: see lzw.go's package-level doc
// comment for why that package's fixed code-width growth point turned
// out to match PDF's non-default /EarlyChange 0 rather than the
// default /EarlyChange 1 - using it here would have quietly tested
// only the rarer of the two cases, and, worse, would have "confirmed"
// exactly the wrong default this file's decoder used to have.
// earlyChange selects which of the two behaviors to produce - see
// lzwCodeWidth's doc comment in lzw.go for what that actually changes;
// this encoder mirrors that function's arithmetic exactly so an
// encode/decode round trip only tests decodeLZW, not a mismatch between
// two different implementations of the same rule.
func encodeLZWForTest(t *testing.T, data []byte) []byte {
	t.Helper()
	return encodeLZWWithEarlyChange(data, true)
}

func encodeLZWWithEarlyChange(data []byte, earlyChange bool) []byte {
	bw := &lzwBitWriter{}
	bw.write(lzwClearCode, 9)

	dict := make(map[string]int, lzwMaxTableSize)
	for i := 0; i < 256; i++ {
		dict[string([]byte{byte(i)})] = i
	}
	nextCode := lzwFirstCode
	codeWidth := 9

	var w []byte
	for _, b := range data {
		wc := append(append([]byte{}, w...), b)
		if _, ok := dict[string(wc)]; ok {
			w = wc
			continue
		}
		bw.write(dict[string(w)], codeWidth)
		if nextCode < lzwMaxTableSize {
			dict[string(wc)] = nextCode
			nextCode++
			// The decoder can only add its equivalent of this entry once
			// it has read the *next* code (see lzw.go's KwKwK comment:
			// it needs to see what follows before it knows the entry's
			// full contents), so it's always one entry "behind" this
			// encoder's own dict at this exact point - nextCode-1, not
			// nextCode, is the table size the decoder will actually see
			// when it computes the width for the code that follows.
			codeWidth = lzwCodeWidth(nextCode-1, earlyChange)
		}
		w = []byte{b}
	}
	if len(w) > 0 {
		bw.write(dict[string(w)], codeWidth)
	}
	bw.write(lzwEODCode, codeWidth)

	return bw.bytes()
}

// lzwBitWriter is encodeLZWWithEarlyChange's counterpart to lzw.go's
// lzwBitReader: it packs fixed-width, MSB-first bit groups into bytes,
// padding the final byte with zero bits.
type lzwBitWriter struct {
	out  []byte
	cur  byte
	nBit int // number of bits already placed in cur, from the top
}

func (w *lzwBitWriter) write(code, n int) {
	for i := n - 1; i >= 0; i-- {
		bit := byte((code >> uint(i)) & 1)
		w.cur |= bit << uint(7-w.nBit)
		w.nBit++
		if w.nBit == 8 {
			w.out = append(w.out, w.cur)
			w.cur = 0
			w.nBit = 0
		}
	}
}

func (w *lzwBitWriter) bytes() []byte {
	if w.nBit > 0 {
		w.out = append(w.out, w.cur)
		w.cur = 0
		w.nBit = 0
	}
	return w.out
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
		// Large and repetitive enough to push the code table across
		// every width boundary (511/1023/2047 table entries with the
		// default /EarlyChange 1 - see lzwCodeWidth), which is exactly
		// where this decoder used to desync against real-world PDFs;
		// see lzw.go's package doc comment.
		{"crosses-width-boundaries", bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog. "), 400)},
	}
	for _, earlyChange := range []bool{true, false} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("earlyChange=%v/%s", earlyChange, c.name), func(t *testing.T) {
				encoded := encodeLZWWithEarlyChange(c.data, earlyChange)
				parms := syntax.Dictionary{}
				if !earlyChange {
					parms["EarlyChange"] = syntax.Integer(0)
				}
				got, err := decodeLZW(encoded, parms)
				if err != nil {
					t.Fatalf("decodeLZW: %v", err)
				}
				if !bytes.Equal(got, c.data) {
					t.Fatalf("decodeLZW round trip mismatch: got %d bytes, want %d bytes", len(got), len(c.data))
				}
			})
		}
	}
}

func TestDecodeLZWInvalidEarlyChange(t *testing.T) {
	encoded := encodeLZWForTest(t, []byte("data"))
	parms := syntax.Dictionary{"EarlyChange": syntax.Integer(2)}
	if _, err := decodeLZW(encoded, parms); err == nil {
		t.Fatal("decodeLZW with /EarlyChange 2: expected an error, got nil")
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
