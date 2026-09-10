package jpx

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// This file is packet_test.go's actual test cases: TestDecodeTilePackets
// RoundTrip builds a complete, from-scratch codestream (SOC through EOC,
// with real tile-part bytes produced by encodeSyntheticTile) for every
// progression order, parses it with ParseHeader (14a) and decodes it
// with decodeTilePackets (14b), and checks every code-block's recovered
// contributions - inclusion layer, pass count, and exact data bytes -
// against the ground truth the encoder was given.
//
// TestPacketIteratorsProduceSamePacketSet is a second, independent
// cross-check that does not depend on the bitstream encoder at all: any
// progression order must produce the same *set* of (resolution,
// precinct, component, layer) packets as any other, only in a different
// order (§B.12's progression orders are defined as reorderings of the
// same packet sequence, not different partitions of it) - a much
// cheaper property to verify exhaustively across every order and a
// multi-resolution, multi-component scenario the round-trip test above
// does not exercise.

// syntheticBytes deterministically fills n bytes of placeholder
// "compressed code-block data" (tier-1/14c does not exist yet, so
// nothing ever interprets these as real MQ-coded content) - distinct
// per code-block index and layer so a mix-up between two code-blocks'
// or two layers' contributions is very unlikely to go unnoticed.
func syntheticBytes(k, layer, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((k*31 + layer*17 + i*7 + 5) & 0xFF)
	}
	return b
}

// codeCODCustom builds a COD marker segment's content with explicit
// control over progression order, SOP/EPH markers, and (if precinctExps
// is non-empty) explicit per-resolution-level precinct sizes - the
// options testutil_test.go's own codeCOD does not expose, since none of
// 14a's tests needed them.
func codeCODCustom(order ProgressionOrder, numLayers uint16, tc tileConfig, sop, eph bool, precinctExps []byte) []byte {
	scod := byte(0)
	if len(precinctExps) > 0 {
		scod |= 0x01
	}
	if sop {
		scod |= 0x02
	}
	if eph {
		scod |= 0x04
	}
	c := []byte{scod, byte(order)}
	c = u16(c, numLayers)
	c = append(c, 0) // MCT off
	c = append(c, byte(tc.decompLevels), byte(tc.cbWidthExp), byte(tc.cbHeightExp), 0, tc.transform)
	c = append(c, precinctExps...)
	return c
}

func TestDecodeTilePacketsRoundTrip(t *testing.T) {
	const imgSize = 32
	tc := tileConfig{decompLevels: 0, cbWidthExp: 1, cbHeightExp: 1, transform: 1, quantStyle: 0, guardBits: 2} // 8x8 code-blocks
	const numLayers = 2
	const gridCells = 4 // 4x4 code-blocks (32/8), so 2x2=4 precincts of 2x2 code-blocks each (16x16 precincts)

	orders := []ProgressionOrder{ProgressionLRCP, ProgressionRLCP, ProgressionRPCL, ProgressionPCRL, ProgressionCPRL}
	for _, order := range orders {
		t.Run(fmt.Sprintf("order=%d", order), func(t *testing.T) {
			cs := CodingStyle{
				PrecinctsDefined: true, UseSOPMarkers: true, UseEPHMarkers: true,
				ProgressionOrder: order, NumLayers: numLayers,
				DecompositionLevels:     tc.decompLevels,
				CodeBlockWidth:          8,
				CodeBlockHeight:         8,
				Transform:               Transform5x3,
				PrecinctWidthExponents:  []int{4},
				PrecinctHeightExponents: []int{4},
			}

			// Build ground truth, keyed by (cbx,cby): a 4x4 grid of
			// code-blocks, with a mix of layer-0 inclusion, deferred
			// (layer-1-only) inclusion, varying pass counts, and varying
			// data lengths (some large enough to force Lblock growth).
			truth := map[[2]int]*codeBlockTruth{}
			k := 0
			for cby := 0; cby < gridCells; cby++ {
				for cbx := 0; cbx < gridCells; cbx++ {
					inc0 := k%3 != 2
					ct := &codeBlockTruth{
						includedAtLayer: []bool{inc0, true},
						numPasses:       make([]int, numLayers),
						data:            make([][]byte, numLayers),
						zeroBitPlanes:   k % 6,
					}
					if inc0 {
						ct.numPasses[0] = 1 + k%3
						ct.data[0] = syntheticBytes(k, 0, 2+(k*3)%9)
					}
					ct.numPasses[1] = 2 + k%4
					// A few code-blocks get a deliberately large
					// contribution to force multiple Lblock increments.
					length := 3 + (k*5)%17
					if k%5 == 0 {
						length += 200
					}
					ct.data[1] = syntheticBytes(k, 1, length)
					truth[[2]int{cbx, cby}] = ct
					k++
				}
			}

			components := []*componentDecode{{coding: cs, resolutions: buildResolutions(0, 0, imgSize, imgSize, cs)}}
			tilePartBody := encodeSyntheticTile(t, cs, components, truth)

			codestream := bareMarker(markerSOC)
			codestream = append(codestream, segment(markerSIZ, codeSIZ(imgSize, imgSize, imgSize, imgSize, 1, 8, false))...)
			codestream = append(codestream, segment(markerCOD, codeCODCustom(order, numLayers, tc, true, true, []byte{0x44}))...)
			codestream = append(codestream, segment(markerQCD, codeQCD(tc, subbandsFor(tc)))...)
			codestream = append(codestream, buildTilePart(0, 0, 1, tilePartBody)...)
			codestream = append(codestream, bareMarker(markerEOC)...)

			h, err := ParseHeader(codestream)
			if err != nil {
				t.Fatalf("ParseHeader: %v", err)
			}
			decoded, err := decodeTilePackets(h, codestream, 0)
			if err != nil {
				t.Fatalf("decodeTilePackets: %v", err)
			}
			if len(decoded) != 1 || len(decoded[0].resolutions) != 1 || len(decoded[0].resolutions[0].subbands) != 1 {
				t.Fatalf("unexpected decoded geometry shape: %+v", decoded)
			}

			got := map[[2]int]*codeBlockInfo{}
			for _, cb := range decoded[0].resolutions[0].subbands[0].codeBlocks {
				got[[2]int{cb.cbx, cb.cby}] = cb
			}
			if len(got) != gridCells*gridCells {
				t.Fatalf("decoded %d code-blocks, want %d", len(got), gridCells*gridCells)
			}

			for key, ct := range truth {
				cb, ok := got[key]
				if !ok {
					t.Fatalf("code-block %v missing from decoded result", key)
				}
				wantContribs := 0
				for l := 0; l < numLayers; l++ {
					if ct.includedAtLayer[l] {
						wantContribs++
					}
				}
				if len(cb.contributions) != wantContribs {
					t.Fatalf("code-block %v: got %d contributions, want %d", key, len(cb.contributions), wantContribs)
				}
				ci := 0
				for l := 0; l < numLayers; l++ {
					if !ct.includedAtLayer[l] {
						continue
					}
					contrib := cb.contributions[ci]
					ci++
					if contrib.layer != l {
						t.Errorf("code-block %v contribution %d: layer=%d, want %d", key, ci, contrib.layer, l)
					}
					if contrib.numPasses != ct.numPasses[l] {
						t.Errorf("code-block %v layer %d: numPasses=%d, want %d", key, l, contrib.numPasses, ct.numPasses[l])
					}
					if !bytes.Equal(contrib.data, ct.data[l]) {
						t.Errorf("code-block %v layer %d: data=%x, want %x", key, l, contrib.data, ct.data[l])
					}
				}
				if cb.zeroBitPlanes != ct.zeroBitPlanes {
					t.Errorf("code-block %v: zeroBitPlanes=%d, want %d", key, cb.zeroBitPlanes, ct.zeroBitPlanes)
				}
			}
		})
	}
}

// TestPacketIteratorsProduceSamePacketSet builds a multi-resolution,
// multi-component tile-component geometry and checks that all five
// progression orders visit exactly the same set of packets (identified
// by resolution level, precinct number, component index and layer),
// each exactly once - only their order may differ.
func TestPacketIteratorsProduceSamePacketSet(t *testing.T) {
	cs0 := CodingStyle{
		DecompositionLevels: 2, CodeBlockWidth: 16, CodeBlockHeight: 16,
		Transform: Transform5x3, PrecinctWidthExponents: []int{15, 15, 15}, PrecinctHeightExponents: []int{15, 15, 15},
	}
	cs1 := cs0
	cs1.DecompositionLevels = 1
	cs1.PrecinctWidthExponents = []int{15, 15}
	cs1.PrecinctHeightExponents = []int{15, 15}

	components := []*componentDecode{
		{coding: cs0, resolutions: buildResolutions(0, 0, 100, 60, cs0)},
		{coding: cs1, resolutions: buildResolutions(0, 0, 100, 60, cs1)},
	}
	const numLayers = 3

	// packetKey identifies a packet by its layer plus the sorted set of
	// code-block object identities it carries - code-blocks are never
	// reallocated between iterator runs in this test (components is
	// built once, shared across all five), so two packets from
	// different progression orders are "the same packet" exactly when
	// they carry the same layer and the same code-blocks, regardless of
	// which order produced them.
	packetKey := func(pkt packet) string {
		ptrs := make([]string, len(pkt.codeBlocks))
		for i, cb := range pkt.codeBlocks {
			ptrs[i] = fmt.Sprintf("%p", cb)
		}
		sort.Strings(ptrs)
		return fmt.Sprintf("L%d:%s", pkt.layer, strings.Join(ptrs, ","))
	}

	var reference map[string]int
	for _, order := range []ProgressionOrder{ProgressionLRCP, ProgressionRLCP, ProgressionRPCL, ProgressionPCRL, ProgressionCPRL} {
		it, err := newPacketIterator(order, components, numLayers)
		if err != nil {
			t.Fatalf("order %d: %v", order, err)
		}
		seen := map[string]int{}
		for {
			pkt, ok := it.nextPacket()
			if !ok {
				break
			}
			if len(pkt.codeBlocks) == 0 {
				t.Fatalf("order %d: packet with no code-blocks", order)
			}
			seen[packetKey(pkt)]++
		}
		if reference == nil {
			reference = seen
			continue
		}
		if len(seen) != len(reference) {
			t.Fatalf("order %d: saw %d distinct packets, reference had %d", order, len(seen), len(reference))
		}
		for k, n := range reference {
			if seen[k] != n {
				t.Errorf("order %d: packet %q seen %d times, reference saw it %d times", order, k, seen[k], n)
			}
		}
	}
}
