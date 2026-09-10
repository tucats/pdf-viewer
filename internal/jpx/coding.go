package jpx

import "encoding/binary"

// This file parses the COD (Coding style Default) and COC (Coding style
// Component) marker segments (ISO/IEC 15444-1 A.6.1/A.6.2), which
// together describe *how* each component's samples were transformed and
// entropy-coded: which wavelet filter, how many resolution levels, how
// big a code-block is, which order packets appear in, and so on. COD
// gives the codestream-wide default; a COC segment overrides it for one
// specific component (e.g. an image using RGB might code the luma-like
// component in fewer decomposition levels than the chroma-like ones -
// unusual, but the standard permits it, so this package tracks the
// override per component rather than assuming COD always applies).

// ProgressionOrder is one of the five ways JPEG 2000 allows a
// codestream's packets (each packet holding one quality layer's worth of
// data for one resolution level, one component, and one spatial
// precinct - packets themselves are 14b's concern) to be interleaved.
// A decoder cannot choose which order an encoder used - all five are
// equally valid, real codestreams - so this package supports every one
// of them; see doc.go's Scope section.
type ProgressionOrder int

// The five progression orders, using the names and numeric values (0-4)
// the standard itself assigns them (Table A.16). The letters name, in
// outermost-to-innermost order, what varies slowest to fastest as
// packets are read: L=layer, R=resolution level, C=component, P=position
// (precinct).
const (
	ProgressionLRCP ProgressionOrder = 0
	ProgressionRLCP ProgressionOrder = 1
	ProgressionRPCL ProgressionOrder = 2
	ProgressionPCRL ProgressionOrder = 3
	ProgressionCPRL ProgressionOrder = 4
)

// WaveletTransform selects which of the two wavelet filters JPEG 2000
// Part 1 defines was used for the discrete wavelet transform - see
// doc.go's Scope section on why both are supported unconditionally.
type WaveletTransform int

const (
	// Transform9x7 is the irreversible Daubechies (9,7) filter, used for
	// lossy compression - decoding it always involves floating-point
	// arithmetic, so it can never reconstruct the original samples
	// exactly even given every bit the encoder produced.
	Transform9x7 WaveletTransform = 0
	// Transform5x3 is the reversible LeGall (5,3) filter, an
	// integer-only transform that can reconstruct the original samples
	// exactly if every coded bit is decoded (used for lossless
	// compression, though an encoder may also choose it for lossy
	// compression at a lower ratio than 9,7 typically achieves).
	Transform5x3 WaveletTransform = 1
)

// Code-block style flag bits (SPcod/SPcoc's 4th byte), named after the
// standard's own flag letters (Table A.19).
const (
	// codeBlockSelectiveBypass permits the "raw" (non-arithmetic-coded)
	// bypass mode in a code-block's later coding passes - an encoder
	// optimization this package does not need to treat specially at the
	// header-parsing level (14c's tier-1 decoder reads this flag to know
	// whether to expect bypassed passes).
	codeBlockSelectiveBypass        = 0x01
	codeBlockResetContext           = 0x02
	codeBlockTermination            = 0x04
	codeBlockVerticallyCausal       = 0x08
	codeBlockPredictableTermination = 0x10
	codeBlockSegmentationSymbols    = 0x20
)

// CodingStyle is one component's (or the codestream default's) coding
// parameters, parsed from a COD or COC marker segment.
type CodingStyle struct {
	// PrecinctsDefined is Scod/Scoc bit 0: true if PrecinctWidthExponents/
	// PrecinctHeightExponents below were explicitly given per resolution
	// level, false if every resolution level uses the standard's default
	// (a precinct as large as the whole resolution level - in effect, no
	// spatial partitioning at all within a tile-component).
	PrecinctsDefined bool
	// UseSOPMarkers and UseEPHMarkers (Scod bits 1 and 2; COC does not
	// carry these - they are only meaningful in COD) record whether
	// optional per-packet marker bytes are present in the packet data
	// tier-2 parsing (14b) will need to step over.
	UseSOPMarkers, UseEPHMarkers bool

	// The following four fields come from COD's SGcod (Scod's
	// companion field group) and are meaningless on a COC-derived
	// CodingStyle, which inherits them from the codestream's COD instead
	// (parseCOC leaves them zero-valued; Header.codingStyleFor never
	// needs them from an override, since progression order, layer count,
	// and the multi-component transform are codestream-wide properties
	// in every real encoder despite technically living in COD's
	// per-tile-part-repeatable segment).
	ProgressionOrder ProgressionOrder
	NumLayers        int
	// MultipleComponentTransform is true if this tile applies the
	// reversible or irreversible component transform (RCT/ICT - which
	// one is implied by Transform below) across its first three
	// components before entropy coding each one - see doc.go's Scope
	// section.
	MultipleComponentTransform bool

	// DecompositionLevels is how many times the wavelet transform was
	// applied (0 means the image was not wavelet-transformed at all -
	// unusual but valid - so there is only ever one resolution level).
	DecompositionLevels int
	// CodeBlockWidth and CodeBlockHeight are the pixel dimensions
	// (already converted from the standard's stored "exponent minus 2"
	// form) of one entropy-coding code-block - the unit tier-1 coding
	// (14c) operates on.
	CodeBlockWidth, CodeBlockHeight int
	// CodeBlockStyle is the raw flag byte - see the codeBlock* bit
	// constants above; 14c interprets these bits directly rather than
	// this type exploding them into named booleans, since tier-1 coding
	// is the only place that reads them.
	CodeBlockStyle byte
	Transform      WaveletTransform

	// PrecinctWidthExponents and PrecinctHeightExponents hold one entry
	// per resolution level (DecompositionLevels+1 of them, resolution
	// level 0 - the coarsest, the "thumbnail" - first), each the base-2
	// exponent of that level's precinct width/height in pixels. When
	// PrecinctsDefined is false these are synthesized as the standard's
	// default (exponent 15, i.e. 32768 pixels - larger than any real
	// resolution level, so it never actually partitions one).
	PrecinctWidthExponents, PrecinctHeightExponents []int
}

// defaultPrecinctExponent is the standard's own default precinct size
// (2^15 pixels) applied whenever Scod/Scoc bit 0 is clear - see
// CodingStyle.PrecinctsDefined's doc comment.
const defaultPrecinctExponent = 15

// parseCOD parses a COD marker segment's content (the bytes after the
// 2-byte Lcod length field).
func parseCOD(content []byte) (CodingStyle, error) {
	if len(content) < 5 {
		return CodingStyle{}, malformedf("COD marker segment is %d bytes, want at least 5", len(content))
	}
	scod := content[0]
	sgcod := content[1:5]

	cs := CodingStyle{
		PrecinctsDefined:           scod&0x01 != 0,
		UseSOPMarkers:              scod&0x02 != 0,
		UseEPHMarkers:              scod&0x04 != 0,
		ProgressionOrder:           ProgressionOrder(sgcod[0]),
		NumLayers:                  int(binary.BigEndian.Uint16(sgcod[1:3])),
		MultipleComponentTransform: sgcod[3] != 0,
	}
	if cs.ProgressionOrder > ProgressionCPRL {
		return CodingStyle{}, malformedf("COD declares progression order %d, outside the valid 0-4 range", sgcod[0])
	}
	if cs.NumLayers < 1 {
		return CodingStyle{}, malformedf("COD declares %d quality layers, want at least 1", cs.NumLayers)
	}

	if err := parseSPcod(content[5:], cs.PrecinctsDefined, &cs); err != nil {
		return CodingStyle{}, err
	}
	return cs, nil
}

// parseCOC parses a COC marker segment's content, given csiz (the
// codestream's total component count, from SIZ, which determines
// whether the leading component-index field is 1 or 2 bytes wide).
// componentIndex is returned separately since it is what the caller
// (markers.go) uses as the ComponentCoding map key, not part of
// CodingStyle itself.
func parseCOC(content []byte, csiz int) (componentIndex int, cs CodingStyle, err error) {
	idxLen := 1
	if csiz >= 257 {
		idxLen = 2
	}
	if len(content) < idxLen+1 {
		return 0, CodingStyle{}, malformedf("COC marker segment is %d bytes, want at least %d", len(content), idxLen+1)
	}
	if idxLen == 1 {
		componentIndex = int(content[0])
	} else {
		componentIndex = int(binary.BigEndian.Uint16(content[0:2]))
	}
	if componentIndex >= csiz {
		return 0, CodingStyle{}, malformedf("COC names component %d, but the codestream has only %d component(s)", componentIndex, csiz)
	}

	scoc := content[idxLen]
	cs.PrecinctsDefined = scoc&0x01 != 0
	if err := parseSPcod(content[idxLen+1:], cs.PrecinctsDefined, &cs); err != nil {
		return 0, CodingStyle{}, err
	}
	return componentIndex, cs, nil
}

// parseSPcod parses the SPcod/SPcoc field group shared, byte-for-byte,
// by both COD and COC (only what precedes it in the marker segment
// differs between the two) - see A.6.1/A.6.2's own tables, which lay the
// two segments' encodings out side by side for exactly this reason.
func parseSPcod(content []byte, precinctsDefined bool, cs *CodingStyle) error {
	const fixedLen = 5
	if len(content) < fixedLen {
		return malformedf("SPcod/SPcoc field group is %d bytes, want at least %d", len(content), fixedLen)
	}

	decompLevels := int(content[0])
	if decompLevels > 32 {
		return malformedf("declares %d decomposition levels, want at most 32", decompLevels)
	}
	cs.DecompositionLevels = decompLevels

	cbWidthExp := int(content[1]) + 2
	cbHeightExp := int(content[2]) + 2
	if cbWidthExp < 2 || cbWidthExp > 10 || cbHeightExp < 2 || cbHeightExp > 10 {
		return malformedf("declares code-block dimensions 2^%d x 2^%d, outside the valid 2^2..2^10 range", cbWidthExp, cbHeightExp)
	}
	if cbWidthExp+cbHeightExp > 12 {
		// The standard additionally requires xcb' + ycb' <= 12 (i.e. a
		// code-block may not exceed 4096 samples) - see A.6.1's SPcod
		// table, note on the code-block exponent fields.
		return malformedf("code-block dimensions 2^%d x 2^%d exceed the standard's combined limit (exponents must sum to at most 12)", cbWidthExp, cbHeightExp)
	}
	cs.CodeBlockWidth = 1 << uint(cbWidthExp)
	cs.CodeBlockHeight = 1 << uint(cbHeightExp)

	cs.CodeBlockStyle = content[3]

	switch content[4] {
	case 0:
		cs.Transform = Transform9x7
	case 1:
		cs.Transform = Transform5x3
	default:
		return malformedf("declares wavelet transform code %d, want 0 (9-7 irreversible) or 1 (5-3 reversible)", content[4])
	}

	pos := fixedLen
	numLevels := decompLevels + 1
	cs.PrecinctWidthExponents = make([]int, numLevels)
	cs.PrecinctHeightExponents = make([]int, numLevels)
	if !precinctsDefined {
		for i := range cs.PrecinctWidthExponents {
			cs.PrecinctWidthExponents[i] = defaultPrecinctExponent
			cs.PrecinctHeightExponents[i] = defaultPrecinctExponent
		}
		return nil
	}

	if len(content) < pos+numLevels {
		return malformedf("declares explicit precinct sizes for %d resolution level(s) but only %d byte(s) remain", numLevels, len(content)-pos)
	}
	for i := 0; i < numLevels; i++ {
		b := content[pos+i]
		cs.PrecinctWidthExponents[i] = int(b & 0x0F)
		cs.PrecinctHeightExponents[i] = int(b >> 4)
	}
	return nil
}
