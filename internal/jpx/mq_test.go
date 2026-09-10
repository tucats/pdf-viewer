package jpx

import (
	"math/rand"
	"testing"
)

// TestMQRoundTrip encodes a pseudo-random sequence of decisions across a
// handful of contexts and confirms mqDecoder reproduces every one, in
// order - the same "opposite ends of the standard" verification doc.go's
// Provenance section describes, since this package has no real-world
// MQ-coded sample to check against instead.
func TestMQRoundTrip(t *testing.T) {
	const numContexts = 5
	const numBits = 20000

	rng := rand.New(rand.NewSource(1))
	bits := make([]int, numBits)
	ctxIndex := make([]int, numBits)
	// Bias the decisions per context so the coder actually exercises its
	// probability adaptation (state transitions), not just a 50/50 coin
	// flip that would mostly wander near state 0.
	bias := []float64{0.5, 0.9, 0.1, 0.99, 0.01}
	for i := range bits {
		c := rng.Intn(numContexts)
		ctxIndex[i] = c
		if rng.Float64() < bias[c] {
			bits[i] = 1
		}
	}

	encCtx := make([]mqContext, numContexts)
	enc := newMQEncoder()
	for i := range bits {
		enc.encodeBit(&encCtx[ctxIndex[i]], bits[i])
	}
	encoded := enc.flush()

	decCtx := make([]mqContext, numContexts)
	dec := newMQDecoder(encoded)
	for i := range bits {
		got := dec.decodeBit(&decCtx[ctxIndex[i]])
		if got != bits[i] {
			t.Fatalf("bit %d (context %d): decoded %d, want %d", i, ctxIndex[i], got, bits[i])
		}
	}
}

// TestMQRoundTripAllMPS and TestMQRoundTripEmpty exercise the coder's
// edges: a run so one-sided it drives every context deep into qeTable,
// and encoding nothing at all.
func TestMQRoundTripAllMPS(t *testing.T) {
	var cx mqContext
	enc := newMQEncoder()
	for i := 0; i < 5000; i++ {
		enc.encodeBit(&cx, 0)
	}
	encoded := enc.flush()

	var dcx mqContext
	dec := newMQDecoder(encoded)
	for i := 0; i < 5000; i++ {
		if got := dec.decodeBit(&dcx); got != 0 {
			t.Fatalf("bit %d: decoded %d, want 0", i, got)
		}
	}
}

func TestMQEncoderEmpty(t *testing.T) {
	enc := newMQEncoder()
	encoded := enc.flush()
	// Just must not panic, and must produce a decoder that doesn't
	// error out when asked for bits (it will just pad with the standard
	// end-of-data convention).
	dec := newMQDecoder(encoded)
	var cx mqContext
	_ = dec.decodeBit(&cx)
}
