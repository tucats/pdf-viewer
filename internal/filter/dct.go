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
// image.CMYK respectively, already reversing YCbCr's own color transform
// and the Adobe-specific CMYK inversion where its APP14 marker calls for
// it. This function converts each into plain interleaved bytes (1, 3, or
// 4 bytes per pixel) in the component order PDF expects for
// DeviceGray/DeviceRGB/DeviceCMYK - internal/image then treats the
// result exactly like any other image filter's output, cross-checking
// its length against the image dictionary's own declared /Width,
// /Height, and /ColorSpace component count.
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
		return cmykToBytes(px), nil
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
