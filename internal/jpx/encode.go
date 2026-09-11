// This file implements 14g's from-scratch JPEG 2000 *encoder* - the
// production counterpart to every earlier sub-phase's own test-only
// verification encoder (mq.go's mqEncoder is the one already production,
// since round-trip testing needed it from 14b on). Unlike
// idwt_test.go's forward53/forward97 or mct_test.go's forwardICT (each
// built purely to synthesize ground truth for one sub-phase's own unit
// tests and never assembled into a real codestream), this file's job is
// to actually produce complete, valid, decodable JPEG 2000 codestreams -
// what tools/genfixtures uses to build this project's own JPXDecode PDF
// fixtures (doc.go's Provenance section explains why: no
// independently-produced real-world JPX sample is available, so this
// package validates itself end to end the same way it always has,
// writing both directions from the standard's own description rather
// than deriving one mechanically from the other).
//
// Several of the primitives below (bitWriter, the tag-tree encode-side
// helpers, encodeCodingPasses/encodeLength, tier1Encoder and
// encodeCodeBlockTier1, and forwardRCT) started life as test-only code
// in packet_test.go, tier1_test.go and mct_test.go, written to validate
// those sub-phases' own decoders (see each's own doc comment, still
// intact where it lives now). They are moved here, unchanged, now that
// 14g needs the same logic reachable from a non-test build
// (tools/genfixtures is an ordinary `go run` command, which never
// compiles _test.go files) - the test files that used to define them
// still use them, just from here.
//
// # Scope
//
// Unlike the decoder (which the earlier sub-phases built to the general
// case: any decomposition level count, any progression order, multiple
// tiles), this encoder deliberately covers a single, simple
// configuration - a real but minimal slice of what the decoder accepts,
// not a mirror of its full generality:
//
//   - a single tile, spanning the whole image (no multi-tile output;
//     the decoder's own multi-tile support is already exercised by its
//     own hand-built test codestreams - markers_test.go's, box_test.go's
//   - not by this encoder).
//   - zero decomposition levels (DecompositionLevels 0 - every sample is
//     coded directly as one subband's worth of code-blocks, with no
//     wavelet transform at all). idwt.go's multi-level inverse transform
//     already has thorough dedicated unit-test coverage (idwt_test.go)
//     built directly against hand-constructed coefficient arrays, so
//     this encoder does not also need to exercise it through a real
//     codestream to validate it; what this encoder's own fixtures need
//     to prove is the *rest* of the pipeline (tier-1 entropy coding,
//     tier-2 packet framing, the multiple component transform, and - via
//     tools/genfixtures - internal/filter's whole JPXDecode wiring),
//     each of which zero decomposition levels still exercises in full.
//   - the 5/3 reversible wavelet filter only (Transform5x3) - trivial
//     with zero decomposition levels, since no filtering happens at all,
//     but declaring it keeps the codestream's own COD marker internally
//     consistent - and the reversible colour transform (RCT), never the
//     irreversible ones (Transform9x7/ICT), so every encoded value stays
//     an exact integer and a round trip through tier-1's real MQ coder
//     can be checked for bit-exact equality rather than only approximate
//     closeness.
//   - a single quality layer, LRCP progression, the default (whole-tile)
//     precinct size, and the default code-block style (no context reset,
//     no segmentation symbols) - none of tier-2's/tier-1's other options
//     need a *second* encoder scenario to be exercised, since
//     packet_roundtrip_test.go's own from-scratch encoder
//     (encodeSyntheticTile) already checks every progression order and a
//     multi-code-block, multi-layer scenario directly against
//     decodeTilePackets, independent of this file.
//
// A production encoder is free to compose the pieces built here (tier-1
// coding, tier-2 packet framing, marker assembly) into a much richer
// range of codestreams; scoping this one down to what fixture generation
// actually needs follows the same "don't build speculative generality"
// principle this project applies elsewhere.
package jpx

import "encoding/binary"

// u16/u32 append a big-endian value's bytes to buf, returning the
// extended slice - the same convention append itself uses, so these
// compose naturally in a builder chain.
func u16(buf []byte, v uint16) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return append(buf, b[:]...)
}

func u32(buf []byte, v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return append(buf, b[:]...)
}

// segment builds one marker segment: the two-byte marker code, a
// two-byte length field covering itself plus content (per the standard's
// own convention - see readMarkerSegment), and content itself.
func segment(code uint16, content []byte) []byte {
	out := u16(nil, code)
	out = u16(out, uint16(len(content)+2))
	return append(out, content...)
}

// bareMarker builds one of the few markers with no length field or
// content at all (SOC, SOD, EOC).
func bareMarker(code uint16) []byte {
	return u16(nil, code)
}

// codeSIZ builds a SIZ marker segment's content for one or more
// components, all sharing bitDepth/signed.
func codeSIZ(xsiz, ysiz, xtsiz, ytsiz uint32, numComponents int, bitDepth int, signed bool) []byte {
	c := u16(nil, 0) // Rsiz
	c = u32(c, xsiz)
	c = u32(c, ysiz)
	c = u32(c, 0) // XOsiz
	c = u32(c, 0) // YOsiz
	c = u32(c, xtsiz)
	c = u32(c, ytsiz)
	c = u32(c, 0) // XTOsiz
	c = u32(c, 0) // YTOsiz
	c = u16(c, uint16(numComponents))
	ssiz := byte(bitDepth - 1)
	if signed {
		ssiz |= 0x80
	}
	for i := 0; i < numComponents; i++ {
		c = append(c, ssiz, 1, 1) // Ssiz, XRsiz=1, YRsiz=1
	}
	return c
}

// codeSOT builds an SOT marker segment's content with a placeholder
// Psot (patched in later by buildTilePart, once the tile-part's total
// length is known).
func codeSOT(tileIndex uint16, partIndex, partCount byte) []byte {
	c := u16(nil, tileIndex)
	c = u32(c, 0) // Psot placeholder
	c = append(c, partIndex, partCount)
	return c
}

// buildTilePart assembles one complete tile-part - SOT segment, SOD
// marker, and data - patching Psot to the tile-part's own total length
// once known, exactly as a real encoder must.
func buildTilePart(tileIndex uint16, partIndex, partCount byte, data []byte) []byte {
	sot := segment(markerSOT, codeSOT(tileIndex, partIndex, partCount))
	tilePart := append(append([]byte{}, sot...), bareMarker(markerSOD)...)
	tilePart = append(tilePart, data...)

	// Psot occupies bytes [6:10) of the tile-part: 2 (marker) + 2
	// (length) + 2 (Isot) = offset 6 within the SOT segment, which starts
	// at the tile-part's own byte 0.
	const psotOffset = 6
	binary.BigEndian.PutUint32(tilePart[psotOffset:psotOffset+4], uint32(len(tilePart)))
	return tilePart
}

// bitWriter is the encode-side mirror of packetBitReader (bitreader.go):
// same MSB-first, 0xFF-stuffed packing convention, in reverse.
type bitWriter struct {
	out       []byte
	buf       uint32
	bufBits   int
	stuffNext bool // true if the most recently emitted byte was 0xFF
}

func (w *bitWriter) writeBit(bit int) {
	w.buf = (w.buf << 1) | uint32(bit&1)
	w.bufBits++
	limit := 8
	if w.stuffNext {
		limit = 7
	}
	if w.bufBits == limit {
		b := byte(w.buf & ((1 << uint(limit)) - 1))
		w.out = append(w.out, b)
		w.stuffNext = b == 0xFF
		w.buf, w.bufBits = 0, 0
	}
}

func (w *bitWriter) writeBits(v, count int) {
	for i := count - 1; i >= 0; i-- {
		w.writeBit((v >> uint(i)) & 1)
	}
}

// align pads any partial byte with zero bits and unconditionally clears
// stuffNext - exactly matching packetBitReader.alignToByte, which always
// leaves its own stuffNext false once done (see that method's doc
// comment): a byte-stuffing lookback only ever governs how the *next
// bit-packed byte* is read, and both sides agree that lookback resets
// once a packet header's bits are exhausted, regardless of whether the
// final (possibly zero-padded) byte happened to equal 0xFF. Raw bytes
// written after this point (SOP/EPH markers, code-block body data - see
// writeRawBytes) are therefore never subject to bit-stuffing either, the
// same asymmetry pdf.js's own parseTilePackets has (skipNextBit is only
// ever set by readBits, never by skipBytes/skipMarkerIfEqual).
func (w *bitWriter) align() {
	if w.bufBits > 0 {
		limit := 8
		if w.stuffNext {
			limit = 7
		}
		b := byte((w.buf << uint(limit-w.bufBits)) & ((1 << uint(limit)) - 1))
		w.out = append(w.out, b)
	}
	w.stuffNext = false
	w.buf, w.bufBits = 0, 0
}

// writeRawBytes appends bs directly (w must already be byte-aligned -
// true at every call site below, right after align()) - used for SOP/EPH
// marker bytes and code-block body data, none of which are bit-packed or
// subject to bit-stuffing - see align's doc comment.
func (w *bitWriter) writeRawBytes(bs ...byte) {
	w.out = append(w.out, bs...)
}

func (w *bitWriter) writeSOP(sequenceNumber uint16) {
	w.writeRawBytes(0xFF, 0x91, 0x00, 0x04, byte(sequenceNumber>>8), byte(sequenceNumber))
}

func (w *bitWriter) writeEPH() {
	w.writeRawBytes(0xFF, 0x92)
}

// buildPyramid computes the standard tag-tree min-reduction pyramid
// (level 0 = leaves themselves; each level above halves width/height,
// rounding up, taking the min of up to 4 children) over ground-truth
// leaf values - the same level geometry tagTreeInclusion/
// tagTreeZeroBitPlanes build internally (see geometry.go/tagtree.go),
// reproduced here structurally so pyramid[level][index] lines up exactly
// with a tree's own t.levels[level]/lvl.index.
func buildPyramid(width, height int, leaves []int) [][]int {
	var levels [][]int
	cur := append([]int(nil), leaves...)
	w, h := width, height
	for {
		levels = append(levels, cur)
		if w <= 1 && h <= 1 {
			break
		}
		nw, nh := (w+1)/2, (h+1)/2
		next := make([]int, nw*nh)
		for i := range next {
			next[i] = 1 << 30
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				v := cur[y*w+x]
				idx := (y/2)*nw + x/2
				if v < next[idx] {
					next[idx] = v
				}
			}
		}
		cur, w, h = next, nw, nh
	}
	return levels
}

// encodeInclusionLeaf drives t's own reset/incrementValue/nextLevel
// state machine to emit whatever bit sequence a decoder reading it back
// (via the identical calls parseOnePacket makes) would need to conclude
// leaf (i,j) is included by stopValue if and only if pyramid says so -
// returns true if this call determined the leaf newly included (mirrors
// parseOnePacket's firstTimeInclusion).
func encodeInclusionLeaf(t *tagTreeInclusion, i, j, stopValue int, pyramid [][]int, w *bitWriter) bool {
	if !t.reset(i, j, stopValue) {
		return false
	}
	for {
		lvl := &t.levels[t.currentLevel]
		want := pyramid[t.currentLevel][lvl.index]
		if want <= stopValue {
			w.writeBit(1)
			if !t.nextLevel() {
				return true
			}
		} else {
			w.writeBit(0)
			t.incrementValue(stopValue)
			return false
		}
	}
}

// encodeZeroBitPlanesLeaf is encodeInclusionLeaf's counterpart for the
// (threshold-free, decode-once) zero-bit-plane tree.
func encodeZeroBitPlanesLeaf(t *tagTreeZeroBitPlanes, i, j int, pyramid [][]int, w *bitWriter) {
	t.reset(i, j)
	for {
		lvl := &t.levels[t.currentLevel]
		want := pyramid[t.currentLevel][lvl.index]
		cur := t.value[t.currentLevel][lvl.index]
		for cur < want {
			w.writeBit(0)
			t.incrementValue()
			cur++
		}
		w.writeBit(1)
		if !t.nextLevel() {
			return
		}
	}
}

// encodeCodingPasses is readCodingPasses' (packet.go) exact inverse.
func encodeCodingPasses(w *bitWriter, n int) {
	switch {
	case n == 1:
		w.writeBit(0)
	case n == 2:
		w.writeBit(1)
		w.writeBit(0)
	case n >= 3 && n <= 5:
		w.writeBit(1)
		w.writeBit(1)
		w.writeBits(n-3, 2)
	case n >= 6 && n <= 36:
		w.writeBit(1)
		w.writeBit(1)
		w.writeBits(3, 2)
		w.writeBits(n-6, 5)
	default:
		w.writeBit(1)
		w.writeBit(1)
		w.writeBits(3, 2)
		w.writeBits(31, 5)
		w.writeBits(n-37, 7)
	}
}

// encodeLength is the exact inverse of packet.go's Lblock/length
// reading: grows cb.Lblock (via 1-bits, terminated by a 0-bit) only as
// far as needed to fit length, then writes length itself.
func encodeLength(w *bitWriter, cb *codeBlockInfo, numPasses, length int) {
	passLog2 := log2Ceil(numPasses)
	needed := passLog2
	if numPasses < 1<<uint(passLog2) {
		needed--
	}
	for length >= 1<<uint(needed+cb.Lblock) {
		w.writeBit(1)
		cb.Lblock++
	}
	w.writeBit(0)
	w.writeBits(length, needed+cb.Lblock)
}

// codeBlockTruth is one code-block's ground truth across every layer.
// packet_test.go's encodeSyntheticTile keys it by (cbx,cby) (its own
// tests only ever build a single component, where that pair is already
// unique); encodeTileData below - which must support several
// components, each with its own code-block at every (cbx,cby)
// coordinate - keys it by *codeBlockInfo pointer identity instead, which
// is always unique regardless of component count.
type codeBlockTruth struct {
	// includedAtLayer[l] is true if this code-block contributes to
	// layer l.
	includedAtLayer []bool
	numPasses       []int
	data            [][]byte
	zeroBitPlanes   int
}

// firstInclusionLayer returns the earliest layer index ct says this
// code-block is included at, or a large sentinel if it is never
// included.
func firstInclusionLayer(ct *codeBlockTruth) int {
	for l, inc := range ct.includedAtLayer {
		if inc {
			return l
		}
	}
	return 1 << 20
}

// tier1Encoder is a from-scratch mirror of tier1Model: it walks the
// exact same coding-pass control flow (significance propagation,
// magnitude refinement, cleanup - including cleanup's run-length
// optimization), but instead of decoding each bit from an
// arithmetic-coded stream, it reads the bit to encode out of a
// caller-supplied ground-truth coefficient array and encodes it. Its own
// "revealed so far" magnitude/sign state (codedMagnitude/codedSign) is
// deliberately separate from the ground truth (trueMagnitude/
// trueSign): control flow (which coefficients are already significant,
// what their neighbor pattern looks like) must depend only on what has
// actually been encoded so far, exactly mirroring what a decoder can
// know at the same point - this is what makes a round trip through it a
// real test of tier1Model, not a tautology.
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
// every bit-plane down to 0) a width*height code-block whose true values
// are given by magnitude/sign, each magnitude entry required to fit
// within numBitPlanes bits. Returns the encoded bytes and the total
// coding-pass count a real packet header would have recorded for it
// (3*(numBitPlanes-1)+1, or 1 if numBitPlanes<=1) - both are what a
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

// forwardRCT is a from-scratch forward reversible colour transform
// (§G.2's own encoder-direction formulas), written independently from
// inverseRCT's own formulas (not derived by mechanically inverting them)
// for the same "opposite ends of the standard's own description" reason
// doc.go's Provenance section gives.
func forwardRCT(r, g, b []float64) {
	for i := range r {
		ri, gi, bi := int64(r[i]), int64(g[i]), int64(b[i])
		y := (ri + 2*gi + bi) >> 2
		cb := bi - gi
		cr := ri - gi
		r[i], g[i], b[i] = float64(y), float64(cb), float64(cr)
	}
}

// encodeTileBitPlanes is how many bit-planes every code-block this file
// produces is coded to: enough to hold any DC-level-shifted 8-bit
// component (magnitude up to 128) and any RCT-transformed Cb/Cr-like
// difference of two such components (magnitude up to 255) - see
// EncodeRGB's own doc comment for the worked-out bound.
const encodeTileBitPlanes = 8

// encodeCodeBlockPixels is the fixed code-block size every codestream
// this file produces uses (see this file's own Scope section) - 16x16,
// small enough that even this package's own small fixture images divide
// into several code-blocks (exercising tier-2's real multi-code-block
// packet framing, not just a single block covering the whole tile).
const encodeCodeBlockPixels = 16

// encodeCodingStyle is the one CodingStyle every codestream this file
// produces shares, mct controlling only the COD marker's own MCT flag
// (and so whether the decoder that reads this codestream back applies
// the multiple component transform) - see this file's Scope section for
// why every other field is fixed.
func encodeCodingStyle(mct bool) CodingStyle {
	return CodingStyle{
		ProgressionOrder:           ProgressionLRCP,
		NumLayers:                  1,
		DecompositionLevels:        0,
		CodeBlockWidth:             encodeCodeBlockPixels,
		CodeBlockHeight:            encodeCodeBlockPixels,
		Transform:                  Transform5x3,
		MultipleComponentTransform: mct,
		PrecinctWidthExponents:     []int{defaultPrecinctExponent},
		PrecinctHeightExponents:    []int{defaultPrecinctExponent},
	}
}

// encodeCODContent builds a COD marker segment's content for cs - no
// explicit precinct sizes (the default, whole-tile precinct this file
// always uses), no SOP/EPH markers, and the default code-block style.
func encodeCODContent(cs CodingStyle) []byte {
	c := []byte{0x00} // Scod: no explicit precincts, no SOP, no EPH
	c = append(c, byte(cs.ProgressionOrder))
	c = u16(c, uint16(cs.NumLayers))
	mct := byte(0)
	if cs.MultipleComponentTransform {
		mct = 1
	}
	c = append(c, mct)
	c = append(c, byte(cs.DecompositionLevels))
	c = append(c, byte(log2Exact(cs.CodeBlockWidth)-2), byte(log2Exact(cs.CodeBlockHeight)-2))
	c = append(c, 0) // code-block style: default
	transform := byte(0)
	if cs.Transform == Transform5x3 {
		transform = 1
	}
	c = append(c, transform)
	return c
}

// encodeQCDContent builds a QCD marker segment's content declaring
// QuantNone with zero guard bits and one step size (decompLevels 0 means
// one subband, the LL band) whose exponent is chosen so
// dequantizeComponent's own mb (guardBits+exponent-1) exactly equals
// numBitPlanes - the same "no scaling, no truncation" convention
// image_test.go's buildTier1LLComponent doc comment explains, needed so
// every code-block's tier-1 output round-trips to an exact integer with
// no dequantization scaling at all (delta 1.0, reversible transform).
func encodeQCDContent(numBitPlanes int) []byte {
	const sqcd = 0 // guard bits 0, QuantNone
	exponent := byte(numBitPlanes + 1)
	return []byte{sqcd, exponent << 3}
}

// encodeTileData is encodeSyntheticTile's (packet_test.go) production
// counterpart: the same tag-tree/coding-pass/length encoding built there
// to validate decodeTilePackets, reused here to actually build one
// tile's packet stream from real tier-1 output. It differs from
// encodeSyntheticTile in the ways this file's own doc comment and
// codeBlockTruth's explain: truth is keyed by code-block pointer
// identity (safe across several components, since createPacket -
// progression.go - never mixes two components' code-blocks into the
// same packet), and every code-block is simply included at the one and
// only layer, with no SOP/EPH or partial-inclusion bookkeeping.
func encodeTileData(coding CodingStyle, components []*componentDecode, truth map[*codeBlockInfo]*codeBlockTruth) ([]byte, error) {
	it, err := newPacketIterator(coding.ProgressionOrder, components, coding.NumLayers)
	if err != nil {
		return nil, err
	}

	inclusionLeaves := map[*precinctState][]int{}
	zbpLeaves := map[*precinctState][]int{}
	zbpDone := map[*codeBlockInfo]bool{}

	w := &bitWriter{}
	for {
		pkt, ok := it.nextPacket()
		if !ok {
			break
		}
		w.writeBit(1) // every packet this file produces carries data - see this file's Scope section

		var queue [][]byte
		for _, cb := range pkt.codeBlocks {
			ct := truth[cb]
			if ct == nil {
				return nil, malformedf("encode: no ground truth for code-block (%d,%d)", cb.cbx, cb.cby)
			}
			p := cb.precinct
			width := p.cbxMax - p.cbxMin + 1
			height := p.cbyMax - p.cbyMin + 1

			if p.inclusionTree == nil {
				p.inclusionTree = newTagTreeInclusion(width, height, pkt.layer)
				p.zeroBitPlanesTree = newTagTreeZeroBitPlanes(width, height)

				leaves := make([]int, width*height)
				zleaves := make([]int, width*height)
				for _, sibling := range pkt.codeBlocks {
					if sibling.precinct != p {
						continue
					}
					sct := truth[sibling]
					if sct == nil {
						return nil, malformedf("encode: no ground truth for code-block (%d,%d)", sibling.cbx, sibling.cby)
					}
					li := (sibling.cby-p.cbyMin)*width + (sibling.cbx - p.cbxMin)
					leaves[li] = firstInclusionLayer(sct)
					zleaves[li] = sct.zeroBitPlanes
				}
				inclusionLeaves[p] = leaves
				zbpLeaves[p] = zleaves
			}

			cbCol := cb.cbx - p.cbxMin
			cbRow := cb.cby - p.cbyMin
			pyramid := buildPyramid(width, height, inclusionLeaves[p])
			encodeInclusionLeaf(p.inclusionTree, cbCol, cbRow, pkt.layer, pyramid, w)

			if !zbpDone[cb] {
				zPyramid := buildPyramid(width, height, zbpLeaves[p])
				encodeZeroBitPlanesLeaf(p.zeroBitPlanesTree, cbCol, cbRow, zPyramid, w)
				zbpDone[cb] = true
			}

			encodeCodingPasses(w, ct.numPasses[pkt.layer])
			encodeLength(w, cb, ct.numPasses[pkt.layer], len(ct.data[pkt.layer]))
			queue = append(queue, ct.data[pkt.layer])
		}

		w.align()
		for _, d := range queue {
			w.writeRawBytes(d...)
		}
	}
	return w.out, nil
}

// encodeCodestream is EncodeGray's and EncodeRGB's shared implementation:
// DC-level-shifts every component (and, if mct, forward-RCTs the first
// three), tier-1 encodes every code-block of every component, frames
// them into one tile's worth of tier-2 packets, and wraps the result in
// a complete bare codestream (SOC through EOC) - see this file's own
// Scope section for the fixed configuration every codestream it produces
// shares.
func encodeCodestream(width, height int, components [][]byte, mct bool) ([]byte, error) {
	if width <= 0 || height <= 0 {
		return nil, malformedf("encode: invalid image size %dx%d", width, height)
	}
	if mct && len(components) != 3 {
		return nil, malformedf("encode: the reversible colour transform requires exactly 3 components, got %d", len(components))
	}
	n := width * height
	shifted := make([][]float64, len(components))
	for i, pix := range components {
		if len(pix) != n {
			return nil, malformedf("encode: component %d has %d byte(s), want %d for a %dx%d image", i, len(pix), n, width, height)
		}
		s := make([]float64, n)
		for j, v := range pix {
			s[j] = float64(v) - 128 // DC level shift, unsigned 8-bit
		}
		shifted[i] = s
	}
	if mct {
		forwardRCT(shifted[0], shifted[1], shifted[2])
	}

	cs := encodeCodingStyle(mct)
	compDecodes := make([]*componentDecode, len(shifted))
	for i := range shifted {
		compDecodes[i] = &componentDecode{coding: cs, resolutions: buildResolutions(0, 0, width, height, cs)}
	}

	truth := map[*codeBlockInfo]*codeBlockTruth{}
	for i, comp := range compDecodes {
		sb := comp.resolutions[0].subbands[0] // decompLevels 0: one resolution, one (LL) subband
		for _, cb := range sb.codeBlocks {
			cw := cb.tbx1 - cb.tbx0
			ch := cb.tby1 - cb.tby0
			magnitude := make([]uint32, cw*ch)
			sign := make([]uint8, cw*ch)
			k := 0
			for y := cb.tby0; y < cb.tby1; y++ {
				for x := cb.tbx0; x < cb.tbx1; x++ {
					v := int64(shifted[i][y*width+x])
					if v < 0 {
						sign[k] = 1
						v = -v
					}
					magnitude[k] = uint32(v)
					k++
				}
			}
			data, numPasses := encodeCodeBlockTier1(cw, ch, magnitude, sign, subbandLL, encodeTileBitPlanes, 0)
			truth[cb] = &codeBlockTruth{
				includedAtLayer: []bool{true},
				numPasses:       []int{numPasses},
				data:            [][]byte{data},
			}
		}
	}

	tileData, err := encodeTileData(cs, compDecodes, truth)
	if err != nil {
		return nil, err
	}
	tilePart := buildTilePart(0, 0, 1, tileData)

	codestream := bareMarker(markerSOC)
	codestream = append(codestream, segment(markerSIZ, codeSIZ(uint32(width), uint32(height), uint32(width), uint32(height), len(components), 8, false))...)
	codestream = append(codestream, segment(markerCOD, encodeCODContent(cs))...)
	codestream = append(codestream, segment(markerQCD, encodeQCDContent(encodeTileBitPlanes))...)
	codestream = append(codestream, tilePart...)
	codestream = append(codestream, bareMarker(markerEOC)...)
	return codestream, nil
}

// EncodeGray encodes a single-component 8-bit grayscale image (pix,
// row-major, one byte per pixel, width*height long) as a bare JPEG 2000
// codestream - see this file's own doc comment for the fixed
// configuration (single tile, no decomposition, 5/3 reversible, single
// layer, LRCP) every codestream this file produces shares.
func EncodeGray(width, height int, pix []byte) ([]byte, error) {
	return encodeCodestream(width, height, [][]byte{pix}, false)
}

// EncodeRGB encodes a 3-component 8-bit RGB image (r, g, b each
// row-major, one byte per pixel, width*height long) the same way as
// EncodeGray, applying the reversible colour transform (RCT) across the
// three components first. Every DC-level-shifted component sample lands
// in [-128,127], so an RCT-transformed Y value (their average, roughly)
// stays within that same range while a Cb/Cr-like difference of two of
// them can reach magnitude 255 - encodeTileBitPlanes' own doc comment is
// where that bound is spent.
func EncodeRGB(width, height int, r, g, b []byte) ([]byte, error) {
	return encodeCodestream(width, height, [][]byte{r, g, b}, true)
}
