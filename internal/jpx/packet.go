package jpx

import "sort"

// This file is tier-2's top-level entry point: decodeTilePackets ties
// together this sub-phase's other pieces - geometry.go's per-tile-
// component resolution/subband/code-block structure, progression.go's
// packet iteration, tagtree.go's inclusion/zero-bit-plane trees, and
// bitreader.go's bit-stuffed packet header reader - to walk one tile's
// packets (ISO/IEC 15444-1 §B.10) across all of its tile-parts and
// locate every code-block's compressed-data contributions. It decodes
// no sample data itself (tier-1, 14c, is what actually runs the MQ
// coder over what this file locates) and is not yet reachable from
// internal/filter (14f wires that up); its own tests exercise it
// directly against codestreams this package's testutil_test.go builds.
//
// Ported from Mozilla's pdf.js's parseTilePackets (jpx.js, Apache
// License 2.0), the same cross-check approach this sub-phase's other
// files document (see tagtree.go's doc comment).
//
// A tile's tile-parts (there may be more than one - see TilePart's doc
// comment) share one continuous packet sequence and one continuous set
// of per-precinct tag-tree/Lblock state (§B.9): a resolution/component/
// precinct's packet for layer 3 might live in a different tile-part than
// its packet for layer 2, and the inclusion tag tree built while
// decoding layer 2's packet must still be there, unmodified, when layer
// 3's packet is decoded - so decodeTilePackets builds that geometry and
// state once per tile and then processes every tile-part's byte range
// against the same packetIterator and the same codeBlockInfo/
// precinctState objects, in TPsot order.

// buildTileComponents constructs the geometry (resolutions, subbands,
// code-blocks) and effective coding style for every component of tile
// tileIndex - resolving any per-tile COC/COD override along the way
// (Header.effectiveCoding) - everything newPacketIterator and packet
// header parsing need per component.
func buildTileComponents(h *Header, tileIndex int) ([]*componentDecode, error) {
	components := make([]*componentDecode, len(h.Components))
	for c := range h.Components {
		tcx0, tcy0, tcx1, tcy1, err := tileComponentBounds(h, tileIndex, c)
		if err != nil {
			return nil, err
		}
		cs := h.effectiveCoding(tileIndex, c)
		components[c] = &componentDecode{
			coding:      cs,
			resolutions: buildResolutions(tcx0, tcy0, tcx1, tcy1, cs),
		}
	}
	return components, nil
}

// readCodingPasses decodes §B.10.7 Table B.4's variable-length code for
// how many coding passes a code-block's contribution to this packet
// holds.
func readCodingPasses(r *packetBitReader) int {
	if r.readBit() == 0 {
		return 1
	}
	if r.readBit() == 0 {
		return 2
	}
	v := r.readBits(2)
	if v < 3 {
		return v + 3
	}
	v = r.readBits(5)
	if v < 31 {
		return v + 6
	}
	v = r.readBits(7)
	return v + 37
}

// queuedContribution is one code-block's parsed-but-not-yet-located
// contribution to the packet currently being read: its header fields are
// known, but its compressed-data byte range depends on every other
// code-block ahead of it in the same packet (§B.10's packet layout is
// header-then-all-code-block-data, not interleaved), so parseOnePacket
// queues them and only resolves byte ranges once the whole header has
// been read and the reader is positioned at the first code-block's data.
type queuedContribution struct {
	cb        *codeBlockInfo
	numPasses int
	length    int
}

// parseOnePacket reads one packet's header (§B.10) from r - which must
// already be byte-aligned and positioned at the packet's start (either
// the very start of a tile-part's data, or immediately after the
// previous packet's own code-block data) - and appends a new
// codeBlockContribution to every code-block pkt includes. sopUsed/
// ephUsed come from the tile's effective default coding style (SOP/EPH
// markers are only ever controlled by COD, never a COC override).
func parseOnePacket(r *packetBitReader, pkt packet, sopUsed, ephUsed bool) error {
	if sopUsed {
		r.skipSOPIfPresent()
	}
	if r.readBit() == 0 {
		// Zero-length packet (§B.10's first header bit): nothing
		// included for any code-block, this layer, at this resolution/
		// component/precinct.
		r.alignToByte()
		if ephUsed {
			r.skipEPHIfPresent()
		}
		return nil
	}

	var queue []queuedContribution
	for _, cb := range pkt.codeBlocks {
		p := cb.precinct
		cbCol := cb.cbx - p.cbxMin
		cbRow := cb.cby - p.cbyMin

		included := false
		firstTimeInclusion := false
		if cb.included {
			// Already included in an earlier layer: a plain bit is
			// enough from here on (§B.10.4).
			included = r.readBit() != 0
		} else {
			if p.inclusionTree == nil {
				width := p.cbxMax - p.cbxMin + 1
				height := p.cbyMax - p.cbyMin + 1
				p.inclusionTree = newTagTreeInclusion(width, height, pkt.layer)
				p.zeroBitPlanesTree = newTagTreeZeroBitPlanes(width, height)
				// This precinct's very first packet touched by any
				// code-block's decode is layer pkt.layer, not
				// necessarily layer 0 (every earlier layer's packet
				// for this precinct was itself empty and skipped
				// above without ever reaching this code) - ported
				// from pdf.js's InclusionTree construction site,
				// which reads this many confirmatory zero bits at
				// this point; see tagtree.go's newTagTreeInclusion
				// doc comment.
				for l := 0; l < pkt.layer; l++ {
					if r.readBit() != 0 {
						return malformedf("packet header: invalid inclusion tag tree initialization")
					}
				}
			}
			if p.inclusionTree.reset(cbCol, cbRow, pkt.layer) {
				for {
					if r.readBit() != 0 {
						if !p.inclusionTree.nextLevel() {
							cb.included = true
							included = true
							firstTimeInclusion = true
							break
						}
					} else {
						p.inclusionTree.incrementValue(pkt.layer)
						break
					}
				}
			}
		}
		if !included {
			continue
		}

		if firstTimeInclusion {
			p.zeroBitPlanesTree.reset(cbCol, cbRow)
			for {
				if r.readBit() != 0 {
					if !p.zeroBitPlanesTree.nextLevel() {
						break
					}
				} else {
					p.zeroBitPlanesTree.incrementValue()
				}
			}
			cb.zeroBitPlanes = p.zeroBitPlanesTree.result()
		}

		numPasses := readCodingPasses(r)
		for r.readBit() != 0 {
			cb.Lblock++
		}
		passLog2 := log2Ceil(numPasses)
		bits := passLog2 + cb.Lblock
		if numPasses < 1<<uint(passLog2) {
			// §B.10.7: the length field is one bit narrower whenever
			// numPasses is not itself an exact power of two.
			bits--
		}
		length := r.readBits(bits)
		queue = append(queue, queuedContribution{cb: cb, numPasses: numPasses, length: length})
	}

	r.alignToByte()
	if ephUsed {
		r.skipEPHIfPresent()
	}
	for _, q := range queue {
		start := r.pos
		end := start + q.length
		if q.length < 0 || end > len(r.data) {
			return malformedf("packet header: code-block contribution length %d exceeds the tile-part's remaining data", q.length)
		}
		q.cb.contributions = append(q.cb.contributions, codeBlockContribution{
			layer: pkt.layer, numPasses: q.numPasses, data: r.data[start:end],
		})
		r.pos = end
	}
	return nil
}

// decodeTilePackets parses every packet across all of tile tileIndex's
// tile-parts (processed in TPsot order), returning the resulting
// per-component geometry with every code-block's compressed-data
// contributions filled in - tier-1 (14c)'s input. codestream must be the
// same byte slice ParseHeader ultimately produced h from (see
// TilePart.Data's doc comment); a caller working from a JP2-wrapped file
// must pass the unwrapped codestream, not the original file bytes (14f's
// internal/filter adapter, once it exists, is expected to keep that
// slice around from its own ParseHeader call for exactly this purpose).
func decodeTilePackets(h *Header, codestream []byte, tileIndex int) ([]*componentDecode, error) {
	components, err := buildTileComponents(h, tileIndex)
	if err != nil {
		return nil, err
	}

	tileCoding := h.effectiveTileDefaultCoding(tileIndex)
	it, err := newPacketIterator(tileCoding.ProgressionOrder, components, tileCoding.NumLayers)
	if err != nil {
		return nil, err
	}

	var parts []TilePart
	for i := range h.TileParts {
		if h.TileParts[i].TileIndex == tileIndex {
			parts = append(parts, h.TileParts[i])
		}
	}
	if len(parts) == 0 {
		return nil, malformedf("tile %d has no tile-parts", tileIndex)
	}
	sort.Slice(parts, func(a, b int) bool { return parts[a].PartIndex < parts[b].PartIndex })

	for _, tp := range parts {
		r := newPacketBitReader(tp.Data(codestream))
		for r.pos < len(r.data) {
			pkt, ok := it.nextPacket()
			if !ok {
				break
			}
			if err := parseOnePacket(r, pkt, tileCoding.UseSOPMarkers, tileCoding.UseEPHMarkers); err != nil {
				return nil, err
			}
		}
	}

	return components, nil
}
