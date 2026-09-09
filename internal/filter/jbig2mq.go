package filter

// This file implements the "MQ-coder": the binary arithmetic coder that
// JBIG2's arithmetic decoding procedures (ITU-T T.88 Annex E) are built
// on. It is not unique to JBIG2 - the identical coder (same state
// machine, same probability-estimation table) is also used by JBIG
// (T.82) and, under a different name, forms the basis of JPEG2000's
// coder (T.800 Annex C) - but this project only needs it for JBIG2
// (jbig2.go), so it lives here rather than in a shared location.
//
// # What an arithmetic coder is, for readers new to the concept
//
// A Huffman coder (like this package's ccitt.go) assigns each possible
// symbol a whole number of bits - even if a symbol's "true" information
// content is, say, 0.3 bits, Huffman coding can only spend 1 whole bit
// on it. An arithmetic coder instead narrows a single fractional
// interval - conceptually a range of real numbers between 0 and 1 - a
// little further for every symbol encoded, so a very predictable symbol
// (one the decoder was already expecting with high probability) can cost
// a small fraction of a bit rather than a whole one. JBIG2 uses this to
// encode individual pixels: a scanned text page's pixels are extremely
// predictable (most of a page is a uniform run of background white or
// interior black), so even though every JBIG2 "generic region" pixel is
// nominally its own encoded decision, the arithmetic coder spends far
// less than one bit apiece on most of them.
//
// The MQ-coder is the specific, fixed-point (integer-only, no floating
// point) arithmetic coder ITU-T standardized for exactly this use. It
// tracks two integers - A (the width of the current coding interval,
// always effectively normalized into [0x8000, 0xFFFF]) and C (a 32-bit
// register whose upper bits are the current coding interval's lower
// bound, i.e. the bits already committed to the output/input stream) -
// instead of true arbitrary-precision real numbers, renormalizing
// (doubling A and C, and pulling in one more bit of input or output)
// whenever A gets too small to keep the required precision.
//
// # Contexts and probability estimation
//
// A single MQ-coder instance decodes a whole sequence of yes/no
// ("MPS"/"LPS", short for "more probable symbol"/"less probable symbol")
// decisions, but different decisions in that sequence are rarely equally
// likely to be 0 or 1 - a pixel deep inside a solid black region is very
// likely to be black again, while a pixel on a text glyph's edge is much
// less predictable. JBIG2 handles this with "contexts": each decision is
// decoded against one of many independent probability estimators (a
// mqContext value below), selected by whatever the *previous* few
// decisions were (for a generic region, its already-decoded neighboring
// pixels - see jbig2.go's context template). Each mqContext adapts its
// own probability estimate over time (via the qeTable state-machine
// transitions below) as more decisions pass through it, so a context
// that has mostly seen "black so far" becomes progressively more
// confident black is coming next, without ever being told the true
// probability up front.
//
// # Provenance
//
// The qeTable below (the probability-estimation state machine: for each
// of 47 states, the probability estimate Qe, the next-state indices to
// move to after a more-probable or less-probable decision, and whether
// that decision should also flip which symbol counts as "more probable")
// is not this project's own derivation - it is the fixed table ITU-T
// T.88 Table E.1 defines, reproduced identically (down to every Qe
// hexadecimal value) in essentially every independent JBIG2/JPEG2000
// implementation, including Mozilla's pdf.js (jbig2.js, Apache
// License 2.0) and jbig2dec. This file's decodeBit/renormalize control
// flow is a direct translation of the same standard's Annex E pseudocode
// (procedures DECODE, MPS_EXCHANGE, LPS_EXCHANGE, BYTEIN, INITDEC),
// which is itself the same shape pdf.js's ArithmeticDecoder implements -
// so while this is a from-scratch Go port rather than a mechanical
// line-by-line translation of any one existing codebase (unlike
// ccitt.go's port of xpdf/pdf.js's CCITT tables), it is deliberately
// written to match that same long-proven algorithm rather than
// reinvented independently, for the same "don't get the bit-level logic
// subtly wrong" reason ccitt.go's own doc comment gives.
type mqQeEntry struct {
	// qe is this state's current probability estimate for the LESS
	// probable symbol, scaled so that the full coding interval is
	// 0x10000 (i.e. qe/0x10000 approximates the LPS probability).
	qe uint32
	// nmps is the state index to move to after decoding the MORE
	// probable symbol in this state.
	nmps uint8
	// nlps is the state index to move to after decoding the LESS
	// probable symbol in this state.
	nlps uint8
	// switchFlag, when 1, says that decoding the LESS probable symbol in
	// this state should also flip which symbol (0 or 1) counts as "more
	// probable" going forward - the probability estimator's way of
	// noticing it had the sense of the bias backwards and correcting
	// itself, rather than only ever refining a probability magnitude it
	// assumed was pointed the right way from the start.
	switchFlag uint8
}

// qeTable is ITU-T T.88 Table E.1, the MQ-coder's 47-state probability
// estimation machine - see this file's doc comment for provenance. Every
// mqContext's index field is an index into this table.
var qeTable = [47]mqQeEntry{
	{0x5601, 1, 1, 1},
	{0x3401, 2, 6, 0},
	{0x1801, 3, 9, 0},
	{0x0AC1, 4, 12, 0},
	{0x0521, 5, 29, 0},
	{0x0221, 38, 33, 0},
	{0x5601, 7, 6, 1},
	{0x5401, 8, 14, 0},
	{0x4801, 9, 14, 0},
	{0x3801, 10, 14, 0},
	{0x3001, 11, 17, 0},
	{0x2401, 12, 18, 0},
	{0x1C01, 13, 20, 0},
	{0x1601, 29, 21, 0},
	{0x5601, 15, 14, 1},
	{0x5401, 16, 14, 0},
	{0x5101, 17, 15, 0},
	{0x4801, 18, 16, 0},
	{0x3801, 19, 17, 0},
	{0x3401, 20, 18, 0},
	{0x3001, 21, 19, 0},
	{0x2801, 22, 19, 0},
	{0x2401, 23, 20, 0},
	{0x2201, 24, 21, 0},
	{0x1C01, 25, 22, 0},
	{0x1801, 26, 23, 0},
	{0x1601, 27, 24, 0},
	{0x1401, 28, 25, 0},
	{0x1201, 29, 26, 0},
	{0x1101, 30, 27, 0},
	{0x0AC1, 31, 28, 0},
	{0x09C1, 32, 29, 0},
	{0x08A1, 33, 30, 0},
	{0x0521, 34, 31, 0},
	{0x0441, 35, 32, 0},
	{0x02A1, 36, 33, 0},
	{0x0221, 37, 34, 0},
	{0x0141, 38, 35, 0},
	{0x0111, 39, 36, 0},
	{0x0085, 40, 37, 0},
	{0x0049, 41, 38, 0},
	{0x0025, 42, 39, 0},
	{0x0015, 43, 40, 0},
	{0x0009, 44, 41, 0},
	{0x0005, 45, 42, 0},
	{0x0001, 45, 43, 0},
	{0x5601, 46, 46, 0},
}

// mqContext is one independent probability estimator, selected by
// whatever "context" (see this file's doc comment) a particular decision
// belongs to. Its zero value - index 0, mps 0 - is the correct initial
// state for a brand new context per T.88 (state 0 is the table's
// "50/50, no information yet" starting point), so a freshly-allocated
// slice of mqContext values (Go zero-initializes every element) is
// already correctly initialized without any explicit setup.
type mqContext struct {
	// index selects this context's current row in qeTable.
	index uint8
	// mps (0 or 1) is which symbol this context currently believes is
	// the more probable one - decoding (or encoding) a run of
	// consistently one-sided decisions through a context slowly builds
	// confidence (moving index forward through qeTable) without ever
	// needing to touch mps, but a long enough run of the *other* symbol
	// can flip mps itself (see switchFlag above).
	mps uint8
}

// mqDecoder reads a sequence of binary decisions back out of MQ-coded
// data, each against a caller-supplied mqContext (mutated in place as
// decoding proceeds, exactly like the encoder's own contexts - see
// mqEncoder below). Create one with newMQDecoder; call decodeBit once
// per decision, in the same order and against the same contexts the
// encoder used.
type mqDecoder struct {
	data []byte
	// bp indexes the single byte most recently folded into c - i.e. "BP"
	// in T.88's Annex E pseudocode, not a "next byte to read" cursor.
	bp int
	// c is the coder's 32-bit code register. Its own top 16 bits track
	// the current coding interval's position (compared directly against
	// qe values, which are themselves scaled to a 16-bit interval); the
	// low 16 bits are pending, not-yet-significant precision shifted in
	// by renormalize/byteIn ahead of when they are needed. This project
	// targets Go (native 32-bit-and-wider integers, no floating-point
	// precision concerns), so - unlike pdf.js's JavaScript port, which
	// has to split this into two 16-bit "chigh"/"clow" halves to stay
	// within JavaScript's safe-integer arithmetic - a single uint32
	// suffices and is closer to T.88's own pseudocode.
	c uint32
	// a is the current coding interval's width (T.88's "A" register),
	// always renormalized back up to at least 0x8000 before the next
	// decision.
	a uint32
	// ct counts down how many more bits of c are still "fresh" (already
	// shifted in and not yet consumed by comparison against a qe value)
	// before byteIn must pull in another input byte.
	ct int
}

// byteAt returns data[bp], or 0xFF if bp is beyond the end of data. Per
// T.88, running out of real input is handled by conceptually padding it
// with an infinite run of 0xFF bytes (which - since 0xFF signals
// "possible marker, look at the following byte" in JBIG2's bit-stuffing
// scheme, see byteIn below - safely stalls the decoder rather than
// consuming any further meaningful bits) rather than being treated as an
// error; a truncated or otherwise malformed input then simply decodes to
// arbitrary trailing garbage instead of panicking, which the higher
// layers (jbig2.go) are already responsible for treating with suspicion
// via their own bounds/consistency checks.
func (d *mqDecoder) byteAt(i int) byte {
	if i >= 0 && i < len(d.data) {
		return d.data[i]
	}
	return 0xFF
}

// newMQDecoder builds a decoder reading MQ-coded bits from data,
// performing T.88's INITDEC procedure.
func newMQDecoder(data []byte) *mqDecoder {
	d := &mqDecoder{data: data}
	d.c = uint32(d.byteAt(0)) << 16
	d.byteIn()
	d.c <<= 7
	d.ct -= 7
	d.a = 0x8000
	return d
}

// byteIn implements T.88's BYTEIN procedure: pulls the next input byte
// into c, handling the "bit stuffing" JBIG2 borrows from the same
// scheme JPEG uses to keep 0xFF bytes from ever being misread as a
// marker code by anything scanning the raw byte stream - a literal data
// byte of 0xFF is always immediately followed by a byte whose top bit is
// forced to 0 (i.e. <= 0x7F, so definitely <= 0x8F) precisely so this
// procedure can tell "a real, if rare, literal 0xFF byte of coded data"
// apart from "the stream has run out and we're now reading synthetic
// padding" (byteAt's 0xFF padding, which is instead always followed by
// more 0xFF).
func (d *mqDecoder) byteIn() {
	if d.byteAt(d.bp) == 0xFF {
		if d.byteAt(d.bp+1) > 0x8F {
			// A genuine marker (or the synthetic end-of-data padding) -
			// stall the decoder by feeding it 1-bits without advancing
			// bp, so it never actually reads past this point.
			d.c += 0xFF00
			d.ct = 8
		} else {
			d.bp++
			d.c += uint32(d.byteAt(d.bp)) << 9
			d.ct = 7
		}
	} else {
		d.bp++
		d.c += uint32(d.byteAt(d.bp)) << 8
		d.ct = 8
	}
}

// decodeBit decodes one binary decision against cx (updated in place),
// implementing T.88's DECODE procedure (with its MPS_EXCHANGE/
// LPS_EXCHANGE sub-procedures inlined) followed by RENORMD whenever the
// coding interval needed renormalizing.
func (d *mqDecoder) decodeBit(cx *mqContext) int {
	row := qeTable[cx.index]
	qe := row.qe
	d.a -= qe

	var bit int
	if (d.c >> 16) < qe {
		// LPS_EXCHANGE.
		if d.a < qe {
			bit = int(cx.mps)
			cx.index = row.nmps
		} else {
			bit = int(1 - cx.mps)
			if row.switchFlag == 1 {
				cx.mps = 1 - cx.mps
			}
			cx.index = row.nlps
		}
		d.a = qe
		d.renormalize()
	} else {
		d.c -= qe << 16
		if d.a&0x8000 == 0 {
			// MPS_EXCHANGE.
			if d.a < qe {
				bit = int(1 - cx.mps)
				if row.switchFlag == 1 {
					cx.mps = 1 - cx.mps
				}
				cx.index = row.nlps
			} else {
				bit = int(cx.mps)
				cx.index = row.nmps
			}
			d.renormalize()
		} else {
			bit = int(cx.mps)
		}
	}
	return bit
}

// renormalize implements T.88's RENORMD procedure: doubles a (and c
// alongside it, pulling in fresh input bytes via byteIn as needed) until
// a's top bit (0x8000) is set again, restoring the coder's required
// working precision after a decision narrowed the interval below it.
func (d *mqDecoder) renormalize() {
	for {
		if d.ct == 0 {
			d.byteIn()
		}
		d.a <<= 1
		d.c <<= 1
		d.ct--
		if d.a&0x8000 != 0 {
			break
		}
	}
}

// mqEncoder is this package's forward (encoding) direction of the
// MQ-coder: it turns a sequence of binary decisions back into MQ-coded
// bytes that mqDecoder above reproduces exactly. It is package-visible
// (see EncodeJBIG2GenericRegion, jbig2generic.go, for this package's one
// genuinely exported encoding entry point) for use by this package's own
// round-trip tests and by tools/genfixtures to build byte-accurate JBIG2
// test fixtures - the same "forward function lives in the production
// package, alongside the decoder it must stay byte-compatible with"
// arrangement internal/crypt's hash56.go uses for its AES-256 forward
// functions (see that file's own doc comment). It exists purely so the
// encoder and decoder cannot silently drift apart from each other, not
// because general-purpose JBIG2 encoding is otherwise in this project's
// scope (this project is a PDF *viewer* - see the repository README's
// stated scope).
//
// This project has no independently-produced real-world JBIG2 sample (no
// encoder tool was available while writing this package - see
// docs/PLAN2.md's Phase 8 progress log for the full explanation) to
// validate the decoder against, which is the reason this type exists at
// all: encoding a known bitmap and then decoding it back with this
// package's own decoder is the strongest verification available without
// one, exactly mirroring how internal/crypt's tests and fixtures work.
// Because the two directions are written from opposite ends of the
// standard's own description (the decoder from Annex E's DECODE/BYTEIN
// procedures, this encoder from E.3's CODEMPS/CODELPS/BYTEOUT/FLUSH
// procedures) rather than one being derived from the other, a round trip
// agreeing is real evidence both match the standard, not just evidence
// they share an assumption.
//
// # How encoding mirrors decoding
//
// The decoder above tracks the coding interval as A (its width) and C
// (its base, whose high bits are the input bits consumed so far), and
// narrows the interval per decision, renormalizing (doubling both, and
// pulling in another input byte) whenever A drops below 0x8000. The
// encoder tracks exactly the same two registers and narrows exactly the
// same way - the only real difference is direction: where the decoder
// *compares* C against the current probability estimate Qe to discover
// which sub-interval the encoder must have chosen, the encoder *adds* Qe
// to C to select that sub-interval, and where the decoder pulls a byte
// in during renormalization, the encoder pushes one out.
//
// # Why pushing bytes out is more intricate than pulling them in
//
// C accumulates additions (`c += qe`) that can carry upward into bits
// that were, moments ago, about to be shifted out as a finished output
// byte. A byte therefore cannot be treated as final the instant its bits
// leave the register: a later decision's carry may still need to
// increment it. byteOut below handles that (and the related 0xFF
// bit-stuffing the decoder's byteIn expects) - it is the one genuinely
// fiddly piece of the encoder, and the reason this type keeps its output
// in a slice it can reach back into rather than streaming bytes straight
// to a writer.
type mqEncoder struct {
	// out holds the bytes produced so far. out[0] is a scratch
	// "pseudo-byte" that is never part of the result (flush strips it):
	// byteOut's carry handling needs a previous byte to potentially
	// increment even when producing the very first real one, and giving
	// it a scratch zero byte to aim at is simpler and safer than
	// special-casing "there is no previous byte yet" on every call. Its
	// initial value of 0 (rather than 0xFF) is also what makes the
	// initial ct of 12 below correct.
	out []byte
	// bp indexes the byte in out most recently written - the one a carry
	// would still propagate into. It mirrors "BP" in T.88's Annex E
	// pseudocode, and is always len(out)-1 here.
	bp int
	// c is the code register: the current coding interval's base, held
	// with more fractional precision than the decoder's copy needs (the
	// decoder consumes C's top bits as it goes, while the encoder must
	// hold onto low bits long enough for pending carries to settle - see
	// byteOut). Bit 27 (0x8000000) is where a carry out of the
	// about-to-be-emitted byte shows up.
	c uint32
	// a is the coding interval's width (T.88's "A"), renormalized back up
	// to at least 0x8000 after every decision that narrows it below that,
	// exactly as in the decoder.
	a uint32
	// ct counts how many more times c can be shifted left before its
	// top bits have to be pushed out as a finished byte. It starts at 12
	// rather than 8 because c starts out with 4 bits of headroom above
	// the byte currently being formed (the space a carry needs), and
	// drops to 7 instead of 8 after a stuffed byte follows an 0xFF, which
	// is how the encoder's byte boundaries stay in step with the
	// decoder's byteIn.
	ct int
}

// newMQEncoder returns an encoder in T.88's INITENC state.
func newMQEncoder() *mqEncoder {
	return &mqEncoder{
		out: []byte{0},
		bp:  0,
		c:   0,
		a:   0x8000,
		ct:  12,
	}
}

// emit appends v to out as the new "most recently written" byte.
func (e *mqEncoder) emit(v byte) {
	e.out = append(e.out, v)
	e.bp = len(e.out) - 1
}

// encodeBit encodes one binary decision d (0 or 1) against cx (updated
// in place, exactly as the decoder updates its own copy of the same
// context), implementing T.88's ENCODE procedure - which dispatches to
// its CODEMPS or CODELPS sub-procedure depending on whether d agrees
// with what cx currently considers the more probable symbol.
func (e *mqEncoder) encodeBit(cx *mqContext, d int) {
	row := qeTable[cx.index]
	qe := row.qe

	if d == int(cx.mps) {
		// CODEMPS: the decision agreed with the prediction, so it takes
		// the larger (A-Qe wide) sub-interval. Most of the time that
		// leaves A still at full precision and there is nothing to do
		// beyond moving C past the other sub-interval.
		e.a -= qe
		if e.a&0x8000 == 0 {
			// A dropped below full precision, so this decision needs
			// renormalizing - and, because the two sub-intervals are now
			// close enough in size that which one is "larger" can have
			// flipped, the standard's "conditional exchange" rule applies:
			// whichever of A and Qe is actually larger is the one the MPS
			// gets.
			if e.a < qe {
				e.a = qe
			} else {
				e.c += qe
			}
			cx.index = row.nmps
			e.renormalize()
		} else {
			e.c += qe
		}
	} else {
		// CODELPS: the decision contradicted the prediction, so it takes
		// the smaller (Qe wide) sub-interval - always narrow enough to
		// need renormalizing, and (via nlps, and switchFlag when set) the
		// event that moves this context's probability estimate toward
		// believing the other symbol.
		e.a -= qe
		if e.a < qe {
			// Conditional exchange again, mirroring the decoder's own
			// LPS_EXCHANGE: when Qe's sub-interval is the larger of the
			// two, the LPS takes the *other* one, which C must move past.
			e.c += qe
		} else {
			e.a = qe
		}
		if row.switchFlag == 1 {
			cx.mps = 1 - cx.mps
		}
		cx.index = row.nlps
		e.renormalize()
	}
}

// renormalize implements T.88's RENORME procedure, the encoder's mirror
// of the decoder's RENORMD: it doubles a (and c alongside it) until a's
// top bit is set again, pushing a finished byte out via byteOut whenever
// ct says c has no room left to shift into.
func (e *mqEncoder) renormalize() {
	for {
		e.a <<= 1
		e.c <<= 1
		e.ct--
		if e.ct == 0 {
			e.byteOut()
		}
		if e.a&0x8000 != 0 {
			return
		}
	}
}

// byteOut implements T.88's BYTEOUT procedure: it moves the byte that
// has finished forming at the top of c into out, and is the encoder-side
// counterpart of the decoder's byteIn. Two complications make it more
// than a shift-and-append.
//
// First, carries. c's additions in encodeBit can carry upward past the
// byte currently being formed and into the byte already written before
// it, so byteOut checks c's bit 27 (0x8000000, the position a carry out
// of the pending byte lands in) and, when set, increments the previously
// written byte instead of ignoring it. This is why out is a slice
// byteOut can reach back into (via bp) rather than an output stream.
//
// Second, bit stuffing. The decoder's byteIn treats a 0xFF byte followed
// by a byte above 0x8F as "stop, this is a marker or the end of the
// data" (see byteIn's own doc comment), so the encoder must never let a
// real 0xFF data byte be followed by one that high. It guarantees that
// by emitting only 7 fresh bits (ct = 7, shifting the byte out from one
// position higher) after any 0xFF it writes, leaving that byte's top bit
// clear - the stuffed bit. Incrementing a previous 0xFE into an 0xFF via
// a carry counts too, which is why that case re-checks and takes the
// same 7-bit path.
func (e *mqEncoder) byteOut() {
	if e.out[e.bp] == 0xFF {
		// Previous byte was 0xFF: stuff a 0 bit by emitting only 7 bits
		// (taken from one bit higher up in c, since bit 27 cannot be a
		// carry into an 0xFF byte - it would have had to carry out of it
		// instead, which cannot happen while the stuffed bit is clear).
		e.emit(byte(e.c >> 20))
		e.c &= 0xFFFFF
		e.ct = 7
		return
	}
	if e.c&0x8000000 != 0 {
		// A carry out of the pending byte: fold it into the previous byte
		// (which cannot itself be 0xFF - that case returned above - so
		// this increment cannot overflow), then re-check whether that
		// increment just *created* an 0xFF needing the stuffed-bit path.
		e.out[e.bp]++
		if e.out[e.bp] == 0xFF {
			e.c &= 0x7FFFFFF
			e.emit(byte(e.c >> 20))
			e.c &= 0xFFFFF
			e.ct = 7
			return
		}
	}
	e.emit(byte(e.c >> 19))
	e.c &= 0x7FFFF
	e.ct = 8
}

// flush finishes the stream and returns the encoded bytes, implementing
// T.88's FLUSH procedure. Encoding leaves the final decision's interval
// only partly written out (C still holds bits no byteOut call has pushed
// yet), so flush first sets those remaining bits to a value that keeps
// the decoder inside the correct final interval while ending in as many
// 1 bits as possible (the standard's SETBITS step, which is what lets a
// trailing 0xFF be dropped below), then pushes out what is left with two
// byteOut calls.
//
// The encoder must not be used again after flush.
func (e *mqEncoder) flush() []byte {
	// SETBITS.
	tempC := e.c + e.a
	e.c |= 0xFFFF
	if e.c >= tempC {
		e.c -= 0x8000
	}

	e.c <<= uint(e.ct)
	e.byteOut()
	e.c <<= uint(e.ct)
	e.byteOut()

	// out[0] is the scratch pseudo-byte (see the field's own comment),
	// never part of the result.
	res := e.out[1:]
	// A trailing 0xFF carries no information the decoder needs: its
	// byteAt already pads a short stream with an endless run of 0xFF
	// bytes, so dropping it here produces the same decoded result from
	// one fewer byte. (This also keeps the encoder from ever ending a
	// stream on the one byte value that would need a stuffed byte after
	// it.)
	for len(res) > 0 && res[len(res)-1] == 0xFF {
		res = res[:len(res)-1]
	}
	return res
}

// encodeMQSequence encodes a whole list of decisions in one call, the
// form this package's round-trip tests and EncodeJBIG2GenericRegion
// (jbig2generic.go) both want: decision i is "encode bit bit[i] against
// contexts[ctxIndex[i]]", where contexts is a freshly zero-initialized
// slice of length numContexts (the correct initial state for every
// context - see mqContext's own doc comment) both here and in whatever
// later decodes the returned bytes with mqDecoder.
//
// ctxIndex and bit must have the same length.
func encodeMQSequence(numContexts int, ctxIndex, bit []int) []byte {
	if len(ctxIndex) != len(bit) {
		panic("filter: encodeMQSequence: ctxIndex and bit must have the same length")
	}
	if len(bit) == 0 {
		return nil
	}

	contexts := make([]mqContext, numContexts)
	enc := newMQEncoder()
	for i := range bit {
		enc.encodeBit(&contexts[ctxIndex[i]], bit[i])
	}
	return enc.flush()
}
