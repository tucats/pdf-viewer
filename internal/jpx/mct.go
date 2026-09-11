package jpx

// This file implements Annex G's two multiple component transformations
// (MCT), each undoing a colour decorrelation an encoder may apply across
// a tile's first three components before its own wavelet transform (see
// CodingStyle.MultipleComponentTransform's doc comment) - always
// symmetric with which wavelet filter the tile uses, since the standard
// pairs the two symmetrically: the reversible colour transform (RCT,
// §G.2) with the 5/3 reversible wavelet filter, so the whole pipeline can
// reconstruct exactly, and the irreversible colour transform (ICT, §G.3 -
// the same YCbCr<->RGB matrix ITU-R BT.601/JFIF use) with the 9/7
// irreversible filter. Only the inverse (decoder) direction is
// implemented here; mct_test.go's forward direction exists only to
// round-trip-test this file, following doc.go's Provenance section - the
// coefficients themselves are simple, unambiguous, and well-documented
// enough (unlike tier1.go's context tables) that no reference
// implementation cross-check was needed to write them with confidence.
//
// applyMultipleComponentTransform (image.go's Decode calls it once per
// tile, after idwt.go's reconstructTile) applies whichever transform the
// tile's own effective COD declares, in place, across the first three of
// that tile's reconstructTile output - the standard requires those three
// to share identical dimensions, which this package's own XRsiz/YRsiz==1
// restriction (siz.go's ComponentInfo doc comment) already guarantees for
// every component this package decodes at all.

// inverseRCT undoes §G.2's forward reversible colour transform in place
// across three same-length component sample arrays: c0, c1, c2 hold
// Y/Cb-like/Cr-like values on entry (a tile's raw component 0, 1, 2
// output, post-wavelet-synthesis) and red/green/blue values on exit.
// Every step is exact integer arithmetic performed in float64 (safe here
// for the same reason filter53's own doc comment gives); Go's ">>" on a
// signed integer is a floor (arithmetic) shift, matching §G.2's own floor
// division exactly.
func inverseRCT(c0, c1, c2 []float64) {
	for i := range c0 {
		y := int64(c0[i])
		cb := int64(c1[i])
		cr := int64(c2[i])

		g := y - ((cb + cr) >> 2)
		r := cr + g
		b := cb + g

		c0[i] = float64(r)
		c1[i] = float64(g)
		c2[i] = float64(b)
	}
}

// The ICT's inverse matrix coefficients (§G.3) - the same ITU-R BT.601
// YCbCr<->RGB constants JFIF/JPEG use, applied here to the zero-centred
// (already DC-level-shifted-out) real-valued domain the wavelet synthesis
// stage produces, not to 0-255 integer samples.
const (
	ictCrToR = 1.402
	ictCbToG = -0.344136
	ictCrToG = -0.714136
	ictCbToB = 1.772
)

// inverseICT undoes §G.3's forward irreversible colour transform in
// place, the same three-array convention inverseRCT uses. Always
// genuinely floating-point, even given a bit-exact tier-1 decode - the
// same "irreversible" reasoning filter97's doc comment gives for the 9/7
// wavelet filter this transform is always paired with.
func inverseICT(c0, c1, c2 []float64) {
	for i := range c0 {
		y, cb, cr := c0[i], c1[i], c2[i]

		r := y + ictCrToR*cr
		g := y + ictCbToG*cb + ictCrToG*cr
		b := y + ictCbToB*cb

		c0[i] = r
		c1[i] = g
		c2[i] = b
	}
}

// applyMultipleComponentTransform reverses tile tileIndex's own MCT (if
// its effective COD declares one - Header.effectiveTileDefaultCoding),
// in place, across the first three of components: RCT if that tile's
// wavelet filter is the 5/3 reversible one, ICT if 9/7, matching
// CodingStyle.MultipleComponentTransform's own doc comment on how the
// two fields combine. A no-op if the tile does not declare an MCT.
func applyMultipleComponentTransform(h *Header, tileIndex int, components []*reconstructedComponent) error {
	cs := h.effectiveTileDefaultCoding(tileIndex)
	if !cs.MultipleComponentTransform {
		return nil
	}
	if len(components) < 3 {
		return malformedf("tile %d declares a multiple component transform with only %d component(s), want at least 3", tileIndex, len(components))
	}

	c0, c1, c2 := components[0], components[1], components[2]
	if c0.width != c1.width || c0.width != c2.width || c0.height != c1.height || c0.height != c2.height {
		return malformedf("tile %d's first three components have mismatched dimensions (%dx%d, %dx%d, %dx%d), which a multiple component transform requires to match", tileIndex, c0.width, c0.height, c1.width, c1.height, c2.width, c2.height)
	}

	if cs.Transform == Transform5x3 {
		inverseRCT(c0.items, c1.items, c2.items)
	} else {
		inverseICT(c0.items, c1.items, c2.items)
	}
	return nil
}
