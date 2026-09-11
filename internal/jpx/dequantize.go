package jpx

import "math"

// This file implements ISO/IEC 15444-1 Annex E.1's inverse quantization
// procedure: turning tier-1's (14c) per-code-block magnitude/sign/
// bitsDecoded output into one real-valued (or, for a reversible
// transform, exactly-integer-valued - see synthesisLevel's doc comment)
// coefficient array per resolution level, ready for idwt.go's inverse
// wavelet transform. It is 14d's other half, alongside idwt.go.
//
// Ported from Mozilla's pdf.js's transformTile/copyCoefficients
// (jpx.js, Apache License 2.0; see doc.go's Provenance section for this
// package's general cross-check approach), restructured around this
// package's own geometry.go/tier1.go types.

// subbandGainLog2 is Table E.1's per-orientation subband gain: added to
// a component's bit depth to get a subband's nominal dynamic range Rb
// (Annex E.1's "the nominal range of a subband is the sum of the number
// of bits used to represent the corresponding component and a gain that
// depends on the subband"). Indexed by subbandKind's own constant
// values (LL, HL, LH, HH, in that order).
var subbandGainLog2 = [4]int{0, 1, 1, 2}

// copyCoefficients dequantizes every code-block subband sb has already
// been divided into (geometry.go) and writes the result into items, the
// full-size coefficient array for the resolution level sb belongs to
// (levelWidth x levelHeight). For any subband other than LL, this
// includes §F.3.3's interleaving: a subband's own (row, column)
// position maps to (2*row+rowBit, 2*column+colBit) in the level's own
// grid, rowBit/colBit chosen by orientation (LL/HL at even rows, LH/HH
// at odd; LL/LH at even columns, HL/HH at odd) so that idwt.go's
// synthesizeLevel need only interleave the LL band itself before
// filtering. A code-block never included in any layer (cb.samples ==
// nil) leaves its positions at their zero-value default, matching a
// coefficient that decoded to exact zero.
func copyCoefficients(items []float64, levelWidth, levelHeight int, sb *subbandInfo, delta float64, mb int, reversible bool) error {
	x0, y0 := sb.tbx0, sb.tby0
	width := sb.tbx1 - sb.tbx0

	colBit, rowBit := 0, 0
	if sb.kind == subbandHL || sb.kind == subbandHH {
		colBit = 1
	}
	if sb.kind == subbandLH || sb.kind == subbandHH {
		rowBit = 1
	}
	interleave := sb.kind != subbandLL

	magnitudeCorrection := 0.5
	if reversible {
		magnitudeCorrection = 0
	}

	for _, cb := range sb.codeBlocks {
		samples := cb.samples
		if samples == nil {
			continue
		}
		blockWidth := cb.tbx1 - cb.tbx0
		offset := (cb.tbx0 - x0) + (cb.tby0-y0)*width
		position := 0
		for j := 0; j < samples.height; j++ {
			row := offset / width
			// levelOffset locates this subband row's interleaved
			// destination row (2*row+rowBit) and column parity
			// (colBit) within items, in one term - see this
			// function's doc comment.
			levelOffset := 2*row*(levelWidth-width) + colBit + rowBit*levelWidth
			for k := 0; k < blockWidth; k++ {
				n := samples.magnitude[position]
				if n != 0 {
					v := (float64(n) + magnitudeCorrection) * delta
					if samples.sign[position] != 0 {
						v = -v
					}
					nb := samples.bitsDecoded[position]

					pos := offset
					if interleave {
						pos = levelOffset + offset*2
					}
					switch {
					case reversible && nb >= mb:
						items[pos] = v
					case nb <= mb:
						items[pos] = v * float64(int64(1)<<uint(mb-nb))
					default:
						return malformedf("code-block decoded %d bit-plane(s), more than its subband's %d nominal bit-plane(s)", nb, mb)
					}
				}
				offset++
				position++
			}
			offset += width - blockWidth
		}
	}
	return nil
}

// dequantizeComponent builds one full coefficient array per resolution
// level of comp (whose code-blocks decodeTileCoefficients, 14c, has
// already decoded): level 0's array holds only the LL subband, not
// interleaved; every level above holds its HL/LH/HH subbands already
// interleaved into their final grid positions, with the corresponding
// LL positions left zero for idwt.go's synthesizeLevel to fill in from
// the previous level's own reconstructed output.
//
// Step-size selection follows comp.quant.Style (Annex E.1, formula
// E-5 for the QuantScalarDerived case - only the LL band's step size is
// given, and every other subband's is derived from it and how many
// levels separate it from the LL band; QuantNone and
// QuantScalarExpounded both give every subband's step size explicitly,
// in resolution-then-subband order, matching how StepSizes is
// documented to be laid out).
func dequantizeComponent(comp *componentDecode) ([]synthesisLevel, error) {
	reversible := comp.coding.Transform == Transform5x3
	derived := comp.quant.Style == QuantScalarDerived
	guardBits := comp.quant.GuardBits

	levels := make([]synthesisLevel, len(comp.resolutions))
	b := 0
	for i, res := range comp.resolutions {
		width := res.trx1 - res.trx0
		height := res.try1 - res.try0
		items := make([]float64, width*height)

		for _, sb := range res.subbands {
			var mantissa, exponent int
			if derived {
				if len(comp.quant.StepSizes) < 1 {
					return nil, malformedf("scalar-derived quantization style declares no step size")
				}
				mantissa = comp.quant.StepSizes[0].Mantissa
				exponent = comp.quant.StepSizes[0].Exponent
				if i > 0 {
					exponent += 1 - i
				}
			} else {
				if b >= len(comp.quant.StepSizes) {
					return nil, malformedf("quantization style declares %d step size(s), not enough for %d subband(s)", len(comp.quant.StepSizes), b+1)
				}
				mantissa = comp.quant.StepSizes[b].Mantissa
				exponent = comp.quant.StepSizes[b].Exponent
				b++
			}

			delta := 1.0
			if !reversible {
				delta = math.Ldexp(1+float64(mantissa)/2048, comp.bitDepth+subbandGainLog2[sb.kind]-exponent)
			}
			mb := guardBits + exponent - 1

			if err := copyCoefficients(items, width, height, sb, delta, mb, reversible); err != nil {
				return nil, err
			}
		}

		levels[i] = synthesisLevel{width: width, height: height, items: items}
	}
	return levels, nil
}
