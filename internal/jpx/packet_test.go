package jpx

// This file is packet.go/progression.go/tagtree.go/bitreader.go's own
// round-trip verification - this package has no real-world JPX sample
// (see doc.go's Provenance section), so it builds a from-scratch,
// test-only packet header *encoder* (encodeSyntheticTile below, on top
// of encode.go's bitWriter plus the tag-tree encode-side helpers - moved
// there by 14g so tools/genfixtures, an ordinary non-test build, can
// reach them too) and checks that decodeTilePackets recovers exactly the
// ground truth the encoder was given, the same "write both directions
// from opposite ends of the standard's own description" approach this
// package's other sub-phases use (see mq.go/mq_test.go for the same
// pattern applied to the MQ coder).
//
// The encoder deliberately reuses the *same* tagTreeInclusion/
// tagTreeZeroBitPlanes types and their reset/incrementValue/nextLevel
// state machine that the decoder drives from a bitstream - reusing that
// bookkeeping is not circular: what this test actually checks is
// whether writeBit/readBit-and-interpret round-trip through it correctly
// for values chosen independently of the decoder's own logic (via the
// min-reduction "pyramid" built directly from ground truth below), which
// is exactly where a real bug (e.g. an increment target off by one, or
// settling the wrong node) would surface as a mismatch.

import (
	"testing"
)

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
// number, which none of this sub-phase's tests currently exercise. Its
// own truth map is keyed by (cbx,cby) rather than code-block pointer
// identity - fine for every test in this file, which only ever builds a
// single component - unlike encode.go's own production encodeTileData,
// which must support several components at once and so cannot use that
// shortcut; see that function's doc comment.
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
