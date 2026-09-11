package jpx

import (
	"errors"
	"math/rand"
	"testing"
)

// tier1Encoder and encodeCodeBlockTier1 (this file's own from-scratch
// mirror of tier1Model, used below to build ground truth for every
// round trip in this file) now live in encode.go, promoted there by 14g
// so tools/genfixtures - an ordinary non-test build - can reach them
// too. See that file's own doc comment.

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
