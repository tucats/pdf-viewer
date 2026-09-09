package filter

import (
	"math/rand"
	"testing"
)

// The tests in this file round-trip jbig2arith.go's integer procedures:
// encode a known sequence of values, decode it back, and require an
// exact match. That is the same verification strategy jbig2mq_test.go
// uses for the underlying bit coder, and for the same reason (see
// jbig2mq.go's mqEncoder doc comment) - with one addition that matters
// here: the encoder is written as the deliberate inverse of the decoder,
// so a round trip alone would not catch a shared misreading of T.88's
// Table A.1. TestArithIntRangeBoundaries below therefore also pins the
// table's own range boundaries, and TestArithIntKnownEncoding pins one
// value's exact bit-level structure independently of the coder.

// arithIntRoundTrip encodes values (an OOB is written wherever
// oobAt[i] is true, in which case values[i] is ignored) and decodes them
// back through a fresh context set, failing the test on any mismatch.
func arithIntRoundTrip(t *testing.T, values []int, oobAt []bool) {
	t.Helper()

	enc := newMQEncoder()
	encCx := newArithIntCtx()
	for i, v := range values {
		if oobAt != nil && oobAt[i] {
			enc.encodeOOB(encCx)
			continue
		}
		enc.encodeInt(encCx, v)
	}
	coded := enc.flush()

	dec := newMQDecoder(coded)
	decCx := newArithIntCtx()
	for i, want := range values {
		got, ok, bad := dec.decodeInt(decCx)
		if bad {
			t.Fatalf("value %d: decodeInt reported an out-of-range magnitude", i)
		}
		if oobAt != nil && oobAt[i] {
			if ok {
				t.Fatalf("value %d: decoded %d, want OOB", i, got)
			}
			continue
		}
		if !ok {
			t.Fatalf("value %d: decoded OOB, want %d", i, want)
		}
		if got != want {
			t.Fatalf("value %d: decoded %d, want %d", i, got, want)
		}
	}
}

func TestArithIntRoundTripSmall(t *testing.T) {
	// Every value in the narrowest range plus its immediate neighbors,
	// both signs - the values symbol dictionary and text region decoding
	// actually spend most of their time reading.
	var values []int
	for v := -6; v <= 6; v++ {
		values = append(values, v)
	}
	arithIntRoundTrip(t, values, nil)
}

func TestArithIntRoundTripRangeBoundaries(t *testing.T) {
	// The first and last value of each of T.88 Table A.1's six ranges,
	// and the first value of the next range up - the encoder's range
	// selection and the decoder's prefix walk have to agree on exactly
	// where each boundary falls, and an off-by-one there would otherwise
	// only show up on rare inputs.
	var values []int
	for _, r := range arithIntRanges {
		last := r.offset + (1 << uint(min(r.valueBits, 20))) - 1
		for _, v := range []int{r.offset, r.offset + 1, last} {
			values = append(values, v, -v)
		}
	}
	arithIntRoundTrip(t, values, nil)
}

func TestArithIntRoundTripOOB(t *testing.T) {
	// OOB is spelled as "negative zero", so a stream mixing OOB with
	// real zeros and small negatives is exactly where a confused
	// implementation would go wrong.
	values := []int{0, 0, 5, -5, 0, 0, -1, 0}
	oobAt := []bool{false, true, false, false, true, false, false, true}
	arithIntRoundTrip(t, values, oobAt)
}

func TestArithIntRoundTripRandom(t *testing.T) {
	// A long run through one shared context set, which is how a real
	// segment uses these: the probability estimates adapt over hundreds
	// of values, so a context-selector (PREV) mismatch between the two
	// directions shows up here even when short sequences pass.
	rng := rand.New(rand.NewSource(7))
	values := make([]int, 2000)
	for i := range values {
		// Skewed toward the small values real JBIG2 fields hold, with
		// occasional large ones to reach the wider ranges.
		switch rng.Intn(10) {
		case 0:
			values[i] = rng.Intn(200000) - 100000
		case 1:
			values[i] = rng.Intn(1000) - 500
		default:
			values[i] = rng.Intn(40) - 20
		}
	}
	arithIntRoundTrip(t, values, nil)
}

func TestArithIntRejectsHugeMagnitude(t *testing.T) {
	// A hostile stream can spell out a magnitude just under 2^32 in
	// Table A.1's last range, which does not fit in an int on a 32-bit
	// platform. decodeInt must report that as out of range rather than
	// hand a caller a value that has silently wrapped negative.
	//
	// encodeInt refuses to build such a value (that is the point), so
	// this reaches past it to encodeIntBits, the shared spelling-out
	// step, which imposes no bound of its own.
	enc := newMQEncoder()
	enc.encodeIntBits(newArithIntCtx(), 0, arithIntRanges[len(arithIntRanges)-1], 0xFFFFFFFF)
	coded := enc.flush()

	if _, _, bad := newMQDecoder(coded).decodeInt(newArithIntCtx()); !bad {
		t.Fatal("decodeInt accepted a magnitude of nearly 2^32; want it reported as out of range")
	}
}

func TestArithIntMagnitudeBoundIsEnforced(t *testing.T) {
	// The bound decodeInt enforces has to be reachable by the encoder's
	// own largest legal value, or the two would disagree about what is
	// encodable at all.
	arithIntRoundTrip(t, []int{maxArithIntMagnitude, -maxArithIntMagnitude}, nil)
}

// arithIAIDRoundTrip encodes ids at the given code length and decodes
// them back.
func arithIAIDRoundTrip(t *testing.T, codeLen int, ids []int) {
	t.Helper()

	enc := newMQEncoder()
	encCx := newArithIAIDCtx(codeLen)
	for _, id := range ids {
		enc.encodeIAID(encCx, id)
	}
	coded := enc.flush()

	dec := newMQDecoder(coded)
	decCx := newArithIAIDCtx(codeLen)
	for i, want := range ids {
		if got := dec.decodeIAID(decCx); got != want {
			t.Fatalf("id %d: decoded %d, want %d (codeLen=%d)", i, got, want, codeLen)
		}
	}
}

func TestArithIAIDRoundTrip(t *testing.T) {
	// codeLen 0 is a real case, not a degenerate one: a text region
	// whose dictionary holds exactly one symbol needs no bits at all to
	// say which symbol an instance uses, and must decode every instance
	// as symbol 0 without consuming anything from the stream.
	arithIAIDRoundTrip(t, 0, []int{0, 0, 0})

	rng := rand.New(rand.NewSource(11))
	for _, codeLen := range []int{1, 3, 8, 11} {
		ids := make([]int, 500)
		for i := range ids {
			ids[i] = rng.Intn(1 << uint(codeLen))
		}
		arithIAIDRoundTrip(t, codeLen, ids)
	}
}

func TestArithIntKnownEncoding(t *testing.T) {
	// An independent check on Table A.1 itself: rather than trusting the
	// encoder, this drives the *decoder* with a hand-built decision
	// sequence and asserts the value it reconstructs. Because every
	// decision here goes through a context in its initial state and the
	// MQ coder is deterministic, encodeMQSequence can produce that exact
	// sequence - but the sequence itself (sign, prefix, value bits) is
	// written out literally from the standard's table, not derived from
	// jbig2arith.go's own range list.
	//
	// The value chosen, 25, sits in the third range (prefix 110, six
	// value bits, offset 20), so its magnitude bits spell 25-20 = 5.
	bits := []int{
		0,       // Sign: positive.
		1, 1, 0, // Prefix selecting the 6-bit, offset-20 range.
		0, 0, 0, 1, 0, 1, // 5, most significant bit first.
	}
	// Context selection mirrors decodeInt's PREV walk, written out here
	// from T.88's own description rather than shared with it: PREV
	// starts at 1, each decoded bit shifts into it, and once PREV passes
	// 255 its top bit is pinned so the selector stays within 512
	// contexts. This value needs 10 bits, so the last two do hit that
	// capped form.
	ctxIndex := make([]int, len(bits))
	prev := 1
	for i, bit := range bits {
		ctxIndex[i] = prev
		if prev < 256 {
			prev = prev<<1 | bit
		} else {
			prev = ((prev<<1 | bit) & 511) | 256
		}
	}
	coded := encodeMQSequence(arithIntNumContexts, ctxIndex, bits)

	dec := newMQDecoder(coded)
	got, ok, bad := dec.decodeInt(newArithIntCtx())
	if bad || !ok {
		t.Fatalf("decodeInt returned ok=%v bad=%v, want a value", ok, bad)
	}
	if got != 25 {
		t.Fatalf("decodeInt = %d, want 25", got)
	}
}
