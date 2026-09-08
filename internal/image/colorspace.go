package image

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/function"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// colorSpace describes how to turn one pixel's raw, already
// decode-array-remapped component values into linear device RGB - the
// same [0,1]-per-channel representation graphics.Color already uses, so
// a decoded pixel can be written straight into a graphics.Image without
// another conversion step.
//
// indexed is non-nil only for an /Indexed color space; every other
// family fills in toRGB directly and leaves indexed nil. Decode (in
// decode.go) checks indexed rather than trying to make one toRGB
// function handle both cases, because Indexed's raw sample is a lookup
// index (an integer into a table), not a set of color components in
// their own right the way every other family's raw samples are.
type colorSpace struct {
	// components is the number of raw samples this color space's pixels
	// carry per pixel in an image (1 for an Indexed color space, since
	// its one raw sample is an index rather than components in their own
	// right - see indexed below).
	components int
	toRGB      func(comps []float64) (r, g, b float64)
	indexed    *indexedInfo

	// decodeDefault, when non-nil, overrides decode.go's usual "[0 1]
	// repeated per component" default /Decode range - needed only for
	// /Lab, whose specification-defined default is [0 100 amin amax bmin
	// bmax] (L is never a [0,1]-normalized fraction, unlike every other
	// color space this package supports). Left nil for every other
	// family, meaning "use the ordinary [0,1]-per-component default".
	decodeDefault []float64
}

// indexedInfo holds an /Indexed color space's lookup table: base is the
// color space each table entry's bytes are interpreted in, hival is the
// largest valid index, and lookup holds (hival+1)*base.components bytes,
// one unsigned byte (0-255, always 8 bits regardless of the *image's*
// own /BitsPerComponent - see the PDF specification, 8.9.5.2) per
// component per table entry.
type indexedInfo struct {
	base   colorSpace
	hival  int
	lookup []byte
}

// resolveColorSpace interprets obj (already resolved one level, i.e. not
// itself a syntax.Reference - see resolveIfRef) as a PDF color space,
// following named resources through resources's /ColorSpace dictionary
// and indirect references within a color space array (an /ICCBased
// stream reference, an /Indexed lookup table) through r as needed.
func resolveColorSpace(r Resolver, obj syntax.Object, resources syntax.Dictionary) (colorSpace, error) {
	switch v := obj.(type) {
	case syntax.Name:
		switch v {
		case "DeviceGray", "G":
			return colorSpace{components: 1, toRGB: grayToRGB}, nil
		case "DeviceRGB", "RGB":
			return colorSpace{components: 3, toRGB: rgbToRGB}, nil
		case "DeviceCMYK", "CMYK":
			return colorSpace{components: 4, toRGB: cmykToRGB}, nil
		default:
			named, err := lookupNamedColorSpace(r, v, resources)
			if err != nil {
				return colorSpace{}, err
			}
			return resolveColorSpace(r, named, resources)
		}
	case syntax.Array:
		return resolveColorSpaceArray(r, v, resources)
	default:
		return colorSpace{}, pdferror.Malformedf("/ColorSpace is neither a name nor an array (found %T)", obj)
	}
}

// lookupNamedColorSpace resolves a color space referred to by name
// (anything other than the three Device families' own names, which
// resolveColorSpace handles directly) through resources's /ColorSpace
// dictionary - the mechanism a content stream's own "cs"/"CS" operators
// also rely on (see internal/content), except there a name with no
// resource-dictionary entry is tolerated by falling back to component
// count; an image's /ColorSpace entry has no such fallback available, so
// a name this function cannot resolve is a genuine, reportable error.
func lookupNamedColorSpace(r Resolver, name syntax.Name, resources syntax.Dictionary) (syntax.Object, error) {
	if resources == nil {
		return nil, pdferror.Malformedf("image /ColorSpace names resource /%s, but the page has no /Resources", name)
	}
	entry, ok := resources["ColorSpace"]
	if !ok {
		return nil, pdferror.Malformedf("image /ColorSpace names resource /%s, but /Resources has no /ColorSpace dictionary", name)
	}
	resolved, err := resolveIfRef(r, entry)
	if err != nil {
		return nil, err
	}
	csDict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return nil, pdferror.Malformedf("/Resources /ColorSpace is not a dictionary (found %T)", resolved)
	}
	value, ok := csDict[name]
	if !ok {
		return nil, pdferror.Malformedf("color space resource /%s not found in /Resources /ColorSpace", name)
	}
	return resolveIfRef(r, value)
}

// resolveColorSpaceArray handles every color space family PDF expresses
// as an array (as opposed to a bare name): [/ICCBased stream],
// [/Indexed base hival lookup], [/CalGray dict], [/CalRGB dict],
// [/Lab dict], [/Separation ...], [/DeviceN ...], and [/Pattern ...].
func resolveColorSpaceArray(r Resolver, arr syntax.Array, resources syntax.Dictionary) (colorSpace, error) {
	if len(arr) == 0 {
		return colorSpace{}, pdferror.Malformedf("/ColorSpace array is empty")
	}
	family, ok := arr[0].(syntax.Name)
	if !ok {
		return colorSpace{}, pdferror.Malformedf("/ColorSpace array's first element is not a name (found %T)", arr[0])
	}
	switch family {
	case "ICCBased":
		return resolveICCBased(r, arr)
	case "Indexed", "I":
		return resolveIndexed(r, arr, resources)
	case "CalRGB":
		// The white point, gamma, and matrix a /CalRGB dictionary carries
		// are not applied - see the package doc comment's "documented
		// approximation" note. The dictionary itself (arr[1]) is not even
		// inspected.
		return colorSpace{components: 3, toRGB: rgbToRGB}, nil
	case "CalGray":
		return colorSpace{components: 1, toRGB: grayToRGB}, nil
	case "Lab":
		return resolveLab(r, arr)
	case "Separation", "DeviceN":
		return resolveSeparationOrDeviceN(r, arr, resources)
	case "Pattern":
		return colorSpace{}, pdferror.Unsupportedf("Pattern color space as an image's /ColorSpace")
	default:
		return colorSpace{}, pdferror.Unsupportedf("color space family %q", family)
	}
}

// resolveICCBased resolves an [/ICCBased stream] color space by
// component count alone (1, 3, or 4 - its stream dictionary's required
// /N entry), aliasing it to DeviceGray/DeviceRGB/DeviceCMYK respectively,
// rather than by parsing and applying the embedded ICC color profile
// itself - see the package doc comment and docs/capability-matrix.md.
func resolveICCBased(r Resolver, arr syntax.Array) (colorSpace, error) {
	if len(arr) < 2 {
		return colorSpace{}, pdferror.Malformedf("/ICCBased array is missing its stream reference")
	}
	resolved, err := resolveIfRef(r, arr[1])
	if err != nil {
		return colorSpace{}, err
	}
	stream, ok := resolved.(syntax.Stream)
	if !ok {
		return colorSpace{}, pdferror.Malformedf("/ICCBased array's second element did not resolve to a stream (found %T)", resolved)
	}
	dict, err := r.ResolveDictionary(stream.Dict)
	if err != nil {
		return colorSpace{}, err
	}
	n, ok := dict["N"].(syntax.Integer)
	if !ok {
		return colorSpace{}, pdferror.Malformedf("/ICCBased stream has no /N (component count) entry")
	}
	switch n {
	case 1:
		return colorSpace{components: 1, toRGB: grayToRGB}, nil
	case 3:
		return colorSpace{components: 3, toRGB: rgbToRGB}, nil
	case 4:
		return colorSpace{components: 4, toRGB: cmykToRGB}, nil
	default:
		return colorSpace{}, pdferror.Malformedf("/ICCBased /N is %d, must be 1, 3, or 4", n)
	}
}

// maxIndexedHival bounds an /Indexed color space's /hival entry - the
// PDF specification itself caps this at 255 for an 8-bit-or-narrower
// image, but this project accepts the wider range some producers use
// with a 16-bit image while still rejecting an implausible value that
// would force allocating an oversized lookup table for no legitimate
// reason.
const maxIndexedHival = 65535

// resolveIndexed resolves an [/Indexed base hival lookup] color space:
// base is itself a full color space (recursively resolved - but not
// permitted to be Indexed itself, since a table of indices into another
// table is not meaningful), hival is the largest valid index, and lookup
// is a string or stream holding (hival+1) consecutive color values in
// base's own component count and byte width (always 8 bits per
// component in the table, regardless of the image's own
// /BitsPerComponent - see indexedInfo's doc comment).
func resolveIndexed(r Resolver, arr syntax.Array, resources syntax.Dictionary) (colorSpace, error) {
	if len(arr) != 4 {
		return colorSpace{}, pdferror.Malformedf("/Indexed array must have 4 elements, has %d", len(arr))
	}

	baseObj, err := resolveIfRef(r, arr[1])
	if err != nil {
		return colorSpace{}, err
	}
	base, err := resolveColorSpace(r, baseObj, resources)
	if err != nil {
		return colorSpace{}, err
	}
	if base.indexed != nil {
		return colorSpace{}, pdferror.Unsupportedf("/Indexed color space whose base is itself /Indexed")
	}

	hivalObj, err := resolveIfRef(r, arr[2])
	if err != nil {
		return colorSpace{}, err
	}
	hivalNum, ok := hivalObj.(syntax.Integer)
	if !ok || hivalNum < 0 || hivalNum > maxIndexedHival {
		return colorSpace{}, pdferror.Malformedf("/Indexed hival must be an integer in [0,%d]", maxIndexedHival)
	}
	hival := int(hivalNum)

	lookupObj, err := resolveIfRef(r, arr[3])
	if err != nil {
		return colorSpace{}, err
	}
	var lookup []byte
	switch lv := lookupObj.(type) {
	case syntax.String:
		lookup = []byte(lv)
	case syntax.Stream:
		lookup, err = r.DecodeStream(lv)
		if err != nil {
			return colorSpace{}, err
		}
	default:
		return colorSpace{}, pdferror.Malformedf("/Indexed lookup table is neither a string nor a stream (found %T)", lookupObj)
	}

	need := (hival + 1) * base.components
	if len(lookup) < need {
		return colorSpace{}, pdferror.Malformedf("/Indexed lookup table has %d bytes, needs at least %d for hival %d over %d component(s)", len(lookup), need, hival, base.components)
	}

	return colorSpace{
		components: 1,
		indexed:    &indexedInfo{base: base, hival: hival, lookup: lookup},
	}, nil
}

// indexedToRGB looks up index in cs's lookup table (clamping to
// [0,hival], since a malformed or hostile content stream can supply an
// index outside that range even though the sample was correctly
// bit-extracted) and converts the resulting base-color-space components
// to RGB.
func indexedToRGB(cs colorSpace, index int) (r, g, b float64) {
	info := cs.indexed
	if index < 0 {
		index = 0
	}
	if index > info.hival {
		index = info.hival
	}
	n := info.base.components
	start := index * n
	comps := make([]float64, n)
	for i := 0; i < n; i++ {
		var raw byte
		if start+i < len(info.lookup) {
			raw = info.lookup[start+i]
		}
		comps[i] = float64(raw) / 255
	}
	return info.base.toRGB(comps)
}

// srgbD65WhitePoint is the CIE XYZ white point (D65) the standard sRGB
// XYZ-to-linear-RGB matrix used by labToRGB below is itself calibrated
// for. It is used as resolveLab's default /WhitePoint whenever a /Lab
// color space's dictionary omits one (the specification actually
// requires /WhitePoint, but this project tolerates the omission rather
// than rejecting an otherwise-usable color space over one missing
// required entry, consistent with this package's general "missing
// resource" tolerance) - defaulting to the matrix's own calibration
// point, rather than some other real-world illuminant such as D50,
// minimizes the color distortion that using a mismatched matrix
// necessarily introduces; see labToRGB's doc comment for the resulting
// documented approximation.
var srgbD65WhitePoint = [3]float64{0.9505, 1.0, 1.089}

// resolveLab resolves a [/Lab dict] color space (8.6.5.4): 3 components
// (L*, a*, b*), with L* always ranging over [0,100] and a*/b* ranging
// over dict's optional /Range (default [-100 100 -100 100]).
func resolveLab(r Resolver, arr syntax.Array) (colorSpace, error) {
	if len(arr) < 2 {
		return colorSpace{}, pdferror.Malformedf("/Lab array is missing its parameter dictionary")
	}
	resolved, err := resolveIfRef(r, arr[1])
	if err != nil {
		return colorSpace{}, err
	}
	dict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return colorSpace{}, pdferror.Malformedf("/Lab array's second element did not resolve to a dictionary (found %T)", resolved)
	}

	white := srgbD65WhitePoint
	if wp, ok := dict["WhitePoint"]; ok {
		resolvedWP, err := resolveIfRef(r, wp)
		if err != nil {
			return colorSpace{}, err
		}
		arr, ok := resolvedWP.(syntax.Array)
		if !ok || len(arr) != 3 {
			return colorSpace{}, pdferror.Malformedf("/Lab /WhitePoint must be a 3-element array")
		}
		for i, e := range arr {
			v, ok := numberValue(e)
			if !ok {
				return colorSpace{}, pdferror.Malformedf("/Lab /WhitePoint entry %d is not a number", i)
			}
			white[i] = v
		}
	}

	abRange := [4]float64{-100, 100, -100, 100}
	if rg, ok := dict["Range"]; ok {
		resolvedRange, err := resolveIfRef(r, rg)
		if err != nil {
			return colorSpace{}, err
		}
		arr, ok := resolvedRange.(syntax.Array)
		if !ok || len(arr) != 4 {
			return colorSpace{}, pdferror.Malformedf("/Lab /Range must be a 4-element array")
		}
		for i, e := range arr {
			v, ok := numberValue(e)
			if !ok {
				return colorSpace{}, pdferror.Malformedf("/Lab /Range entry %d is not a number", i)
			}
			abRange[i] = v
		}
	}

	return colorSpace{
		components: 3,
		toRGB: func(c []float64) (rr, gg, bb float64) {
			return labToRGB(c[0], c[1], c[2], white)
		},
		// Per 8.9.5.2's table of default /Decode ranges: L* in [0,100],
		// a*/b* in the color space's own /Range.
		decodeDefault: []float64{0, 100, abRange[0], abRange[1], abRange[2], abRange[3]},
	}, nil
}

// labToRGB converts one CIE L*a*b* color to gamma-encoded sRGB, via the
// standard CIELAB -> CIEXYZ formula (using white as the reference white
// point) followed by the standard XYZ -> linear sRGB matrix (calibrated
// for the D65 illuminant - see srgbD65WhitePoint) and the sRGB gamma
// (transfer function) curve.
//
// This is a documented approximation in the same spirit as this
// project's CalRGB/CalGray handling and its non-color-managed CMYK
// conversion (see cmykToRGB): the XYZ -> sRGB matrix is only exactly
// correct when white is D65 itself, so a /Lab color space specifying a
// different white point (D50 is common in prepress-oriented PDFs) will
// render with a color cast this function does not correct for. A full,
// chromatic-adaptation-aware conversion would need to adapt white to
// D65 before applying the matrix; this project does not implement ICC-
// grade color management anywhere (see docs/capability-matrix.md), so
// adding that machinery for /Lab alone was judged not worth the
// complexity relative to this simpler, still broadly recognizable
// conversion.
func labToRGB(l, a, b float64, white [3]float64) (r, g, bOut float64) {
	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - b/200

	x := white[0] * labInverseF(fx)
	y := white[1] * labInverseF(fy)
	z := white[2] * labInverseF(fz)

	rLin := 3.2406*x - 1.5372*y - 0.4986*z
	gLin := -0.9689*x + 1.8758*y + 0.0415*z
	bLin := 0.0557*x - 0.2040*y + 1.0570*z

	return srgbGammaEncode(rLin), srgbGammaEncode(gLin), srgbGammaEncode(bLin)
}

// labInverseF inverts the nonlinear function CIELAB's forward transform
// applies to each XYZ/whitepoint ratio, per the standard piecewise
// definition (CIE 15:2004): t^3 above a small threshold, a linear
// approximation below it (chosen so the two pieces and their slopes
// agree exactly at the threshold, avoiding a visible seam in dark
// colors).
func labInverseF(t float64) float64 {
	const delta = 6.0 / 29.0
	if t > delta {
		return t * t * t
	}
	return 3 * delta * delta * (t - 4.0/29.0)
}

// srgbGammaEncode applies the sRGB transfer function (IEC 61966-2-1) to
// one linear-light color component, converting it to the gamma-encoded
// value every other color space in this package already produces
// (graphics.Color's own documented convention - see that type's doc
// comment - is a plainer normalized value than true linear light, but
// sRGB gamma encoding is the closest standard match available without
// this package adopting a distinct "Lab-flavored" Color representation
// solely for this one color space), then clamps to [0,1] - out-of-gamut
// Lab colors (as Range technically permits) map to the nearest
// displayable sRGB color rather than an unrepresentable one.
func srgbGammaEncode(c float64) float64 {
	if c <= 0.0031308 {
		c = 12.92 * c
	} else {
		c = 1.055*math.Pow(c, 1/2.4) - 0.055
	}
	return clamp01(c)
}

// resolveSeparationOrDeviceN resolves a [/Separation name alt tintFn] or
// [/DeviceN names alt tintFn attrs?] color space (8.6.6.4/8.6.6.5): each
// pixel's raw "tint" component(s) - one for Separation, len(names) for
// DeviceN - are passed through tintFn (a PDF function, see
// internal/function) to compute alt's own components, which are then
// converted to RGB by alt itself. This is what lets either family
// represent an arbitrary spot-color or multi-ink scheme while still
// ultimately painting in a color space this package already knows how to
// display.
func resolveSeparationOrDeviceN(r Resolver, arr syntax.Array, resources syntax.Dictionary) (colorSpace, error) {
	if len(arr) < 4 {
		return colorSpace{}, pdferror.Malformedf("/%s array must have at least 4 elements, has %d", arr[0], len(arr))
	}

	var numTints int
	switch names, err := resolveIfRef(r, arr[1]); {
	case err != nil:
		return colorSpace{}, err
	default:
		switch v := names.(type) {
		case syntax.Name:
			// /Separation's second element is a single colorant name.
			numTints = 1
		case syntax.Array:
			// /DeviceN's second element is an array of colorant names,
			// one per tint component.
			numTints = len(v)
		default:
			return colorSpace{}, pdferror.Malformedf("/%s colorant name(s) entry is neither a name nor an array (found %T)", arr[0], names)
		}
	}
	if numTints < 1 {
		return colorSpace{}, pdferror.Malformedf("/%s has no colorant names", arr[0])
	}

	altObj, err := resolveIfRef(r, arr[2])
	if err != nil {
		return colorSpace{}, err
	}
	alt, err := resolveColorSpace(r, altObj, resources)
	if err != nil {
		return colorSpace{}, err
	}
	if alt.indexed != nil {
		return colorSpace{}, pdferror.Unsupportedf("/%s alternate color space that is itself /Indexed", arr[0])
	}

	fn, err := function.Parse(r, arr[3])
	if err != nil {
		return colorSpace{}, err
	}
	if fn.NumInputs() != numTints {
		return colorSpace{}, pdferror.Malformedf("/%s tint transform expects %d input(s), declares %d", arr[0], numTints, fn.NumInputs())
	}
	if fn.NumOutputs() != alt.components {
		return colorSpace{}, pdferror.Malformedf("/%s tint transform produces %d output(s), alternate color space needs %d", arr[0], fn.NumOutputs(), alt.components)
	}

	return colorSpace{
		components: numTints,
		toRGB: func(comps []float64) (rr, gg, bb float64) {
			out, err := fn.Eval(comps)
			if err != nil || len(out) != alt.components {
				// A well-formed tint transform, called with the right
				// number of inputs (guaranteed above), essentially cannot
				// fail - but a defensive fallback to black is cheaper and
				// safer than propagating the error through toRGB's
				// signature, which every other color space family in this
				// file also does not need.
				return 0, 0, 0
			}
			return alt.toRGB(out)
		},
	}, nil
}

func grayToRGB(c []float64) (r, g, b float64) {
	return c[0], c[0], c[0]
}

func rgbToRGB(c []float64) (r, g, b float64) {
	return c[0], c[1], c[2]
}

// cmykToRGB uses the same baseline (non-color-managed) DeviceCMYK ->
// DeviceRGB conversion internal/content's cmykColor uses for solid CMYK
// fill/stroke colors (r = 1 - min(1, c+k), and likewise for g and b) -
// see that function's doc comment for the rationale. The formula is
// small enough, and used by two otherwise-unrelated packages (this one
// has no dependency on internal/content, nor should it gain one just to
// share three lines of arithmetic), that duplicating it here is more
// direct than introducing a shared-utility package for it.
func cmykToRGB(c []float64) (r, g, b float64) {
	cc, mm, yy, kk := c[0], c[1], c[2], c[3]
	return 1 - clamp01(cc+kk), 1 - clamp01(mm+kk), 1 - clamp01(yy+kk)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
