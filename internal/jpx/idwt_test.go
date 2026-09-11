package jpx

import (
	"math"
	"math/rand"
	"testing"
)

// forward53 is a from-scratch, test-only forward 5/3 analysis transform
// (the standard textbook lifting-scheme inverse of filter53's own
// synthesis steps, in reverse order and opposite sign - predict then
// update rather than filter53's update then predict), used only to
// build known-good coefficient inputs for filter53's round-trip tests
// below. It is deliberately not derived from filter53 itself (each
// direction is written from the lifting scheme's own textbook
// definition, independently), so a round trip is real evidence filter53
// is correct rather than a tautology - the same "opposite ends of the
// standard's own description" approach this package's other from-scratch
// verifications use (see doc.go's Provenance section, tier1_test.go's
// tier1Encoder).
//
// x must already be symmetrically extended by waveletPadding on each
// side (extendSymmetric), matching what filter53 itself expects.
//
// filter53 gets away with a single upfront extension (no re-extension
// between its own two steps) because each step deliberately overreaches
// one position into the padding the other step needs - see idwt.go's
// own doc comments. Undoing the steps in reverse still needs that same
// padding, but for the *intermediate* (partially-transformed) buffer
// state, which the single original extension does not describe - so
// this function re-extends between its own two steps, each time
// mirroring the coefficient-domain values the update step (run second
// here, first in filter53) actually needs at its boundary. This is not
// a deviation from filter53's own convention, just this function
// needing it explicitly since it does not get the decoder's lucky
// single-pass overreach ordering for free.
func forward53(x []float64, offset, length int) {
	half := length / 2

	// Predict: odd-indexed samples are replaced by their own prediction
	// residual from their even-indexed neighbors.
	j := offset + 1
	for n := 0; n < half; n++ {
		x[j] -= float64((int64(x[j-1]) + int64(x[j+1])) >> 1)
		j += 2
	}
	extendSymmetric(x, offset, length)

	// Update: even-indexed samples absorb a rounded quarter of their
	// (now-residual) odd-indexed neighbors.
	j = offset
	for n := 0; n < half+1; n++ {
		x[j] += float64((int64(x[j-1]) + int64(x[j+1]) + 2) >> 2)
		j += 2
	}
}

// forward97 is forward53's floating-point counterpart for the 9/7
// filter: the same six lifting steps filter97 undoes, run in reverse
// order with each step's sign flipped, written independently from the
// standard's own lifting definition for the same "opposite ends" reason.
func forward97(x []float64, offset, length int) {
	half := length / 2

	// Step 6 (undoes filter97's step 6).
	if half != 0 {
		j := offset + 1
		for n := 0; n < half; n++ {
			x[j] += filter97Alpha*x[j-1] + filter97Alpha*x[j+1]
			j += 2
		}
	}
	extendSymmetric(x, offset, length)

	// Step 5.
	j := offset
	for n := 0; n < half+1; n++ {
		x[j] += filter97Beta*x[j-1] + filter97Beta*x[j+1]
		j += 2
	}
	extendSymmetric(x, offset, length)

	// Step 4.
	j = offset - 1
	for n := 0; n < half+2; n++ {
		x[j] += filter97Gamma*x[j-1] + filter97Gamma*x[j+1]
		j += 2
	}
	extendSymmetric(x, offset, length)

	// Steps 1 & 3.
	j = offset - 2
	for n := 0; n < half+3; n++ {
		x[j] = (x[j] + filter97Delta*x[j-1] + filter97Delta*x[j+1]) / filter97K
		j += 2
	}
	extendSymmetric(x, offset, length)

	// Step 2.
	j = offset - 3
	for n := 0; n < half+4; n++ {
		x[j] *= filter97K
		j += 2
	}
}

// padded builds a waveletPadding-bordered buffer holding samples at
// [waveletPadding : waveletPadding+len(samples)], symmetrically extended
// exactly as synthesizeLevel would before calling a filter.
func padded(samples []float64) []float64 {
	buf := make([]float64, len(samples)+2*waveletPadding)
	copy(buf[waveletPadding:], samples)
	extendSymmetric(buf, waveletPadding, len(samples))
	return buf
}

func TestFilter53RoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, length := range []int{1, 2, 3, 4, 5, 8, 9, 16, 17, 31} {
		original := make([]float64, length)
		for i := range original {
			original[i] = float64(rng.Intn(2001) - 1000)
		}

		forwardBuf := padded(original)
		forward53(forwardBuf, waveletPadding, length)
		// forward53 only ever reads/writes within [offset, offset+length);
		// re-extend before the inverse, since the inverse also needs
		// correctly-extended border samples of the *transformed* signal.
		extendSymmetric(forwardBuf, waveletPadding, length)
		filter53(forwardBuf, waveletPadding, length)

		for i, want := range original {
			if got := forwardBuf[waveletPadding+i]; got != want {
				t.Fatalf("length %d, sample %d: got %v, want %v", length, i, got, want)
			}
		}
	}
}

func TestFilter97RoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, length := range []int{1, 2, 3, 4, 5, 8, 9, 16, 17, 31} {
		original := make([]float64, length)
		for i := range original {
			original[i] = rng.Float64()*2000 - 1000
		}

		forwardBuf := padded(original)
		forward97(forwardBuf, waveletPadding, length)
		extendSymmetric(forwardBuf, waveletPadding, length)
		filter97(forwardBuf, waveletPadding, length)

		for i, want := range original {
			got := forwardBuf[waveletPadding+i]
			if math.Abs(got-want) > 1e-6*(1+math.Abs(want)) {
				t.Fatalf("length %d, sample %d: got %v, want %v", length, i, got, want)
			}
		}
	}
}

// TestSynthesizeLevelDegenerateAxis confirms synthesizeLevel's
// width==1/height==1 special cases (§F.3.4/F.3.5's own halving rule,
// keyed on the tile-component's pixel-offset parity) match the general
// lifting-step path in the one place they can be compared directly: a
// single sample, which every synthesis filter (with no real neighbor to
// lift against) must leave that sample's low-pass value untouched
// regardless of offset parity - filter53/filter97 would divide by zero
// neighbors if this path ever fell through to the general filter
// instead.
func TestSynthesizeLevelDegenerateAxis(t *testing.T) {
	for _, u0 := range []int{0, 1} {
		ll := synthesisLevel{width: 1, height: 1, items: []float64{7}}
		hh := synthesisLevel{width: 1, height: 1, items: []float64{0}}
		result := synthesizeLevel(true, ll, hh, u0, 0)

		want := 7.0
		if u0&1 != 0 {
			want = 3.5
		}
		if result.items[0] != want {
			t.Fatalf("u0=%d: got %v, want %v", u0, result.items[0], want)
		}
	}
}

// buildTestSubband constructs a single-precinct subband and its
// code-blocks directly (bypassing tier-2 packet parsing entirely, which
// this test does not need), with samples already attached - letting
// dequantizeComponent/copyCoefficients be tested against hand-picked
// magnitude/sign/bitsDecoded values without a full codestream.
func buildTestSubband(kind subbandKind, tbx0, tby0, tbx1, tby1 int, samples *codeBlockSamples) *subbandInfo {
	return &subbandInfo{
		kind: kind,
		tbx0: tbx0, tby0: tby0, tbx1: tbx1, tby1: tby1,
		codeBlocks: []*codeBlockInfo{
			{tbx0: tbx0, tby0: tby0, tbx1: tbx1, tby1: tby1, samples: samples},
		},
	}
}

func TestDequantizeComponentReversibleExact(t *testing.T) {
	// A single 2x2 LL-only (DecompositionLevels 0) component: every
	// coefficient fully decoded (bitsDecoded == mb), so the reversible
	// path must recover the encoded integer magnitudes exactly, signs
	// applied, with no scaling at all (delta is always 1 for a
	// reversible transform).
	const mb = 5 // guardBits(2) + exponent(4) - 1
	samples := &codeBlockSamples{
		width: 2, height: 2,
		magnitude:   []uint32{3, 0, 7, 12},
		sign:        []uint8{0, 0, 1, 0},
		bitsDecoded: []int{mb, mb, mb, mb},
	}
	sb := buildTestSubband(subbandLL, 0, 0, 2, 2, samples)
	res := &resolutionInfo{level: 0, trx0: 0, try0: 0, trx1: 2, try1: 2, subbands: []*subbandInfo{sb}}
	comp := &componentDecode{
		coding:      CodingStyle{Transform: Transform5x3, DecompositionLevels: 0},
		quant:       QuantizationStyle{Style: QuantNone, GuardBits: 2, StepSizes: []QuantStep{{Exponent: 4}}},
		bitDepth:    8,
		resolutions: []*resolutionInfo{res},
	}

	levels, err := dequantizeComponent(comp)
	if err != nil {
		t.Fatalf("dequantizeComponent: %v", err)
	}
	want := []float64{3, 0, -7, 12}
	for i, w := range want {
		if levels[0].items[i] != w {
			t.Fatalf("item %d: got %v, want %v", i, levels[0].items[i], w)
		}
	}
}

func TestDequantizeComponentTruncatedReconstruction(t *testing.T) {
	// One coefficient decoded to only 2 of its subband's 5 nominal
	// bit-planes (mb=5, an irreversible-transform subband whose delta
	// works out to exactly 1 - bitDepth 3, LL gain 0, exponent 3, mu 0:
	// delta=2^(3+0-3)=1): Annex E.1's reconstruction rule inserts a
	// single "1" bit at the first never-transmitted bit-plane position
	// (bit 2, i.e. value 4) as the best available guess for the missing
	// low-order bits, on top of the 2 bits actually coded (raw magnitude
	// 3, i.e. bits 4 and 3 of the true value) shifted up into position:
	// reconstructed = (3<<3) | (1<<2) = 24+4 = 28. This reconstruction
	// bit is irreversible-only (magnitudeCorrection is always 0, not
	// 0.5, for a reversible transform - see copyCoefficients' doc
	// comment - so a truncated reversible code-block is simply
	// zero-padded instead; TestDequantizeComponentReversibleExact
	// covers that path's full-precision case).
	const guardBits, epsilon = 3, 3
	const mb = guardBits + epsilon - 1
	samples := &codeBlockSamples{
		width: 1, height: 1,
		magnitude:   []uint32{3},
		sign:        []uint8{0},
		bitsDecoded: []int{2},
	}
	sb := buildTestSubband(subbandLL, 0, 0, 1, 1, samples)
	res := &resolutionInfo{level: 0, trx0: 0, try0: 0, trx1: 1, try1: 1, subbands: []*subbandInfo{sb}}
	comp := &componentDecode{
		coding:      CodingStyle{Transform: Transform9x7, DecompositionLevels: 0},
		quant:       QuantizationStyle{Style: QuantScalarExpounded, GuardBits: guardBits, StepSizes: []QuantStep{{Exponent: epsilon}}},
		bitDepth:    3,
		resolutions: []*resolutionInfo{res},
	}

	levels, err := dequantizeComponent(comp)
	if err != nil {
		t.Fatalf("dequantizeComponent: %v", err)
	}
	if got, want := levels[0].items[0], 28.0; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDequantizeComponentIrreversibleDelta(t *testing.T) {
	// Irreversible transform: delta = 2^(bitDepth+gainLog2-epsilon) *
	// (1+mu/2048). With bitDepth=8, LL's gainLog2=0, epsilon=6, mu=0:
	// delta = 2^(8-6) = 4. A fully-decoded magnitude of 5 (bitsDecoded
	// == mb, no missing-bit correction) dequantizes to (5+0.5)*4 = 22.
	const guardBits = 1
	const epsilon = 6
	mb := guardBits + epsilon - 1
	samples := &codeBlockSamples{
		width: 1, height: 1,
		magnitude:   []uint32{5},
		sign:        []uint8{1},
		bitsDecoded: []int{mb},
	}
	sb := buildTestSubband(subbandLL, 0, 0, 1, 1, samples)
	res := &resolutionInfo{level: 0, trx0: 0, try0: 0, trx1: 1, try1: 1, subbands: []*subbandInfo{sb}}
	comp := &componentDecode{
		coding:      CodingStyle{Transform: Transform9x7, DecompositionLevels: 0},
		quant:       QuantizationStyle{Style: QuantScalarExpounded, GuardBits: guardBits, StepSizes: []QuantStep{{Exponent: epsilon}}},
		bitDepth:    8,
		resolutions: []*resolutionInfo{res},
	}

	levels, err := dequantizeComponent(comp)
	if err != nil {
		t.Fatalf("dequantizeComponent: %v", err)
	}
	if got, want := levels[0].items[0], -22.0; got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDequantizeComponentInterleaving(t *testing.T) {
	// A single decomposition level: HL/LH/HH subbands (each a single
	// 1x1 code-block) must land at their own interleaved position
	// within the resolution's 2x2 grid, with the LL corner left zero
	// for synthesizeLevel to fill in later.
	mkSamples := func(mag uint32) *codeBlockSamples {
		return &codeBlockSamples{width: 1, height: 1, magnitude: []uint32{mag}, sign: []uint8{0}, bitsDecoded: []int{5}}
	}
	hl := buildTestSubband(subbandHL, 0, 0, 1, 1, mkSamples(1))
	lh := buildTestSubband(subbandLH, 0, 0, 1, 1, mkSamples(2))
	hh := buildTestSubband(subbandHH, 0, 0, 1, 1, mkSamples(3))
	res := &resolutionInfo{level: 1, trx0: 0, try0: 0, trx1: 2, try1: 2, subbands: []*subbandInfo{hl, lh, hh}}
	comp := &componentDecode{
		coding:      CodingStyle{Transform: Transform5x3, DecompositionLevels: 1},
		quant:       QuantizationStyle{Style: QuantNone, GuardBits: 2, StepSizes: []QuantStep{{Exponent: 4}, {Exponent: 4}, {Exponent: 4}, {Exponent: 4}}},
		bitDepth:    8,
		resolutions: []*resolutionInfo{{trx0: 0, try0: 0, trx1: 1, try1: 1}, res},
	}

	levels, err := dequantizeComponent(comp)
	if err != nil {
		t.Fatalf("dequantizeComponent: %v", err)
	}
	// Grid layout (row-major, width 2): [LL(0,0)=0, HL(0,1)=1, LH(1,0)=2, HH(1,1)=3]
	want := []float64{0, 1, 2, 3}
	for i, w := range want {
		if levels[1].items[i] != w {
			t.Fatalf("item %d: got %v, want %v (full grid %v)", i, levels[1].items[i], w, levels[1].items)
		}
	}
}

// TestReconstructTileComponentTier1RoundTrip exercises the real 14c
// (tier1Encoder/decodeCodeBlockTier1) round trip feeding straight into
// 14d, for a single-resolution (DecompositionLevels 0, LL-only, no
// wavelet synthesis needed) reversible component: the final
// reconstructed sample array must equal the originally-encoded integer
// coefficients exactly, since a reversible transform's delta is always
// 1 and this code-block is never truncated (bitsDecoded always equals
// mb).
func TestReconstructTileComponentTier1RoundTrip(t *testing.T) {
	const width, height = 4, 4
	const numBitPlanes = 6
	rng := rand.New(rand.NewSource(3))
	magnitude := make([]uint32, width*height)
	sign := make([]uint8, width*height)
	for i := range magnitude {
		magnitude[i] = uint32(rng.Intn(1 << numBitPlanes))
		sign[i] = uint8(rng.Intn(2))
	}

	data, numPasses := encodeCodeBlockTier1(width, height, magnitude, sign, subbandLL, numBitPlanes, 0)
	cb := &codeBlockInfo{
		tbx0: 0, tby0: 0, tbx1: width, tby1: height,
		contributions: []codeBlockContribution{{numPasses: numPasses, data: data}},
	}
	samples, err := decodeCodeBlockTier1(cb, width, height, subbandLL, 0)
	if err != nil {
		t.Fatalf("decodeCodeBlockTier1: %v", err)
	}
	cb.samples = samples

	sb := &subbandInfo{kind: subbandLL, tbx0: 0, tby0: 0, tbx1: width, tby1: height, codeBlocks: []*codeBlockInfo{cb}}
	res := &resolutionInfo{level: 0, trx0: 0, try0: 0, trx1: width, try1: height, subbands: []*subbandInfo{sb}}
	comp := &componentDecode{
		coding:      CodingStyle{Transform: Transform5x3, DecompositionLevels: 0},
		quant:       QuantizationStyle{Style: QuantNone, GuardBits: 0, StepSizes: []QuantStep{{Exponent: numBitPlanes + 1}}},
		bitDepth:    8,
		resolutions: []*resolutionInfo{res},
	}

	reconstructed, err := reconstructTileComponent(comp)
	if err != nil {
		t.Fatalf("reconstructTileComponent: %v", err)
	}
	for i := range magnitude {
		want := float64(magnitude[i])
		if sign[i] != 0 {
			want = -want
		}
		if reconstructed.items[i] != want {
			t.Fatalf("sample %d: got %v, want %v", i, reconstructed.items[i], want)
		}
	}
}
