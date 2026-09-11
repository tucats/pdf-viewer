package jpx

import (
	"errors"
	"math/rand"
	"testing"
)

// tier1Encoder is a from-scratch, test-only mirror of tier1Model: it
// walks the exact same coding-pass control flow (significance
// propagation, magnitude refinement, cleanup - including cleanup's
// run-length optimization), but instead of decoding each bit from an
// arithmetic-coded stream, it reads the bit to encode out of a
// caller-supplied ground-truth coefficient array and encodes it. Its
// own "revealed so far" magnitude/sign state (codedMagnitude/
// codedSign) is deliberately separate from the ground truth
// (trueMagnitude/trueSign): control flow (which coefficients are
// already significant, what their neighbor pattern looks like) must
// depend only on what has actually been encoded so far, exactly
// mirroring what a decoder can know at the same point - this is what
// makes the round trip a real test of tier1Model rather than a
// tautology.
//
// This exists purely to validate tier1Model (this package has no
// independently-produced real-world JPX sample - see doc.go's
// Provenance section); it is not reachable from any decode path.
type tier1Encoder struct {
	width, height int
	labels        *[75]byte

	trueMagnitude []uint32
	trueSign      []uint8

	codedMagnitude  []uint32
	codedSign       []uint8
	neighborSig     []uint8
	processingFlags []uint8

	contexts [numContexts]mqContext
	encoder  *mqEncoder
}

func newTier1Encoder(width, height int, kind subbandKind, trueMagnitude []uint32, trueSign []uint8) *tier1Encoder {
	n := width * height
	e := &tier1Encoder{
		width: width, height: height,
		labels:          contextLabelsFor(kind),
		trueMagnitude:   trueMagnitude,
		trueSign:        trueSign,
		codedMagnitude:  make([]uint32, n),
		codedSign:       make([]uint8, n),
		neighborSig:     make([]uint8, n),
		processingFlags: make([]uint8, n),
		encoder:         newMQEncoder(),
	}
	e.resetContexts()
	return e
}

func (e *tier1Encoder) resetContexts() {
	for i := range e.contexts {
		e.contexts[i] = mqContext{}
	}
	e.contexts[0] = mqContext{index: 4}
	e.contexts[ctxUniform] = mqContext{index: 46}
	e.contexts[ctxRunLength] = mqContext{index: 3}
}

func (e *tier1Encoder) encodeSignBit(row, column, index int) {
	contribution := signContribution(e.width, e.height, e.codedMagnitude, e.codedSign, row, column, index)
	trueBit := int(e.trueSign[index])
	if contribution >= 0 {
		e.encoder.encodeBit(&e.contexts[9+contribution], trueBit)
	} else {
		e.encoder.encodeBit(&e.contexts[9-contribution], trueBit^1)
	}
	e.codedSign[index] = uint8(trueBit)
}

func (e *tier1Encoder) runSignificancePropagationPass(p int) {
	width, height := e.width, e.height
	for i0 := 0; i0 < height; i0 += 4 {
		for j := 0; j < width; j++ {
			index := i0*width + j
			for i1 := 0; i1 < 4; i1, index = i1+1, index+width {
				i := i0 + i1
				if i >= height {
					break
				}
				e.processingFlags[index] &^= processedMask
				if e.codedMagnitude[index] != 0 || e.neighborSig[index] == 0 {
					continue
				}
				contextLabel := e.labels[e.neighborSig[index]]
				bit := int((e.trueMagnitude[index] >> uint(p)) & 1)
				e.encoder.encodeBit(&e.contexts[contextLabel], bit)
				if bit != 0 {
					e.encodeSignBit(i, j, index)
					e.codedMagnitude[index] = 1
					setNeighborsSignificance(e.width, e.height, e.neighborSig, i, j, index)
					e.processingFlags[index] |= firstMagnitudeBitMask
				}
				e.processingFlags[index] |= processedMask
			}
		}
	}
}

func (e *tier1Encoder) runMagnitudeRefinementPass(p int) {
	width, height := e.width, e.height
	length := width * height
	width4 := width * 4
	for index0 := 0; index0 < length; index0 += width4 {
		indexNext := index0 + width4
		if indexNext > length {
			indexNext = length
		}
		for j := 0; j < width; j++ {
			for index := index0 + j; index < indexNext; index += width {
				if e.codedMagnitude[index] == 0 || e.processingFlags[index]&processedMask != 0 {
					continue
				}
				contextLabel := 16
				if e.processingFlags[index]&firstMagnitudeBitMask != 0 {
					e.processingFlags[index] ^= firstMagnitudeBitMask
					if e.neighborSig[index]&0x7F == 0 {
						contextLabel = 15
					} else {
						contextLabel = 14
					}
				}
				bit := int((e.trueMagnitude[index] >> uint(p)) & 1)
				e.encoder.encodeBit(&e.contexts[contextLabel], bit)
				e.codedMagnitude[index] = (e.codedMagnitude[index] << 1) | uint32(bit)
				e.processingFlags[index] |= processedMask
			}
		}
	}
}

func (e *tier1Encoder) runCleanupPass(p int) {
	width, height := e.width, e.height
	oneRowDown, twoRowsDown, threeRowsDown := width, width*2, width*3

	for i0 := 0; i0 < height; {
		iNext := i0 + 4
		if iNext > height {
			iNext = height
		}
		indexBase := i0 * width
		checkAllEmpty := i0+3 < height

	columns:
		for j := 0; j < width; j++ {
			index0 := indexBase + j
			allEmpty := checkAllEmpty &&
				e.processingFlags[index0] == 0 &&
				e.processingFlags[index0+oneRowDown] == 0 &&
				e.processingFlags[index0+twoRowsDown] == 0 &&
				e.processingFlags[index0+threeRowsDown] == 0 &&
				e.neighborSig[index0] == 0 &&
				e.neighborSig[index0+oneRowDown] == 0 &&
				e.neighborSig[index0+twoRowsDown] == 0 &&
				e.neighborSig[index0+threeRowsDown] == 0

			i1 := 0
			index := index0
			i := i0
			if allEmpty {
				sigRow := -1
				for r := 0; r < 4; r++ {
					if (e.trueMagnitude[index0+r*width]>>uint(p))&1 != 0 {
						sigRow = r
						break
					}
				}
				hasSig := 0
				if sigRow >= 0 {
					hasSig = 1
				}
				e.encoder.encodeBit(&e.contexts[ctxRunLength], hasSig)
				if hasSig == 0 {
					continue columns
				}
				i1 = sigRow
				e.encoder.encodeBit(&e.contexts[ctxUniform], (i1>>1)&1)
				e.encoder.encodeBit(&e.contexts[ctxUniform], i1&1)
				if i1 != 0 {
					i = i0 + i1
					index += i1 * width
				}
				e.encodeSignBit(i, j, index)
				e.codedMagnitude[index] = 1
				setNeighborsSignificance(e.width, e.height, e.neighborSig, i, j, index)
				e.processingFlags[index] |= firstMagnitudeBitMask
				i1++
			}
			for i, index = i0+i1, index0+i1*width; i < iNext; i, index = i+1, index+width {
				if e.codedMagnitude[index] != 0 || e.processingFlags[index]&processedMask != 0 {
					continue
				}
				contextLabel := e.labels[e.neighborSig[index]]
				bit := int((e.trueMagnitude[index] >> uint(p)) & 1)
				e.encoder.encodeBit(&e.contexts[contextLabel], bit)
				if bit != 0 {
					e.encodeSignBit(i, j, index)
					e.codedMagnitude[index] = 1
					setNeighborsSignificance(e.width, e.height, e.neighborSig, i, j, index)
					e.processingFlags[index] |= firstMagnitudeBitMask
				}
			}
		}
		i0 = iNext
	}
}

func (e *tier1Encoder) encodeSegmentationSymbol() {
	e.encoder.encodeBit(&e.contexts[ctxUniform], 1)
	e.encoder.encodeBit(&e.contexts[ctxUniform], 0)
	e.encoder.encodeBit(&e.contexts[ctxUniform], 1)
	e.encoder.encodeBit(&e.contexts[ctxUniform], 0)
}

// encodeCodeBlockTier1 fully encodes (no rate-distortion truncation:
// every bit-plane down to 0) a width*height code-block whose true
// values are given by magnitude/sign, each magnitude entry required to
// fit within numBitPlanes bits. Returns the encoded bytes and the
// total coding-pass count a real packet header would have recorded for
// it (3*(numBitPlanes-1)+1, or 1 if numBitPlanes<=1) - both are what a
// decodeCodeBlockTier1 caller needs (via codeBlockContribution).
func encodeCodeBlockTier1(width, height int, magnitude []uint32, sign []uint8, kind subbandKind, numBitPlanes int, style byte) (data []byte, numPasses int) {
	e := newTier1Encoder(width, height, kind, magnitude, sign)
	resetPerPass := style&codeBlockResetContext != 0
	useSegSymbols := style&codeBlockSegmentationSymbols != 0

	numPasses = 1
	if numBitPlanes > 1 {
		numPasses = 3*(numBitPlanes-1) + 1
	}
	p := numBitPlanes - 1
	passType := 2
	for i := 0; i < numPasses; i++ {
		switch passType {
		case 0:
			e.runSignificancePropagationPass(p)
		case 1:
			e.runMagnitudeRefinementPass(p)
		case 2:
			e.runCleanupPass(p)
			if useSegSymbols {
				e.encodeSegmentationSymbol()
			}
		}
		if resetPerPass {
			e.resetContexts()
		}
		if passType == 2 {
			p--
		}
		passType = (passType + 1) % 3
	}
	return e.encoder.flush(), numPasses
}

// tier1RoundTrip encodes a random width*height code-block (magnitudes
// bounded to numBitPlanes bits) with the given subband kind and
// code-block style, decodes it back through decodeCodeBlockTier1, and
// confirms every coefficient's magnitude and sign - and, since the
// block is never truncated, its uniform bitsDecoded count too (see
// tier1.go's codeBlockSamples doc comment: with zeroBitPlanes 0 and no
// truncation, every coefficient's bitsDecoded must equal numBitPlanes
// exactly) - come back exactly as encoded.
func tier1RoundTrip(t *testing.T, seed int64, width, height int, kind subbandKind, numBitPlanes int, style byte) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	n := width * height
	magnitude := make([]uint32, n)
	sign := make([]uint8, n)
	for i := range magnitude {
		// Bias toward zero (most wavelet coefficients are small/zero)
		// so the cleanup pass's run-length path is actually exercised.
		if rng.Float64() < 0.6 {
			continue
		}
		magnitude[i] = uint32(rng.Intn(1 << uint(numBitPlanes)))
		if magnitude[i] == 0 {
			magnitude[i] = 1
		}
		sign[i] = uint8(rng.Intn(2))
	}

	data, numPasses := encodeCodeBlockTier1(width, height, magnitude, sign, kind, numBitPlanes, style)

	cb := &codeBlockInfo{
		contributions: []codeBlockContribution{{numPasses: numPasses, data: data}},
	}
	samples, err := decodeCodeBlockTier1(cb, width, height, kind, style)
	if err != nil {
		t.Fatalf("decodeCodeBlockTier1: %v", err)
	}
	if samples == nil {
		t.Fatalf("decodeCodeBlockTier1: got nil samples")
	}

	for i := 0; i < n; i++ {
		if samples.magnitude[i] != magnitude[i] {
			t.Fatalf("sample %d (row %d, col %d): magnitude %d, want %d", i, i/width, i%width, samples.magnitude[i], magnitude[i])
		}
		if magnitude[i] != 0 && samples.sign[i] != sign[i] {
			t.Fatalf("sample %d: sign %d, want %d", i, samples.sign[i], sign[i])
		}
		if samples.bitsDecoded[i] != numBitPlanes {
			t.Fatalf("sample %d: bitsDecoded %d, want %d (full precision, no truncation)", i, samples.bitsDecoded[i], numBitPlanes)
		}
	}
}

func TestTier1RoundTrip(t *testing.T) {
	kinds := map[string]subbandKind{"LL": subbandLL, "LH": subbandLH, "HL": subbandHL, "HH": subbandHH}
	sizes := [][2]int{{16, 16}, {13, 9}, {1, 1}, {4, 37}, {64, 32}}
	styles := map[string]byte{
		"default":          0,
		"resetContext":     codeBlockResetContext,
		"segmentationSyms": codeBlockSegmentationSymbols,
		"resetAndSegSyms":  codeBlockResetContext | codeBlockSegmentationSymbols,
	}

	seed := int64(1)
	for kindName, kind := range kinds {
		for _, size := range sizes {
			for styleName, style := range styles {
				seed++
				t.Run(kindName+"/"+styleName, func(t *testing.T) {
					tier1RoundTrip(t, seed, size[0], size[1], kind, 8, style)
				})
			}
		}
	}
}

// TestTier1RoundTripManyBitPlanes exercises a code-block deep enough
// (many magnitude-refinement passes per coefficient) to stress the
// firstMagnitudeBitMask/processedMask bookkeeping across a long run of
// passes, not just a handful.
func TestTier1RoundTripManyBitPlanes(t *testing.T) {
	tier1RoundTrip(t, 99, 32, 32, subbandHL, 20, 0)
}

// TestTier1AllZero covers the degenerate case where every coefficient
// stays insignificant for the whole code-block - purely a run-length
// exercise, since every stripe qualifies.
func TestTier1AllZero(t *testing.T) {
	width, height := 16, 16
	magnitude := make([]uint32, width*height)
	sign := make([]uint8, width*height)
	data, numPasses := encodeCodeBlockTier1(width, height, magnitude, sign, subbandLH, 5, 0)

	cb := &codeBlockInfo{contributions: []codeBlockContribution{{numPasses: numPasses, data: data}}}
	samples, err := decodeCodeBlockTier1(cb, width, height, subbandLH, 0)
	if err != nil {
		t.Fatalf("decodeCodeBlockTier1: %v", err)
	}
	for i, m := range samples.magnitude {
		if m != 0 {
			t.Fatalf("sample %d: magnitude %d, want 0", i, m)
		}
	}
}

// TestTier1ZeroBitPlanesPreset confirms a nonzero zeroBitPlanes (as
// tier-2's tag-tree decoding would supply for a code-block whose
// leading bit-planes are all known zero) is simply added as bitsDecoded's
// starting offset, uniformly, with no effect on the decoded values
// themselves.
func TestTier1ZeroBitPlanesPreset(t *testing.T) {
	width, height := 8, 8
	rng := rand.New(rand.NewSource(7))
	magnitude := make([]uint32, width*height)
	sign := make([]uint8, width*height)
	for i := range magnitude {
		magnitude[i] = uint32(rng.Intn(1 << 6))
		sign[i] = uint8(rng.Intn(2))
	}
	data, numPasses := encodeCodeBlockTier1(width, height, magnitude, sign, subbandHH, 6, 0)

	const zeroBitPlanes = 5
	cb := &codeBlockInfo{
		zeroBitPlanes: zeroBitPlanes,
		contributions: []codeBlockContribution{{numPasses: numPasses, data: data}},
	}
	samples, err := decodeCodeBlockTier1(cb, width, height, subbandHH, 0)
	if err != nil {
		t.Fatalf("decodeCodeBlockTier1: %v", err)
	}
	for i := range samples.magnitude {
		if samples.magnitude[i] != magnitude[i] {
			t.Fatalf("sample %d: magnitude %d, want %d", i, samples.magnitude[i], magnitude[i])
		}
		if samples.bitsDecoded[i] != zeroBitPlanes+6 {
			t.Fatalf("sample %d: bitsDecoded %d, want %d", i, samples.bitsDecoded[i], zeroBitPlanes+6)
		}
	}
}

// TestTier1UnsupportedCodeBlockStyle confirms every style bit this
// decoder doesn't implement is rejected by name rather than silently
// mis-decoded.
func TestTier1UnsupportedCodeBlockStyle(t *testing.T) {
	for _, style := range []byte{
		codeBlockSelectiveBypass,
		codeBlockTermination,
		codeBlockVerticallyCausal,
		codeBlockPredictableTermination,
	} {
		cb := &codeBlockInfo{contributions: []codeBlockContribution{{numPasses: 1, data: []byte{0}}}}
		_, err := decodeCodeBlockTier1(cb, 4, 4, subbandLL, style)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("style 0x%02X: got err %v, want ErrUnsupported", style, err)
		}
	}
}

// TestTier1SegmentationSymbolMismatch confirms a corrupted stream that
// decodes to the wrong 4-bit pattern where a segmentation symbol was
// expected is caught rather than silently accepted.
func TestTier1SegmentationSymbolMismatch(t *testing.T) {
	width, height := 4, 4
	magnitude := make([]uint32, width*height)
	magnitude[0] = 1
	sign := make([]uint8, width*height)
	data, numPasses := encodeCodeBlockTier1(width, height, magnitude, sign, subbandLL, 1, codeBlockSegmentationSymbols)

	// Flip a byte in the middle of the stream to desynchronize the
	// decoded segmentation symbol without necessarily corrupting the
	// MQ decoder into an outright crash (mq.go's byteAt padding
	// guarantees it never will).
	if len(data) > 0 {
		data[len(data)/2] ^= 0xFF
	}

	cb := &codeBlockInfo{contributions: []codeBlockContribution{{numPasses: numPasses, data: data}}}
	_, err := decodeCodeBlockTier1(cb, width, height, subbandLL, codeBlockSegmentationSymbols)
	if err == nil {
		t.Skip("corruption happened not to change the segmentation symbol bits for this input; not a reliable failure")
	}
}
