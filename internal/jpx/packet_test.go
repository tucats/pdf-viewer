package jpx

// This file is packet.go/progression.go/tagtree.go/bitreader.go's own
// round-trip verification - this package has no real-world JPX sample
// (see doc.go's Provenance section), so it builds a from-scratch,
// test-only packet header *encoder* (bitWriter plus the tag-tree
// encode-side helpers below) and checks that decodeTilePackets recovers
// exactly the ground truth the encoder was given, the same "write both
// directions from opposite ends of the standard's own description"
// approach this package's other sub-phases use (see mq.go/mq_test.go
// for the same pattern applied to the MQ coder).
//
// The encoder deliberately reuses the *same* tagTreeInclusion/
// tagTreeZeroBitPlanes types and their reset/incrementValue/nextLevel
// state machine that the decoder drives from a bitstream - reusing that
// bookkeeping is not circular: what this test actually checks is
// whether writeBit/readBit-and-interpret round-trip through it
// correctly for values chosen independently of the decoder's own logic
// (via the min-reduction "pyramid" built directly from ground truth
// below), which is exactly where a real bug (e.g. an increment target
// off by one, or settling the wrong node) would surface as a mismatch.

import (
	"math"
	"testing"
)

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
// reproduced here structurally so pyramid[level][index] lines up
// exactly with a tree's own t.levels[level]/lvl.index.
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
			next[i] = math.MaxInt
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

// codeBlockTruth is one code-block's ground truth across every layer,
// keyed by (cbx,cby) so it lines up with a codeBlockInfo built by an
// independent (encode-side vs. decode-side) buildTileComponents call.
type codeBlockTruth struct {
	// includedAtLayer[l] is true if this code-block contributes to
	// layer l.
	includedAtLayer []bool
	numPasses       []int
	data            [][]byte
	zeroBitPlanes   int
}

func cbKey(cb *codeBlockInfo) [2]int { return [2]int{cb.cbx, cb.cby} }

// encodeSyntheticTile builds a complete, valid tile-part byte stream (no
// SOT/SOD framing - the caller wraps it, e.g. via buildTilePart) from
// truth (keyed by cbKey) and coding (whose ProgressionOrder/NumLayers/
// UseSOPMarkers/UseEPHMarkers drive iteration - components' own
// resolutions/subbands/code-blocks must already reflect coding, e.g.
// via buildResolutions). Restricted to scenarios where each precinct
// number names code-blocks in exactly one subband (true whenever every
// component's DecompositionLevels is 0, so only the LL subband exists) -
// a precinct spanning HL/LH/HH would need this function to build one
// truth grid per subband's own precinctState instead of one per precinct
// number, which none of this sub-phase's tests currently exercise.
func encodeSyntheticTile(t *testing.T, coding CodingStyle, components []*componentDecode, truth map[[2]int]*codeBlockTruth) []byte {
	t.Helper()
	it, err := newPacketIterator(coding.ProgressionOrder, components, coding.NumLayers)
	if err != nil {
		t.Fatalf("newPacketIterator: %v", err)
	}

	// Per-precinct ground-truth grids (first-inclusion layer, zero-bit-
	// plane count), built lazily the first time a precinct's tag trees
	// are created - exactly when the decoder side would need them too.
	inclusionLeaves := map[*precinctState][]int{}
	zbpLeaves := map[*precinctState][]int{}
	zbpDone := map[*codeBlockInfo]bool{}

	w := &bitWriter{}
	seq := uint16(0)
	for {
		pkt, ok := it.nextPacket()
		if !ok {
			break
		}
		if coding.UseSOPMarkers {
			w.writeSOP(seq)
			seq++
		}

		anyIncluded := false
		for _, cb := range pkt.codeBlocks {
			if truth[cbKey(cb)].includedAtLayer[pkt.layer] {
				anyIncluded = true
				break
			}
		}
		if !anyIncluded {
			w.writeBit(0)
			w.align()
			if coding.UseEPHMarkers {
				w.writeEPH()
			}
			continue
		}
		w.writeBit(1)

		var queue [][]byte
		for _, cb := range pkt.codeBlocks {
			ct := truth[cbKey(cb)]
			included := ct.includedAtLayer[pkt.layer]
			p := cb.precinct
			width := p.cbxMax - p.cbxMin + 1
			height := p.cbyMax - p.cbyMin + 1

			if cb.included {
				if included {
					w.writeBit(1)
				} else {
					w.writeBit(0)
				}
			} else {
				if p.inclusionTree == nil {
					p.inclusionTree = newTagTreeInclusion(width, height, pkt.layer)
					p.zeroBitPlanesTree = newTagTreeZeroBitPlanes(width, height)
					for l := 0; l < pkt.layer; l++ {
						w.writeBit(0)
					}

					leaves := make([]int, width*height)
					zleaves := make([]int, width*height)
					for _, sibling := range pkt.codeBlocks {
						if sibling.precinct != p {
							continue
						}
						sct := truth[cbKey(sibling)]
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
				if encodeInclusionLeaf(p.inclusionTree, cbCol, cbRow, pkt.layer, pyramid, w) {
					cb.included = true
					included = true
				}
			}
			if !included {
				continue
			}

			if !zbpDone[cb] {
				cbCol := cb.cbx - p.cbxMin
				cbRow := cb.cby - p.cbyMin
				zPyramid := buildPyramid(width, height, zbpLeaves[p])
				encodeZeroBitPlanesLeaf(p.zeroBitPlanesTree, cbCol, cbRow, zPyramid, w)
				zbpDone[cb] = true
			}

			passes := ct.numPasses[pkt.layer]
			data := ct.data[pkt.layer]
			encodeCodingPasses(w, passes)
			encodeLength(w, cb, passes, len(data))
			queue = append(queue, data)
		}

		w.align()
		if coding.UseEPHMarkers {
			w.writeEPH()
		}
		for _, d := range queue {
			w.writeRawBytes(d...)
		}
	}
	return w.out
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
