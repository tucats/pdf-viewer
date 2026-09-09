package filter

import (
	"math/rand"
	"testing"
)

// mqRoundTrip encodes bits (each 0 or 1) through numContexts independent
// contexts (bits[i] is encoded against context bits[i]%numContexts, so a
// small numContexts exercises real context reuse and state adaptation,
// while a numContexts >= len(bits) exercises every context starting from
// its initial, never-before-seen state) and decodes them back, failing
// the test if the round trip does not reproduce bits exactly.
func mqRoundTrip(t *testing.T, bits []int, numContexts int) {
	t.Helper()

	ctxIndex := make([]int, len(bits))
	for i := range bits {
		ctxIndex[i] = i % numContexts
	}
	coded := encodeMQSequence(numContexts, ctxIndex, bits)

	decContexts := make([]mqContext, numContexts)
	dec := newMQDecoder(coded)
	for i, want := range bits {
		got := dec.decodeBit(&decContexts[i%numContexts])
		if got != want {
			t.Fatalf("bit %d: decoded %d, want %d (numContexts=%d, len(bits)=%d)", i, got, want, numContexts, len(bits))
		}
	}
}

func TestMQRoundTripAllZeros(t *testing.T) {
	bits := make([]int, 1000)
	mqRoundTrip(t, bits, 1)
}

func TestMQRoundTripAllOnes(t *testing.T) {
	bits := make([]int, 1000)
	for i := range bits {
		bits[i] = 1
	}
	mqRoundTrip(t, bits, 1)
}

func TestMQRoundTripAlternating(t *testing.T) {
	// Alternating bits against a single context is close to a coder's
	// worst case (the context's probability estimate is constantly
	// being contradicted, so it keeps oscillating near 50/50 - the
	// point in the qeTable state machine with the least "runway" before
	// a switchFlag transition), a good stress case for the
	// renormalization and byte-stuffing logic.
	bits := make([]int, 300)
	for i := range bits {
		bits[i] = i % 2
	}
	mqRoundTrip(t, bits, 1)
}

func TestMQRoundTripSingleContextFreshEveryBit(t *testing.T) {
	// numContexts == len(bits) means every bit is encoded against a
	// context in its untouched initial state (index 0, mps 0) - the
	// simplest possible case, and a useful baseline if a more elaborate
	// test ever fails.
	bits := make([]int, 100)
	rng := rand.New(rand.NewSource(1))
	for i := range bits {
		bits[i] = rng.Intn(2)
	}
	mqRoundTrip(t, bits, len(bits))
}

func TestMQRoundTripRandom(t *testing.T) {
	// A skewed random bit source (90% zeros) run through a handful of
	// shared, reused contexts - the shape real generic-region decoding
	// actually produces (a context sees many decisions over the course
	// of an image, and most real scanned content is heavily biased
	// toward background).
	rng := rand.New(rand.NewSource(42))
	for _, numContexts := range []int{1, 2, 5, 16} {
		bits := make([]int, 400)
		for i := range bits {
			if rng.Intn(10) == 0 {
				bits[i] = 1
			}
		}
		mqRoundTrip(t, bits, numContexts)
	}
}

func TestMQRoundTripManyShapes(t *testing.T) {
	// A broad randomized sweep over sequence lengths, 0/1 balances and
	// context counts. Its real purpose is to exercise the encoder's
	// byteOut procedure (jbig2mq.go), whose carry-propagation and
	// 0xFF bit-stuffing paths are the subtlest part of this file: they
	// only trigger on particular bit patterns, so a handful of
	// hand-written cases could easily miss them entirely. This test
	// therefore also asserts that a decent share of the streams it
	// produces actually contain an 0xFF byte, so that if some future
	// change made that path unreachable, the test would fail rather than
	// quietly stop covering it.
	streams := 0
	streamsWithFF := 0

	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(3000)
		// oneInN controls how strongly the bits lean toward 0: 1 is an
		// even coin flip, larger values approach the heavily
		// background-biased content real scanned pages produce.
		oneInN := 1 + rng.Intn(20)

		bits := make([]int, n)
		for i := range bits {
			if rng.Intn(oneInN) == 0 {
				bits[i] = 1
			}
		}

		for _, numContexts := range []int{1, 3, 64, n} {
			ctxIndex := make([]int, n)
			for i := range bits {
				ctxIndex[i] = i % numContexts
			}

			coded := encodeMQSequence(numContexts, ctxIndex, bits)
			streams++
			for _, b := range coded {
				if b == 0xFF {
					streamsWithFF++
					break
				}
			}

			decContexts := make([]mqContext, numContexts)
			dec := newMQDecoder(coded)
			for i, want := range bits {
				if got := dec.decodeBit(&decContexts[ctxIndex[i]]); got != want {
					t.Fatalf("seed %d, numContexts %d, len(bits) %d: bit %d decoded %d, want %d",
						seed, numContexts, n, i, got, want)
				}
			}
		}
	}

	if streamsWithFF*4 < streams {
		t.Errorf("only %d of %d encoded streams contained an 0xFF byte; the encoder's bit-stuffing path is barely being exercised", streamsWithFF, streams)
	}
}

func TestMQRoundTripHighlyCompressible(t *testing.T) {
	// A very long run of identical decisions through one context drives
	// that context all the way to the confident end of qeTable, where
	// each further decision costs a tiny fraction of a bit - the regime
	// that makes JBIG2 worth having for scanned pages (see jbig2mq.go's
	// doc comment). It doubles as a check that nothing in the coder
	// misbehaves over a sequence far longer than the other tests use.
	const n = 200000
	bits := make([]int, n)
	ctxIndex := make([]int, n)

	coded := encodeMQSequence(1, ctxIndex, bits)
	if len(coded) > 64 {
		t.Errorf("encoded %d identical decisions into %d bytes; expected a near-constant handful, so the coder is not reaching its confident states", n, len(coded))
	}

	decContexts := make([]mqContext, 1)
	dec := newMQDecoder(coded)
	for i, want := range bits {
		if got := dec.decodeBit(&decContexts[0]); got != want {
			t.Fatalf("bit %d: decoded %d, want %d", i, got, want)
		}
	}
}

func TestMQRoundTripEmpty(t *testing.T) {
	mqRoundTrip(t, nil, 1)
}

func TestMQRoundTripSingleBit(t *testing.T) {
	mqRoundTrip(t, []int{0}, 1)
	mqRoundTrip(t, []int{1}, 1)
}

func TestMQRoundTripCtxIndexBitLengthMismatchPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected a panic for mismatched ctxIndex/bit lengths")
		}
	}()
	encodeMQSequence(1, []int{0, 0}, []int{0})
}
