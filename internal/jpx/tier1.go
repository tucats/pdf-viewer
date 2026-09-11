// This file implements tier-1 coding (ISO/IEC 15444-1 Annex D): the
// EBCOT ("Embedded Block Coding with Optimized Truncation") bit-plane
// entropy decoder that turns one code-block's compressed bytes -
// mq.go's MQ coder driven by 14b's decodeTilePackets, which locates
// each code-block's contributions but does not decode them - into a
// two-dimensional array of coefficient magnitudes and signs.
//
// # Scope: what this file's output is, and is not, yet
//
// decodeCodeBlockTier1's result (codeBlockSamples) is deliberately
// *not* the code-block's final dequantized wavelet coefficients: it is
// the magnitude and sign bits as actually decoded, plus - per
// coefficient - how many bit-planes were spent on it (bitsDecoded).
// Turning that into a real coefficient value needs the subband's
// quantization step size and its total nominal bit-plane count (Mb),
// both derived from the QCD/QCC data quant.go parses combined with the
// component's bit depth - that computation, plus the "insert a
// reconstruction bit for any bit-planes truncated by rate-distortion
// encoding" rule (Annex E.1's own inverse-quantization procedure), is
// 14d's job, not this one's. This split keeps tier-1 (a pure entropy
// decoder, working from nothing but a code-block's own bytes and its
// tier-2-supplied zeroBitPlanes count) independent of quantization
// entirely, matching how the standard itself separates Annex D from
// Annex E.
//
// # Provenance
//
// Table D.1/D.2/D.3's zero-coding context assignments (one 9-context
// table per subband orientation - LL and LH share one, HL another, HH
// a third) are among the most transcription-error-prone constants in
// the entire standard: 75 entries apiece, indexed by a packed
// significant-neighbor-count byte, with no self-checking structure
// that would catch a single wrong entry the way (say) a checksum
// would. Given that risk, and unlike this package's usual "write from
// the specification's own prose, cross-check the result against
// pdf.js's old v3.11.174 tag" approach (see doc.go's Provenance
// section), this file's context tables and its four coding-pass
// procedures (significance propagation, sign coding, magnitude
// refinement, cleanup - including the cleanup pass's run-length
// optimization) were checked directly against that same pdf.js
// version's BitModel class (jpx.js, Apache License 2.0) line-by-line
// while writing this file, rather than from memory of the standard's
// prose alone. This is a deliberate exception to this package's
// general provenance policy, justified by the specific, unusually high
// risk of an unverified silent transcription error in a 75-entry
// lookup table with no reference to check against otherwise; the
// result is still this package's own Go code, structured around this
// package's own types (codeBlockInfo, subbandKind), not a mechanical
// port. As with every other cross-check this package performs, no
// runtime dependency on pdf.js or its license terms results.
//
// This file's own round-trip test (tier1_test.go's from-scratch
// encoder, mirroring mq.go's and 14b's own "write both directions from
// opposite ends" verification approach) is what actually establishes
// this package's own confidence in the result, independent of that
// cross-check.
//
// # Code-block style scope
//
// Of the six code-block style bits COD/COC's SPcod/SPcoc field can set
// (coding.go's codeBlock* constants), this decoder supports only the
// default (no bits set), plus the two that are cheap to add and do not
// change the bitstream's fundamental structure: codeBlockResetContext
// (reset every context's probability state after each coding pass) and
// codeBlockSegmentationSymbols (a fixed 4-bit 0xA marker expected after
// every cleanup pass, letting a decoder detect desynchronization).
// codeBlockSelectiveBypass, codeBlockTermination,
// codeBlockVerticallyCausal, and codeBlockPredictableTermination are
// reported as ErrUnsupported by name rather than decoded - notably,
// the same scope cut pdf.js's own BitModel/copyCoefficients makes (it
// implements none of those four either), which is real-world evidence
// that encoders producing PDF-embedded JPX rarely if ever use them.
package jpx

// Context labels (T.800 Table D.7's context assignments): 0-8 are the
// nine zero-coding contexts (looked up via the tables below), 9-13 the
// five sign-coding contexts, 14-16 the three magnitude-refinement
// contexts, and the two fixed contexts below.
const (
	ctxUniform   = 17
	ctxRunLength = 18
	numContexts  = 19
)

const (
	processedMask         = 1
	firstMagnitudeBitMask = 2
)

// maxTier1BitPlanes bounds how many bit-planes (and therefore coding
// passes: up to 3*maxTier1BitPlanes+1) this decoder will process for a
// single code-block - "bounded work against hostile input", the same
// policy siz.go's maxReasonableDimension documents: no real subband
// (even a 32-bit-per-sample one with every guard bit the standard
// allows) needs more than a few dozen bit-planes, so a codestream
// whose packet headers claim far more passes than that is malformed
// rather than merely unusual.
const maxTier1BitPlanes = 48

// zeroCodingContext tables (T.800 Table D.1 for LL/LH, D.2 for HL, D.3
// for HH), indexed by a packed neighbor-significance byte: bits 0-1
// hold the horizontal significant-neighbor count (0-2), bits 2-3 the
// vertical count as a multiple of 4 (0, 4 or 8), and bits 4-6 the
// diagonal count as a multiple of 16 (0, 16, 32, 48 or 64) - see
// setNeighborsSignificance, which builds exactly this encoding.  HL's
// table is not simply LH's with h/v swapped in this transcription (see
// this file's doc comment on provenance); it is its own table.
var llAndLHContextLabels = [75]byte{
	0, 5, 8, 0, 3, 7, 8, 0, 4, 7, 8, 0, 0, 0, 0, 0, 1, 6, 8, 0, 3, 7, 8, 0, 4,
	7, 8, 0, 0, 0, 0, 0, 2, 6, 8, 0, 3, 7, 8, 0, 4, 7, 8, 0, 0, 0, 0, 0, 2, 6,
	8, 0, 3, 7, 8, 0, 4, 7, 8, 0, 0, 0, 0, 0, 2, 6, 8, 0, 3, 7, 8, 0, 4, 7, 8,
}

var hlContextLabels = [75]byte{
	0, 3, 4, 0, 5, 7, 7, 0, 8, 8, 8, 0, 0, 0, 0, 0, 1, 3, 4, 0, 6, 7, 7, 0, 8,
	8, 8, 0, 0, 0, 0, 0, 2, 3, 4, 0, 6, 7, 7, 0, 8, 8, 8, 0, 0, 0, 0, 0, 2, 3,
	4, 0, 6, 7, 7, 0, 8, 8, 8, 0, 0, 0, 0, 0, 2, 3, 4, 0, 6, 7, 7, 0, 8, 8, 8,
}

var hhContextLabels = [75]byte{
	0, 1, 2, 0, 1, 2, 2, 0, 2, 2, 2, 0, 0, 0, 0, 0, 3, 4, 5, 0, 4, 5, 5, 0, 5,
	5, 5, 0, 0, 0, 0, 0, 6, 7, 7, 0, 7, 7, 7, 0, 7, 7, 7, 0, 0, 0, 0, 0, 8, 8,
	8, 0, 8, 8, 8, 0, 8, 8, 8, 0, 0, 0, 0, 0, 8, 8, 8, 0, 8, 8, 8, 0, 8, 8, 8,
}

// contextLabelsFor returns the zero-coding context table for a
// subband of the given orientation - LL and LH share one (§D.3.1
// treats them identically: both have a "smooth" horizontal direction
// and a "detail" vertical one relative to the code-block's own
// samples... in LL's case there being no wavelet detail at all, but
// the same table works since LL's neighbor patterns behave the same
// way statistically).
func contextLabelsFor(kind subbandKind) *[75]byte {
	switch kind {
	case subbandHH:
		return &hhContextLabels
	case subbandHL:
		return &hlContextLabels
	default:
		return &llAndLHContextLabels
	}
}

// codeBlockSamples is tier-1's decoded output for one code-block, one
// entry per sample in raster order (width*height, width = the
// code-block's own tbx1-tbx0) - see this file's doc comment on what
// dequantization (14d) still needs to do with it.
type codeBlockSamples struct {
	width, height int
	// magnitude[i] is the coefficient's decoded magnitude bits,
	// accumulated MSB-first exactly as decoded (0 if the coefficient
	// never became significant).
	magnitude []uint32
	// sign[i] is 1 if the coefficient is negative, 0 otherwise
	// (meaningless where magnitude[i] == 0).
	sign []uint8
	// bitsDecoded[i] is how many bit-planes were spent on this
	// coefficient's position, including any leading zeroBitPlanes this
	// code-block's tag-tree data established were skipped entirely -
	// 14d compares this against the subband's own Mb to know how many
	// (if any) trailing bit-planes this code-block's contributions
	// never included, per Annex E.1's truncation-reconstruction rule.
	bitsDecoded []int
}

// tier1Model holds the neighbor-significance and context state shared
// by every coding pass over one code-block, and the arithmetic coder
// driving it - the decode side. See tier1Encoder (tier1_test.go) for
// its from-scratch, test-only mirror image, built the same "opposite
// ends of the standard's own description" way as this package's other
// round-trip-tested pieces (mq.go, packet_test.go).
type tier1Model struct {
	width, height int
	labels        *[75]byte

	magnitude       []uint32
	sign            []uint8
	neighborSig     []uint8
	processingFlags []uint8
	bitsDecoded     []int

	contexts [numContexts]mqContext
	decoder  *mqDecoder
}

func newTier1Model(width, height int, kind subbandKind, zeroBitPlanes int) *tier1Model {
	n := width * height
	m := &tier1Model{
		width: width, height: height,
		labels:          contextLabelsFor(kind),
		magnitude:       make([]uint32, n),
		sign:            make([]uint8, n),
		neighborSig:     make([]uint8, n),
		processingFlags: make([]uint8, n),
		bitsDecoded:     make([]int, n),
	}
	if zeroBitPlanes != 0 {
		for i := range m.bitsDecoded {
			m.bitsDecoded[i] = zeroBitPlanes
		}
	}
	m.resetContexts()
	return m
}

// resetContexts (re)initializes every context to T.800 Table D.7's
// initial state: context 0 (the zero-coding "no significant neighbor
// at all" context) starts at probability-estimation state 4 rather
// than 0, UNIFORM_CONTEXT at state 46 (the Qe table's fixed, non-
// adapting 0.5-probability entry - see mq.go's qeTable), RUNLENGTH at
// state 3, and every other context (the rest of the zero-coding
// contexts, all five sign contexts, all three magnitude-refinement
// contexts) at state 0 with an initial MPS of 0 - the zero value of
// mqContext already matches that last case. Called once per code-block
// and, if codeBlockResetContext is set, once more after every pass.
func (m *tier1Model) resetContexts() {
	for i := range m.contexts {
		m.contexts[i] = mqContext{}
	}
	m.contexts[0] = mqContext{index: 4}
	m.contexts[ctxUniform] = mqContext{index: 46}
	m.contexts[ctxRunLength] = mqContext{index: 3}
}

// setNeighborsSignificance records, in every one of index's up-to-8
// neighbors' own neighborSig byte, that index has just become
// significant - horizontal neighbors contribute 1, vertical neighbors
// 4, diagonal neighbors 16 (matching contextLabelsFor's packed-byte
// encoding), and index's own cell is marked 0x80 purely so magnitude-
// refinement's "does this coefficient have any significant neighbor"
// check (which masks that bit off) never confuses "I am significant"
// with "my neighbor is". A free function for the same sharing reason
// signContribution above is one.
func setNeighborsSignificance(width, height int, neighborSig []uint8, row, column, index int) {
	left := column > 0
	right := column+1 < width

	if row > 0 {
		i := index - width
		if left {
			neighborSig[i-1] += 0x10
		}
		if right {
			neighborSig[i+1] += 0x10
		}
		neighborSig[i] += 0x04
	}
	if row+1 < height {
		i := index + width
		if left {
			neighborSig[i-1] += 0x10
		}
		if right {
			neighborSig[i+1] += 0x10
		}
		neighborSig[i] += 0x04
	}
	if left {
		neighborSig[index-1] += 0x01
	}
	if right {
		neighborSig[index+1] += 0x01
	}
	neighborSig[index] |= 0x80
}

// signContribution computes §D.3.2's horizontal-then-vertical sign
// prediction contribution for the coefficient at (row, column, index):
// each already-significant horizontal (then vertical) neighbor
// contributes +1 if positive, -1 if negative, and the two directions
// combine as 3*horizontal+vertical before being mapped to one of the
// five sign contexts (9-13) by decodeSignBit/encodeSignBit's shared
// "9+contribution, or 9-contribution with the decoded bit inverted"
// rule - the standard's way of using one physical context for a
// prediction and its mirror image. A free function (rather than a
// tier1Model method) so tier1Encoder (tier1_test.go) - which tracks
// its own, separate "revealed so far" magnitude/sign state - can share
// it verbatim instead of risking the two definitions drifting apart.
func signContribution(width, height int, mag []uint32, sgn []uint8, row, column, index int) int {
	var contribution int
	sig1 := column > 0 && mag[index-1] != 0
	switch {
	case column+1 < width && mag[index+1] != 0:
		sign1 := int(sgn[index+1])
		if sig1 {
			sign0 := int(sgn[index-1])
			contribution = 1 - sign1 - sign0
		} else {
			contribution = 1 - sign1 - sign1
		}
	case sig1:
		sign0 := int(sgn[index-1])
		contribution = 1 - sign0 - sign0
	default:
		contribution = 0
	}
	horizontal := 3 * contribution

	sig1 = row > 0 && mag[index-width] != 0
	switch {
	case row+1 < height && mag[index+width] != 0:
		sign1 := int(sgn[index+width])
		if sig1 {
			sign0 := int(sgn[index-width])
			contribution = 1 - sign1 - sign0 + horizontal
		} else {
			contribution = 1 - sign1 - sign1 + horizontal
		}
	case sig1:
		sign0 := int(sgn[index-width])
		contribution = 1 - sign0 - sign0 + horizontal
	default:
		contribution = horizontal
	}
	return contribution
}

// decodeSignBit decodes one coefficient's sign, immediately after its
// significance bit decoded to 1.
func (m *tier1Model) decodeSignBit(row, column, index int) int {
	contribution := signContribution(m.width, m.height, m.magnitude, m.sign, row, column, index)
	if contribution >= 0 {
		return m.decoder.decodeBit(&m.contexts[9+contribution])
	}
	return m.decoder.decodeBit(&m.contexts[9-contribution]) ^ 1
}

// runSignificancePropagationPass is §D.3.1's SPP: for every not-yet-
// significant coefficient with at least one significant neighbor
// already (neighborSig != 0 - a coefficient with no significant
// neighbor at all is left for the cleanup pass instead, which is what
// makes the cleanup pass's own run-length optimization possible),
// decode whether it becomes significant this bit-plane, and if so its
// sign immediately.
func (m *tier1Model) runSignificancePropagationPass() {
	width, height := m.width, m.height
	for i0 := 0; i0 < height; i0 += 4 {
		for j := 0; j < width; j++ {
			index := i0*width + j
			for i1 := 0; i1 < 4; i1, index = i1+1, index+width {
				i := i0 + i1
				if i >= height {
					break
				}
				m.processingFlags[index] &^= processedMask
				if m.magnitude[index] != 0 || m.neighborSig[index] == 0 {
					continue
				}
				contextLabel := m.labels[m.neighborSig[index]]
				if m.decoder.decodeBit(&m.contexts[contextLabel]) != 0 {
					sign := m.decodeSignBit(i, j, index)
					m.sign[index] = uint8(sign)
					m.magnitude[index] = 1
					setNeighborsSignificance(m.width, m.height, m.neighborSig, i, j, index)
					m.processingFlags[index] |= firstMagnitudeBitMask
				}
				m.bitsDecoded[index]++
				m.processingFlags[index] |= processedMask
			}
		}
	}
}

// runMagnitudeRefinementPass is §D.3.3's MRP: every coefficient that
// was already significant before this bit-plane (not one SPP just made
// significant, tracked via processedMask - SPP already set that flag
// for every coefficient it touched this bit-plane, significant or not)
// gets one more magnitude bit appended. The context is 14 or 15 for a
// coefficient's very first refinement bit (whether it currently has
// any significant neighbor or not - firstMagnitudeBitMask, cleared
// once used), 16 for every refinement after that.
func (m *tier1Model) runMagnitudeRefinementPass() {
	width, height := m.width, m.height
	length := width * height
	width4 := width * 4
	for index0 := 0; index0 < length; index0 += width4 {
		indexNext := index0 + width4
		if indexNext > length {
			indexNext = length
		}
		for j := 0; j < width; j++ {
			for index := index0 + j; index < indexNext; index += width {
				if m.magnitude[index] == 0 || m.processingFlags[index]&processedMask != 0 {
					continue
				}
				contextLabel := 16
				if m.processingFlags[index]&firstMagnitudeBitMask != 0 {
					m.processingFlags[index] ^= firstMagnitudeBitMask
					if m.neighborSig[index]&0x7F == 0 {
						contextLabel = 15
					} else {
						contextLabel = 14
					}
				}
				bit := m.decoder.decodeBit(&m.contexts[contextLabel])
				m.magnitude[index] = (m.magnitude[index] << 1) | uint32(bit)
				m.bitsDecoded[index]++
				m.processingFlags[index] |= processedMask
			}
		}
	}
}

// runCleanupPass is §D.3.4's cleanup pass: every coefficient neither
// already significant nor already visited by this bit-plane's SPP gets
// its significance decided here. Coefficients are processed in
// 4-row-tall column stripes; whenever an entire stripe (4 full rows -
// checkAllEmpty is false for a short trailing stripe) has no processed
// coefficient and no significant neighbor anywhere in it, §D.3.4's
// run-length optimization applies: a single RUNLENGTH-context bit says
// whether *any* of the 4 are about to become significant, and if so
// two UNIFORM-context bits (a raw, non-adapting 2-bit index - not a
// zero-coding decision) say which row is the first one.
func (m *tier1Model) runCleanupPass() {
	width, height := m.width, m.height
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
				m.processingFlags[index0] == 0 &&
				m.processingFlags[index0+oneRowDown] == 0 &&
				m.processingFlags[index0+twoRowsDown] == 0 &&
				m.processingFlags[index0+threeRowsDown] == 0 &&
				m.neighborSig[index0] == 0 &&
				m.neighborSig[index0+oneRowDown] == 0 &&
				m.neighborSig[index0+twoRowsDown] == 0 &&
				m.neighborSig[index0+threeRowsDown] == 0

			i1 := 0
			index := index0
			i := i0
			if allEmpty {
				if m.decoder.decodeBit(&m.contexts[ctxRunLength]) == 0 {
					m.bitsDecoded[index0]++
					m.bitsDecoded[index0+oneRowDown]++
					m.bitsDecoded[index0+twoRowsDown]++
					m.bitsDecoded[index0+threeRowsDown]++
					continue columns
				}
				b1 := m.decoder.decodeBit(&m.contexts[ctxUniform])
				b0 := m.decoder.decodeBit(&m.contexts[ctxUniform])
				i1 = (b1 << 1) | b0
				if i1 != 0 {
					i = i0 + i1
					index += i1 * width
				}
				sign := m.decodeSignBit(i, j, index)
				m.sign[index] = uint8(sign)
				m.magnitude[index] = 1
				setNeighborsSignificance(m.width, m.height, m.neighborSig, i, j, index)
				m.processingFlags[index] |= firstMagnitudeBitMask

				for idx, i2 := index0, i0; i2 <= i; idx, i2 = idx+width, i2+1 {
					m.bitsDecoded[idx]++
				}
				i1++
			}
			for i, index = i0+i1, index0+i1*width; i < iNext; i, index = i+1, index+width {
				if m.magnitude[index] != 0 || m.processingFlags[index]&processedMask != 0 {
					continue
				}
				contextLabel := m.labels[m.neighborSig[index]]
				if m.decoder.decodeBit(&m.contexts[contextLabel]) == 1 {
					sign := m.decodeSignBit(i, j, index)
					m.sign[index] = uint8(sign)
					m.magnitude[index] = 1
					setNeighborsSignificance(m.width, m.height, m.neighborSig, i, j, index)
					m.processingFlags[index] |= firstMagnitudeBitMask
				}
				m.bitsDecoded[index]++
			}
		}
		i0 = iNext
	}
}

// checkSegmentationSymbol decodes the 4 UNIFORM-context bits
// codeBlockSegmentationSymbols requires after every cleanup pass and
// confirms they read the fixed pattern 0xA (1010) the standard defines
// - a decoder's cheapest available check that the bitstream and its
// own pass bookkeeping haven't desynchronized.
func (m *tier1Model) checkSegmentationSymbol() error {
	b3 := m.decoder.decodeBit(&m.contexts[ctxUniform])
	b2 := m.decoder.decodeBit(&m.contexts[ctxUniform])
	b1 := m.decoder.decodeBit(&m.contexts[ctxUniform])
	b0 := m.decoder.decodeBit(&m.contexts[ctxUniform])
	symbol := b3<<3 | b2<<2 | b1<<1 | b0
	if symbol != 0xA {
		return malformedf("code-block: segmentation symbol 0x%X after a cleanup pass, want 0xA", symbol)
	}
	return nil
}

// unsupportedCodeBlockStyle is every codeBlockStyle bit this decoder
// does not implement - see this file's doc comment.
const unsupportedCodeBlockStyle = codeBlockSelectiveBypass | codeBlockTermination | codeBlockVerticallyCausal | codeBlockPredictableTermination

// decodeCodeBlockTier1 runs cb's compressed contributions (already
// located by 14b's decodeTilePackets) through the coding-pass sequence
// - cleanup, then (significance propagation, magnitude refinement,
// cleanup) repeating once per remaining bit-plane - returning nil if
// cb was never included in any layer (nothing to decode: every sample
// stays zero).
func decodeCodeBlockTier1(cb *codeBlockInfo, width, height int, kind subbandKind, style byte) (*codeBlockSamples, error) {
	if len(cb.contributions) == 0 {
		return nil, nil
	}
	if style&unsupportedCodeBlockStyle != 0 {
		return nil, unsupportedf("code-block style 0x%02X uses a coding mode this decoder does not implement (selective bypass, per-pass MQ termination, vertically-causal context formation, or predictable termination)", style)
	}

	totalLength, totalPasses := 0, 0
	for _, c := range cb.contributions {
		totalLength += len(c.data)
		totalPasses += c.numPasses
	}
	if totalPasses > 3*maxTier1BitPlanes+1 {
		return nil, malformedf("code-block declares %d coding passes, more than %d bit-planes could ever need", totalPasses, maxTier1BitPlanes)
	}

	// §B.10's packets store a code-block's layers' contributions in
	// increasing layer order for any progression order (layer is
	// always either the outermost or the innermost varied dimension -
	// see packet.go's decodeTilePackets doc comment), and
	// decodeTilePackets appends contributions in the order packets are
	// encountered - so simply concatenating in slice order reproduces
	// the code-block's single continuous MQ-coded bitstream.
	data := make([]byte, 0, totalLength)
	for _, c := range cb.contributions {
		data = append(data, c.data...)
	}

	m := newTier1Model(width, height, kind, cb.zeroBitPlanes)
	m.decoder = newMQDecoder(data)

	resetPerPass := style&codeBlockResetContext != 0
	useSegSymbols := style&codeBlockSegmentationSymbols != 0
	passType := 2 // the very first pass is always a cleanup pass (§D.3's own requirement: nothing can be significant yet).
	for i := 0; i < totalPasses; i++ {
		switch passType {
		case 0:
			m.runSignificancePropagationPass()
		case 1:
			m.runMagnitudeRefinementPass()
		case 2:
			m.runCleanupPass()
			if useSegSymbols {
				if err := m.checkSegmentationSymbol(); err != nil {
					return nil, err
				}
			}
		}
		if resetPerPass {
			m.resetContexts()
		}
		passType = (passType + 1) % 3
	}

	return &codeBlockSamples{width: width, height: height, magnitude: m.magnitude, sign: m.sign, bitsDecoded: m.bitsDecoded}, nil
}

// decodeTileCoefficients runs tier-1 decoding (this file) over every
// code-block decodeTilePackets (packet.go, 14b) located across
// components, storing each code-block's result on its own
// codeBlockInfo.samples. A code-block whose subband bounds are empty
// (possible at a tile's own edge - geometry.go's buildCodeBlocks
// already skips creating such code-blocks, but a defensive check costs
// nothing) is left nil, same as one never included in any layer.
func decodeTileCoefficients(components []*componentDecode) error {
	for _, comp := range components {
		style := comp.coding.CodeBlockStyle
		for _, res := range comp.resolutions {
			for _, sb := range res.subbands {
				for _, cb := range sb.codeBlocks {
					width := cb.tbx1 - cb.tbx0
					height := cb.tby1 - cb.tby0
					if width <= 0 || height <= 0 {
						continue
					}
					samples, err := decodeCodeBlockTier1(cb, width, height, sb.kind, style)
					if err != nil {
						return err
					}
					cb.samples = samples
				}
			}
		}
	}
	return nil
}

// decodeTileTier1 ties 14b's tier-2 packet parsing to this file's
// tier-1 entropy decoding for one tile: decodeTilePackets locates every
// code-block's compressed-data contributions, and
// decodeTileCoefficients decodes them. Not yet reachable from
// internal/filter - 14d (dequantization and the inverse wavelet
// transform) is what will turn this sub-phase's per-code-block
// magnitude/sign/bitsDecoded output into actual image samples.
func decodeTileTier1(h *Header, codestream []byte, tileIndex int) ([]*componentDecode, error) {
	components, err := decodeTilePackets(h, codestream, tileIndex)
	if err != nil {
		return nil, err
	}
	if err := decodeTileCoefficients(components); err != nil {
		return nil, err
	}
	return components, nil
}
