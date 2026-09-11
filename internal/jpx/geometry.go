package jpx

// This file computes the geometry tier-2 packet parsing (packetheader.go)
// and packet iteration (progression.go) both need: how one tile's one
// component divides into resolution levels, how each resolution level's
// subbands divide into precincts, and how each precinct's code-blocks
// are numbered and grouped - ISO/IEC 15444-1 §B.2 (component mapping),
// §B.3 (tiling), §B.5 (resolution levels and subbands), §B.6 (precincts)
// and §B.7 (code-blocks), in that order. None of this decodes any
// sample data; it only establishes the coordinate system tier-1 (14c)
// and tier-2 both need to agree on for a given code-block to mean the
// same thing to both.
//
// Ported from Mozilla's pdf.js (jpx.js's calculateComponentDimensions/
// calculateTileGrids/getBlocksDimensions/buildPrecincts/buildCodeblocks/
// buildPackets - Apache License 2.0), the same cross-check approach
// tagtree.go's doc comment explains, restructured into Go types (pointer
// trees rather than pdf.js's ad hoc JavaScript objects) and integer-only
// arithmetic in place of pdf.js's few floating-point Math.ceil calls
// (exact for this package's bounded image dimensions - see
// maxReasonableDimension in siz.go - so no precision is lost either
// way; this file's versions just avoid float64 entirely on principle,
// matching this package's "integer-only where the standard's own
// arithmetic is integer-only" style elsewhere).

import "math/bits"

// ceilDivPos returns ceil(n/d) for any integer n and positive integer d,
// exactly (no floating point) - used everywhere this file needs
// "smallest whole grid unit that covers n", including for negative n
// (which arises from the half-pixel HL/LH/HH subband boundary formulas
// below; Go's / truncates toward zero, which for a negative numerator
// already equals the ceiling, so only a positive, nonzero remainder ever
// needs the +1 adjustment).
func ceilDivPos(n, d int) int {
	q := n / d
	if n%d > 0 {
		q++
	}
	return q
}

// log2Exact returns the base-2 exponent of x, which callers only ever
// invoke on values already established elsewhere to be exact powers of
// two (siz.go's CodeBlockWidth/CodeBlockHeight) - not a general ceiling
// or rounding log2.
func log2Exact(x int) int {
	return bits.Len(uint(x)) - 1
}

// log2Ceil returns ceil(log2(x)) for x > 0 (0 for x <= 0, matching
// pdf.js's own log2 helper, which packet.go's readCodingPasses and
// tagtree.go's tree-depth calculations are both ultimately derived
// from) - unlike log2Exact, x need not be a power of two.
func log2Ceil(x int) int {
	if x <= 0 {
		return 0
	}
	return bits.Len(uint(x - 1))
}

// subbandKind names one of a resolution level's subbands - LL only for
// resolution level 0 (the coarsest, containing the whole low-frequency
// thumbnail), HL/LH/LL for every level above it.
type subbandKind int

const (
	subbandLL subbandKind = iota
	subbandHL
	subbandLH
	subbandHH
)

// componentPixelBounds returns component c's pixel-area bounds within
// the reference grid (§B.2), independent of tiling: XRsiz/YRsiz above 1
// (subsampled components) are rejected here with ErrUnsupported, per
// siz.go's ComponentInfo doc comment on which sub-phase enforces that -
// this one, since it is the first point a subsampled component's pixel
// geometry actually needs computing.
func componentPixelBounds(h *Header, c int) (x0, y0, x1, y1 int, err error) {
	ci := h.Components[c]
	if ci.XRsiz != 1 || ci.YRsiz != 1 {
		return 0, 0, 0, 0, unsupportedf("component %d is subsampled (XRsiz=%d, YRsiz=%d); only unsubsampled (1,1) components are supported", c, ci.XRsiz, ci.YRsiz)
	}
	x0 = ceilDivPos(h.XOsiz, ci.XRsiz)
	x1 = ceilDivPos(h.Xsiz, ci.XRsiz)
	y0 = ceilDivPos(h.YOsiz, ci.YRsiz)
	y1 = ceilDivPos(h.Ysiz, ci.YRsiz)
	return x0, y0, x1, y1, nil
}

// tileGridBounds returns tile tileIndex's pixel-area bounds on the
// reference grid (§B.3), before any per-component subsampling. Tiles are
// numbered in raster order (Isot, matching TilePart.TileIndex): row q,
// column p, tileIndex = q*NumTilesX + p.
func tileGridBounds(h *Header, tileIndex int) (tx0, ty0, tx1, ty1 int) {
	p := tileIndex % h.NumTilesX
	q := tileIndex / h.NumTilesX
	tx0 = max(h.XTOsiz+p*h.XTsiz, h.XOsiz)
	ty0 = max(h.YTOsiz+q*h.YTsiz, h.YOsiz)
	tx1 = min(h.XTOsiz+(p+1)*h.XTsiz, h.Xsiz)
	ty1 = min(h.YTOsiz+(q+1)*h.YTsiz, h.Ysiz)
	return tx0, ty0, tx1, ty1
}

// tileComponentBounds returns tile tileIndex's bounds for component c,
// combining tileGridBounds with that component's own sampling (§B.3,
// "tile-components").
func tileComponentBounds(h *Header, tileIndex, c int) (tcx0, tcy0, tcx1, tcy1 int, err error) {
	tx0, ty0, tx1, ty1 := tileGridBounds(h, tileIndex)
	ci := h.Components[c]
	if ci.XRsiz != 1 || ci.YRsiz != 1 {
		return 0, 0, 0, 0, unsupportedf("component %d is subsampled (XRsiz=%d, YRsiz=%d); only unsubsampled (1,1) components are supported", c, ci.XRsiz, ci.YRsiz)
	}
	tcx0 = ceilDivPos(tx0, ci.XRsiz)
	tcy0 = ceilDivPos(ty0, ci.YRsiz)
	tcx1 = ceilDivPos(tx1, ci.XRsiz)
	tcy1 = ceilDivPos(ty1, ci.YRsiz)
	return tcx0, tcy0, tcx1, tcy1, nil
}

// precinctGrid is one resolution level's precinct partitioning (§B.6):
// how big one precinct is (in this resolution level's own grid units),
// how many precincts the level divides into along each axis, and (for
// mapping a code-block's position into a precinct number) how big a
// precinct is measured in its child subband's own coordinate system.
type precinctGrid struct {
	width, height                   int // precinct size, resolution-level grid units
	numWide, numHigh, numPrecincts  int
	widthInSubband, heightInSubband int
}

// codeBlockDimensions returns one resolution level's code-block size
// (xcb', ycb' in the standard's own notation - already the minimum of
// the coding style's nominal code-block size and this level's precinct
// size, per §B.7) as base-2 exponents, plus the precinct-size exponents
// (PPx, PPy) it was derived from.
func codeBlockDimensions(cs CodingStyle, resLevel int) (ppx, ppy, xcbPrime, ycbPrime int) {
	if cs.PrecinctsDefined {
		ppx = cs.PrecinctWidthExponents[resLevel]
		ppy = cs.PrecinctHeightExponents[resLevel]
	} else {
		ppx, ppy = defaultPrecinctExponent, defaultPrecinctExponent
	}
	xcb := log2Exact(cs.CodeBlockWidth)
	ycb := log2Exact(cs.CodeBlockHeight)
	if resLevel > 0 {
		xcbPrime, ycbPrime = min(xcb, ppx-1), min(ycb, ppy-1)
	} else {
		xcbPrime, ycbPrime = min(xcb, ppx), min(ycb, ppy)
	}
	return ppx, ppy, xcbPrime, ycbPrime
}

// buildPrecinctGrid computes resolution level resLevel's precinct
// partitioning (§B.6) from its own pixel bounds (trx0/try0/trx1/try1 -
// the resolution level's extent, not any one subband's) and the
// PPx/PPy exponents codeBlockDimensions derived.
func buildPrecinctGrid(resLevel, trx0, try0, trx1, try1, ppx, ppy int) precinctGrid {
	width := 1 << uint(ppx)
	height := 1 << uint(ppy)

	widthInSubbandExp, heightInSubbandExp := ppx, ppy
	if resLevel > 0 {
		widthInSubbandExp--
		heightInSubbandExp--
	}

	var numWide, numHigh int
	if trx1 > trx0 {
		numWide = ceilDivPos(trx1, width) - trx0/width
	}
	if try1 > try0 {
		numHigh = ceilDivPos(try1, height) - try0/height
	}

	return precinctGrid{
		width: width, height: height,
		numWide: numWide, numHigh: numHigh, numPrecincts: numWide * numHigh,
		widthInSubband:  1 << uint(widthInSubbandExp),
		heightInSubband: 1 << uint(heightInSubbandExp),
	}
}

// codeBlockInfo is one code-block: its position within its subband's
// code-block grid, which precinct it belongs to, and the running
// tier-2 state (Lblock, inclusion/zero-bit-plane status) packet header
// parsing accumulates across every layer's packets that touch it.
type codeBlockInfo struct {
	cbx, cby       int
	precinctNumber int
	precinct       *precinctState

	// tbx0/tby0/tbx1/tby1 are this code-block's own pixel bounds within
	// its subband's coordinate system (already clipped to the subband's
	// own extent by buildCodeBlocks - a code-block at a subband's edge
	// may be smaller than the nominal code-block size) - tier-1 (14c)
	// uses these to size and place its per-code-block sample arrays.
	tbx0, tby0, tbx1, tby1 int

	// Lblock starts at 3 (§B.10.7) and only ever grows, by however many
	// extra bits a later packet's header says its length field needed.
	Lblock int
	// included is true once this code-block has appeared in any layer's
	// packet - after which every later layer's packet uses a single raw
	// inclusion bit instead of the inclusion tag tree (§B.10.4).
	included      bool
	zeroBitPlanes int

	contributions []codeBlockContribution

	// samples is this code-block's tier-1 output (14c's decodeTileCoefficients),
	// nil until decoded - see codeBlockSamples' doc comment (tier1.go).
	samples *codeBlockSamples
}

// codeBlockContribution is one layer's worth of one code-block's
// compressed data, as tier-2 parsing locates it within a tile-part's
// byte range - tier-1 (14c) is what actually runs the MQ coder over it.
type codeBlockContribution struct {
	layer     int
	numPasses int
	data      []byte
}

// precinctState is the tag-tree state (and code-block index bounds)
// shared by every code-block a given precinct's subband contains -
// see tagtree.go's doc comment on why that sharing is the whole point
// of tag-tree coding. Lazily built the first time a packet's header
// parsing actually touches this precinct (packetheader.go), since a
// precinct with no code-block ever included needs no tree at all.
type precinctState struct {
	cbxMin, cbyMin, cbxMax, cbyMax int

	inclusionTree     *tagTreeInclusion
	zeroBitPlanesTree *tagTreeZeroBitPlanes
}

// subbandInfo is one resolution level's one subband: its pixel bounds
// (§B.5) and the code-blocks (§B.7) it divides into, grouped by
// precinct.
type subbandInfo struct {
	kind                   subbandKind
	tbx0, tby0, tbx1, tby1 int
	codeBlocks             []*codeBlockInfo
	precincts              map[int]*precinctState
}

// resolutionInfo is one tile-component's one resolution level: its
// pixel bounds, precinct partitioning, and subbands (one - LL - at
// level 0, three - HL, LH, HH - at every level above it).
type resolutionInfo struct {
	level                  int
	trx0, try0, trx1, try1 int
	precinctGrid           precinctGrid
	subbands               []*subbandInfo
}

// buildCodeBlocks divides subband sb (already given its pixel bounds)
// into code-blocks (§B.7) and groups them into sb.precincts by the
// precinct-number mapping §B.6's own commentary describes: a precinct
// partitions its resolution level's LL-equivalent area, and that
// partition is carried down into each child subband by simple
// coordinate halving - which is exactly what widthInSubband/
// heightInSubband (already halved for resLevel>0 by buildPrecinctGrid)
// being measured in the subband's own coordinate system achieves.
func buildCodeBlocks(sb *subbandInfo, pg precinctGrid, xcbPrime, ycbPrime int) {
	codeblockWidth := 1 << uint(xcbPrime)
	codeblockHeight := 1 << uint(ycbPrime)
	cbx0 := sb.tbx0 >> uint(xcbPrime)
	cby0 := sb.tby0 >> uint(ycbPrime)
	cbx1 := (sb.tbx1 + codeblockWidth - 1) >> uint(xcbPrime)
	cby1 := (sb.tby1 + codeblockHeight - 1) >> uint(ycbPrime)

	sb.precincts = make(map[int]*precinctState)
	for j := cby0; j < cby1; j++ {
		for i := cbx0; i < cbx1; i++ {
			tbx0 := codeblockWidth * i
			tby0 := codeblockHeight * j
			tbx0c := max(sb.tbx0, tbx0)
			tby0c := max(sb.tby0, tby0)
			tbx1c := min(sb.tbx1, tbx0+codeblockWidth)
			tby1c := min(sb.tby1, tby0+codeblockHeight)
			if tbx1c <= tbx0c || tby1c <= tby0c {
				// This grid cell does not actually overlap the subband
				// (only possible at the grid's own edges) - no
				// code-block exists here.
				continue
			}

			pi := (tbx0c - sb.tbx0) / pg.widthInSubband
			pj := (tby0c - sb.tby0) / pg.heightInSubband
			precinctNumber := pi + pj*pg.numWide

			cb := &codeBlockInfo{
				cbx: i, cby: j, precinctNumber: precinctNumber, Lblock: 3,
				tbx0: tbx0c, tby0: tby0c, tbx1: tbx1c, tby1: tby1c,
			}
			sb.codeBlocks = append(sb.codeBlocks, cb)

			p, ok := sb.precincts[precinctNumber]
			if !ok {
				p = &precinctState{cbxMin: i, cbyMin: j, cbxMax: i, cbyMax: j}
				sb.precincts[precinctNumber] = p
			} else {
				if i < p.cbxMin {
					p.cbxMin = i
				} else if i > p.cbxMax {
					p.cbxMax = i
				}
				if j < p.cbyMin {
					p.cbyMin = j
				} else if j > p.cbyMax {
					p.cbyMax = j
				}
			}
			cb.precinct = p
		}
	}
}

// buildResolutions constructs every resolution level and subband for
// one tile-component (§B.5), given that tile-component's pixel bounds
// and effective coding style (which may be a per-tile COD/COC override
// - see markers.go's Header.effectiveCoding).
func buildResolutions(tcx0, tcy0, tcx1, tcy1 int, cs CodingStyle) []*resolutionInfo {
	decompLevels := cs.DecompositionLevels
	resolutions := make([]*resolutionInfo, decompLevels+1)

	for r := 0; r <= decompLevels; r++ {
		scale := 1 << uint(decompLevels-r)
		ri := &resolutionInfo{
			level: r,
			trx0:  ceilDivPos(tcx0, scale), try0: ceilDivPos(tcy0, scale),
			trx1: ceilDivPos(tcx1, scale), try1: ceilDivPos(tcy1, scale),
		}
		ppx, ppy, xcbPrime, ycbPrime := codeBlockDimensions(cs, r)
		ri.precinctGrid = buildPrecinctGrid(r, ri.trx0, ri.try0, ri.trx1, ri.try1, ppx, ppy)

		if r == 0 {
			sb := &subbandInfo{
				kind: subbandLL,
				tbx0: ceilDivPos(tcx0, scale), tby0: ceilDivPos(tcy0, scale),
				tbx1: ceilDivPos(tcx1, scale), tby1: ceilDivPos(tcy1, scale),
			}
			buildCodeBlocks(sb, ri.precinctGrid, xcbPrime, ycbPrime)
			ri.subbands = []*subbandInfo{sb}
		} else {
			bscale := 1 << uint(decompLevels-r+1)
			hl := &subbandInfo{
				kind: subbandHL,
				tbx0: ceilDivPos(2*tcx0-bscale, 2*bscale), tby0: ceilDivPos(tcy0, bscale),
				tbx1: ceilDivPos(2*tcx1-bscale, 2*bscale), tby1: ceilDivPos(tcy1, bscale),
			}
			lh := &subbandInfo{
				kind: subbandLH,
				tbx0: ceilDivPos(tcx0, bscale), tby0: ceilDivPos(2*tcy0-bscale, 2*bscale),
				tbx1: ceilDivPos(tcx1, bscale), tby1: ceilDivPos(2*tcy1-bscale, 2*bscale),
			}
			hh := &subbandInfo{
				kind: subbandHH,
				tbx0: ceilDivPos(2*tcx0-bscale, 2*bscale), tby0: ceilDivPos(2*tcy0-bscale, 2*bscale),
				tbx1: ceilDivPos(2*tcx1-bscale, 2*bscale), tby1: ceilDivPos(2*tcy1-bscale, 2*bscale),
			}
			for _, sb := range []*subbandInfo{hl, lh, hh} {
				buildCodeBlocks(sb, ri.precinctGrid, xcbPrime, ycbPrime)
			}
			ri.subbands = []*subbandInfo{hl, lh, hh}
		}
		resolutions[r] = ri
	}
	return resolutions
}
