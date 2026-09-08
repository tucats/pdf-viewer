package function

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// maxType0Inputs bounds a Type 0 function's number of input dimensions
// (its /Domain array's length / 2, equivalently /Size's length). Eval's
// multilinear interpolation (see below) examines 2^(number of inputs)
// sample-grid corners per call, so this bound also caps that work at
// 2^8=256 corners - consistent with this project's "bounded work even on
// hostile input" policy (see the repository README's "Dependency and
// safety policy"). Every real-world Type 0 function this project's
// fixture corpus or the wider PDF ecosystem is expected to produce has 1
// or 2 inputs (a shading's parametric position, or occasionally a
// two-variable transfer function); 8 is generous headroom above that,
// not a realistic real-world value.
const maxType0Inputs = 8

// type0 implements a PDF Type 0 (sampled) function (7.10.2): a
// precomputed table of output samples over a regular m-dimensional input
// grid (m = len(size)), evaluated at an arbitrary input point by
// multilinear interpolation between the 2^m grid points surrounding it.
type type0 struct {
	domain []float64 // 2*m entries
	rng    []float64 // 2*n entries (required for Type 0, unlike Type 2/3)
	size   []int     // m entries, each >= 1
	bps    int       // BitsPerSample: 1, 2, 4, 8, 12, 16, 24, or 32
	encode []float64 // 2*m entries
	decode []float64 // 2*n entries

	samples []byte // the stream's decoded sample data, read as a continuous
	// bitstream (see sampleAt) - not byte-aligned per row/sample the way
	// internal/image's image sample data is; the specification does not
	// describe any padding for Type 0 function sample data the way it
	// does for image rows.
}

// parseType0 reads a Type 0 function's dictionary and decodes its
// stream's sample data through every filter its own dictionary names
// (stream.Dict and dict are the same dictionary - a stream's dictionary
// *is* the object dictionary - kept as two parameters only because
// parseSingle already has both in hand).
func parseType0(r Resolver, dict syntax.Dictionary, stream syntax.Stream) (Function, error) {
	domain, err := requiredNumberArray(r, dict, "Domain")
	if err != nil {
		return nil, err
	}
	if len(domain) == 0 || len(domain)%2 != 0 {
		return nil, pdferror.Malformedf("Type 0 function /Domain must have a positive even number of entries, has %d", len(domain))
	}
	m := len(domain) / 2
	if m > maxType0Inputs {
		return nil, pdferror.Unsupportedf("Type 0 function with %d inputs, exceeding this package's %d-input limit", m, maxType0Inputs)
	}

	rangeArr, err := requiredNumberArray(r, dict, "Range")
	if err != nil {
		return nil, err
	}
	if len(rangeArr) == 0 || len(rangeArr)%2 != 0 {
		return nil, pdferror.Malformedf("Type 0 function /Range must have a positive even number of entries, has %d", len(rangeArr))
	}
	n := len(rangeArr) / 2

	sizeFloats, err := requiredNumberArray(r, dict, "Size")
	if err != nil {
		return nil, err
	}
	if len(sizeFloats) != m {
		return nil, pdferror.Malformedf("Type 0 function /Size must have %d entries (one per input), has %d", m, len(sizeFloats))
	}
	size := make([]int, m)
	for i, v := range sizeFloats {
		iv := int(v + 0.5)
		if iv < 1 {
			return nil, pdferror.Malformedf("Type 0 function /Size entry %d must be a positive integer, got %v", i, v)
		}
		size[i] = iv
	}

	bpsObj, ok := dict["BitsPerSample"]
	if !ok {
		return nil, pdferror.Malformedf("Type 0 function has no required /BitsPerSample entry")
	}
	resolvedBps, err := resolveIfRef(r, bpsObj)
	if err != nil {
		return nil, err
	}
	bpsVal, ok := numberValue(resolvedBps)
	if !ok {
		return nil, pdferror.Malformedf("Type 0 function /BitsPerSample is not a number (found %T)", resolvedBps)
	}
	bps := int(bpsVal)
	switch bps {
	case 1, 2, 4, 8, 12, 16, 24, 32:
	default:
		return nil, pdferror.Malformedf("Type 0 function /BitsPerSample %d is not one of 1, 2, 4, 8, 12, 16, 24, 32", bps)
	}

	encode, err := optionalNumberArray(r, dict, "Encode")
	if err != nil {
		return nil, err
	}
	if encode == nil {
		// Default (7.10.2, Table 42): [0 Size0-1 0 Size1-1 ...] - i.e. the
		// input's own encoded range is simply its grid index range.
		encode = make([]float64, 2*m)
		for i := 0; i < m; i++ {
			encode[2*i], encode[2*i+1] = 0, float64(size[i]-1)
		}
	}
	if len(encode) != 2*m {
		return nil, pdferror.Malformedf("Type 0 function /Encode must have %d entries, has %d", 2*m, len(encode))
	}

	decode, err := optionalNumberArray(r, dict, "Decode")
	if err != nil {
		return nil, err
	}
	if decode == nil {
		// Default: identical to /Range.
		decode = append([]float64(nil), rangeArr...)
	}
	if len(decode) != 2*n {
		return nil, pdferror.Malformedf("Type 0 function /Decode must have %d entries, has %d", 2*n, len(decode))
	}

	samples, err := r.DecodeStream(stream)
	if err != nil {
		return nil, err
	}
	totalGridPoints := 1
	for _, s := range size {
		totalGridPoints *= s
	}
	needBits := totalGridPoints * n * bps
	if len(samples)*8 < needBits {
		return nil, pdferror.Malformedf("Type 0 function sample data has %d bytes, need at least %d bits (%d grid points x %d output(s) x %d bits/sample)", len(samples), needBits, totalGridPoints, n, bps)
	}

	return &type0{
		domain: domain, rng: rangeArr, size: size, bps: bps,
		encode: encode, decode: decode, samples: samples,
	}, nil
}

func (f *type0) NumInputs() int  { return len(f.size) }
func (f *type0) NumOutputs() int { return len(f.rng) / 2 }

// Eval implements 7.10.2's evaluation procedure: each input is clipped to
// its Domain, mapped through Encode into a real-valued grid coordinate
// (then clipped to the valid [0,Size-1] index range - the specification
// requires this second clip too, since Encode is caller-supplied data
// that could otherwise map outside the actual table), and multilinearly
// interpolated between the 2^m surrounding integer grid points before
// each raw output sample is mapped through Decode.
func (f *type0) Eval(inputs []float64) ([]float64, error) {
	m := len(f.size)
	if len(inputs) != m {
		return nil, pdferror.Malformedf("Type 0 function expects %d input(s), got %d", m, len(inputs))
	}
	n := f.NumOutputs()

	// gridCoord[i] is the (fractional) index into dimension i's sample
	// axis that input i maps to; e0[i]/e1[i] are the two integer indices
	// (equal, at either end of the axis, or when the coordinate lands
	// exactly on an integer) it lies between, and frac[i] is how far
	// between them (0 = exactly at e0, 1 = exactly at e1).
	gridCoord := make([]float64, m)
	e0 := make([]int, m)
	e1 := make([]int, m)
	frac := make([]float64, m)
	for i := 0; i < m; i++ {
		x := clip(inputs[i], f.domain[2*i], f.domain[2*i+1])
		g := interpolate(x, f.domain[2*i], f.domain[2*i+1], f.encode[2*i], f.encode[2*i+1])
		g = clip(g, 0, float64(f.size[i]-1))
		gridCoord[i] = g
		lo := int(math.Floor(g))
		if lo >= f.size[i]-1 {
			lo = f.size[i] - 1
		}
		hi := lo + 1
		if hi > f.size[i]-1 {
			hi = f.size[i] - 1
		}
		e0[i], e1[i] = lo, hi
		frac[i] = g - float64(lo)
	}

	// Accumulate a weighted sum over every corner of the m-dimensional
	// hypercube surrounding gridCoord - the standard multilinear
	// ("bilinear" in 2D, "trilinear" in 3D, ...) interpolation formula,
	// generalized to m dimensions by iterating corner as an m-bit number
	// where bit i selects e0[i] (0) or e1[i] (1) for that dimension.
	acc := make([]float64, n)
	corners := 1 << uint(m)
	idx := make([]int, m)
	for corner := 0; corner < corners; corner++ {
		weight := 1.0
		for i := 0; i < m; i++ {
			if corner&(1<<uint(i)) != 0 {
				idx[i] = e1[i]
				weight *= frac[i]
			} else {
				idx[i] = e0[i]
				weight *= 1 - frac[i]
			}
		}
		if weight == 0 {
			// A zero-weight corner (exactly on a grid line in every
			// dimension that matters) contributes nothing - skip the
			// sample fetch entirely, which also avoids any work at all
			// in the overwhelmingly common m=1 exact-hit case.
			continue
		}
		flat := flatIndex(idx, f.size)
		for j := 0; j < n; j++ {
			acc[j] += weight * float64(f.sampleAt(flat, j))
		}
	}

	maxRaw := float64((uint64(1) << uint(f.bps)) - 1)
	out := make([]float64, n)
	for j := 0; j < n; j++ {
		raw := acc[j]
		out[j] = safeFloat(interpolate(raw, 0, maxRaw, f.decode[2*j], f.decode[2*j+1]))
	}
	return clipToRange(out, f.rng), nil
}

// flatIndex converts a per-dimension grid index idx into a flat sample
// index, with dimension 0 varying fastest - the order 7.10.2 describes
// sample data as being stored in ("the first coordinate ... varies
// fastest").
func flatIndex(idx []int, size []int) int {
	flat := 0
	stride := 1
	for i := range idx {
		flat += idx[i] * stride
		stride *= size[i]
	}
	return flat
}

// sampleAt returns the raw (undecoded) sample value for output component
// output at flat grid index flatGridIndex, read as a BitsPerSample-wide,
// most-significant-bit-first field out of the sample data's bitstream -
// every grid point stores its n output values consecutively, so the bit
// offset is simply (grid index * n + output) * BitsPerSample. Reading
// past the end of f.samples returns 0 rather than panicking, matching
// this project's general tolerance for malformed/truncated input (see,
// for example, internal/image's identically-behaved bitReader).
func (f *type0) sampleAt(flatGridIndex, output int) uint64 {
	n := f.NumOutputs()
	bitOffset := (flatGridIndex*n + output) * f.bps
	var v uint64
	for i := 0; i < f.bps; i++ {
		bitPos := bitOffset + i
		byteIdx := bitPos / 8
		bitIdx := 7 - (bitPos % 8)
		var bit uint64
		if byteIdx < len(f.samples) {
			bit = uint64(f.samples[byteIdx]>>uint(bitIdx)) & 1
		}
		v = v<<1 | bit
	}
	return v
}
