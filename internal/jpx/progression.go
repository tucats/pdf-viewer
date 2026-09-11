package jpx

// This file implements packet iteration: the order in which a tile's
// packets (each one resolution level x precinct x component x layer
// combination's worth of code-block contributions - see geometry.go's
// resolutionInfo/subbandInfo/codeBlockInfo) appear in the codestream,
// per ISO/IEC 15444-1 §B.12. All five progression orders the standard
// defines are implemented unconditionally - see doc.go's Scope section
// on why a decoder cannot pick and choose among them - each as its own
// small resumable iterator (one nextPacket call returns exactly the
// next packet in codestream order, or signals exhaustion), matching
// packetheader.go's own one-packet-at-a-time parsing loop.
//
// Ported from Mozilla's pdf.js (jpx.js's five *Iterator functions,
// createPacket, getPrecinctSizesInImageScale and
// getPrecinctIndexIfExist - Apache License 2.0), the same cross-check
// approach tagtree.go's doc comment explains, restructured from
// JavaScript closures capturing mutable locals into Go structs holding
// the same state as fields (each iterator's nextPacket is a direct,
// line-by-line translation of pdf.js's own nested, resumable for-loops -
// resuming a partially-iterated Go for loop across calls by simply not
// resetting its loop variable works exactly the same way a JS closure's
// captured variables do). One divergence from pdf.js's own
// getPrecinctIndexIfExist: it divides one axis of a candidate position
// by the OTHER axis's precinct-size field (posY by sizeInImageScale's
// width, and vice versa) - harmless in practice only because real
// encoders overwhelmingly use square precincts (width == height, making
// the two fields interchangeable), but wrong on its own terms for a
// general decoder; this file divides each axis by its own matching
// field, which is what a precinct index actually means.

// packet is one resolution/precinct/layer combination's set of
// code-blocks (in the standard's own fixed subband order - LL alone, or
// HL/LH/HH - since each subbandInfo's own codeBlocks slice is already in
// that order and createPacket only ever filters it, never reorders it).
type packet struct {
	layer      int
	codeBlocks []*codeBlockInfo
}

// createPacket collects every code-block belonging to precinctNumber
// across all of ri's subbands (§B.10.8's "order of information within a
// packet": subbands in a fixed order, and within each, code-blocks in
// raster order - already how buildCodeBlocks appended them).
func createPacket(ri *resolutionInfo, precinctNumber, layer int) packet {
	var codeBlocks []*codeBlockInfo
	for _, sb := range ri.subbands {
		for _, cb := range sb.codeBlocks {
			if cb.precinctNumber == precinctNumber {
				codeBlocks = append(codeBlocks, cb)
			}
		}
	}
	return packet{layer: layer, codeBlocks: codeBlocks}
}

// packetIterator yields a tile's packets in codestream order, one at a
// time - nextPacket returns ok=false once every packet has been
// produced.
type packetIterator interface {
	nextPacket() (p packet, ok bool)
}

// componentDecode is one tile-component's geometry and effective coding
// style (with any per-tile COC override already resolved - see
// Header.effectiveCoding), everything the packet iterators and
// packetheader.go need per component. quant, bitDepth, and tcx0/tcy0
// are unused before 14d (dequantization and the inverse wavelet
// transform, idwt.go), which needs each of them: quant and bitDepth to
// recover a subband's real-valued step size (Annex E.1), and tcx0/tcy0
// (this tile-component's own pixel offset - see tileComponentBounds) to
// resolve the degenerate width==1/height==1 boundary case §F.3.4/F.3.5
// call out.
type componentDecode struct {
	coding      CodingStyle
	quant       QuantizationStyle
	bitDepth    int
	tcx0, tcy0  int
	resolutions []*resolutionInfo
}

func maxDecompositionLevels(components []*componentDecode) int {
	m := 0
	for _, c := range components {
		m = max(m, c.coding.DecompositionLevels)
	}
	return m
}

// newPacketIterator dispatches to one of the five progression-order
// iterators below.
func newPacketIterator(order ProgressionOrder, components []*componentDecode, layersCount int) (packetIterator, error) {
	switch order {
	case ProgressionLRCP:
		return &lrcpIterator{components: components, layersCount: layersCount, maxDecomp: maxDecompositionLevels(components)}, nil
	case ProgressionRLCP:
		return &rlcpIterator{components: components, layersCount: layersCount, maxDecomp: maxDecompositionLevels(components)}, nil
	case ProgressionRPCL:
		return newRPCLIterator(components, layersCount), nil
	case ProgressionPCRL:
		return newPCRLIterator(components, layersCount), nil
	case ProgressionCPRL:
		return newCPRLIterator(components, layersCount), nil
	default:
		return nil, unsupportedf("progression order %d", order)
	}
}

// lrcpIterator implements §B.12.1.1, Layer-Resolution-Component-Position:
// every packet for layer 0 (across every resolution, component and
// precinct) before any packet for layer 1, and so on.
type lrcpIterator struct {
	components  []*componentDecode
	layersCount int
	maxDecomp   int
	l, r, i, k  int
}

func (it *lrcpIterator) nextPacket() (packet, bool) {
	for ; it.l < it.layersCount; it.l++ {
		for ; it.r <= it.maxDecomp; it.r++ {
			for ; it.i < len(it.components); it.i++ {
				comp := it.components[it.i]
				if it.r > comp.coding.DecompositionLevels {
					continue
				}
				ri := comp.resolutions[it.r]
				num := ri.precinctGrid.numPrecincts
				for it.k < num {
					p := createPacket(ri, it.k, it.l)
					it.k++
					return p, true
				}
				it.k = 0
			}
			it.i = 0
		}
		it.r = 0
	}
	return packet{}, false
}

// rlcpIterator implements §B.12.1.2, Resolution-Layer-Component-Position.
type rlcpIterator struct {
	components  []*componentDecode
	layersCount int
	maxDecomp   int
	r, l, i, k  int
}

func (it *rlcpIterator) nextPacket() (packet, bool) {
	for ; it.r <= it.maxDecomp; it.r++ {
		for ; it.l < it.layersCount; it.l++ {
			for ; it.i < len(it.components); it.i++ {
				comp := it.components[it.i]
				if it.r > comp.coding.DecompositionLevels {
					continue
				}
				ri := comp.resolutions[it.r]
				num := ri.precinctGrid.numPrecincts
				for it.k < num {
					p := createPacket(ri, it.k, it.l)
					it.k++
					return p, true
				}
				it.k = 0
			}
			it.i = 0
		}
		it.l = 0
	}
	return packet{}, false
}

// rpclIterator implements §B.12.1.3, Resolution-Position-Component-Layer.
type rpclIterator struct {
	components          []*componentDecode
	layersCount         int
	maxDecomp           int
	maxPrecinctsInLevel []int
	r, p, c, l          int
}

func newRPCLIterator(components []*componentDecode, layersCount int) *rpclIterator {
	maxDecomp := maxDecompositionLevels(components)
	maxPrecinctsInLevel := make([]int, maxDecomp+1)
	for r := 0; r <= maxDecomp; r++ {
		m := 0
		for _, comp := range components {
			if r < len(comp.resolutions) {
				m = max(m, comp.resolutions[r].precinctGrid.numPrecincts)
			}
		}
		maxPrecinctsInLevel[r] = m
	}
	return &rpclIterator{components: components, layersCount: layersCount, maxDecomp: maxDecomp, maxPrecinctsInLevel: maxPrecinctsInLevel}
}

func (it *rpclIterator) nextPacket() (packet, bool) {
	for ; it.r <= it.maxDecomp; it.r++ {
		for ; it.p < it.maxPrecinctsInLevel[it.r]; it.p++ {
			for ; it.c < len(it.components); it.c++ {
				comp := it.components[it.c]
				if it.r > comp.coding.DecompositionLevels {
					continue
				}
				ri := comp.resolutions[it.r]
				num := ri.precinctGrid.numPrecincts
				if it.p >= num {
					continue
				}
				for it.l < it.layersCount {
					p := createPacket(ri, it.p, it.l)
					it.l++
					return p, true
				}
				it.l = 0
			}
			it.c = 0
		}
		it.p = 0
	}
	return packet{}, false
}

// precinctSizeInImageScale is one resolution level's precinct size, in
// "image scale" units (its own precinct size scaled up by how many
// wavelet decomposition levels separate it from the finest resolution -
// this puts every resolution level's precincts on one common coordinate
// system so PCRL/CPRL can walk positions independently of resolution).
type precinctSizeInImageScale struct {
	width, height int
}

// componentPrecinctSizes is one component's per-resolution precinct
// sizes in image scale, plus that component's own minimum size and
// maximum precinct-grid extent across all its resolution levels.
type componentPrecinctSizes struct {
	resolutions            []precinctSizeInImageScale
	minWidth, minHeight    int
	maxNumWide, maxNumHigh int
}

// precinctSizes is getPrecinctSizesInImageScale's result: per-component
// sizes, plus the same minimums/maximums taken across every component -
// PCRL's iteration bounds and position-to-precinct mapping use the
// cross-component ones; CPRL (which iterates one component fully before
// moving to the next) uses each component's own.
type precinctSizes struct {
	components             []componentPrecinctSizes
	minWidth, minHeight    int
	maxNumWide, maxNumHigh int
}

func getPrecinctSizesInImageScale(components []*componentDecode) precinctSizes {
	result := precinctSizes{components: make([]componentPrecinctSizes, len(components))}
	first := true
	for c, comp := range components {
		decompLevels := comp.coding.DecompositionLevels
		sizePerRes := make([]precinctSizeInImageScale, decompLevels+1)
		cs := componentPrecinctSizes{resolutions: sizePerRes}
		scale := 1
		firstRes := true
		for r := decompLevels; r >= 0; r-- {
			ri := comp.resolutions[r]
			w := scale * ri.precinctGrid.width
			h := scale * ri.precinctGrid.height
			if firstRes || w < cs.minWidth {
				cs.minWidth = w
			}
			if firstRes || h < cs.minHeight {
				cs.minHeight = h
			}
			cs.maxNumWide = max(cs.maxNumWide, ri.precinctGrid.numWide)
			cs.maxNumHigh = max(cs.maxNumHigh, ri.precinctGrid.numHigh)
			sizePerRes[r] = precinctSizeInImageScale{width: w, height: h}
			scale <<= 1
			firstRes = false
		}
		result.components[c] = cs
		if first || cs.minWidth < result.minWidth {
			result.minWidth = cs.minWidth
		}
		if first || cs.minHeight < result.minHeight {
			result.minHeight = cs.minHeight
		}
		result.maxNumWide = max(result.maxNumWide, cs.maxNumWide)
		result.maxNumHigh = max(result.maxNumHigh, cs.maxNumHigh)
		first = false
	}
	return result
}

// getPrecinctIndexIfExist checks whether image-scale grid position
// (pxIndex, pyIndex) - scaled by (minWidth, minHeight), the finest
// granularity any resolution level's precincts align to - actually
// falls on one of resolution ri's own precinct boundaries, returning
// that precinct's index if so. See this file's doc comment on the one
// place this deliberately does not match pdf.js's own version.
func getPrecinctIndexIfExist(pxIndex, pyIndex int, sizeInImageScale precinctSizeInImageScale, minWidth, minHeight int, ri *resolutionInfo) (int, bool) {
	posX := pxIndex * minWidth
	posY := pyIndex * minHeight
	if posX%sizeInImageScale.width != 0 || posY%sizeInImageScale.height != 0 {
		return 0, false
	}
	col := posX / sizeInImageScale.width
	row := posY / sizeInImageScale.height
	return col + row*ri.precinctGrid.numWide, true
}

// pcrlIterator implements §B.12.1.4, Position-Component-Resolution-Layer.
type pcrlIterator struct {
	components      []*componentDecode
	layersCount     int
	sizes           precinctSizes
	l, r, c, px, py int
}

func newPCRLIterator(components []*componentDecode, layersCount int) *pcrlIterator {
	return &pcrlIterator{components: components, layersCount: layersCount, sizes: getPrecinctSizesInImageScale(components)}
}

func (it *pcrlIterator) nextPacket() (packet, bool) {
	for ; it.py < it.sizes.maxNumHigh; it.py++ {
		for ; it.px < it.sizes.maxNumWide; it.px++ {
			for ; it.c < len(it.components); it.c++ {
				comp := it.components[it.c]
				decompLevels := comp.coding.DecompositionLevels
				for ; it.r <= decompLevels; it.r++ {
					ri := comp.resolutions[it.r]
					sizeInImageScale := it.sizes.components[it.c].resolutions[it.r]
					k, exists := getPrecinctIndexIfExist(it.px, it.py, sizeInImageScale, it.sizes.minWidth, it.sizes.minHeight, ri)
					if !exists {
						continue
					}
					for it.l < it.layersCount {
						p := createPacket(ri, k, it.l)
						it.l++
						return p, true
					}
					it.l = 0
				}
				it.r = 0
			}
			it.c = 0
		}
		it.px = 0
	}
	return packet{}, false
}

// cprlIterator implements §B.12.1.5, Component-Position-Resolution-Layer.
type cprlIterator struct {
	components      []*componentDecode
	layersCount     int
	sizes           precinctSizes
	c, py, px, r, l int
}

func newCPRLIterator(components []*componentDecode, layersCount int) *cprlIterator {
	return &cprlIterator{components: components, layersCount: layersCount, sizes: getPrecinctSizesInImageScale(components)}
}

func (it *cprlIterator) nextPacket() (packet, bool) {
	for ; it.c < len(it.components); it.c++ {
		comp := it.components[it.c]
		compSizes := it.sizes.components[it.c]
		decompLevels := comp.coding.DecompositionLevels
		for ; it.py < compSizes.maxNumHigh; it.py++ {
			for ; it.px < compSizes.maxNumWide; it.px++ {
				for ; it.r <= decompLevels; it.r++ {
					ri := comp.resolutions[it.r]
					sizeInImageScale := compSizes.resolutions[it.r]
					k, exists := getPrecinctIndexIfExist(it.px, it.py, sizeInImageScale, compSizes.minWidth, compSizes.minHeight, ri)
					if !exists {
						continue
					}
					for it.l < it.layersCount {
						p := createPacket(ri, k, it.l)
						it.l++
						return p, true
					}
					it.l = 0
				}
				it.r = 0
			}
			it.px = 0
		}
		it.py = 0
	}
	return packet{}, false
}
