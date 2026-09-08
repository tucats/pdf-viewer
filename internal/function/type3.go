package function

import (
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// type3 implements a PDF Type 3 (stitching) function (7.10.4): a single
// input x selects one of k subfunctions by which subinterval of /Domain
// it falls in (the boundaries between subintervals given by /Bounds),
// remaps x linearly into that subfunction's own expected input range (via
// /Encode), and evaluates it. This is how a PDF gradient with more than
// two color stops is normally expressed: k Type 2 functions, one per
// stop-to-stop interval, stitched together into a single function that
// still presents as "one input, N outputs" to whatever calls it (a
// shading, or a Separation/DeviceN tint transform).
type type3 struct {
	domain   [2]float64
	fns      []Function
	bounds   []float64 // len(fns)-1 entries, in non-decreasing order
	encode   []float64 // 2*len(fns) entries
	rangeArr []float64 // optional
}

// parseType3 reads a Type 3 function's dictionary. /Domain, /Functions,
// /Bounds, and /Encode are all required by the specification's function
// dictionary table for this type - unlike Type 2, none of Type 3's own
// entries has a documented default, since there would be no sensible
// default for "how many subfunctions" or "where do they meet".
func parseType3(r Resolver, dict syntax.Dictionary) (Function, error) {
	domain, err := requiredNumberArray(r, dict, "Domain")
	if err != nil {
		return nil, err
	}
	if len(domain) != 2 {
		return nil, pdferror.Malformedf("Type 3 function /Domain must have 2 entries, has %d", len(domain))
	}

	fnsObj, ok := dict["Functions"]
	if !ok {
		return nil, pdferror.Malformedf("Type 3 function has no required /Functions entry")
	}
	resolvedFns, err := resolveIfRef(r, fnsObj)
	if err != nil {
		return nil, err
	}
	fnArr, ok := resolvedFns.(syntax.Array)
	if !ok {
		return nil, pdferror.Malformedf("Type 3 function /Functions is not an array (found %T)", resolvedFns)
	}
	if len(fnArr) == 0 {
		return nil, pdferror.Malformedf("Type 3 function /Functions is empty")
	}
	k := len(fnArr)
	fns := make([]Function, k)
	for i, elem := range fnArr {
		fn, err := Parse(r, elem)
		if err != nil {
			return nil, err
		}
		fns[i] = fn
	}

	bounds, err := requiredNumberArray(r, dict, "Bounds")
	if err != nil {
		return nil, err
	}
	if len(bounds) != k-1 {
		return nil, pdferror.Malformedf("Type 3 function /Bounds must have %d entries (one less than /Functions), has %d", k-1, len(bounds))
	}

	encode, err := requiredNumberArray(r, dict, "Encode")
	if err != nil {
		return nil, err
	}
	if len(encode) != 2*k {
		return nil, pdferror.Malformedf("Type 3 function /Encode must have %d entries (2 per subfunction), has %d", 2*k, len(encode))
	}

	rangeArr, err := optionalNumberArray(r, dict, "Range")
	if err != nil {
		return nil, err
	}

	return &type3{
		domain:   [2]float64{domain[0], domain[1]},
		fns:      fns,
		bounds:   bounds,
		encode:   encode,
		rangeArr: rangeArr,
	}, nil
}

func (f *type3) NumInputs() int { return 1 }

func (f *type3) NumOutputs() int {
	// Every subfunction is expected to agree on output count (they are
	// stitched into one gradient), so the first one's count is used
	// directly rather than summed - unlike multiFunction, whose
	// subfunctions each contribute a *different* output component.
	return f.fns[0].NumOutputs()
}

// Eval implements 7.10.4's selection-and-remapping algorithm: x is
// clipped to Domain, then the subinterval index i is found such that x
// falls in [low_i, high_i) for every i except the last, which is
// [low_k-1, high_k-1] (closed on both ends, so that x exactly at Domain's
// upper bound still lands in the final subfunction rather than falling
// through every comparison). x is then linearly remapped from
// [low_i,high_i] to [Encode[2i],Encode[2i+1]] before being handed to
// Functions[i].
func (f *type3) Eval(inputs []float64) ([]float64, error) {
	if len(inputs) != 1 {
		return nil, pdferror.Malformedf("Type 3 function expects 1 input, got %d", len(inputs))
	}
	x := clip(inputs[0], f.domain[0], f.domain[1])

	k := len(f.fns)
	idx := k - 1
	low := f.domain[0]
	high := f.domain[1]
	for i := 0; i < k-1; i++ {
		boundHigh := f.bounds[i]
		if x < boundHigh {
			idx = i
			if i == 0 {
				low = f.domain[0]
			} else {
				low = f.bounds[i-1]
			}
			high = boundHigh
			break
		}
	}
	if idx == k-1 {
		if k > 1 {
			low = f.bounds[k-2]
		}
		high = f.domain[1]
	}

	encoded := interpolate(x, low, high, f.encode[2*idx], f.encode[2*idx+1])
	out, err := f.fns[idx].Eval([]float64{encoded})
	if err != nil {
		return nil, err
	}
	return clipToRange(out, f.rangeArr), nil
}
