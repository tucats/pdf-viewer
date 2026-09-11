package filter

import (
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements PDF's LZWDecode filter from scratch, rather than
// using the standard library's compress/lzw. That package's own doc
// comment claims to implement "LZW as used by the GIF and PDF file
// formats", which sounded like exactly the right fit - but debugging a
// real-world PDF (produced by an early-2000s Adobe tool) that this
// project's viewer failed to render, while macOS Preview rendered it
// fine, turned up a genuine mismatch: compress/lzw grows its code width
// (9 bits -> 10 -> 11 -> 12, as the code table fills up) one code later
// than ISO 32000-1's default /EarlyChange behavior requires. Comparing
// byte-for-byte against qpdf's independent, spec-conforming decoder
// confirmed it: compress/lzw's fixed, undocumented behavior actually
// matches /EarlyChange 0 (the rare, non-default case, which the spec
// describes as growing the code width "postponed by one code" relative
// to the default), not /EarlyChange 1 (the default - see lzwCodeWidth's
// doc comment for the exact arithmetic). Since compress/lzw offers no
// way to select the other behavior, and /EarlyChange 1 is what the
// overwhelming majority of real-world PDF producers use (it is the
// default, and /DecodeParms rarely bothers to say so explicitly), a
// document written by any producer that didn't happen to match
// compress/lzw's assumption would silently desync mid-image the moment
// the code width needed to grow - every code after that point decodes
// to noise, though this shows up to a caller merely as an "invalid
// code" error, since a desynced bit position essentially never lines up
// with a valid code by chance. Implementing the filter directly, with
// the code-width arithmetic spelled out explicitly, means this
// package's behavior no longer depends on guessing right about an
// undocumented standard library implementation detail.

// lzwClearCode resets the table to its initial 256 literal entries -
// PDF (like GIF) reserves this code value, one past the highest
// possible 8-bit literal, for that purpose.
//
// lzwEODCode ends the stream - a conforming encoder always emits it
// before running out of input, but a truncated file might not, so
// lzwDecodeBytes below also treats simply running out of bits as the
// end of the stream rather than an error.
//
// lzwFirstCode is the first code number the table assigns to an actual
// multi-byte sequence, once decoding finds one worth adding - codes
// below it are always either a literal byte (0-255) or one of the two
// codes above.
const (
	lzwClearCode = 256
	lzwEODCode   = 257
	lzwFirstCode = 258

	// lzwMaxTableSize is 2^12, the largest a PDF LZW table is ever
	// allowed to grow (code widths top out at 12 bits). A conforming
	// encoder sends a Clear code before this would ever matter; an
	// encoder that doesn't (relying on the reader to simply keep using
	// the frozen, no-longer-growing table) is tolerated here rather
	// than rejected, matching this project's general preference for
	// lenient reading over strict validation - see the package doc
	// comment's note on filter chains.
	lzwMaxTableSize = 1 << 12
)

// decodeLZW reverses LZWDecode: data is the still-encoded bytes taken
// directly from a stream's /Filter LZWDecode content, and parms is that
// stream's (already filter-specific) /DecodeParms dictionary, or nil if
// it had none.
func decodeLZW(data []byte, parms syntax.Dictionary) ([]byte, error) {
	early := intParm(parms, "EarlyChange", 1)
	if early != 0 && early != 1 {
		return nil, pdferror.Malformedf("LZWDecode: /EarlyChange must be 0 or 1, found %d", early)
	}

	out, err := lzwDecodeBytes(data, early == 1)
	if err != nil {
		return nil, pdferror.Malformedf("LZW decode: %v", err)
	}

	return applyPredictor(out, parms)
}

// lzwDecodeBytes does the actual decoding: data is read as a stream of
// MSB-first, variable-width codes (9 bits, growing to 12 - see
// lzwCodeWidth), each of which is either the Clear or EOD code, a
// literal byte (0-255), or a reference to a longer byte sequence built
// up earlier in the same stream.
//
// earlyChange selects which of PDF's two /EarlyChange behaviors this
// stream's producer used - see lzwCodeWidth's doc comment for what that
// actually changes.
func lzwDecodeBytes(data []byte, earlyChange bool) ([]byte, error) {
	br := newLZWBitReader(data)

	// table[c] is the byte sequence code c expands to. The first 256
	// entries are the fixed one-byte literals; entries 256 and 257 are
	// never looked up (they're the Clear and EOD codes, handled
	// separately below) but are still counted, so that every later
	// code's table index lines up with its code value exactly as the
	// encoder saw it. Everything from lzwFirstCode on is appended while
	// decoding, one entry per code read (see the KwKwK comment below),
	// mirroring what the encoder built while compressing.
	table := make([][]byte, lzwFirstCode, lzwMaxTableSize)
	for i := 0; i < 256; i++ {
		table[i] = []byte{byte(i)}
	}

	codeWidth := 9
	var out []byte
	var prev []byte // the immediately preceding code's expansion

	for {
		code, ok := br.read(codeWidth)
		if !ok {
			// A conforming stream ends with an explicit EOD code; a
			// truncated one might not, but simply running out of input
			// (rather than hitting an actually invalid code) is
			// tolerated the same way this project's other decoders
			// tolerate a missing terminator - see the package doc
			// comment's note on lenient reading.
			return out, nil
		}

		switch code {
		case lzwClearCode:
			table = table[:lzwFirstCode]
			codeWidth = 9
			prev = nil
			continue
		case lzwEODCode:
			return out, nil
		}

		var entry []byte
		switch {
		case code < len(table):
			entry = table[code]
		case code == len(table) && prev != nil:
			// The KwKwK case: the encoder assigned this exact code to a
			// table entry of its own just before emitting it, so the
			// decoder - one step behind, since it can only add a table
			// entry once it knows what follows - has to reconstruct the
			// same entry itself: the previous expansion followed by its
			// own first byte.
			entry = append(append([]byte{}, prev...), prev[0])
		default:
			return nil, pdferror.Malformedf("invalid code %d (table has %d entries)", code, len(table))
		}

		if len(out)+len(entry) > maxDecodedSize {
			return nil, pdferror.Malformedf("LZW-decoded output exceeds %d bytes", maxDecodedSize)
		}
		out = append(out, entry...)

		if prev != nil && len(table) < lzwMaxTableSize {
			table = append(table, append(append([]byte{}, prev...), entry[0]))
		}
		prev = entry

		codeWidth = lzwCodeWidth(len(table), earlyChange)
	}
}

// lzwCodeWidth returns the number of bits the next code should be read
// with, given that the table currently holds tableLen entries.
//
// Per ISO 32000-1's LZWDecode filter, the code width must grow (9 bits
// -> 10 -> 11 -> 12) as the table fills - a 9-bit code can only address
// 512 distinct values, so once a 512th entry exists, a wider code is
// needed to refer to it. /EarlyChange (default 1) controls exactly
// which entry triggers that growth:
//
//   - EarlyChange 1 (the default): the width grows for the code that
//     immediately follows the table reaching 511 entries - one entry
//     *before* it would actually overflow a 9-bit code. This apparent
//     off-by-one exists because the encoder makes the same decision
//     without waiting to see whether the code it's about to emit would
//     have overflowed the current width: it grows as soon as adding the
//     table's 511th entry would leave no more room, rather than after
//     the entry that actually needs the extra bit.
//   - EarlyChange 0: the width grows only once the table has actually
//     reached 512 entries - "postponed by one code" relative to the
//     default, in the specification's own words.
//
// The same one-entry-earlier-or-later pattern repeats at the 1024 and
// 2048 boundaries for the later 10->11 and 11->12 transitions.
func lzwCodeWidth(tableLen int, earlyChange bool) int {
	bump := 0
	if earlyChange {
		bump = 1
	}
	switch {
	case tableLen >= 2048-bump:
		return 12
	case tableLen >= 1024-bump:
		return 11
	case tableLen >= 512-bump:
		return 10
	default:
		return 9
	}
}

// lzwBitReader pulls fixed-width, MSB-first bit groups out of a byte
// slice - the packing PDF's LZWDecode filter (like GIF's, and TIFF's)
// uses for its variable-width codes, most significant bit of each byte
// first.
type lzwBitReader struct {
	data []byte
	pos  int // next bit to read, counting from the start of data
}

func newLZWBitReader(data []byte) *lzwBitReader {
	return &lzwBitReader{data: data}
}

// read returns the next n bits (n is always 9-12 here) as an integer,
// or false if fewer than n bits remain.
func (r *lzwBitReader) read(n int) (int, bool) {
	if r.pos+n > len(r.data)*8 {
		return 0, false
	}
	code := 0
	for i := 0; i < n; i++ {
		byteIndex := r.pos / 8
		bitIndex := 7 - r.pos%8 // 0 = most significant bit of the byte
		bit := (r.data[byteIndex] >> uint(bitIndex)) & 1
		code = code<<1 | int(bit)
		r.pos++
	}
	return code, true
}
