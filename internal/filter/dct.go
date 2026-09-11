package filter

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements decodeDCT, reversing PDF's DCTDecode filter -
// baseline and progressive JPEG compression, exactly the format the
// standard library's image/jpeg package already knows how to read, which
// is why this project can support it without writing its own JPEG
// decoder (see the package doc comment's "Supported filters" section).
//
// # Why JPEG decoding belongs in this package, not internal/image
//
// It might seem like image-specific decoding (as opposed to ASCII85,
// Flate, and so on, which are generic byte-stream filters with no idea
// what the bytes mean) belongs in internal/image instead, since that is
// the package with all the other PDF-image-specific knowledge (color
// spaces, decode arrays, masks - see its package doc comment). But
// DCTDecode is, from the point of view of everything else in this
// module's pipeline, still just another entry in a stream's /Filter
// chain: internal/parser.Document.DecodeStream applies every named
// filter in order and hands back one final byte slice, and
// internal/image only ever wants to receive that already-fully-decoded
// byte slice (one 8-bit sample per color component, row-major,
// left-to-right then top-to-bottom - precisely PDF's own image sample
// order, which conveniently matches how Go's standard image types store
// pixels too). Doing the JPEG-to-raw-samples conversion here, rather than
// threading a special case for DCTDecode through DecodeStream and
// internal/image both, keeps that "a filter chain decodes to raw
// samples" contract true for every filter uniformly, images included.
//
// # Component count and color conversion
//
// A JPEG file's own headers say how many color components it has (1 for
// grayscale, 3 for YCbCr, or 4 for CMYK/YCCK - the "Adobe" varieties);
// Go's image/jpeg decodes each into image.Gray, image.YCbCr, or
// image.CMYK respectively, already reversing YCbCr's own color transform.
// This function converts each into plain interleaved bytes (1, 3, or 4
// bytes per pixel) in the component order PDF expects for
// DeviceGray/DeviceRGB/DeviceCMYK - internal/image then treats the
// result exactly like any other image filter's output, cross-checking
// its length against the image dictionary's own declared /Width,
// /Height, and /ColorSpace component count.
//
// # Undoing Go's own Adobe CMYK inversion
//
// For a 4-component (CMYK) JPEG, Go's decoder requires an Adobe APP14
// marker to even attempt a decode (an unmarked 4-component JPEG is
// UnsupportedError), and whenever that marker is present it always
// inverts every sample ("v = 255 - v") before handing back an
// *image.CMYK - see the standard library's own applyBlack doc comment.
// That inversion is the right, well-established convention for a
// *standalone* CMYK JPEG file written by an Adobe application
// (Photoshop, etc.), which really does store 255 for "no ink". It is
// the wrong convention here: a CMYK JPEG Distiller/Acrobat embeds
// *inside a PDF* via DCTDecode is written with direct, uninverted
// samples (0 = no ink), because the PDF's own /Decode array - not the
// JPEG's internal Adobe marker - is the specification-defined place to
// request inversion (7.4.8), and defaults to [0 1 0 1 0 1 0 1] (direct,
// no inversion) when absent. A producer's own JPEG encoder still writes
// the Adobe marker regardless (it is a property of the encoder, not a
// per-file choice tied to the PDF's /Decode array), so Go's blanket
// invert-whenever-marker-present rule silently flips every such image.
//
// unApplyAdobeCMYKInversion inverts the samples back ("v = 255 - v" again,
// which cancels Go's own inversion) so decodeDCT hands internal/image
// the same direct, un-inverted bytes the file's /Decode array (almost
// always left at its [0 1]-repeated default for this exact reason) is
// written to expect - see internal/image/decode.go's decodeArray. This
// was confirmed against a real production PDF (an Apple hardware
// manual originally distilled from QuarkXPress) whose photographic
// figures rendered as solid near-black blobs before this fix: macOS's
// own PDF renderer (Quartz/PDFKit, as used by Preview.app) shows the
// same file's same images correctly, and does so precisely because it
// does not apply Go's standalone-JPEG-file convention to a DCTDecode
// image stream either.
func decodeDCT(data []byte) ([]byte, error) {
	// Peek at the JPEG header only (image.Config, not the full pixel
	// data) before committing to a full decode, so a tiny file with a
	// maliciously huge declared width/height cannot force this function
	// to allocate an enormous image - see the package doc comment's
	// "Bounded decompression" section. maxDecodedSize/4 is the largest
	// pixel count whose worst case (4 bytes/pixel, DeviceCMYK) still fits
	// within maxDecodedSize.
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, pdferror.Malformedf("DCT decode: reading JPEG header: %v", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxDecodedSize/4 {
		return nil, pdferror.Malformedf("DCT-encoded image is %dx%d pixels, exceeding this package's bound", cfg.Width, cfg.Height)
	}

	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, pdferror.Malformedf("DCT decode: %v", err)
	}

	switch px := img.(type) {
	case *image.Gray:
		return grayToBytes(px), nil
	case *image.YCbCr:
		return ycbcrToRGBBytes(px), nil
	case *image.CMYK:
		return cmykToBytes(unApplyAdobeCMYKInversion(px)), nil
	default:
		// image/jpeg only ever produces one of the three concrete types
		// above; this default only guards against a future standard
		// library change adding a new one.
		return nil, pdferror.Unsupportedf("DCT-encoded image decoded to unexpected Go image type %T", img)
	}
}

func grayToBytes(img *image.Gray) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out[y*w+x] = img.GrayAt(b.Min.X+x, b.Min.Y+y).Y
		}
	}
	return out
}

func ycbcrToRGBBytes(img *image.YCbCr) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h*3)
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.YCbCrAt(b.Min.X+x, b.Min.Y+y)
			r, g, bl := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
			out[i], out[i+1], out[i+2] = r, g, bl
			i += 3
		}
	}
	return out
}

// unApplyAdobeCMYKInversion inverts every sample of img ("v = 255 - v")
// in place and returns it, undoing the unconditional Adobe-marker-driven
// inversion Go's own image/jpeg decoder already applied - see decodeDCT's
// doc comment for why a DCTDecode image stream inside a PDF needs that
// inversion undone rather than kept.
func unApplyAdobeCMYKInversion(img *image.CMYK) *image.CMYK {
	for i, v := range img.Pix {
		img.Pix[i] = 255 - v
	}
	return img
}

func cmykToBytes(img *image.CMYK) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, w*h*4)
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.CMYKAt(b.Min.X+x, b.Min.Y+y)
			out[i], out[i+1], out[i+2], out[i+3] = c.C, c.M, c.Y, c.K
			i += 4
		}
	}
	return out
}
