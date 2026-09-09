package filter

// This file implements JBIG2's "arithmetic integer decoding procedure"
// (ITU-T T.88 Annex A) and its companion "IAID decoding procedure"
// (A.3), plus the matching encoding directions this package's own tests
// and fixtures need (see jbig2mq.go's mqEncoder doc comment for why an
// encoder lives in the production package at all).
//
// # What these are for
//
// jbig2generic.go decodes a generic region as a stream of individual
// *pixel* decisions, each a single yes/no bit through the MQ coder
// (jbig2mq.go). JBIG2's symbol dictionary and text region procedures
// (jbig2symbol.go, jbig2text.go) instead need to read whole *numbers*
// out of the same coded bit stream - "this symbol is 14 pixels wide",
// "the next text instance sits 37 pixels to the right of the last one",
// "this strip is 3 rows below the previous one". Annex A defines how a
// signed integer is spelled out as a short sequence of MQ decisions:
// a sign bit, then a few prefix bits selecting one of six magnitude
// ranges, then that range's fixed number of value bits.
//
// # Why an integer needs its own contexts
//
// Every decision in an MQ stream is decoded against a "context" - an
// adaptive probability estimate selected by what has already been seen
// (see jbig2mq.go's doc comment). For a generic region that selector is
// the pixel's already-decoded neighborhood. For an integer it is instead
// the bits of the *same integer* decoded so far, tracked in the running
// PREV value below: the sign bit is decoded against one context, the
// first prefix bit against a different one depending on what the sign
// turned out to be, and so on. That way the coder learns, for instance,
// that a particular field is almost always a small positive number, and
// spends a fraction of a bit apiece on the prefix bits saying so.
//
// Crucially, each *field* gets its own independent set of these
// contexts: T.88 names thirteen of them (IADH, IADW, IAEX, IAAI, IADT,
// IAFS, IADS, IAIT, IARI, IARDW, IARDH, IARDX, IARDY), and a decoder
// must keep them separate so that, say, symbol widths and text
// positions do not pollute each other's probability estimates. That is
// what the arithIntCtx type below is: one field's 512 contexts, created
// fresh per procedure that needs one.
//
// # Provenance
//
// Like the rest of this package's JBIG2 code (see jbig2.go's doc
// comment), this is a from-scratch implementation written directly
// against T.88's own pseudocode - here Annex A's DECODE-loop figures and
// the Table A.1 range/offset breakdown, reproduced identically in every
// independent JBIG2 implementation because the bit layout is what it is.
// The encoding direction is not given by the standard at all (T.88
// specifies only decoding); it is derived here as the exact inverse of
// the decoder in the same file, which is precisely what makes the
// round-trip tests in jbig2arith_test.go meaningful.

// arithIntRange is one row of T.88 Table A.1: a magnitude range, the
// prefix that selects it, how many value bits follow, and the offset
// added to those bits' value. The ranges are cumulative - each starts
// exactly where the previous one ended - so every non-negative
// magnitude has exactly one legal encoding.
type arithIntRange struct {
	// prefix is the sequence of prefix bits (after the sign bit) that
	// selects this range: 0, 10, 110, 1110, 11110, 11111. Each row's
	// prefix is the previous row's with one more 1 in front of the
	// terminating 0, except the last, which has no terminating 0 because
	// there is no further range to distinguish it from.
	prefix []int
	// valueBits is how many bits of magnitude follow the prefix.
	valueBits int
	// offset is added to those bits to get the final magnitude, so range
	// n picks up where range n-1 stopped.
	offset int
}

// arithIntRanges is T.88 Table A.1 in full. The last row's 32 value bits
// mean a JBIG2 integer is nominally a full 32-bit quantity, but nothing
// this package decodes (symbol dimensions, pixel coordinates, instance
// counts) can legitimately be anywhere near that large - callers range-
// check every value they read, and decodeInt itself refuses a magnitude
// that will not fit in an int on a 32-bit platform (see below).
var arithIntRanges = [6]arithIntRange{
	{prefix: []int{0}, valueBits: 2, offset: 0},
	{prefix: []int{1, 0}, valueBits: 4, offset: 4},
	{prefix: []int{1, 1, 0}, valueBits: 6, offset: 20},
	{prefix: []int{1, 1, 1, 0}, valueBits: 8, offset: 84},
	{prefix: []int{1, 1, 1, 1, 0}, valueBits: 12, offset: 340},
	{prefix: []int{1, 1, 1, 1, 1}, valueBits: 32, offset: 4436},
}

// arithIntNumContexts is how many independent contexts one integer
// field's arithIntCtx holds. The selector is the running PREV value
// described in decodeInt below, deliberately capped by T.88 at 9 bits
// (values 1..511), so 512 contexts covers every reachable selector.
const arithIntNumContexts = 512

// arithIntCtx is one integer field's context set - the "IADH", "IADW",
// "IAEX" and friends of T.88's procedures. Its zero value is not usable;
// create one with newArithIntCtx, which sizes the slice (its element
// values, being freshly-zeroed mqContext values, are already in the
// correct initial state - see mqContext's own doc comment).
type arithIntCtx []mqContext

func newArithIntCtx() arithIntCtx {
	return make(arithIntCtx, arithIntNumContexts)
}

// maxArithIntMagnitude bounds what decodeInt will return. T.88's largest
// range can spell out a magnitude just under 2^32, which does not fit in
// an int on a 32-bit platform and is absurd on any platform for the
// quantities this package reads (the largest legitimate one is a pixel
// coordinate, already bounded far below this by
// maxGenericRegionDimension). Clamping here - rather than in each of the
// dozen call sites - means no caller can be handed a value that has
// silently wrapped negative, and keeps decodeInt's contract simple: what
// it returns is always a sane, in-range int.
const maxArithIntMagnitude = 1 << 30

// decodeInt implements T.88's arithmetic integer decoding procedure
// (Annex A.2), reading one signed integer from d against the field
// context set cx (updated in place, exactly as the encoder updates its
// own copy).
//
// The second return value is false for "OOB" - out of band - which is
// not an error but a legitimate in-stream sentinel meaning "no value
// here, the list you were reading has ended". T.88 spells OOB as the
// otherwise-meaningless encoding of negative zero (sign bit 1, magnitude
// 0), and the text region procedure (jbig2text.go) relies on it to know
// when a strip's run of symbol instances is finished. Callers of fields
// where OOB is not meaningful simply treat false as malformed input.
//
// The third return value reports a magnitude beyond maxArithIntMagnitude
// (or a decoder that has run off the end of its data and is now
// producing garbage - see mqDecoder.byteAt), which callers must treat as
// malformed rather than clamping.
func (d *mqDecoder) decodeInt(cx arithIntCtx) (value int, ok bool, err bool) {
	// PREV accumulates the bits of this integer decoded so far, and is
	// what selects the context each next bit is decoded against. It
	// starts at 1 (not 0) so that the leading 1 acts as a marker
	// separating "the first bit of the integer" from "a 0 bit decoded
	// earlier" - without it, PREV could not tell a two-bit 01 apart from
	// a one-bit 1.
	prev := 1

	// next decodes one more bit and folds it into prev. T.88 caps prev at
	// 9 significant bits: once it has grown past 255, the top bit is
	// pinned to 1 and only the low 8 bits keep shifting. That keeps the
	// context count at a fixed 512 no matter how many value bits a large
	// magnitude needs, at the cost of the later bits of a large value
	// sharing contexts with each other - which is fine, because those
	// bits are close to random anyway.
	next := func() int {
		bit := d.decodeBit(&cx[prev])
		if prev < 256 {
			prev = prev<<1 | bit
		} else {
			prev = (((prev<<1 | bit) & 511) | 256)
		}
		return bit
	}

	sign := next()

	// Walk the prefix: each range but the last is selected by a 0 bit at
	// its own position, so reading a 0 stops the walk at that range.
	rangeIndex := len(arithIntRanges) - 1
	for i := 0; i < len(arithIntRanges)-1; i++ {
		if next() == 0 {
			rangeIndex = i
			break
		}
	}
	r := arithIntRanges[rangeIndex]

	// The magnitude's value bits, most significant first. This
	// accumulates into a uint32 rather than an int because the largest
	// range really is 32 bits wide; the range check below is what brings
	// it back into int territory safely.
	var magnitude uint32
	for i := 0; i < r.valueBits; i++ {
		magnitude = magnitude<<1 | uint32(next())
	}
	total := uint64(magnitude) + uint64(r.offset)

	if sign == 1 && total == 0 {
		// Negative zero: the OOB sentinel, not a value.
		return 0, false, false
	}
	if total > maxArithIntMagnitude {
		return 0, false, true
	}

	value = int(total)
	if sign == 1 {
		value = -value
	}
	return value, true, false
}

// encodeInt is decodeInt's exact inverse, writing v into e against the
// field context set cx. It exists for this package's round-trip tests
// and fixture building only (see this file's doc comment).
//
// v must be within maxArithIntMagnitude; encoding OOB is
// encodeOOB's job, not this function's (v of 0 is always encoded as
// positive zero, since negative zero is the OOB sentinel).
func (e *mqEncoder) encodeInt(cx arithIntCtx, v int) {
	if v > maxArithIntMagnitude || v < -maxArithIntMagnitude {
		panic("filter: encodeInt: value out of range")
	}

	sign := 0
	magnitude := v
	if v < 0 {
		sign = 1
		magnitude = -v
	}

	// Pick the first (narrowest) range that can hold this magnitude -
	// the same range the decoder will infer from the prefix, since the
	// ranges partition the non-negative integers without overlap.
	// (The comparison is done in uint64 because the last range's 1<<32
	// does not fit in an int on a 32-bit platform - the same reason
	// decodeInt accumulates its magnitude in a uint32.)
	rangeIndex := len(arithIntRanges) - 1
	for i, r := range arithIntRanges {
		if uint64(magnitude) < uint64(r.offset)+uint64(1)<<uint(r.valueBits) {
			rangeIndex = i
			break
		}
	}
	r := arithIntRanges[rangeIndex]

	e.encodeIntBits(cx, sign, r, uint32(magnitude-r.offset))
}

// encodeOOB writes the out-of-band sentinel decodeInt reports as
// ok == false: sign bit 1 with a zero magnitude in the narrowest range,
// i.e. "negative zero".
func (e *mqEncoder) encodeOOB(cx arithIntCtx) {
	e.encodeIntBits(cx, 1, arithIntRanges[0], 0)
}

// encodeIntBits writes one integer's decisions - sign, then r's prefix,
// then r.valueBits bits of magnitude - maintaining exactly the same PREV
// context selector decodeInt maintains while reading them back.
func (e *mqEncoder) encodeIntBits(cx arithIntCtx, sign int, r arithIntRange, magnitude uint32) {
	prev := 1
	emit := func(bit int) {
		e.encodeBit(&cx[prev], bit)
		if prev < 256 {
			prev = prev<<1 | bit
		} else {
			prev = (((prev<<1 | bit) & 511) | 256)
		}
	}

	emit(sign)
	for _, bit := range r.prefix {
		emit(bit)
	}
	for i := r.valueBits - 1; i >= 0; i-- {
		emit(int(magnitude>>uint(i)) & 1)
	}
}

// arithIAIDCtx is the context set for T.88's IAID decoding procedure
// (A.3), which reads a symbol ID - an index into a text region's symbol
// list - rather than a general integer. A symbol ID is decoded as a
// fixed number of bits (codeLen, chosen by the text region from how many
// symbols it has to choose between), not as Annex A.2's sign-and-prefix
// structure, so it needs its own context arrangement: a binary tree of
// contexts indexed by the bits decoded so far, hence 2^(codeLen+1) of
// them rather than 512.
type arithIAIDCtx struct {
	codeLen int
	cx      []mqContext
}

// maxSymbolCodeLength bounds codeLen, and so the 2^(codeLen+1) contexts
// an arithIAIDCtx allocates. A text region needs codeLen bits to
// distinguish its symbols, so 32 would mean a symbol dictionary holding
// billions of symbols - impossible for any real file, and a cheap way
// for a hostile one to ask for an enormous allocation. 24 allows over 16
// million symbols, far past anything legitimate (the sample scans this
// package was developed against use fewer than 200).
const maxSymbolCodeLength = 24

func newArithIAIDCtx(codeLen int) *arithIAIDCtx {
	return &arithIAIDCtx{codeLen: codeLen, cx: make([]mqContext, 1<<uint(codeLen+1))}
}

// decodeIAID implements T.88's IAID decoding procedure (A.3): codeLen
// bits, most significant first, each decoded against a context selected
// by all the bits read before it.
func (d *mqDecoder) decodeIAID(cx *arithIAIDCtx) int {
	// prev doubles as both the context selector and the accumulating
	// value, for the same reason decodeInt's does - it starts at 1 so
	// that leading zero bits are distinguishable - which is why the
	// leading 1 has to be subtracted back off at the end.
	prev := 1
	for i := 0; i < cx.codeLen; i++ {
		prev = prev<<1 | d.decodeBit(&cx.cx[prev])
	}
	return prev - (1 << uint(cx.codeLen))
}

// encodeIAID is decodeIAID's inverse, for this package's tests and
// fixtures.
func (e *mqEncoder) encodeIAID(cx *arithIAIDCtx, id int) {
	prev := 1
	for i := cx.codeLen - 1; i >= 0; i-- {
		bit := (id >> uint(i)) & 1
		e.encodeBit(&cx.cx[prev], bit)
		prev = prev<<1 | bit
	}
}
