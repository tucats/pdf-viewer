package graphics

// Image is a fully decoded raster image, ready for internal/raster to
// paint onto a Canvas - the image-painting counterpart to a solid Color.
// It is produced by internal/image from a PDF image XObject or inline
// image (see that package's doc comment for all of the PDF-specific
// interpretation involved: color spaces, /Decode arrays, /ImageMask
// stencils, and /SMask soft masks). By the time an Image reaches this
// package, none of that PDF-specific knowledge remains - it is nothing
// more than a plain grid of already-final pixels, which is deliberate:
// this package (and internal/raster, which actually paints an Image) has
// no PDF-specific knowledge at all anywhere else, and Image is not going
// to be the exception.
//
// # Pixel layout
//
// Pix stores four bytes per pixel - red, green, blue, then alpha - in
// row-major order starting from the top-left pixel: Pix[0:4] is the
// pixel at (0,0), Pix[4:8] is (1,0), and so on, wrapping to the next row
// after Width pixels. This matches two things at once, which is exactly
// why it was chosen: Go's standard image.NRGBA pixel layout (so a
// caller wanting a standard library image.Image from decoded PDF image
// data has an easy, obvious conversion available), and the sample order
// the PDF specification itself defines for image data - image space has
// its origin at the upper-left corner, with samples running
// left-to-right and then top-to-bottom (ISO 32000-2, 8.9.5.1) - so
// internal/image can decode a PDF image's samples directly into Pix in
// the order they are already stored in the file, without an extra
// vertical-flip step.
//
// Alpha is not premultiplied - RGB always holds the pixel's own true
// color, even where A is partially or fully transparent - again matching
// image.NRGBA rather than image.RGBA (which premultiplies). This matters
// for internal/raster's compositing math, which needs a pixel's original
// color regardless of how transparent an /SMask or /ImageMask makes it;
// premultiplied color would need to be "divided back out" by alpha
// before compositing, an extra step (and an extra source of division-by-
// zero bugs for fully transparent pixels) with no benefit here.
type Image struct {
	Width, Height int
	Pix           []byte
}

// At returns the pixel at (x, y) as (r, g, b, a), each in [0,1] - the
// same [0,1]-per-component convention Color's fields use, so a sampled
// Image pixel and a solid Color can be blended by the same compositing
// code in internal/raster without a unit conversion in between.
//
// A coordinate outside the image's bounds returns fully transparent
// black (all four results 0) rather than panicking. internal/raster
// relies on this: it maps a device pixel's center back into image space
// through the inverse of an affine transform (see Matrix.Invert), and
// ordinary floating-point rounding can occasionally place the mapped
// coordinate one unit outside an edge row or column even for a device
// pixel that is legitimately, visibly part of the image - returning
// "nothing here" for that case is the correct, simple behavior, rather
// than a special edge-clamping rule that would only matter for a
// fraction of a pixel at an image's border.
func (img *Image) At(x, y int) (r, g, b, a float64) {
	if img == nil || x < 0 || y < 0 || x >= img.Width || y >= img.Height {
		return 0, 0, 0, 0
	}
	i := (y*img.Width + x) * 4
	return float64(img.Pix[i]) / 255, float64(img.Pix[i+1]) / 255, float64(img.Pix[i+2]) / 255, float64(img.Pix[i+3]) / 255
}
