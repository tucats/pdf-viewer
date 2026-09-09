package filter

import (
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements decodeCCITT, reversing PDF's CCITTFaxDecode
// filter: the same bi-level (black-and-white, one bit per pixel)
// compression used by fax machines and, by extension, by most
// "scan to PDF" software for scanned pages. Unlike every other filter in
// this package, Go's standard library has no CCITT decoder at all - this
// is a complete decoder written for this project.
//
// # Provenance
//
// Rather than invent the bit-level algorithm from scratch (easy to get
// subtly wrong in ways that only show up as garbled images on some
// inputs), this file is a deliberate, close port of the CCITT decoder
// xpdf has shipped since the 1990s and Mozilla's pdf.js later ported to
// JavaScript (src/core/ccitt.js, "CCITTFaxDecoder", MPL/Apache-2.0
// dual-licensed by Glyph & Cog / Mozilla) - specifically its Huffman
// code tables (twoDimTable, whiteTable1/2, blackTable1/2/3 below) and
// the "changing element" row-decoding algorithm (decodeRow). Both are
// mechanically translated to Go and restructured to fit this package's
// conventions (error handling via pdferror, decoding straight into a
// packed-bit output buffer rather than pdf.js's pull-based byte stream),
// but the bit-level logic itself - which run belongs to which Huffman
// code, and how a row's black/white transitions are derived from the
// previous row's - is intentionally unchanged from that long-proven
// implementation, rather than re-derived from the ITU-T T.4/T.6
// recommendations directly.
//
// # What CCITT fax compression actually encodes
//
// A CCITT-encoded row is not stored as bits at all: it is stored as a
// list of "changing elements" - the column at which the color (black or
// white) changes, alternating starting from white (the standard assumes
// a row conceptually starts with white "paper" and may have a
// zero-length white run if the very first pixel is already black). Each
// run's length is Huffman-coded (see the white/black tables below); a
// four-pixel white run and a four-pixel black run use completely
// different code words, which is why decoding must track which color it
// is currently reading.
//
// "2D" (two-dimensional) coding, used by Group 4 (T.6, selected by a
// negative /K - the overwhelmingly common case for scanned PDFs, and the
// only mode this package's own test corpus exercises against a real
// document) goes one step further: instead of Huffman-coding a run's
// absolute length, it Huffman-codes the change in position of the
// current row's transitions relative to the *previous* row's -
// generally a much smaller number, and so a much shorter code, since two
// consecutive scan lines of a real document rarely differ by much. This
// is what makes the row-decoding logic reference a refLine (the previous
// row's transitions) alongside the codingLine being built for the
// current row.
//
// # Interaction with the rest of this package's image pipeline
//
// Like DCTDecode (see dct.go's own doc comment for the fuller
// rationale), CCITT decoding happens here rather than in internal/image,
// because from the point of view of everything downstream it is still
// just one more /Filter step that must produce plain sample bytes:
// specifically one bit per pixel, most-significant-bit first, each row
// starting on a fresh byte - exactly what internal/image's bitReader
// (see decode.go there) already expects for any /BitsPerComponent 1
// image, which a CCITT-encoded image always declares itself to be.
func decodeCCITT(data []byte, parms syntax.Dictionary) ([]byte, error) {
	columns := intParm(parms, "Columns", 1728)
	rows := intParm(parms, "Rows", 0)
	k := intParm(parms, "K", 0)
	blackIs1 := boolParm(parms, "BlackIs1", false)
	byteAlign := boolParm(parms, "EncodedByteAlign", false)
	eol := boolParm(parms, "EndOfLine", false)
	// /EndOfBlock (default true) says the encoder terminated the data
	// with an EOFB marker (six consecutive EOL codes back to back, T.6's
	// "return to control" sequence) rather than relying solely on a
	// known /Rows count. This package uses that marker for exactly one
	// thing: recognizing "end of image" when /Rows is 0 (unknown) - see
	// the row loop below.
	eoblock := boolParm(parms, "EndOfBlock", true)

	// maxCCITTDimension bounds /Columns and /Rows individually, matching
	// internal/image's own per-axis pixel cap (decode.go's
	// maxDimension) - a CCITT image's declared size always agrees with
	// its enclosing image dictionary's /Width and /Height, which that
	// package already bounds the same way, so this is not an additional
	// real-world restriction, just this package's own defense against a
	// hostile /DecodeParms dictionary read before internal/image ever
	// sees it.
	const maxCCITTDimension = 1 << 20
	if columns <= 0 || columns > maxCCITTDimension {
		return nil, pdferror.Malformedf("CCITTFaxDecode: /Columns must be in (0, %d] (got %d)", maxCCITTDimension, columns)
	}
	if rows < 0 || rows > maxCCITTDimension {
		return nil, pdferror.Malformedf("CCITTFaxDecode: /Rows must be in [0, %d] (got %d)", maxCCITTDimension, rows)
	}

	rowBytes := (columns + 7) / 8
	knownRows := rows > 0
	// A malicious /Rows and /Columns pair could each individually pass
	// the per-axis check above yet still multiply out to an enormous
	// output (up to 1<<20 rows of 1<<20/8 bytes each is over 100GB) long
	// before this package's caller (Decode, in filter.go) gets a chance
	// to reject an over-large result - it only checks *after* a filter
	// step returns. So bound the total here too, up front, exactly the
	// same "check before allocating" pattern dct.go uses for a JPEG's
	// declared width times height - see the package doc comment's
	// "Bounded decompression" section.
	if knownRows && rowBytes*rows > maxDecodedSize {
		return nil, pdferror.Malformedf("CCITTFaxDecode: %d rows of %d bytes exceeds this package's %d-byte bound", rows, rowBytes, maxDecodedSize)
	}

	d := &ccittDecoder{
		bits:       ccittBits{data: data},
		columns:    columns,
		byteAlign:  byteAlign,
		eol:        eol,
		eoblock:    eoblock,
		k:          k,
		codingLine: make([]int, columns+1),
		refLine:    make([]int, columns+2),
		// K < 0 selects pure Group 4 (T.6): every row is 2D-coded
		// against the previous one, with no per-row tag bit deciding
		// otherwise (that tag bit is only a K > 0, "mixed 1D/2D Group 3"
		// thing - see the K > 0 branches below). K == 0 (plain Group 3)
		// is pure 1D and never switches into 2D at all.
		nextLine2D: k < 0,
	}
	// The decoder's very first "previous row" is imaginary: an entirely
	// white line whose only transition is at the right edge. Recording
	// that as codingLine (rather than a separately-tracked flag) lets
	// decodeRow's normal "copy codingLine into refLine" step at the top
	// of every row - including the first - work unmodified.
	d.codingLine[0] = columns

	// Skip any leading fill bits before the first row's real data -
	// real-world encoders sometimes pad the very start of the stream out
	// to a byte boundary, or begin with an optional EOL code, neither of
	// which carries any pixel data of its own.
	code := d.bits.look(12)
	for code == 0 {
		d.bits.eat(1)
		code = d.bits.look(12)
	}
	if code == 1 {
		d.bits.eat(12)
	}
	if k > 0 {
		d.nextLine2D = d.bits.look(1) == 0
		d.bits.eat(1)
	}

	// maxRows bounds how many rows this function will ever attempt to
	// decode. When /Rows is known it is simply that count (already
	// proven small enough above). When /Rows is 0 ("unknown" - the PDF
	// specification leaves it to be inferred some other way, but this
	// package only ever sees a stream's own /DecodeParms, never the
	// enclosing image dictionary's /Height - see internal/image, which
	// does have that and cross-checks the total sample count against
	// it), this package instead decodes rows until the data itself says
	// to stop (an EOFB marker, or the input simply running out - see the
	// loop below), bounded here only as a defensive last resort so a
	// pathological input cannot loop forever.
	maxRows := rows
	if !knownRows {
		maxRows = maxDecodedSize / max(rowBytes, 1)
	}

	out := make([]byte, 0, maxRows*rowBytes)
	row := make([]byte, rowBytes)

	for r := 0; r < maxRows; r++ {
		if d.eof {
			if !knownRows {
				// /Rows was unknown and the data has signaled its own
				// end (see below) - stop here rather than padding out
				// to maxRows, which is only a safety ceiling, not a
				// real row count.
				break
			}
			// /Rows was known (so the caller - ultimately
			// internal/image, cross-checking against the image
			// dictionary's /Height - expects exactly that many rows of
			// output) but the data ran out first, e.g. a truncated or
			// otherwise damaged stream. Pad the remainder white rather
			// than failing the whole image: this matches how other PDF
			// viewers degrade a damaged fax stream, and lets
			// internal/image's own length check succeed instead of
			// rejecting an otherwise-readable page over its last few
			// rows.
			clear(row)
			setRun(row, 0, columns, !blackIs1)
			out = append(out, row...)
			continue
		}

		d.decodeRow()
		clear(row)
		packCCITTRow(row, d.codingLine, columns, blackIs1)
		out = append(out, row...)

		if byteAlign {
			d.bits.alignToByte()
		}

		gotEOL := false
		if !eoblock && knownRows && r == rows-1 {
			// /EndOfBlock is false, so the stream was never going to end
			// with an EOFB marker (six EOL codes) in the first place -
			// and this was the last row the caller asked for, so there
			// is nothing further worth scanning for.
		} else {
			code = d.bits.look(12)
			if eol {
				for code != ccittEOF && code != 1 {
					d.bits.eat(1)
					code = d.bits.look(12)
				}
			} else {
				for code == 0 {
					d.bits.eat(1)
					code = d.bits.look(12)
				}
			}
			if code == 1 {
				d.bits.eat(12)
				gotEOL = true
			} else if code == ccittEOF {
				d.eof = true
			}
		}

		if !d.eof && k > 0 {
			d.nextLine2D = d.bits.look(1) == 0
			d.bits.eat(1)
		}

		if eoblock && gotEOL && byteAlign {
			// Having just consumed one EOL code, check whether five more
			// immediately follow - together, six consecutive EOL codes
			// form T.6's "return to control" (RTC) sequence, which is
			// how a stream with /EndOfBlock true (the default) marks its
			// own end. This is the mechanism that lets the /Rows-unknown
			// case above know when to stop.
			code = d.bits.look(12)
			if code == 1 {
				d.bits.eat(12)
				if k > 0 {
					d.bits.eat(1)
				}
				for i := 0; i < 4; i++ {
					d.bits.look(12)
					d.bits.eat(12)
					if k > 0 {
						d.bits.eat(1)
					}
				}
				d.eof = true
			}
		}
	}

	return out, nil
}

// ccittEOF is the sentinel ccittBits.look returns once the input is
// completely exhausted (as opposed to merely short of the requested bit
// count, which look instead handles by zero-padding - see its own doc
// comment). It doubles as the twoDim/white/black code functions' own
// "ran out of data mid-code" return value, since -1 can never collide
// with a real run length or 2D mode (both always >= 0).
const ccittEOF = -1

// ccittEOL is the run-length value the white/black Huffman tables report
// for the one code word (000000000001) that is never actually a run
// length: the optional end-of-line marker, which can only legally appear
// between rows, not in the middle of decoding one. See getWhiteCode and
// getBlackCode's doc comments for how encountering it mid-run is
// (leniently) handled.
const ccittEOL = -2

// The nine things a 2D-coded row's Huffman code can mean, per T.6:
// "pass" and "horizontal" mode, and seven "vertical" modes (V0 plus
// three "left" and three "right" variants) - see decodeRow's own doc
// comment for what each one means geometrically.
const (
	twoDimPass = iota
	twoDimHoriz
	twoDimVert0
	twoDimVertR1
	twoDimVertL1
	twoDimVertR2
	twoDimVertL2
	twoDimVertR3
	twoDimVertL3
)

// huffCode is one entry in a Huffman code table: bits is the code word's
// length, and run is what it decodes to (a pixel run length for the
// white/black tables, or one of the twoDim* mode constants above for
// twoDimTable). bits <= 0 marks an entry that is never itself a complete
// code word - only ever seen as an intermediate lookup miss, since every
// table below is indexed by a fixed-width lookahead rather than walked
// bit by bit (see ccittDecoder.getWhiteCode's doc comment for why that
// works).
type huffCode struct {
	bits int8
	run  int16
}

// twoDimTable, whiteTable1, whiteTable2, blackTable1, blackTable2, and
// blackTable3 are the CCITT (ITU-T T.4 Tables 2-4, T.6 Table 1) Huffman
// code tables, transcribed - not retyped by hand, to eliminate any risk
// of a stray digit - from xpdf/pdf.js's implementation (see this file's
// package doc comment). Each table is sized and indexed to support a
// direct array lookup by a fixed-width bit lookahead (7 bits for 2D mode
// codes, 12 for white run codes, 13 for black run codes) instead of the
// more obvious "read one bit at a time until a code matches" approach:
// a short code word (say, 4 bits) that is a valid complete code on its
// own occupies every table slot whose top 4 bits match it, regardless of
// what the remaining lookahead bits happen to be (hence the repeated
// entries below) - so indexing by the full lookahead and checking the
// stored bits field (how much of the lookahead the matched code word
// actually used) finds the right code in one lookup rather than up to
// 13 sequential ones. See getWhiteCode/getBlackCode/getTwoDimCode.
var twoDimTable = [128]huffCode{
	{-1, -1}, {-1, -1}, {7, 8}, {7, 7}, {6, 6}, {6, 6}, {6, 5}, {6, 5},
	{4, 0}, {4, 0}, {4, 0}, {4, 0}, {4, 0}, {4, 0}, {4, 0}, {4, 0},
	{3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1},
	{3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1},
	{3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4},
	{3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4},
	{3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3},
	{3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3}, {3, 3},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
	{1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2},
}

var whiteTable1 = [32]huffCode{
	{-1, -1}, {12, -2}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1},
	{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1},
	{11, 1792}, {11, 1792}, {12, 1984}, {12, 2048}, {12, 2112}, {12, 2176}, {12, 2240}, {12, 2304},
	{11, 1856}, {11, 1856}, {11, 1920}, {11, 1920}, {12, 2368}, {12, 2432}, {12, 2496}, {12, 2560},
}

var whiteTable2 = [512]huffCode{
	{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {8, 29}, {8, 29}, {8, 30}, {8, 30},
	{8, 45}, {8, 45}, {8, 46}, {8, 46}, {7, 22}, {7, 22}, {7, 22}, {7, 22},
	{7, 23}, {7, 23}, {7, 23}, {7, 23}, {8, 47}, {8, 47}, {8, 48}, {8, 48},
	{6, 13}, {6, 13}, {6, 13}, {6, 13}, {6, 13}, {6, 13}, {6, 13}, {6, 13},
	{7, 20}, {7, 20}, {7, 20}, {7, 20}, {8, 33}, {8, 33}, {8, 34}, {8, 34},
	{8, 35}, {8, 35}, {8, 36}, {8, 36}, {8, 37}, {8, 37}, {8, 38}, {8, 38},
	{7, 19}, {7, 19}, {7, 19}, {7, 19}, {8, 31}, {8, 31}, {8, 32}, {8, 32},
	{6, 1}, {6, 1}, {6, 1}, {6, 1}, {6, 1}, {6, 1}, {6, 1}, {6, 1},
	{6, 12}, {6, 12}, {6, 12}, {6, 12}, {6, 12}, {6, 12}, {6, 12}, {6, 12},
	{8, 53}, {8, 53}, {8, 54}, {8, 54}, {7, 26}, {7, 26}, {7, 26}, {7, 26},
	{8, 39}, {8, 39}, {8, 40}, {8, 40}, {8, 41}, {8, 41}, {8, 42}, {8, 42},
	{8, 43}, {8, 43}, {8, 44}, {8, 44}, {7, 21}, {7, 21}, {7, 21}, {7, 21},
	{7, 28}, {7, 28}, {7, 28}, {7, 28}, {8, 61}, {8, 61}, {8, 62}, {8, 62},
	{8, 63}, {8, 63}, {8, 0}, {8, 0}, {8, 320}, {8, 320}, {8, 384}, {8, 384},
	{5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10},
	{5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10}, {5, 10},
	{5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11},
	{5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11}, {5, 11},
	{7, 27}, {7, 27}, {7, 27}, {7, 27}, {8, 59}, {8, 59}, {8, 60}, {8, 60},
	{9, 1472}, {9, 1536}, {9, 1600}, {9, 1728}, {7, 18}, {7, 18}, {7, 18}, {7, 18},
	{7, 24}, {7, 24}, {7, 24}, {7, 24}, {8, 49}, {8, 49}, {8, 50}, {8, 50},
	{8, 51}, {8, 51}, {8, 52}, {8, 52}, {7, 25}, {7, 25}, {7, 25}, {7, 25},
	{8, 55}, {8, 55}, {8, 56}, {8, 56}, {8, 57}, {8, 57}, {8, 58}, {8, 58},
	{6, 192}, {6, 192}, {6, 192}, {6, 192}, {6, 192}, {6, 192}, {6, 192}, {6, 192},
	{6, 1664}, {6, 1664}, {6, 1664}, {6, 1664}, {6, 1664}, {6, 1664}, {6, 1664}, {6, 1664},
	{8, 448}, {8, 448}, {8, 512}, {8, 512}, {9, 704}, {9, 768}, {8, 640}, {8, 640},
	{8, 576}, {8, 576}, {9, 832}, {9, 896}, {9, 960}, {9, 1024}, {9, 1088}, {9, 1152},
	{9, 1216}, {9, 1280}, {9, 1344}, {9, 1408}, {7, 256}, {7, 256}, {7, 256}, {7, 256},
	{4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2},
	{4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2},
	{4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2},
	{4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2}, {4, 2},
	{4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3},
	{4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3},
	{4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3},
	{4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3}, {4, 3},
	{5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128},
	{5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128}, {5, 128},
	{5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8},
	{5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8}, {5, 8},
	{5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9},
	{5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9}, {5, 9},
	{6, 16}, {6, 16}, {6, 16}, {6, 16}, {6, 16}, {6, 16}, {6, 16}, {6, 16},
	{6, 17}, {6, 17}, {6, 17}, {6, 17}, {6, 17}, {6, 17}, {6, 17}, {6, 17},
	{4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4},
	{4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4},
	{4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4},
	{4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4}, {4, 4},
	{4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5},
	{4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5},
	{4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5},
	{4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5}, {4, 5},
	{6, 14}, {6, 14}, {6, 14}, {6, 14}, {6, 14}, {6, 14}, {6, 14}, {6, 14},
	{6, 15}, {6, 15}, {6, 15}, {6, 15}, {6, 15}, {6, 15}, {6, 15}, {6, 15},
	{5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64},
	{5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64}, {5, 64},
	{4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6},
	{4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6},
	{4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6},
	{4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 6},
	{4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7},
	{4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7},
	{4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7},
	{4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7}, {4, 7},
}

var blackTable1 = [128]huffCode{
	{-1, -1}, {-1, -1}, {12, -2}, {12, -2}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1},
	{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1},
	{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1},
	{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1},
	{11, 1792}, {11, 1792}, {11, 1792}, {11, 1792}, {12, 1984}, {12, 1984}, {12, 2048}, {12, 2048},
	{12, 2112}, {12, 2112}, {12, 2176}, {12, 2176}, {12, 2240}, {12, 2240}, {12, 2304}, {12, 2304},
	{11, 1856}, {11, 1856}, {11, 1856}, {11, 1856}, {11, 1920}, {11, 1920}, {11, 1920}, {11, 1920},
	{12, 2368}, {12, 2368}, {12, 2432}, {12, 2432}, {12, 2496}, {12, 2496}, {12, 2560}, {12, 2560},
	{10, 18}, {10, 18}, {10, 18}, {10, 18}, {10, 18}, {10, 18}, {10, 18}, {10, 18},
	{12, 52}, {12, 52}, {13, 640}, {13, 704}, {13, 768}, {13, 832}, {12, 55}, {12, 55},
	{12, 56}, {12, 56}, {13, 1280}, {13, 1344}, {13, 1408}, {13, 1472}, {12, 59}, {12, 59},
	{12, 60}, {12, 60}, {13, 1536}, {13, 1600}, {11, 24}, {11, 24}, {11, 24}, {11, 24},
	{11, 25}, {11, 25}, {11, 25}, {11, 25}, {13, 1664}, {13, 1728}, {12, 320}, {12, 320},
	{12, 384}, {12, 384}, {12, 448}, {12, 448}, {13, 512}, {13, 576}, {12, 53}, {12, 53},
	{12, 54}, {12, 54}, {13, 896}, {13, 960}, {13, 1024}, {13, 1088}, {13, 1152}, {13, 1216},
	{10, 64}, {10, 64}, {10, 64}, {10, 64}, {10, 64}, {10, 64}, {10, 64}, {10, 64},
}

var blackTable2 = [192]huffCode{
	{8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13},
	{8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13}, {8, 13},
	{11, 23}, {11, 23}, {12, 50}, {12, 51}, {12, 44}, {12, 45}, {12, 46}, {12, 47},
	{12, 57}, {12, 58}, {12, 61}, {12, 256}, {10, 16}, {10, 16}, {10, 16}, {10, 16},
	{10, 17}, {10, 17}, {10, 17}, {10, 17}, {12, 48}, {12, 49}, {12, 62}, {12, 63},
	{12, 30}, {12, 31}, {12, 32}, {12, 33}, {12, 40}, {12, 41}, {11, 22}, {11, 22},
	{8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14},
	{8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14}, {8, 14},
	{7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10},
	{7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10},
	{7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10},
	{7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10}, {7, 10},
	{7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11},
	{7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11},
	{7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11},
	{7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11}, {7, 11},
	{9, 15}, {9, 15}, {9, 15}, {9, 15}, {9, 15}, {9, 15}, {9, 15}, {9, 15},
	{12, 128}, {12, 192}, {12, 26}, {12, 27}, {12, 28}, {12, 29}, {11, 19}, {11, 19},
	{11, 20}, {11, 20}, {12, 34}, {12, 35}, {12, 36}, {12, 37}, {12, 38}, {12, 39},
	{11, 21}, {11, 21}, {12, 42}, {12, 43}, {10, 0}, {10, 0}, {10, 0}, {10, 0},
	{7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12},
	{7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12},
	{7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12},
	{7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12}, {7, 12},
}

var blackTable3 = [64]huffCode{
	{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {6, 9}, {6, 8}, {5, 7}, {5, 7},
	{4, 6}, {4, 6}, {4, 6}, {4, 6}, {4, 5}, {4, 5}, {4, 5}, {4, 5},
	{3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1}, {3, 1},
	{3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4}, {3, 4},
	{2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3},
	{2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3}, {2, 3},
	{2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2},
	{2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2}, {2, 2},
}
// ccittBits is a most-significant-bit-first bit reader over an
// in-memory byte slice, with one non-obvious feature the rest of this
// file leans on: look can be asked for more bits than remain in data
// without that being an error. Every CCITT code word this package looks
// up is found by peeking a fixed number of bits (7, 12, or 13 - see the
// table doc comment above) and then, once the actual code word's
// shorter length is known, giving back the unused ones with eat; but
// near the very end of a row's - or the whole stream's - data there may
// genuinely be fewer bits left than the fixed peek width calls for. Real
// encoders rely on this: they do not pad the final code word out to 13
// bits, so a correct reader must treat "peeked past the end" as
// "the remaining real bits, followed by zeros" rather than a hard error,
// only actually failing once there is nothing at all left to peek at.
type ccittBits struct {
	data []byte
	pos  int // index into data of the next unread byte

	// buf holds the most recently read bytes, and bufBits how many of
	// its low-order bits are still "unconsumed" - i.e. still ahead of
	// the reader's logical position. Bytes are pulled into buf 8 bits at
	// a time only when look needs more bits than bufBits currently
	// holds; eat then just decrements bufBits; it never has to shift buf
	// itself; the +8-bits-at-a-time growth keeps bufBits comfortably
	// under 32 (look never asks for more than 13 bits at once), so buf
	// never risks overflowing its 32 bits.
	buf     uint32
	bufBits int
}

// look returns the next n bits (n <= 16) of the stream without consuming
// them - call eat afterwards to actually advance past however many of
// them turned out to belong to the code word just decoded. It returns
// ccittEOF only once not even one more bit of real data remains; short
// of that, running out mid-peek zero-pads the result (see the type's own
// doc comment for why that is the correct behavior here, not a bug).
func (b *ccittBits) look(n int) int {
	for b.bufBits < n {
		if b.pos >= len(b.data) {
			if b.bufBits == 0 {
				return ccittEOF
			}
			return int((b.buf << uint(n-b.bufBits)) & (0xFFFF >> uint(16-n)))
		}
		b.buf = b.buf<<8 | uint32(b.data[b.pos])
		b.pos++
		b.bufBits += 8
	}
	return int((b.buf >> uint(b.bufBits-n)) & (0xFFFF >> uint(16-n)))
}

// eat discards the first n bits most recently returned by look, so the
// next look call starts just past them.
func (b *ccittBits) eat(n int) {
	b.bufBits -= n
	if b.bufBits < 0 {
		b.bufBits = 0
	}
}

// alignToByte discards whatever fractional bits (0-7 of them) remain
// before the next byte boundary of the original data - CCITTFaxDecode's
// /EncodedByteAlign parameter asks for exactly this between rows, so
// that a row's data always starts at a byte boundary even though its
// Huffman-coded length is essentially never a multiple of 8 bits.
func (b *ccittBits) alignToByte() {
	b.bufBits -= b.bufBits % 8
}

// ccittDecoder holds the state that decodeRow (and the run-length/mode
// code lookups it calls) needs to carry from one row to the next -
// principally the previous row's decoded transitions, since Group 4's 2D
// coding is defined relative to them. See decodeCCITT for how this is
// constructed and driven, and decodeRow's own doc comment for what the
// fields below actually mean during a single row's decoding.
type ccittDecoder struct {
	bits ccittBits

	columns   int
	k         int
	byteAlign bool
	eol       bool
	eoblock   bool

	// codingLine holds the row currently being (or most recently)
	// decoded, as a sequence of ascending "changing element" columns:
	// pixels [0, codingLine[0]) are white, [codingLine[0], codingLine[1])
	// are black, and so on, alternating, until a value >= columns closes
	// the row off. codingPos is the index of the last entry written so
	// far. refLine holds the previous row's finished codingLine, copied
	// there at the start of decodeRow before codingLine is overwritten -
	// it needs two extra slots beyond columns+1 for the pair of
	// past-the-edge sentinel entries decodeRow appends (see there).
	codingLine []int
	refLine    []int
	codingPos  int

	// nextLine2D says whether the row about to be decoded uses 2D
	// (Group 4 / Group 3-2D) or plain 1D coding - see decodeCCITT's
	// comment on K for how this is initialized and, for K > 0, updated
	// after every row from a one-bit tag the encoder writes there.
	nextLine2D bool

	// eof becomes true once the data has signaled (via T.6's "return to
	// control" marker, or by genuinely running out) that there is
	// nothing more to decode - see decodeCCITT's row loop.
	eof bool
}

// decodeRow decodes exactly one row into d.codingLine, consuming
// whatever Huffman-coded bits that takes from d.bits, using either 1D
// (Modified Huffman: every run's absolute length is coded directly) or
// 2D (Modified READ: every run is coded relative to the previous row's
// matching transition) coding according to d.nextLine2D.
//
// The 2D case is the one worth understanding geometrically, since it is
// what Group 4 (the overwhelmingly common real-world case) always uses.
// At any point while scanning a row left to right, call a0 the position
// just decided (starting at an imaginary -1, i.e. before column 0), and
// on the reference line (the previous row, already fully decoded, held
// in d.refLine): b1 is the first transition to the right of a0 whose
// color is the *opposite* of a0's current color, and b2 is the next
// transition after that. Each 2D code word says how to extend the
// coding line past a0 using those two reference points:
//
//   - Pass: the reference line's run (from b1 to b2) is entirely
//     skipped over - the current row's run continues at least that far
//     without a changing element of its own yet, so a0 effectively jumps
//     to b2 without recording a transition at b1.
//   - Vertical (V0, VR1-3, VL1-3): the current row has a transition
//     close to b1 - within 3 pixels either side of it, hence seven
//     variants (0, right-1..3, left-1..3) - which is by far the most
//     common case for real scanned pages, since consecutive lines rarely
//     differ by more than a few pixels at any one edge.
//   - Horizontal: the current row's transition is nowhere near b1, so
//     give up comparing against the reference line for this run and
//     instead code the next *two* runs' absolute lengths directly, 1D-
//     style (one of whichever color a0 currently is, then one of the
//     other).
//
// codingPos/refPos below are indexes into codingLine/refLine tracking a0
// and (via b1 = refLine[refPos], b2 = refLine[refPos+1]) the reference
// line position, exactly mirroring that description; addPixels and
// addPixelsNeg are what actually record a new transition once one of the
// above has decided where it belongs.
func (d *ccittDecoder) decodeRow() {
	if d.nextLine2D {
		i := 0
		for d.codingLine[i] < d.columns {
			d.refLine[i] = d.codingLine[i]
			i++
		}
		d.refLine[i] = d.columns
		i++
		d.refLine[i] = d.columns

		d.codingLine[0] = 0
		d.codingPos = 0
		refPos := 0
		black := 0

		for d.codingLine[d.codingPos] < d.columns {
			switch mode := d.getTwoDimCode(); mode {
			case twoDimPass:
				d.addPixels(d.refLine[refPos+1], black)
				if d.refLine[refPos+1] < d.columns {
					refPos += 2
				}
			case twoDimHoriz:
				var run1, run2 int
				if black != 0 {
					run1 = d.readRun(true)
					run2 = d.readRun(false)
				} else {
					run1 = d.readRun(false)
					run2 = d.readRun(true)
				}
				d.addPixels(d.codingLine[d.codingPos]+run1, black)
				if d.codingLine[d.codingPos] < d.columns {
					d.addPixels(d.codingLine[d.codingPos]+run2, black^1)
				}
				for d.refLine[refPos] <= d.codingLine[d.codingPos] && d.refLine[refPos] < d.columns {
					refPos += 2
				}
			case twoDimVert0:
				refPos = d.vertMode(0, refPos, &black)
			case twoDimVertR1:
				refPos = d.vertMode(1, refPos, &black)
			case twoDimVertR2:
				refPos = d.vertMode(2, refPos, &black)
			case twoDimVertR3:
				refPos = d.vertMode(3, refPos, &black)
			case twoDimVertL1:
				refPos = d.vertModeNeg(-1, refPos, &black)
			case twoDimVertL2:
				refPos = d.vertModeNeg(-2, refPos, &black)
			case twoDimVertL3:
				refPos = d.vertModeNeg(-3, refPos, &black)
			default: // ccittEOF: no more 2D codes to read.
				d.addPixels(d.columns, 0)
				d.eof = true
			}
		}
		return
	}

	d.codingLine[0] = 0
	d.codingPos = 0
	black := 0
	for d.codingLine[d.codingPos] < d.columns {
		run := d.readRun(black != 0)
		d.addPixels(d.codingLine[d.codingPos]+run, black)
		black ^= 1
	}
}

// readRun reads one full run length: a Huffman code from the black or
// white table names either a "terminating" run length (0-63, ending the
// run) or a "makeup" length (a multiple of 64, from 64 up to 2560 -
// T.4's way of coding a long run as a sequence of large chunks followed
// by one final terminating code without needing individual code words
// for every possible length). readRun keeps reading and summing makeup
// codes (>= 64) until a terminating one (< 64) ends the run.
func (d *ccittDecoder) readRun(black bool) int {
	total := 0
	for {
		var n int
		if black {
			n = d.getBlackCode()
		} else {
			n = d.getWhiteCode()
		}
		total += n
		if n < 64 {
			return total
		}
	}
}

// vertMode and vertModeNeg both implement one of the seven "vertical"
// 2D modes described in decodeRow's doc comment: the current row's
// transition sits offset pixels to the right (vertMode, offset 0-3) or
// left (vertModeNeg, offset -1 to -3) of the reference line's b1
// (d.refLine[refPos]). They differ only in which of addPixels/
// addPixelsNeg is correct for recording that new position - see those
// two functions' own doc comments for why a "left" offset needs the more
// careful one - and otherwise share the same bookkeeping: toggle the
// current color, then advance refPos past whatever reference-line
// transitions the new a0 has now moved beyond.
func (d *ccittDecoder) vertMode(offset, refPos int, black *int) int {
	d.addPixels(d.refLine[refPos]+offset, *black)
	*black ^= 1
	if d.codingLine[d.codingPos] < d.columns {
		refPos++
		for d.refLine[refPos] <= d.codingLine[d.codingPos] && d.refLine[refPos] < d.columns {
			refPos += 2
		}
	}
	return refPos
}

func (d *ccittDecoder) vertModeNeg(offset, refPos int, black *int) int {
	d.addPixelsNeg(d.refLine[refPos]+offset, *black)
	*black ^= 1
	if d.codingLine[d.codingPos] < d.columns {
		if refPos > 0 {
			refPos--
		} else {
			refPos++
		}
		for d.refLine[refPos] <= d.codingLine[d.codingPos] && d.refLine[refPos] < d.columns {
			refPos += 2
		}
	}
	return refPos
}

// addPixels records a1 as the coding line's next transition, provided it
// actually advances past the current one (a2D code that would move
// backwards is simply ignored, rather than corrupting the row - a
// malformed-input safety net, not something well-formed data should ever
// trigger). black indicates which color is being closed off by this
// transition, which decides whether it lands at an even or odd
// codingPos - codingLine's entries must keep alternating white/black
// starting from white, so if the parity implied by black does not match
// codingPos's own, codingPos is advanced by one first to fix that up.
func (d *ccittDecoder) addPixels(a1, black int) {
	if a1 > d.codingLine[d.codingPos] {
		if a1 > d.columns {
			a1 = d.columns
		}
		if (d.codingPos&1)^black != 0 {
			d.codingPos++
		}
		d.codingLine[d.codingPos] = a1
	}
}

// addPixelsNeg is addPixels' counterpart for the three "left" vertical
// modes, which can legitimately compute a position at or behind the
// current codingPos (unlike every other mode): b1 minus up to 3 can land
// on top of - or before - a transition already recorded earlier in this
// same row. When that happens this rewinds codingPos to wherever a1
// actually belongs (scanning back past any already-recorded transitions
// greater than a1) instead of simply refusing the update, since here a
// backwards-looking value is an expected, correctly-decodable case, not
// a sign of malformed input.
func (d *ccittDecoder) addPixelsNeg(a1, black int) {
	switch {
	case a1 > d.codingLine[d.codingPos]:
		if a1 > d.columns {
			a1 = d.columns
		}
		if (d.codingPos&1)^black != 0 {
			d.codingPos++
		}
		d.codingLine[d.codingPos] = a1
	case a1 < d.codingLine[d.codingPos]:
		if a1 < 0 {
			a1 = 0
		}
		for d.codingPos > 0 && a1 < d.codingLine[d.codingPos-1] {
			d.codingPos--
		}
		d.codingLine[d.codingPos] = a1
	}
}

// getTwoDimCode reads one 2D mode code word (see decodeRow's doc
// comment for what each mode means) via a direct 7-bit table lookup -
// see the huffCode/table doc comment above for why a fixed-width lookup
// works without walking the code bit by bit. Returns ccittEOF both when
// the stream has genuinely run out and - since real encoder output never
// produces a 7-bit lookahead that fails to match any table entry - on
// the data being malformed in a way this package chooses not to try
// recovering from; either way, the caller (decodeRow) treats that as
// "nothing more to decode in this row or image", which is a reasonable
// way to degrade on damaged input without risking a decode loop that
// never terminates.
func (d *ccittDecoder) getTwoDimCode() int {
	code := d.bits.look(7)
	if code < 0 {
		return ccittEOF
	}
	e := twoDimTable[code]
	if e.bits <= 0 {
		return ccittEOF
	}
	d.bits.eat(int(e.bits))
	return int(e.run)
}

// getWhiteCode and getBlackCode each read one run-length code word from
// their respective Huffman table (T.4 uses entirely different code words
// for a white run of a given length than a black run of the same
// length, hence separate tables and functions). Both use the same
// direct-lookup trick as getTwoDimCode, just at wider fixed widths (12
// and 13 bits respectively, matching the longest code word either table
// contains) and split across two or three tables purely to keep any one
// literal array a manageable size - see whiteTable1/2 and
// blackTable1/2/3's own indexing (code >> 5, code >> 7, etc.), which is
// just "which of these tables' 12/13-bit lookahead ranges does this code
// word's leading bits fall into".
//
// On a lookahead that matches no code word at all, or that happens to
// spell out the mid-row-illegal EOL code word (see the ccittEOL constant
// above), both functions deliberately do not fail the whole decode: they
// eat one bit and report a run of 1, letting the reader resynchronize
// against the next bit rather than aborting outright. This matches the
// xpdf/pdf.js implementation this file ports (see the package doc
// comment) and is a deliberate leniency, not an oversight - some
// real-world scanner output is not perfectly conformant, and producing a
// locally-wrong pixel or two is preferable to refusing to render an
// otherwise-readable page over it.
func (d *ccittDecoder) getWhiteCode() int {
	code := d.bits.look(12)
	if code == ccittEOF {
		return 1
	}
	var e huffCode
	if code>>5 == 0 {
		e = whiteTable1[code]
	} else {
		e = whiteTable2[code>>3]
	}
	if e.bits > 0 {
		d.bits.eat(int(e.bits))
		return int(e.run)
	}
	d.bits.eat(1)
	return 1
}

func (d *ccittDecoder) getBlackCode() int {
	code := d.bits.look(13)
	if code == ccittEOF {
		return 1
	}
	var e huffCode
	switch {
	case code>>7 == 0:
		e = blackTable1[code]
	case code>>9 == 0:
		e = blackTable2[(code>>1)-64]
	default:
		e = blackTable3[code>>7]
	}
	if e.bits > 0 {
		d.bits.eat(int(e.bits))
		return int(e.run)
	}
	d.bits.eat(1)
	return 1
}

// packCCITTRow converts one fully-decoded row's transitions
// (codingLine, as described on ccittDecoder's codingLine field) into
// this package's actual output format: columns bits, most-significant-
// bit first, packed into row (which the caller has already zeroed and
// sized to exactly (columns+7)/8 bytes - CCITTFaxDecode images are
// always /BitsPerComponent 1, so that is the packing internal/image's
// own bitReader expects for every row).
//
// Per the PDF specification, a 0 bit means black and a 1 bit means white
// unless /BlackIs1 says the reverse - blackIs1 here is exactly that
// parameter, already read out of /DecodeParms by decodeCCITT.
func packCCITTRow(row []byte, codingLine []int, columns int, blackIs1 bool) {
	pos := 0
	black := false
	for i := 0; pos < columns; i++ {
		end := codingLine[i]
		if end > columns {
			end = columns
		}
		if end > pos {
			setRun(row, pos, end-pos, black == blackIs1)
			pos = end
		}
		black = !black
	}
}

// setRun sets count consecutive bits starting at bit offset start within
// row to 1 if one is true or 0 otherwise, using the same most-
// significant-bit-first packing internal/image's bitReader (decode.go
// there) reads: bit 0 of a byte is its most significant bit. Whole bytes
// in the middle of the range are set in one assignment each rather than
// bit by bit, since a run frequently spans many bytes (a wide white
// margin, for instance) and there is no reason to pay for 8 individual
// bit-set operations when one byte-set does the same job.
func setRun(row []byte, start, count int, one bool) {
	if count <= 0 {
		return
	}
	end := start + count

	for start < end && start%8 != 0 {
		setBitMSB(row, start, one)
		start++
	}

	fill := byte(0x00)
	if one {
		fill = 0xFF
	}
	for start+8 <= end {
		row[start/8] = fill
		start += 8
	}

	for start < end {
		setBitMSB(row, start, one)
		start++
	}
}

// setBitMSB sets (or clears) a single bit within row, bit 0 of each byte
// being its most significant bit.
func setBitMSB(row []byte, pos int, one bool) {
	idx := pos / 8
	mask := byte(1 << uint(7-pos%8))
	if one {
		row[idx] |= mask
	} else {
		row[idx] &^= mask
	}
}
