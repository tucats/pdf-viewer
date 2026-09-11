package jpx

import "encoding/binary"

// This file parses the SIZ marker segment (ISO/IEC 15444-1 A.5.1), the
// first marker segment after SOC in every JPEG 2000 codestream. SIZ
// describes the overall image geometry - not just its pixel dimensions,
// but the "reference grid" the whole codestream is defined against, how
// that grid is divided into tiles, and each color component's bit depth
// and (for components that were subsampled before encoding, e.g. chroma
// channels in a YCbCr-like space) its sampling density relative to that
// grid.
//
// # Why there is a separate "reference grid" at all
//
// PDF images (and most other image formats) describe a single width and
// height with every component sampled at that same resolution. JPEG 2000
// instead defines one reference grid, an image area within it (XOsiz/
// YOsiz/Xsiz/Ysiz - most files set the offsets to zero and the image area
// to the full grid, but the standard allows an image to occupy only part
// of the grid), and lets each component declare its own sampling density
// via XRsiz/YRsiz (e.g. a chroma component sampled at half the luma
// resolution has XRsiz=2). This package does not implement decoding a
// component whose XRsiz/YRsiz differs from 1 (see ComponentInfo's doc
// comment) since no real-world PDF-embedded JPX sample motivating this
// phase uses it, but the fields are still parsed and recorded so that
// case can be detected and reported rather than silently mishandled.

// ComponentInfo describes one image component's sample format, as
// declared by the SIZ marker segment.
type ComponentInfo struct {
	// BitDepth is how many bits this component's samples were encoded
	// with, in the range 1-38 (the standard's own limit; this package
	// additionally bounds it well below that - see parseSIZ).
	BitDepth int
	// Signed is true if this component's samples are signed integers
	// (rare in practice - almost every real image uses unsigned
	// samples, letting a decoder skip a sign-extension step).
	Signed bool
	// XRsiz and YRsiz are this component's horizontal and vertical
	// sampling separation relative to the reference grid: a value of 1
	// (by far the most common, and the only value this package's
	// decoding sub-phases (14b onward) implement) means the component
	// is sampled at full reference-grid resolution. A value above 1
	// means the component was subsampled (e.g. chroma subsampling) and
	// needs upsampling this package does not perform - see Header's own
	// doc comment on which decoding sub-phase enforces that.
	XRsiz, YRsiz int
}

// Header is everything this package's structural parsing sub-phase
// (14a) extracts from a codestream's main header: overall geometry,
// tiling, per-component sample format, and the default (plus any
// per-component override) coding style and quantization style every
// later decoding sub-phase needs. It deliberately does not yet include
// any decoded pixel data - see doc.go's development plan.
type Header struct {
	// Xsiz, Ysiz is the reference grid's size; XOsiz, YOsiz is the
	// image area's offset within that grid. The image's own pixel
	// dimensions are Xsiz-XOsiz by Ysiz-YOsiz (Width/Height below).
	Xsiz, Ysiz    int
	XOsiz, YOsiz  int
	Width, Height int

	// XTsiz, YTsiz is the size of one reference tile; XTOsiz, YTOsiz is
	// the offset of the first tile's top-left corner from the grid
	// origin. A single-tile image (common for smaller images) simply
	// has a tile at least as large as the whole image area.
	XTsiz, YTsiz   int
	XTOsiz, YTOsiz int
	// NumTilesX, NumTilesY are derived from the above: how many tiles
	// the image is divided into along each axis.
	NumTilesX, NumTilesY int

	// Components holds one entry per image component, in codestream
	// order (component 0 is conventionally red/luma/gray, and so on).
	Components []ComponentInfo

	// DefaultCoding and DefaultQuant are this codestream's COD and QCD
	// marker segments: the coding and quantization style every
	// component uses unless overridden. ComponentCoding and
	// ComponentQuant hold any per-component overrides from COC/QCC
	// marker segments, keyed by component index; a component with no
	// entry in these maps uses the default.
	DefaultCoding   CodingStyle
	ComponentCoding map[int]CodingStyle
	DefaultQuant    QuantizationStyle
	ComponentQuant  map[int]QuantizationStyle

	// TileParts lists every tile-part this codestream's structure was
	// walked to find, in the order they appear in the byte stream - see
	// markers.go's ParseHeader and the TilePart type's own doc comment.
	TileParts []TilePart
}

// codingStyleFor returns the effective coding style for component c:
// its COC override if one was declared, otherwise the codestream's
// default (COD) style.
func (h *Header) codingStyleFor(c int) CodingStyle {
	if cs, ok := h.ComponentCoding[c]; ok {
		return cs
	}
	return h.DefaultCoding
}

// quantStyleFor returns the effective quantization style for component
// c, the same way codingStyleFor does for coding style.
func (h *Header) quantStyleFor(c int) QuantizationStyle {
	if qs, ok := h.ComponentQuant[c]; ok {
		return qs
	}
	return h.DefaultQuant
}

// effectiveTileDefaultCoding returns the coding style tile tileIndex
// uses as its own default: the last tile-part-scoped COD override any
// of that tile's tile-parts declared (ISO/IEC 15444-1 A.4.2 permits any
// tile-part to carry one, not only the first), or the codestream-wide
// default if none did. This is the source of progression order, layer
// count, and the multiple component transform flag for the whole tile -
// per CodingStyle's own doc comment, those three fields are only ever
// meaningful on a COD-derived style, never a COC override, so callers
// needing them (progression.go) should use this rather than
// effectiveCoding for any specific component.
func (h *Header) effectiveTileDefaultCoding(tileIndex int) CodingStyle {
	result := h.DefaultCoding
	for i := range h.TileParts {
		if tp := &h.TileParts[i]; tp.TileIndex == tileIndex && tp.TileCoding != nil {
			result = *tp.TileCoding
		}
	}
	return result
}

// effectiveCoding returns the coding style tile tileIndex, component c
// should use for that component's own wavelet/code-block/precinct
// parameters: that tile's own COC override for c if any of its
// tile-parts declared one, else that tile's own COD override if any of
// its tile-parts declared one (applied uniformly, the same "tile
// default" semantics the main header's own COD/COC pair has - a tile
// that repeats COD but not COC for c is documented by this package as
// intending its new COD to be c's effective style too, not as falling
// back further to the main header's own per-component COC, since the
// standard does not clearly specify layering across the two header
// scopes and no real-world sample motivates a different reading), else
// the codestream-wide effective style (codingStyleFor, which already
// applies the main header's own COC-over-COD priority).
func (h *Header) effectiveCoding(tileIndex, c int) CodingStyle {
	var tileDefault *CodingStyle
	var tileComponent *CodingStyle
	for i := range h.TileParts {
		tp := &h.TileParts[i]
		if tp.TileIndex != tileIndex {
			continue
		}
		if tp.TileCoding != nil {
			tileDefault = tp.TileCoding
		}
		if cs, ok := tp.TileComponentCoding[c]; ok {
			tileComponent = &cs
		}
	}
	switch {
	case tileComponent != nil:
		return *tileComponent
	case tileDefault != nil:
		return *tileDefault
	default:
		return h.codingStyleFor(c)
	}
}

// effectiveQuant is effectiveCoding's counterpart for quantization
// style, with the same tile-COC-then-tile-COD-then-codestream-default
// layering (and the same documented scope decision on how the two
// header scopes combine).
func (h *Header) effectiveQuant(tileIndex, c int) QuantizationStyle {
	var tileDefault *QuantizationStyle
	var tileComponent *QuantizationStyle
	for i := range h.TileParts {
		tp := &h.TileParts[i]
		if tp.TileIndex != tileIndex {
			continue
		}
		if tp.TileQuant != nil {
			tileDefault = tp.TileQuant
		}
		if qs, ok := tp.TileComponentQuant[c]; ok {
			tileComponent = &qs
		}
	}
	switch {
	case tileComponent != nil:
		return *tileComponent
	case tileDefault != nil:
		return *tileDefault
	default:
		return h.quantStyleFor(c)
	}
}

// maxReasonableDimension bounds Xsiz/Ysiz/tile sizes against a hostile
// or corrupt file claiming an absurd image size, the same "bounded work"
// policy this project applies throughout (see internal/image's
// maxDimension, sized identically for consistency at the layer where
// JPX's decoded pixels eventually meet the rest of the image pipeline).
const maxReasonableDimension = 1 << 20

// parseSIZ parses a SIZ marker segment's content (the bytes strictly
// after the 2-byte Lsiz length field, which the caller has already read
// and used to size content exactly).
func parseSIZ(content []byte) (*Header, error) {
	const fixedLen = 36 // everything up to and including Csiz
	if len(content) < fixedLen {
		return nil, malformedf("SIZ marker segment is %d bytes, want at least %d", len(content), fixedLen)
	}

	h := &Header{}
	// content[0:2] is Rsiz (codestream capability/profile) - not
	// checked, since this package's own scope (see doc.go) is what
	// determines what it can decode, not what a profile field claims.
	h.Xsiz = int(binary.BigEndian.Uint32(content[2:6]))
	h.Ysiz = int(binary.BigEndian.Uint32(content[6:10]))
	h.XOsiz = int(binary.BigEndian.Uint32(content[10:14]))
	h.YOsiz = int(binary.BigEndian.Uint32(content[14:18]))
	h.XTsiz = int(binary.BigEndian.Uint32(content[18:22]))
	h.YTsiz = int(binary.BigEndian.Uint32(content[22:26]))
	h.XTOsiz = int(binary.BigEndian.Uint32(content[26:30]))
	h.YTOsiz = int(binary.BigEndian.Uint32(content[30:34]))
	csiz := int(binary.BigEndian.Uint16(content[34:36]))

	if err := validateGeometry(h); err != nil {
		return nil, err
	}
	h.Width = h.Xsiz - h.XOsiz
	h.Height = h.Ysiz - h.YOsiz
	h.NumTilesX = (h.Xsiz - h.XTOsiz + h.XTsiz - 1) / h.XTsiz
	h.NumTilesY = (h.Ysiz - h.YTOsiz + h.YTsiz - 1) / h.YTsiz

	if csiz <= 0 || csiz > 16384 {
		// 16384 is the standard's own limit (Csiz is a 14-bit-plus field
		// in spirit, though encoded as a full 16-bit value); no real
		// image remotely approaches it, so this bound only ever rejects
		// corrupt input.
		return nil, malformedf("SIZ marker segment declares %d components, outside the valid 1-16384 range", csiz)
	}
	const perComponentLen = 3
	want := fixedLen + csiz*perComponentLen
	if len(content) != want {
		return nil, malformedf("SIZ marker segment is %d bytes, want %d for %d component(s)", len(content), want, csiz)
	}

	h.Components = make([]ComponentInfo, csiz)
	for i := 0; i < csiz; i++ {
		off := fixedLen + i*perComponentLen
		ssiz := content[off]
		depth := int(ssiz&0x7F) + 1
		if depth < 1 || depth > 38 {
			return nil, malformedf("SIZ component %d declares bit depth %d, outside the valid 1-38 range", i, depth)
		}
		h.Components[i] = ComponentInfo{
			BitDepth: depth,
			Signed:   ssiz&0x80 != 0,
			XRsiz:    int(content[off+1]),
			YRsiz:    int(content[off+2]),
		}
		if h.Components[i].XRsiz < 1 || h.Components[i].YRsiz < 1 {
			return nil, malformedf("SIZ component %d declares sampling separation (%d,%d), must be at least (1,1)", i, h.Components[i].XRsiz, h.Components[i].YRsiz)
		}
	}

	return h, nil
}

// validateGeometry checks the size and offset fields SIZ just filled in
// for the basic ordering and bounds constraints the standard requires
// (e.g. an image area cannot start past where it ends), catching a
// corrupt or hostile file before any later code tries to derive an
// allocation size from these numbers.
func validateGeometry(h *Header) error {
	switch {
	case h.Xsiz <= 0 || h.Ysiz <= 0:
		return malformedf("SIZ declares non-positive image grid size %dx%d", h.Xsiz, h.Ysiz)
	case h.Xsiz > maxReasonableDimension || h.Ysiz > maxReasonableDimension:
		return unsupportedf("SIZ declares a %dx%d image grid, exceeding this package's %d-pixel-per-axis limit", h.Xsiz, h.Ysiz, maxReasonableDimension)
	case h.XOsiz < 0 || h.YOsiz < 0 || h.XOsiz >= h.Xsiz || h.YOsiz >= h.Ysiz:
		return malformedf("SIZ image area offset (%d,%d) is outside the reference grid %dx%d", h.XOsiz, h.YOsiz, h.Xsiz, h.Ysiz)
	case h.XTsiz <= 0 || h.YTsiz <= 0:
		return malformedf("SIZ declares non-positive tile size %dx%d", h.XTsiz, h.YTsiz)
	case h.XTOsiz < 0 || h.YTOsiz < 0 || h.XTOsiz > h.XOsiz || h.YTOsiz > h.YOsiz:
		return malformedf("SIZ tile grid offset (%d,%d) must be non-negative and no greater than the image area offset (%d,%d)", h.XTOsiz, h.YTOsiz, h.XOsiz, h.YOsiz)
	case h.XTOsiz+h.XTsiz <= h.XOsiz || h.YTOsiz+h.YTsiz <= h.YOsiz:
		// A.5.1 additionally requires XTOsiz+XTsiz > XOsiz (and the same
		// for Y): the tile grid's very first column/row must actually
		// reach the image area's own origin, not stop short of it. Without
		// this check, tileGridBounds' tx0 (clamped up to XOsiz) can exceed
		// its own tx1 (the first tile's unclamped right edge) for tile
		// column/row 0, handing a negative width/height down to every
		// later computation that assumes tx1 >= tx0 - caught by fuzzing
		// (FuzzDecode) as a makeslice panic in dequantizeComponent, well
		// downstream of where the real defect (an unvalidated geometry
		// field) actually is.
		return malformedf("SIZ tile grid's first column/row (offset (%d,%d), size %dx%d) does not reach the image area's own origin (%d,%d)", h.XTOsiz, h.YTOsiz, h.XTsiz, h.YTsiz, h.XOsiz, h.YOsiz)
	}
	return nil
}
