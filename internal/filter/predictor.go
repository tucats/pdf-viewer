package filter

import (
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements the PNG and TIFF predictors that FlateDecode and
// LZWDecode streams may additionally apply before compression (most
// commonly for image sample data, though the specification does not
// restrict predictors to images specifically). A predictor rewrites each
// row of sample data as a difference from a neighboring value, which
// compresses much better than raw sample data for typical (smoothly
// varying) images - but is meaningless to anything downstream unless it
// is reversed first, which is this file's job.
//
// The /Predictor DecodeParms entry selects which predictor was used: 1
// (the default) means none; 2 selects the TIFF predictor; any of 10-15
// selects the PNG predictor (the specific value beyond "10 or more"
// carries no meaning to a PDF reader - PNG's own per-row filter-type
// byte, embedded in the data itself, is what actually varies row to
// row - so every value in that range is handled identically here).

// applyPredictor reverses whatever predictor parms's /Predictor entry
// selects (default 1, meaning none, in which case data is returned
// unchanged). /Colors (default 1), /BitsPerComponent (default 8), and
// /Columns (default 1) describe the shape of the sample data the
// predictor was applied to, exactly as their names describe an image's
// sample data - because a predictor's whole job is undoing a
// row-by-row, sample-by-sample transformation, it cannot be reversed
// without knowing that shape.
func applyPredictor(data []byte, parms syntax.Dictionary) ([]byte, error) {
	predictor := intParm(parms, "Predictor", 1)
	if predictor <= 1 {
		return data, nil
	}

	colors := intParm(parms, "Colors", 1)
	bpc := intParm(parms, "BitsPerComponent", 8)
	columns := intParm(parms, "Columns", 1)
	// maxPredictorDimension bounds /Colors and /Columns well above any
	// real image this project anticipates (a many-thousand-column image
	// with dozens of color components), specifically so that
	// colors*bpc*columns below cannot overflow a 64-bit int and wrap
	// around to a small, negative, or otherwise bogus row length - a
	// hostile /DecodeParms dictionary is exactly the kind of input this
	// guard exists for; see the "Bounded decompression" section of the
	// package doc comment.
	const maxPredictorDimension = 1 << 28
	if colors <= 0 || columns <= 0 || colors > maxPredictorDimension || columns > maxPredictorDimension {
		return nil, pdferror.Malformedf("predictor: /Colors and /Columns must be in (0, %d] (got %d, %d)", maxPredictorDimension, colors, columns)
	}
	switch bpc {
	case 1, 2, 4, 8, 16:
	default:
		return nil, pdferror.Malformedf("predictor: unsupported /BitsPerComponent %d", bpc)
	}

	if predictor == 2 {
		return applyTIFFPredictor(data, colors, bpc, columns)
	}
	// Any value 10-15 (or, tolerantly, any other value greater than 2)
	// selects the PNG predictor; PDF producers are only meant to use
	// 10-15, but nothing about how PNG per-row filtering works depends
	// on which specific value in that family was named.
	return applyPNGPredictor(data, colors, bpc, columns)
}

// bytesPerPixelForPredictor returns the "bpp" distance predictors use to
// find the sample one whole pixel to the left: the number of bytes
// occupied by one pixel's worth of components, rounded up, and never
// less than 1 - even a 1-bit grayscale image (bpp would compute to 0)
// still predicts from a whole byte to its left, per the PNG
// specification this filter mode is borrowed from.
func bytesPerPixelForPredictor(colors, bpc int) int {
	bpp := (colors*bpc + 7) / 8
	if bpp < 1 {
		bpp = 1
	}
	return bpp
}

// --- TIFF predictor (Predictor 2) ------------------------------------

// applyTIFFPredictor reverses horizontal differencing: each sample
// (there are colors samples per pixel, bpc bits wide) was replaced by
// its difference (modulo 2^bpc) from the same color component's sample
// in the previous pixel of the same row; the first pixel in each row is
// unchanged. Rows do not predict from one another (unlike the PNG "Up"
// filter below), matching the TIFF specification this predictor is
// named after.
func applyTIFFPredictor(data []byte, colors, bpc, columns int) ([]byte, error) {
	rowBytes := (colors*bpc*columns + 7) / 8
	if rowBytes == 0 {
		return data, nil
	}
	if len(data)%rowBytes != 0 {
		return nil, pdferror.Malformedf("TIFF predictor: data length %d is not a multiple of row length %d", len(data), rowBytes)
	}

	out := make([]byte, len(data))
	copy(out, data)
	numRows := len(data) / rowBytes

	switch bpc {
	case 8:
		for r := 0; r < numRows; r++ {
			row := out[r*rowBytes : (r+1)*rowBytes]
			for i := colors; i < len(row); i++ {
				row[i] += row[i-colors]
			}
		}
	case 16:
		for r := 0; r < numRows; r++ {
			row := out[r*rowBytes : (r+1)*rowBytes]
			for i := 2 * colors; i+1 < len(row); i += 2 {
				prevHi, prevLo := row[i-2*colors], row[i-2*colors+1]
				prev := uint16(prevHi)<<8 | uint16(prevLo)
				cur := uint16(row[i])<<8 | uint16(row[i+1])
				sum := cur + prev
				row[i], row[i+1] = byte(sum>>8), byte(sum)
			}
		}
	default: // 1, 2, or 4: sub-byte samples, handled bit by bit.
		max := uint32(1) << uint(bpc)
		for r := 0; r < numRows; r++ {
			row := out[r*rowBytes : (r+1)*rowBytes]
			prev := make([]uint32, colors)
			bitPos := 0
			for col := 0; col < columns; col++ {
				for c := 0; c < colors; c++ {
					v := readBitsMSB(row, bitPos, bpc)
					v = (v + prev[c]) % max
					prev[c] = v
					writeBitsMSB(row, bitPos, bpc, v)
					bitPos += bpc
				}
			}
		}
	}
	return out, nil
}

// readBitsMSB and writeBitsMSB read/write an n-bit (n <= 32) big-endian
// bitfield starting at bit offset bitPos within row, with bit 0 of each
// byte being its most significant bit - the packing PDF (and TIFF) use
// for sub-byte sample data.
func readBitsMSB(row []byte, bitPos, n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		byteIdx := (bitPos + i) / 8
		bitIdx := 7 - (bitPos+i)%8
		bit := (row[byteIdx] >> uint(bitIdx)) & 1
		v = v<<1 | uint32(bit)
	}
	return v
}

func writeBitsMSB(row []byte, bitPos, n int, v uint32) {
	for i := 0; i < n; i++ {
		bit := byte((v >> uint(n-1-i)) & 1)
		byteIdx := (bitPos + i) / 8
		bitIdx := uint(7 - (bitPos+i)%8)
		if bit == 1 {
			row[byteIdx] |= 1 << bitIdx
		} else {
			row[byteIdx] &^= 1 << bitIdx
		}
	}
}

// --- PNG predictor (Predictor 10-15) ----------------------------------

// applyPNGPredictor reverses PNG-style per-row filtering: unlike the
// TIFF predictor, each row in the data is prefixed with one extra filter
// -type byte (0 None, 1 Sub, 2 Up, 3 Average, or 4 Paeth - see the PNG
// specification, section on filtering) chosen independently per row by
// the encoder, and filtering (unlike TIFF's) may reference the previous
// *row*, not just the previous pixel in the same row. This predictor
// always operates byte-wise, even for sub-byte bit depths - see
// bytesPerPixelForPredictor.
func applyPNGPredictor(data []byte, colors, bpc, columns int) ([]byte, error) {
	rowBytes := (colors*bpc*columns + 7) / 8
	stride := rowBytes + 1 // +1 for the leading filter-type byte
	if stride <= 1 {
		return data, nil
	}
	if len(data)%stride != 0 {
		return nil, pdferror.Malformedf("PNG predictor: data length %d is not a multiple of row stride %d (1 filter-type byte + %d data bytes)", len(data), stride, rowBytes)
	}
	bpp := bytesPerPixelForPredictor(colors, bpc)
	numRows := len(data) / stride

	out := make([]byte, numRows*rowBytes)
	prev := make([]byte, rowBytes) // implicit all-zero row above the first

	for r := 0; r < numRows; r++ {
		in := data[r*stride : (r+1)*stride]
		filterType := in[0]
		raw := in[1:]
		cur := out[r*rowBytes : (r+1)*rowBytes]

		for i := 0; i < rowBytes; i++ {
			var a, b, c byte
			if i >= bpp {
				a = cur[i-bpp]
				c = prev[i-bpp]
			}
			b = prev[i]

			var recon byte
			switch filterType {
			case 0:
				recon = raw[i]
			case 1:
				recon = raw[i] + a
			case 2:
				recon = raw[i] + b
			case 3:
				recon = raw[i] + byte((int(a)+int(b))/2)
			case 4:
				recon = raw[i] + paeth(a, b, c)
			default:
				return nil, pdferror.Malformedf("PNG predictor: invalid row filter type %d", filterType)
			}
			cur[i] = recon
		}
		prev = cur
	}
	return out, nil
}

// paeth implements the PNG specification's Paeth predictor: it picks
// whichever of a (left), b (above), or c (above-left) is numerically
// closest to a+b-c, breaking ties in favor of a, then b.
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
