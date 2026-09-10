package jpx

// This file implements the "MQ-coder", the binary arithmetic coder ITU-T
// T.800 Annex C defines for JPEG 2000's entropy coding (tier-1, 14c's
// concern - this sub-phase only builds the coder itself, since 14b's
// tier-2 packet header work below does not use it: packet headers are
// bit-stuffed, not arithmetic-coded - see bitreader.go's doc comment).
//
// It is the same coder, state machine and probability-estimation table
// included, as JBIG2's (T.88 Annex E, implemented independently in
// internal/filter/jbig2mq.go) - T.800 Annex C says as much explicitly.
// This package still reimplements it from scratch rather than importing
// that one, per doc.go's "why this package exists" section: internal/jpx
// deliberately depends on nothing outside the standard library, so it
// could be pulled into its own module without internal/filter coming
// along for the ride. See jbig2mq.go's own doc comment for a fuller
// explanation of what an arithmetic coder is and why contexts exist;
// this file assumes that background and only documents where JPEG 2000's
// own use of the coder differs.
//
// # Provenance
//
// qeTable is ITU-T T.88 Table E.1 / T.800 Table C.2 (the two standards
// define byte-identical tables), reproduced the same way jbig2mq.go's
// own copy is - down to every Qe value - and cross-checked against
// Mozilla's pdf.js (arithmetic_decoder.js, Apache License 2.0), which
// implements the identical DECODE/BYTEIN/INITDEC procedures this file's
// mqDecoder below is a direct, from-scratch Go translation of (T.800
// Annex C.3's pseudocode).

type mqQeEntry struct {
	qe         uint32
	nmps       uint8
	nlps       uint8
	switchFlag uint8
}

// qeTable is the MQ-coder's 47-state probability estimation machine -
// see this file's doc comment for provenance.
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

// mqContext is one independent probability estimator. Its zero value
// (index 0, mps 0) is state 0 - T.800's correct starting point for a
// freshly allocated context.
type mqContext struct {
	index uint8
	mps   uint8
}

// mqDecoder decodes a sequence of MQ-coded binary decisions, each
// against a caller-supplied mqContext. Create with newMQDecoder; call
// decodeBit once per decision, in the same order and against the same
// contexts the encoder used.
type mqDecoder struct {
	data []byte
	bp   int
	c    uint32
	a    uint32
	ct   int
}

// byteAt returns data[i], or 0xFF (the standard's own end-of-data
// padding convention) once i runs past the end.
func (d *mqDecoder) byteAt(i int) byte {
	if i >= 0 && i < len(d.data) {
		return d.data[i]
	}
	return 0xFF
}

// newMQDecoder performs T.800's INITDEC procedure.
func newMQDecoder(data []byte) *mqDecoder {
	d := &mqDecoder{data: data}
	d.c = uint32(d.byteAt(0)) << 16
	d.byteIn()
	d.c <<= 7
	d.ct -= 7
	d.a = 0x8000
	return d
}

// byteIn implements BYTEIN: pulls the next input byte into c, applying
// the bit-stuffing convention that keeps a coded 0xFF byte from ever
// being misread as the start of a marker - see markers.go's
// tilePartData, which relies on the same guarantee.
func (d *mqDecoder) byteIn() {
	if d.byteAt(d.bp) == 0xFF {
		if d.byteAt(d.bp+1) > 0x8F {
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
// implementing DECODE (with MPS_EXCHANGE/LPS_EXCHANGE inlined) followed
// by RENORMD whenever the interval needed renormalizing.
func (d *mqDecoder) decodeBit(cx *mqContext) int {
	row := qeTable[cx.index]
	qe := row.qe
	d.a -= qe

	var bit int
	if (d.c >> 16) < qe {
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

// renormalize implements RENORMD.
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

// mqEncoder is the forward direction, test/fixture-only (see doc.go's
// Provenance section: this package has no independently-produced
// real-world JPX sample, so a from-scratch encoder round-tripping
// against mqDecoder above is the strongest verification available). It
// is package-visible for use by this package's own tests and by
// tools/genfixtures once 14g builds real fixtures.
type mqEncoder struct {
	// out[0] is a scratch pseudo-byte, stripped by flush - see byteOut's
	// carry handling, which always needs a previous byte to potentially
	// increment.
	out []byte
	bp  int
	c   uint32
	a   uint32
	ct  int
}

func newMQEncoder() *mqEncoder {
	return &mqEncoder{out: []byte{0}, bp: 0, c: 0, a: 0x8000, ct: 12}
}

// emit appends v to out as the new "most recently written" byte.
func (e *mqEncoder) emit(v byte) {
	e.out = append(e.out, v)
	e.bp = len(e.out) - 1
}

// encodeBit implements ENCODE (dispatching to its CODEMPS/CODELPS
// sub-procedures) against cx, updated in place exactly as the decoder's
// copy of the same context is.
func (e *mqEncoder) encodeBit(cx *mqContext, bit int) {
	row := qeTable[cx.index]
	qe := row.qe

	if bit == int(cx.mps) {
		// CODEMPS.
		e.a -= qe
		if e.a&0x8000 == 0 {
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
		// CODELPS.
		e.a -= qe
		if e.a < qe {
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

// renormalize implements RENORME, the encoder's mirror of the decoder's
// RENORMD.
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

// byteOut implements BYTEOUT: moves the byte that has finished forming
// at the top of c into out, handling carries into a previously-emitted
// byte and the bit-stuffing convention byteIn expects on decode - see
// jbig2mq.go's byteOut doc comment (internal/filter) for the fuller
// account of both, since this is the same procedure applied to this
// package's own encoder state.
func (e *mqEncoder) byteOut() {
	if e.out[e.bp] == 0xFF {
		e.emit(byte(e.c >> 20))
		e.c &= 0xFFFFF
		e.ct = 7
		return
	}
	if e.c&0x8000000 != 0 {
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

// flush implements FLUSH: sets the remaining undetermined bits (SETBITS)
// and pushes out what is left of c with two more byteOut calls,
// returning the finished output (out[0], the scratch pseudo-byte,
// stripped) with any trailing 0xFF dropped - it carries no information
// the decoder needs, since byteAt already pads a short stream with an
// endless run of 0xFF. The encoder must not be used again after flush.
func (e *mqEncoder) flush() []byte {
	tempC := e.c + e.a
	e.c |= 0xFFFF
	if e.c >= tempC {
		e.c -= 0x8000
	}

	e.c <<= uint(e.ct)
	e.byteOut()
	e.c <<= uint(e.ct)
	e.byteOut()

	res := e.out[1:]
	for len(res) > 0 && res[len(res)-1] == 0xFF {
		res = res[:len(res)-1]
	}
	return res
}
