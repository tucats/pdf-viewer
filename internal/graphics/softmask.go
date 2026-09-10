package graphics

// This file adds ExtGState-level soft mask support (PDF spec section
// 11.6.4.3, "Soft-Mask Dictionaries", 11.6.5.2) to internal/graphics'
// vocabulary of "things a DrawOp can carry." A soft mask is a way of
// saying "attenuate everything painted from here on, per-pixel, by how
// bright (or how opaque) a *completely separate* piece of content
// happens to be at that same pixel" - unlike a plain "ca"/"CA" constant
// alpha (a single number applied everywhere equally), a soft mask can
// vary continuously across the page, the same way a gradient does.
//
// Concretely: a soft mask is built (by internal/content, see that
// package's softmask.go - this package has no idea what an ExtGState
// dictionary or a PDF Form XObject even is) by rendering an entire
// separate chunk of PDF content ("the mask group") to an offscreen
// image, and then reducing every pixel of that image down to one number
// in [0,1] - either that pixel's luminosity (how bright it is) or its
// own alpha (how opaque/covered it is), depending on which the PDF
// content asked for. The result is exactly what this file's SoftMask
// type stores: a small grid of single numbers, plus a matrix saying how
// that grid lines up with device (pixel) space.

// SoftMask is a fully-resolved soft mask: a per-pixel alpha-attenuation
// value, ready for internal/raster to multiply into a DrawOp's own alpha
// exactly the way a constant "ca"/"CA" alpha already is (see
// State.SoftMask and DrawOp.SoftMask's own doc comments for how a value
// gets attached to a paint operation).
//
// A nil *SoftMask means "no soft mask is active" - full, unattenuated
// opacity, i.e. the same as every DrawOp before this feature existed.
// This mirrors how FillShading/FillTiling being nil means "no shading/
// tiling pattern is selected" elsewhere in this package: a pointer field
// that is usually nil, and only ever non-nil once something in the
// content stream (here, a "gs" operator naming an ExtGState with a real
// /SMask dictionary) explicitly asks for it.
type SoftMask struct {
	// Width and Height are the pixel dimensions of the offscreen buffer
	// the mask group was rendered into. This is *not* necessarily the
	// same size as the final page canvas - see internal/content's
	// buildSoftMask, which picks a size from the mask group's own
	// /BBox mapped into device space (bounded, like a tiling pattern's
	// own tile image, so a malformed or huge /BBox cannot demand an
	// unbounded allocation).
	Width, Height int

	// Values holds one mask value per pixel, row-major starting from the
	// top-left pixel (Values[0] is (0,0), Values[1] is (1,0), and so on,
	// wrapping to the next row after Width pixels) - the same row-major,
	// top-left-origin layout graphics.Image.Pix uses, just one byte per
	// pixel instead of four, since a mask has no color, only a single
	// "how much" value.
	//
	// Each byte is on the same 0-255 scale every other color channel in
	// this package uses (see Color and Image's own doc comments): 0
	// means "fully masked out" (paint nothing here, as if this pixel had
	// zero additional alpha) and 255 means "fully unmasked" (paint at
	// whatever alpha the DrawOp would have had anyway).
	Values []byte

	// DeviceToMask maps a device-space (pixel) coordinate directly to
	// this mask's own pixel coordinates - (0,0) at Values' first entry,
	// (Width,Height) just past its last. This is deliberately the
	// *device-to-mask* direction, not mask-to-device the way
	// DrawOp.ImageToDevice is for an ordinary image: internal/raster
	// only ever needs to go from "here is the device pixel I am about
	// to paint" to "which mask sample does that correspond to," so
	// storing the matrix already inverted (computed once, when
	// internal/content builds the mask - see buildSoftMask) means
	// internal/raster's own per-pixel sampling loop (see At below, and
	// Canvas.paint) never has to invert a matrix itself, unlike
	// Canvas.DrawImage, which does invert ImageToDevice - but only once,
	// outside its own per-pixel loop, not once per pixel either.
	DeviceToMask Matrix
}

// At returns m's mask value at device-space coordinates (deviceX,
// deviceY), as a float in [0,1] (Values' 0-255 byte scale divided down
// to match every other per-pixel alpha this package computes with,
// e.g. graphics.Color's own components). A nil receiver, a mask with no
// pixels at all (Width or Height <= 0 - defensive; internal/content
// never actually builds one this way), or a device coordinate that maps
// outside the mask's own pixel grid all return 0 ("fully masked out")
// rather than 1 ("unmasked") - matching the specification's documented
// default: a soft mask's group is composited over a fully opaque black
// backdrop (11.6.5.2), so anywhere the group's own content never painted
// (including everywhere outside its own /BBox) reads as black -
// luminosity zero, i.e. fully transparent - not as "no mask." Getting
// this default backward would be easy to do and easy to miss in
// testing (most hand-built test masks paint over their whole area), but
// would silently un-mask an entire page's edges whenever a mask group's
// /BBox does not happen to cover it exactly.
func (m *SoftMask) At(deviceX, deviceY float64) float64 {
	if m == nil || m.Width <= 0 || m.Height <= 0 {
		return 0
	}
	mx, my := m.DeviceToMask.Apply(deviceX, deviceY)
	ix := int(mx)
	iy := int(my)
	// int() truncates toward zero, which for a negative coordinate
	// rounds the wrong way (e.g. -0.5 truncates to 0, not -1) - but any
	// ix/iy that is actually negative is already going to fail the
	// bounds check on the next line regardless of which way it rounded,
	// so no separate floor() is needed here the way wrap01 needs one
	// elsewhere in this codebase (wrap01 has to fold a negative value
	// back into a valid *positive* range; this function just has to
	// reject it, and any negative value is rejected the same way).
	if ix < 0 || ix >= m.Width || iy < 0 || iy >= m.Height {
		return 0
	}
	return float64(m.Values[iy*m.Width+ix]) / 255
}
