package filter

import (
	"bytes"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// testBitWriter is a minimal most-significant-bit-first bit writer, used
// only by this file's hand-built round-trip tests to assemble a known
// CCITT-encoded bitstream from a sequence of (code value, bit count)
// pairs - the same MSB-first packing ccittBits reads (see that type's
// own doc comment).
type testBitWriter struct {
	buf     []byte
	cur     byte
	curBits int
}

func (w *testBitWriter) writeBits(code uint, n int) {
	for i := n - 1; i >= 0; i-- {
		bit := byte((code >> uint(i)) & 1)
		w.cur = w.cur<<1 | bit
		w.curBits++
		if w.curBits == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur, w.curBits = 0, 0
		}
	}
}

// bytes flushes any partial trailing byte, zero-padding it on the right -
// exactly how a real encoder pads its final byte, and exactly what
// ccittBits.look already has to tolerate at the end of genuine CCITT
// data (see that type's doc comment).
func (w *testBitWriter) bytes() []byte {
	if w.curBits == 0 {
		return w.buf
	}
	return append(w.buf, w.cur<<uint(8-w.curBits))
}

// The bit patterns used by these two test-only encoding helpers
// (writeWhiteRun/writeBlackRun/writeMode) are not derived or guessed:
// they are copied from the CCITT (ITU-T T.4/T.6) code tables' own
// bundled comments in ccitt.go's ported source (see that file's package
// doc comment for provenance) - specifically the small subset of short,
// unambiguous terminating run-length and 2D mode codes needed to build
// a handful of tiny test images, not a general-purpose encoder.

// writeWhiteRun and writeBlackRun each encode one run length using the
// terminating (non-makeup, i.e. < 64) CCITT code for that exact length -
// only a handful of specific lengths this file's tests actually use are
// supported, not the full table.
func writeWhiteRun(w *testBitWriter, run int) {
	switch run {
	case 1:
		w.writeBits(0b000111, 6)
	case 2:
		w.writeBits(0b0111, 4)
	case 4:
		w.writeBits(0b1011, 4)
	case 5:
		w.writeBits(0b1100, 4)
	default:
		panic("writeWhiteRun: unsupported test run length")
	}
}

func writeBlackRun(w *testBitWriter, run int) {
	switch run {
	case 1:
		w.writeBits(0b010, 3)
	case 2:
		w.writeBits(0b11, 2)
	case 3:
		w.writeBits(0b10, 2)
	case 4:
		w.writeBits(0b011, 3)
	default:
		panic("writeBlackRun: unsupported test run length")
	}
}

// write2DMode encodes one T.6 2D mode code word - again, only the handful
// (horizontal and vertical-0) this file's Group 4 test actually needs.
func write2DMode(w *testBitWriter, mode int) {
	switch mode {
	case twoDimHoriz:
		w.writeBits(0b001, 3)
	case twoDimVert0:
		w.writeBits(0b1, 1)
	default:
		panic("write2DMode: unsupported test mode")
	}
}

// TestDecodeCCITT1D round-trips two small hand-encoded rows through
// decodeCCITT using /K 0 (pure Group 3, one-dimensional coding: every
// run's absolute length is Huffman-coded directly - see decodeRow's doc
// comment), checking the decoded packed-bit output exactly reproduces
// the intended black/white pattern.
func TestDecodeCCITT1D(t *testing.T) {
	const columns = 11

	w := &testBitWriter{}
	// Row 0: white(2) black(3) white(4) black(2) = 11 columns.
	writeWhiteRun(w, 2)
	writeBlackRun(w, 3)
	writeWhiteRun(w, 4)
	writeBlackRun(w, 2)
	// Row 1: white(1) black(4) white(5) black(1) = 11 columns.
	writeWhiteRun(w, 1)
	writeBlackRun(w, 4)
	writeWhiteRun(w, 5)
	writeBlackRun(w, 1)

	parms := syntax.Dictionary{
		"K":       syntax.Integer(0),
		"Columns": syntax.Integer(columns),
		"Rows":    syntax.Integer(2),
	}
	got, err := decodeCCITT(w.bytes(), parms)
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}

	wantRow0 := []bool{false, false, true, true, true, false, false, false, false, true, true}
	wantRow1 := []bool{false, true, true, true, true, false, false, false, false, false, true}
	checkCCITTRows(t, got, columns, [][]bool{wantRow0, wantRow1})
}

// TestDecodeCCITT2D round-trips two small hand-encoded rows through
// decodeCCITT using /K -1 (pure Group 4, two-dimensional coding
// throughout - see decodeRow's doc comment): row 0 is coded via
// Horizontal mode (absolute run lengths, like 1D), and row 1 - an exact
// repeat of row 0's pattern - via two Vertical-0 codes, each just one
// bit, exercising the reference-line lookup that is Group 4's whole
// reason for being smaller than Group 3.
func TestDecodeCCITT2D(t *testing.T) {
	const columns = 8

	w := &testBitWriter{}
	// Row 0 (2D, but coded via Horizontal mode): white(4) black(4).
	write2DMode(w, twoDimHoriz)
	writeWhiteRun(w, 4)
	writeBlackRun(w, 4)
	// Row 1: identical to row 0, coded as two Vertical-0 (no change from
	// the reference line's matching transition) codes.
	write2DMode(w, twoDimVert0)
	write2DMode(w, twoDimVert0)

	parms := syntax.Dictionary{
		"K":       syntax.Integer(-1),
		"Columns": syntax.Integer(columns),
		"Rows":    syntax.Integer(2),
	}
	got, err := decodeCCITT(w.bytes(), parms)
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}

	row := []bool{false, false, false, false, true, true, true, true}
	checkCCITTRows(t, got, columns, [][]bool{row, row})
}

// checkCCITTRows asserts that got (decodeCCITT's packed-bit output, using
// the default /BlackIs1 false convention - see packCCITTRow's doc
// comment: white pixels are 1 bits, black are 0) holds exactly the rows
// of black/white pixels in want.
func checkCCITTRows(t *testing.T, got []byte, columns int, want [][]bool) {
	t.Helper()
	rowBytes := (columns + 7) / 8
	if len(got) != rowBytes*len(want) {
		t.Fatalf("decoded output is %d bytes, want %d (%d rows of %d bytes)", len(got), rowBytes*len(want), len(want), rowBytes)
	}
	for r, wantRow := range want {
		row := got[r*rowBytes : (r+1)*rowBytes]
		for x, black := range wantRow {
			bit := (row[x/8] >> uint(7-x%8)) & 1
			wantBit := byte(1) // white
			if black {
				wantBit = 0
			}
			if bit != wantBit {
				t.Errorf("row %d, column %d: got bit %d, want %d (pixel should be %s)", r, x, bit, wantBit, colorName(black))
			}
		}
	}
}

func colorName(black bool) string {
	if black {
		return "black"
	}
	return "white"
}

// ccittFixture1393 is the exact, unmodified /CCITTFaxDecode stream bytes
// for one small image (a 378x315 /ImageMask, "brake system warning"
// icon) from a real, non-synthetic 2017 Chevrolet Volt owner's manual
// PDF that this decoder was written to fix rendering of (see this
// package's own filter.go and the repository's issue history) - Group 4
// (/K -1), /EncodedByteAlign true, /BlackIs1 false, exactly the
// DecodeParms combination every CCITTFaxDecode stream in that
// real-world document uses. This is a regression fixture, not a
// synthetic one, specifically so a future change cannot silently
// reintroduce a decode bug that only manifests on real encoder output
// (hand-built tests like TestDecodeCCITT1D/2D above cover the algorithm
// deliberately, but are not a substitute for real-world data).
var ccittFixture1393 = []byte{
	0x26, 0xa0, 0x66, 0x0b, 0x20, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0x90, 0xc8, 0x10, 0xe4,
	0x0f, 0x05, 0x08, 0x80, 0x84, 0x18, 0x4e, 0xaa, 0x70, 0xa7, 0xc0, 0xfc, 0xfc, 0xfc, 0xfc, 0xfc,
	0xfc, 0xfc, 0xfc, 0xfc, 0xf7, 0xfc, 0xd1, 0x0d, 0x5a, 0x5c, 0xaa, 0x0f, 0x80, 0xe1, 0x06, 0xf0,
	0xaa, 0xf0, 0xe8, 0x36, 0xe0, 0xd2, 0xf0, 0xe8, 0x3e, 0xd7, 0x70, 0xaa, 0xbc, 0xaf, 0xc0, 0xd2,
	0x6d, 0xc0, 0xff, 0xa5, 0xdc, 0xfb, 0xc0, 0xae, 0xdc, 0xd7, 0xc0, 0xa5, 0xdc, 0xfb, 0xc0, 0xa5,
	0xb7, 0xff, 0xa5, 0xb7, 0xaf, 0xc0, 0xd6, 0xdc, 0xa5, 0xdc, 0xae, 0x1e, 0xd7, 0x70, 0xa5, 0xb7,
	0xaf, 0xc0, 0xd6, 0xdc, 0xa5, 0xb7, 0xa5, 0xbc, 0xd7, 0x70, 0xae, 0xdc, 0xd6, 0xf0, 0xa5, 0xf0,
	0xfb, 0x70, 0xd6, 0xdc, 0xaf, 0xc0, 0xa5, 0xbc, 0xff, 0xd6, 0xdc, 0xfd, 0xc0, 0xa5, 0xbc, 0xaf,
	0xc0, 0xd6, 0xf0, 0xfd, 0xc0, 0xd7, 0xc0, 0xfb, 0x70, 0xaf, 0xc0, 0xfb, 0xc0, 0xa5, 0xf0, 0xff,
	0xd7, 0x70, 0xfb, 0xc0, 0xff, 0xfd, 0xc0, 0xae, 0xf0, 0xd7, 0xc0, 0xff, 0xff, 0xa5, 0xf0, 0xff,
	0xff, 0xff, 0xfb, 0x70, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xed, 0x70, 0xff,
	0xef, 0xc0, 0xf5, 0xc0, 0xed, 0x54, 0xee, 0xb0, 0xec, 0x2e, 0xed, 0x70, 0xb6, 0x18, 0x50, 0x8a,
	0x1e, 0xe1, 0x85, 0x1c, 0xe3, 0xc0, 0xfc, 0xb7, 0xc0, 0xeb, 0xf5, 0xf0, 0x8a, 0x1e, 0xe9, 0xc0,
	0xdf, 0xbf, 0xdf, 0xe4, 0x19, 0xf1, 0x60, 0xb8, 0x41, 0xf0, 0xe0, 0x81, 0xa5, 0xda, 0x7c, 0xda,
	0x0e, 0xa0, 0xba, 0xac, 0xe9, 0xac, 0xbd, 0xf0, 0xda, 0xd4, 0xde, 0xb0, 0xe9, 0xa5, 0xdf, 0xc0,
	0xb7, 0xac, 0xff, 0xb7, 0xa5, 0xff, 0xde, 0x94, 0xfa, 0xc0, 0xb0, 0xda, 0x58, 0xff, 0xb6, 0xe9,
	0x40, 0xdd, 0x2c, 0xdb, 0x49, 0x40, 0xb6, 0xfc, 0xb8, 0x61, 0x05, 0x80, 0xdd, 0x54, 0xc3, 0x06,
	0x08, 0x2c, 0xd8, 0x61, 0x25, 0xb6, 0x2b, 0xb6, 0xb0, 0xd8, 0x4a, 0xda, 0x50, 0xb0, 0xc2, 0xc0,
	0xdf, 0xb0, 0xc2, 0x50, 0xda, 0xc0, 0xb0, 0xc2, 0x50, 0xda, 0xc0, 0xb1, 0x50, 0xf0, 0xd4, 0xbc,
	0xb5, 0xf0, 0xd4, 0xbc, 0xb2, 0x3a, 0x50, 0xc7, 0xd4, 0xb2, 0x3a, 0xc0, 0xb2, 0x3a, 0x50, 0xc7,
	0xb2, 0x3a, 0x50, 0xf2, 0x0c, 0x67, 0x42, 0x3a, 0x0a, 0x74, 0x17, 0xb2, 0x3a, 0xff, 0x50, 0xc7,
	0xff, 0xb2, 0x3a, 0xff, 0x50, 0xc7, 0xff, 0xbf, 0xf0, 0xa0, 0xbf, 0xfc, 0xbf, 0xf0, 0xa0, 0x86,
	0x9a, 0x69, 0xa6, 0xa0, 0x88, 0x88, 0x8c, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0,
	0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0xc0, 0x00, 0x10,
	0x01,
}

func ccittFixture1393Parms() syntax.Dictionary {
	return syntax.Dictionary{
		"BlackIs1":         syntax.Boolean(false),
		"Columns":          syntax.Integer(378),
		"EncodedByteAlign": syntax.Boolean(true),
		"EndOfLine":        syntax.Boolean(false),
		"K":                syntax.Integer(-1),
		"Rows":             syntax.Integer(315),
	}
}

// TestDecodeCCITTRealWorldFixture decodes ccittFixture1393 (see its own
// doc comment) and checks the result is the right shape and not obviously
// wrong (e.g. entirely one color, which a badly broken decoder producing
// "no transitions found" would output). This complements
// TestDecodeCCITT1D/2D's hand-verified algorithmic coverage with a
// regression check against real encoder output; the image itself was
// visually confirmed (rendered via cmd/pdfpreview against the source
// PDF) to be a crisp, correctly-shaped brake warning icon, not something
// this automated test alone re-derives.
func TestDecodeCCITTRealWorldFixture(t *testing.T) {
	const width, height = 378, 315
	rowBytes := (width + 7) / 8

	got, err := decodeCCITT(ccittFixture1393, ccittFixture1393Parms())
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}
	if len(got) != rowBytes*height {
		t.Fatalf("decoded output is %d bytes, want %d (%d rows of %d bytes)", len(got), rowBytes*height, height, rowBytes)
	}

	blackBits, whiteBits := 0, 0
	for _, b := range got {
		for i := 0; i < 8; i++ {
			if (b>>uint(i))&1 == 1 {
				whiteBits++
			} else {
				blackBits++
			}
		}
	}
	if blackBits == 0 || whiteBits == 0 {
		t.Fatalf("decoded image is a single solid color (black=%d, white=%d bits) - decoding is almost certainly broken", blackBits, whiteBits)
	}

	// /BlackIs1 true must invert every pixel's bit relative to /BlackIs1
	// false's output (see packCCITTRow's doc comment) - the two are
	// decoding the exact same transitions, just packing them with the
	// opposite bit convention, so this is a strong self-consistency
	// check independent of knowing any pixel's "true" value up front.
	// This deliberately checks only the width's actual 378 columns, not
	// every bit of every row's last byte: 378 is not a multiple of 8, so
	// each row's final byte carries a few unused trailing bits beyond
	// the image's own width, which packCCITTRow never writes to (they
	// stay at whatever clear left them, 0, under both conventions) and
	// so do not invert - internal/image's own bitReader never reads
	// them either, for the same reason (see decode.go there), so their
	// value truly is a don't-care, not something worth asserting on.
	invertedParms := ccittFixture1393Parms()
	invertedParms["BlackIs1"] = syntax.Boolean(true)
	inverted, err := decodeCCITT(ccittFixture1393, invertedParms)
	if err != nil {
		t.Fatalf("decodeCCITT (BlackIs1 true): %v", err)
	}
	if len(inverted) != len(got) {
		t.Fatalf("BlackIs1 true output is %d bytes, want %d (same as BlackIs1 false)", len(inverted), len(got))
	}
	for r := 0; r < height; r++ {
		for x := 0; x < width; x++ {
			byteIdx := r*rowBytes + x/8
			mask := byte(1) << uint(7-x%8)
			plainBit := got[byteIdx]&mask != 0
			invertedBit := inverted[byteIdx]&mask != 0
			if plainBit == invertedBit {
				t.Fatalf("row %d, column %d: BlackIs1 true bit equals BlackIs1 false bit (%v) - want the opposite", r, x, plainBit)
			}
		}
	}
}

// TestDecodeCCITTFilterDispatch checks that Decode routes both the full
// filter name and its PDF-defined abbreviation (/Filter /CCF - see the
// PDF specification's Table 8) to decodeCCITT, producing identical
// output either way.
func TestDecodeCCITTFilterDispatch(t *testing.T) {
	for _, name := range []syntax.Name{"CCITTFaxDecode", "CCF"} {
		t.Run(string(name), func(t *testing.T) {
			dict := syntax.Dictionary{
				"Filter":      name,
				"DecodeParms": ccittFixture1393Parms(),
			}
			got, err := Decode(dict, ccittFixture1393)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			want, err := decodeCCITT(ccittFixture1393, ccittFixture1393Parms())
			if err != nil {
				t.Fatalf("decodeCCITT: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Decode via filter name %q did not match decodeCCITT called directly", name)
			}
		})
	}
}

// TestDecodeCCITTInvalidParms checks that a few kinds of hostile or
// malformed /DecodeParms are rejected with an error rather than causing
// an excessive allocation or a panic - see decodeCCITT's own comments on
// bounding /Columns, /Rows, and their product.
func TestDecodeCCITTInvalidParms(t *testing.T) {
	tests := []struct {
		name  string
		parms syntax.Dictionary
	}{
		{"zero columns", syntax.Dictionary{"Columns": syntax.Integer(0)}},
		{"negative columns", syntax.Dictionary{"Columns": syntax.Integer(-1)}},
		{"columns too large", syntax.Dictionary{"Columns": syntax.Integer(1 << 21)}},
		{"negative rows", syntax.Dictionary{"Columns": syntax.Integer(10), "Rows": syntax.Integer(-1)}},
		{"rows too large", syntax.Dictionary{"Columns": syntax.Integer(10), "Rows": syntax.Integer(1 << 21)}},
		{"rows*columns exceeds bound", syntax.Dictionary{"Columns": syntax.Integer(1 << 20), "Rows": syntax.Integer(1 << 20)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeCCITT(nil, tt.parms); err == nil {
				t.Fatalf("decodeCCITT(%+v): got nil error, want one", tt.parms)
			}
		})
	}
}

// TestDecodeCCITTShortRowsUnknownDimension checks decodeCCITT's two
// "ran out of data" fallbacks (see decodeCCITT's row loop) both actually
// terminate and produce a sensible result: a known /Rows pads the
// remainder of the image white rather than erroring, and an unknown
// (/Rows 0) count simply stops at however many rows the data actually
// held.
func TestDecodeCCITTShortRowsUnknownDimension(t *testing.T) {
	const columns = 8
	w := &testBitWriter{}
	write2DMode(w, twoDimHoriz)
	writeWhiteRun(w, 4)
	writeBlackRun(w, 4)
	data := w.bytes()

	t.Run("known rows pads remainder white", func(t *testing.T) {
		parms := syntax.Dictionary{"K": syntax.Integer(-1), "Columns": syntax.Integer(columns), "Rows": syntax.Integer(5)}
		got, err := decodeCCITT(data, parms)
		if err != nil {
			t.Fatalf("decodeCCITT: %v", err)
		}
		rowBytes := (columns + 7) / 8
		if len(got) != rowBytes*5 {
			t.Fatalf("got %d bytes, want %d (5 full rows)", len(got), rowBytes*5)
		}
		// Every row after the first (which the encoded data actually
		// covers) should have decoded as entirely white, i.e. 0xFF for
		// an 8-column row under the default /BlackIs1 false.
		for r := 1; r < 5; r++ {
			if got[r] != 0xFF {
				t.Errorf("padding row %d = %#08b, want 0xff (all white)", r, got[r])
			}
		}
	})

	t.Run("unknown rows stops at what the data holds", func(t *testing.T) {
		parms := syntax.Dictionary{"K": syntax.Integer(-1), "Columns": syntax.Integer(columns), "EndOfBlock": syntax.Boolean(false)}
		got, err := decodeCCITT(data, parms)
		if err != nil {
			t.Fatalf("decodeCCITT: %v", err)
		}
		rowBytes := (columns + 7) / 8
		if len(got) != rowBytes {
			t.Fatalf("got %d bytes, want %d (exactly the one row the data covers)", len(got), rowBytes)
		}
	})
}
