package jpx

import "encoding/binary"

// This file parses the QCD (Quantization Default) and QCC (Quantization
// Component) marker segments (ISO/IEC 15444-1 A.6.4/A.6.5): how each
// wavelet subband's coefficients are scaled back from the integers the
// entropy coder produces into the values the inverse wavelet transform
// expects (14d's job; this file only extracts the numbers that step
// needs). Like COD/COC, QCD gives the codestream-wide default and QCC
// overrides it for one component.

// QuantStyle selects which of the three ways SPqcd/SPqcc's step-size
// values are laid out and interpreted (Table A.28).
type QuantStyle int

const (
	// QuantNone means no quantization is applied at all (every step size
	// is implicitly 1) - this is the style a reversible (5/3, lossless)
	// codestream always uses, since dequantization would otherwise
	// perturb values the wavelet transform needs to reconstruct exactly.
	// Confusingly, SPqcd still carries one byte per subband in this
	// style (an exponent only, no mantissa) - see StepSizes' doc
	// comment - which 14d needs to determine how many extra bits of
	// precision each subband's coefficients were encoded with.
	QuantNone QuantStyle = 0
	// QuantScalarDerived means only the LL (lowest-frequency) subband's
	// step size is given explicitly; every other subband's step size is
	// derived from it by a formula (Annex E.1) depending on how many
	// decomposition levels separate that subband from the LL band. Used
	// only with the irreversible (9/7, lossy) transform.
	QuantScalarDerived QuantStyle = 1
	// QuantScalarExpounded means every subband's step size is given
	// explicitly - the most common style for irreversible-transform
	// codestreams in practice, since it lets an encoder tune each
	// subband's quantization independently.
	QuantScalarExpounded QuantStyle = 2
)

// QuantStep is one subband's step-size exponent and mantissa, exactly as
// SPqcd/SPqcc encodes them (Annex E.1's Table). 14d combines these with
// a subband's resolution level and the component's bit depth to recover
// the actual floating-point (or, for QuantNone, purely integer) step
// size.
type QuantStep struct {
	Exponent int
	// Mantissa is always 0 for QuantNone (whose entries carry no
	// mantissa bits at all - see QuantNone's doc comment), and an 11-bit
	// value (0-2047) otherwise.
	Mantissa int
}

// QuantizationStyle is one component's (or the codestream default's)
// quantization parameters, parsed from a QCD or QCC marker segment.
type QuantizationStyle struct {
	Style QuantStyle
	// GuardBits is how many extra most-significant bit-planes were
	// reserved across every subband to absorb overflow from the wavelet
	// transform and the multiple component transform (Annex E.1) -
	// needed by 14c/14d to know which bit-plane a code-block's decoded
	// bits actually start at.
	GuardBits int
	// StepSizes holds one entry per subband for QuantNone/
	// QuantScalarExpounded (in the standard's own subband order: this
	// codestream's LL subband first, then each decomposition level's
	// HL/LH/HH from the coarsest level to the finest), or exactly one
	// entry (the LL band's) for QuantScalarDerived.
	StepSizes []QuantStep
}

// parseQuantStepSizes reads SPqcd/SPqcc's step-size entries: one byte
// each (exponent only) for QuantNone, two bytes each (exponent plus an
// 11-bit mantissa) otherwise. The number of entries is derived from how
// many bytes remain, rather than from the coding style's decomposition
// level count, so this file's parsing has no ordering dependency on
// COD/COC having already been parsed first (real codestreams don't
// guarantee an order between the two marker types beyond both preceding
// the first SOT).
func parseQuantStepSizes(content []byte, style QuantStyle) ([]QuantStep, error) {
	entrySize := 2
	if style == QuantNone {
		entrySize = 1
	}
	if len(content)%entrySize != 0 || len(content) == 0 {
		return nil, malformedf("quantization step-size data is %d bytes, not a multiple of the %d-byte entry size", len(content), entrySize)
	}
	n := len(content) / entrySize
	if style == QuantScalarDerived && n != 1 {
		return nil, malformedf("quantization style is scalar-derived, which carries exactly one step size, but %d were found", n)
	}

	steps := make([]QuantStep, n)
	for i := 0; i < n; i++ {
		if entrySize == 1 {
			steps[i] = QuantStep{Exponent: int(content[i] >> 3)}
		} else {
			v := binary.BigEndian.Uint16(content[i*2:])
			steps[i] = QuantStep{Exponent: int(v >> 11), Mantissa: int(v & 0x07FF)}
		}
	}
	return steps, nil
}

// parseQCD parses a QCD marker segment's content (the bytes after the
// 2-byte Lqcd length field).
func parseQCD(content []byte) (QuantizationStyle, error) {
	if len(content) < 1 {
		return QuantizationStyle{}, malformedf("QCD marker segment is empty, want at least 1 byte")
	}
	sqcd := content[0]
	style := QuantStyle(sqcd & 0x1F)
	if style > QuantScalarExpounded {
		return QuantizationStyle{}, malformedf("QCD declares quantization style %d, want 0, 1 or 2", style)
	}
	guardBits := int(sqcd >> 5)

	steps, err := parseQuantStepSizes(content[1:], style)
	if err != nil {
		return QuantizationStyle{}, err
	}
	return QuantizationStyle{Style: style, GuardBits: guardBits, StepSizes: steps}, nil
}

// parseQCC parses a QCC marker segment's content, given csiz (as
// parseCOC needs it, to size the leading component-index field).
func parseQCC(content []byte, csiz int) (componentIndex int, qs QuantizationStyle, err error) {
	idxLen := 1
	if csiz >= 257 {
		idxLen = 2
	}
	if len(content) < idxLen+1 {
		return 0, QuantizationStyle{}, malformedf("QCC marker segment is %d bytes, want at least %d", len(content), idxLen+1)
	}
	if idxLen == 1 {
		componentIndex = int(content[0])
	} else {
		componentIndex = int(binary.BigEndian.Uint16(content[0:2]))
	}
	if componentIndex >= csiz {
		return 0, QuantizationStyle{}, malformedf("QCC names component %d, but the codestream has only %d component(s)", componentIndex, csiz)
	}

	sqcc := content[idxLen]
	style := QuantStyle(sqcc & 0x1F)
	if style > QuantScalarExpounded {
		return 0, QuantizationStyle{}, malformedf("QCC declares quantization style %d, want 0, 1 or 2", style)
	}
	guardBits := int(sqcc >> 5)

	steps, err := parseQuantStepSizes(content[idxLen+1:], style)
	if err != nil {
		return 0, QuantizationStyle{}, err
	}
	return componentIndex, QuantizationStyle{Style: style, GuardBits: guardBits, StepSizes: steps}, nil
}
