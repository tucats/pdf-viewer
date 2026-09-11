// This file implements the inverse discrete wavelet transform (ISO/IEC
// 15444-1 Annex F, "Wavelet transformation"): both defined synthesis
// filters (§F.3.8's 5/3 reversible integer filter and the 9/7
// irreversible filter its own annex documents by reference to the
// standard's Table F.4/F.5 lifting coefficients), plus the
// row-then-column 2D application (§F.3.4 HOR_SR, §F.3.5 VER_SR) and the
// per-resolution-level recursion (§F.3.1) that reassembles one
// tile-component's full sample array from dequantize.go's per-level
// coefficient arrays. Together with that file, this is 14d.
//
// Ported from Mozilla's pdf.js's Transform/IrreversibleTransform/
// ReversibleTransform classes (jpx.js, Apache License 2.0 - see doc.go's
// Provenance section for this package's general cross-check approach),
// restructured from pdf.js's own in-place Float32Array buffers and
// manually loop-unrolled filter steps (a JavaScript-engine-specific
// performance trick that does not change the arithmetic) into ordinary
// Go loops over []float64 - one extra bit of precision headroom this
// package takes deliberately, since nothing here is performance-critical
// enough to need pdf.js's Float32Array footprint.
package jpx

// waveletPadding is how many samples idwt.go extends a row or column by
// on each side before filtering (§F.3.7's symmetric extension) - large
// enough for the 9/7 filter's own support (the wider of the two), and
// harmlessly more than the 5/3 filter needs.
const waveletPadding = 4

// extendSymmetric performs §F.3.7's whole-sample symmetric boundary
// extension on buf[offset:offset+size], the in-bounds samples a
// horizontal or vertical filter pass is about to run over: for each of
// waveletPadding positions on either side, mirrors the in-bounds sample
// that many positions in, without repeating the boundary sample itself
// (buf[offset-1] gets buf[offset+1], not buf[offset]). buf must have at
// least waveletPadding samples of headroom on both sides of
// [offset, offset+size); synthesizeLevel's row/column buffers are always
// sized exactly size+2*waveletPadding for this reason.
func extendSymmetric(buf []float64, offset, size int) {
	for k := 1; k <= waveletPadding; k++ {
		buf[offset-k] = buf[offset+k]
		buf[offset+size-1+k] = buf[offset+size-1-k]
	}
}

// filter53 applies the 5/3 reversible synthesis filter (§F.3.8, the
// LeGall filter) in place to buf[offset-waveletPadding:offset+length+
// waveletPadding], already symmetrically extended. Every step here is
// exact integer arithmetic performed in float64 (safe: this package's
// bounded component bit depths, guard bits, and decomposition levels -
// see maxReasonableDimension and maxTier1BitPlanes - keep every
// intermediate value far inside float64's 52-bit exact-integer range);
// Go's ">>" on a signed integer is an arithmetic (floor) shift, matching
// the standard's own floor-division definition of this filter exactly.
func filter53(buf []float64, offset, length int) {
	half := length / 2

	// "Update": even-indexed (low-pass) samples absorb their two
	// neighboring odd-indexed (high-pass) samples' contribution back
	// out.
	j := offset
	for n := 0; n < half+1; n++ {
		buf[j] -= float64((int64(buf[j-1]) + int64(buf[j+1]) + 2) >> 2)
		j += 2
	}

	// "Predict": odd-indexed samples are restored from their now-final
	// even-indexed neighbors.
	j = offset + 1
	for n := 0; n < half; n++ {
		buf[j] += float64((int64(buf[j-1]) + int64(buf[j+1])) >> 1)
		j += 2
	}
}

// The 9/7 irreversible filter's lifting-step coefficients (§F.3.8,
// Table F.4) and the two scaling constants (K, its reciprocal) Table
// F.5's own normalization step applies.
const (
	filter97Alpha = -1.586134342059924
	filter97Beta  = -0.052980118572961
	filter97Gamma = 0.882911075530934
	filter97Delta = 0.443506852043971
	filter97K     = 1.230174104914001
)

// filter97 applies the 9/7 irreversible synthesis filter (§F.3.8) in
// place, the same buffer layout filter53 expects. Unlike filter53, every
// step here is genuinely floating-point - the filter coefficients above
// are irrational-valued approximations - so even a bit-exact tier-1
// decode can never reconstruct the original samples exactly, only
// approximately (the "irreversible" in this transform's name).
func filter97(buf []float64, offset, length int) {
	half := length / 2
	kInv := 1 / filter97K

	// Step 2: undo Table F.5's K/K_ normalization on the odd-indexed
	// (high-pass) samples.
	j := offset - 3
	for n := 0; n < half+4; n++ {
		buf[j] *= kInv
		j += 2
	}

	// Steps 1 and 3 combined: scale the even-indexed samples by K and
	// undo the delta lifting step.
	j = offset - 2
	for n := 0; n < half+3; n++ {
		buf[j] = filter97K*buf[j] - filter97Delta*buf[j-1] - filter97Delta*buf[j+1]
		j += 2
	}

	// Step 4: undo the gamma lifting step.
	j = offset - 1
	for n := 0; n < half+2; n++ {
		buf[j] -= filter97Gamma*buf[j-1] + filter97Gamma*buf[j+1]
		j += 2
	}

	// Step 5: undo the beta lifting step.
	j = offset
	for n := 0; n < half+1; n++ {
		buf[j] -= filter97Beta*buf[j-1] + filter97Beta*buf[j+1]
		j += 2
	}

	// Step 6: undo the alpha lifting step.
	if half != 0 {
		j = offset + 1
		for n := 0; n < half; n++ {
			buf[j] -= filter97Alpha*buf[j-1] + filter97Alpha*buf[j+1]
			j += 2
		}
	}
}

// synthesisLevel is one resolution level's coefficient (before every
// level but the last has been synthesized) or reconstructed (after) 2D
// sample array, at that level's own pixel size (trx1-trx0 by
// try1-try0 - geometry.go's resolutionInfo). Every entry is an exact
// integer for a reversible-transform component (dequantize.go's delta
// is always 1 there) and a real number otherwise; dequantizeComponent's
// doc comment describes how level 0's array differs from every level
// above it.
type synthesisLevel struct {
	width, height int
	items         []float64
}

// synthesizeLevel performs one §F.3.1 inverse-transform iteration:
// interleaves ll's samples into hlLhHh's own LL-band positions (§F.3.3 -
// the even row, even column positions of hlLhHh's grid, which
// dequantizeComponent left zero for exactly this purpose), then filters
// hlLhHh's now-complete grid in place, row by row (§F.3.4) and then
// column by column (§F.3.5), producing this resolution level's fully
// reconstructed samples - which becomes the next iteration's ll, or, on
// the last resolution level, reconstructTileComponent's final result.
//
// u0, v0 are the tile-component's own pixel offset (componentDecode's
// tcx0/tcy0): needed only for the degenerate width==1 or height==1 case,
// where §F.3.4/F.3.5 define the "filter" as a single conditional halving
// keyed on whether that offset is odd, rather than the general lifting
// steps (which need at least one neighbor on each side to mean
// anything).
func synthesizeLevel(reversible bool, ll, hlLhHh synthesisLevel, u0, v0 int) synthesisLevel {
	width, height := hlLhHh.width, hlLhHh.height
	items := hlLhHh.items
	if width == 0 || height == 0 {
		// Only reachable from a degenerate (zero-extent) resolution
		// level at a tile's own edge - nothing to interleave or
		// filter; returning early also avoids the row/column buffers
		// below being sized too small to extend (waveletPadding needs
		// at least one real sample to mirror against).
		return synthesisLevel{width: width, height: height, items: items}
	}

	for i := 0; i < ll.height; i++ {
		base := i * 2 * width
		for j := 0; j < ll.width; j++ {
			items[base+2*j] = ll.items[i*ll.width+j]
		}
	}

	filter := filter53
	if !reversible {
		filter = filter97
	}

	// §F.3.4 HOR_SR: one row at a time.
	if width == 1 {
		if u0&1 != 0 {
			for v := 0; v < height; v++ {
				items[v*width] *= 0.5
			}
		}
	} else {
		row := make([]float64, width+2*waveletPadding)
		for v := 0; v < height; v++ {
			copy(row[waveletPadding:], items[v*width:v*width+width])
			extendSymmetric(row, waveletPadding, width)
			filter(row, waveletPadding, width)
			copy(items[v*width:v*width+width], row[waveletPadding:waveletPadding+width])
		}
	}

	// §F.3.5 VER_SR: one column at a time.
	if height == 1 {
		if v0&1 != 0 {
			for u := 0; u < width; u++ {
				items[u] *= 0.5
			}
		}
	} else {
		col := make([]float64, height+2*waveletPadding)
		for u := 0; u < width; u++ {
			for v := 0; v < height; v++ {
				col[waveletPadding+v] = items[v*width+u]
			}
			extendSymmetric(col, waveletPadding, height)
			filter(col, waveletPadding, height)
			for v := 0; v < height; v++ {
				items[v*width+u] = col[waveletPadding+v]
			}
		}
	}

	return synthesisLevel{width: width, height: height, items: items}
}

// reconstructedComponent is 14d's final output for one tile-component:
// its full reconstructed sample array, still real-valued (not yet
// rounded or clamped) and not yet DC-level-shifted or passed through any
// multiple component transform - 14e's job, along with compositing every
// component into a final image - plus the pixel offset (left, top) it
// belongs at within the tile.
type reconstructedComponent struct {
	left, top     int
	width, height int
	items         []float64
}

// reconstructTileComponent runs 14d's whole pipeline for one
// tile-component whose tier-1 coefficients decodeTileCoefficients (14c)
// has already decoded: dequantize.go's dequantizeComponent builds each
// resolution level's own coefficient array, and this file's
// synthesizeLevel folds them together one level at a time (§F.3.1's
// "ll = iterate(ll, next level)" recursion), coarsest to finest.
func reconstructTileComponent(comp *componentDecode) (*reconstructedComponent, error) {
	levels, err := dequantizeComponent(comp)
	if err != nil {
		return nil, err
	}

	reversible := comp.coding.Transform == Transform5x3
	ll := levels[0]
	for i := 1; i < len(levels); i++ {
		ll = synthesizeLevel(reversible, ll, levels[i], comp.tcx0, comp.tcy0)
	}

	return &reconstructedComponent{
		left: comp.tcx0, top: comp.tcy0,
		width: ll.width, height: ll.height,
		items: ll.items,
	}, nil
}

// reconstructTile runs 14d over every component decodeTileTier1 (14c)
// decoded for one tile. Not yet reachable from internal/filter - 14e
// (the multiple component transform, DC level shifting, and tile
// compositing) is what turns this sub-phase's per-component output into
// a finished image, and 14f is what wires the whole chain up to
// internal/filter's JPXDecode case.
func reconstructTile(components []*componentDecode) ([]*reconstructedComponent, error) {
	result := make([]*reconstructedComponent, len(components))
	for i, comp := range components {
		r, err := reconstructTileComponent(comp)
		if err != nil {
			return nil, err
		}
		result[i] = r
	}
	return result, nil
}

// decodeTile ties 14b/14c's decodeTileTier1 to this file's
// reconstructTile for one tile: tier-2 packet parsing and tier-1 entropy
// decoding, followed by 14d's dequantization and inverse wavelet
// transform. See reconstructTile's doc comment on what still isn't done
// once this returns.
func decodeTile(h *Header, codestream []byte, tileIndex int) ([]*reconstructedComponent, error) {
	components, err := decodeTileTier1(h, codestream, tileIndex)
	if err != nil {
		return nil, err
	}
	return reconstructTile(components)
}
