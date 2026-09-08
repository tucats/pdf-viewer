package image

import "github.com/tucats/pdf-viewer/internal/syntax"

// ColorSpace is a resolved PDF color space, ready to convert a pixel's
// (or a content stream color operator's) raw component values into
// linear device RGB - the same [0,1]-per-channel representation
// graphics.Color already uses.
//
// This is the exported counterpart of this package's own internal
// colorSpace type (colorspace.go), letting internal/content reuse the
// exact same color-space resolution logic Decode already relies on for
// images - named-resource lookup, /ICCBased/CalGray/CalRGB aliasing,
// /Indexed, /Lab, and /Separation/DeviceN tint transforms - for "cs"/
// "CS" and "sc"/"scn"/"SC"/"SCN" as well, rather than internal/content
// only ever guessing DeviceGray/RGB/CMYK from operand count (which
// remains its fallback for a color space this type cannot resolve - see
// that package's colorFromComponents).
type ColorSpace struct {
	cs colorSpace
}

// Components reports how many numeric components a color in this space
// takes (1 for DeviceGray, Separation, and Indexed - whose one component
// is an index rather than a color component in its own right, see
// ToRGB's doc comment; 3 for DeviceRGB/Lab; 4 for DeviceCMYK; the number
// of colorant names for DeviceN).
func (c ColorSpace) Components() int {
	return c.cs.components
}

// ToRGB converts comps - exactly Components() raw values - to linear
// device RGB. For an /Indexed color space, comps must hold exactly one
// value: the raw index itself (not normalized to [0,1] - an index is a
// small integer, unlike every other family's components), matching how
// a content stream's "scn" operator supplies an Indexed color space's
// single operand directly as the table index to use.
//
// A comps slice of the wrong length, or a ColorSpace holding no
// conversion logic at all (the zero value), returns black rather than
// panicking - this project's usual policy of "malformed/mismatched input
// degrades the rendered result, it does not crash the renderer".
func (c ColorSpace) ToRGB(comps []float64) (r, g, b float64) {
	if c.cs.indexed != nil {
		if len(comps) != 1 {
			return 0, 0, 0
		}
		return indexedToRGB(c.cs, int(comps[0]+0.5))
	}
	if len(comps) != c.cs.components || c.cs.toRGB == nil {
		return 0, 0, 0
	}
	return c.cs.toRGB(comps)
}

// ResolveColorSpace resolves obj (a bare Device-family name, a named
// /Resources /ColorSpace resource, or a color-space array such as
// [/Indexed ...] or [/Separation ...] - exactly the same object shapes
// an image's own /ColorSpace entry may take) into a ColorSpace, following
// any indirect references within it via r. resources is consulted only
// when obj names a resource rather than a Device family or a self-
// contained array - see lookupNamedColorSpace.
func ResolveColorSpace(r Resolver, obj syntax.Object, resources syntax.Dictionary) (ColorSpace, error) {
	cs, err := resolveColorSpace(r, obj, resources)
	if err != nil {
		return ColorSpace{}, err
	}
	return ColorSpace{cs: cs}, nil
}
