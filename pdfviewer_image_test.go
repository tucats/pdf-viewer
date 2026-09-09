package pdfviewer_test

import (
	"context"
	"image"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file exercises Phase 3's image support (referenced XObject
// images, inline images, image masks, soft masks, and DCTDecode/JPEG),
// plus Phase 8's JBIG2Decode, end to end through the public API, against
// the fixtures tools/genfixtures/main.go's buildImageRGB,
// buildImageMask, buildImageSMask, buildImageJPEG, buildImageJBIG2,
// buildInlineImage, and buildRotatedPage add - the image counterpart to
// pdfviewer_render_test.go's vector-graphics rendering tests.

// renderPage opens fixture, renders its first page at the default
// scale, and returns the resulting image - shared setup for every test
// in this file.
func renderPage(t *testing.T, fixture string) image.Image {
	t.Helper()
	doc, err := pdfviewer.OpenFile(fixturePath(fixture))
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", fixture, err)
	}
	t.Cleanup(func() { doc.Close() })
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
	if err != nil {
		t.Fatalf("Render(%s): %v", fixture, err)
	}
	return img
}

// assertRGB8 checks the pixel at (x, y) against an 8-bit-per-channel
// expected color, within tol per channel - a generous tolerance absorbs
// this project's anti-aliasing at shape edges and (for the JPEG test)
// lossy compression, without masking an actually-wrong color.
func assertRGB8(t *testing.T, img image.Image, x, y int, wantR, wantG, wantB uint32, tol int) {
	t.Helper()
	r, g, b, _ := img.At(x, y).RGBA()
	r, g, b = r>>8, g>>8, b>>8
	if diffU(r, wantR) > tol || diffU(g, wantG) > tol || diffU(b, wantB) > tol {
		t.Errorf("pixel (%d,%d) = (%d,%d,%d), want ~(%d,%d,%d)", x, y, r, g, b, wantR, wantG, wantB)
	}
}

func diffU(a, b uint32) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestRenderReferencedImageRGB confirms "Do" painting a referenced
// DeviceRGB image XObject (buildImageRGB: a 2x2 red/green/blue/yellow
// image scaled across the whole 100x100 page) lands the right color in
// each quadrant.
func TestRenderReferencedImageRGB(t *testing.T) {
	img := renderPage(t, "image-rgb.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	assertRGB8(t, img, 25, 25, 255, 0, 0, 2)   // top-left: red
	assertRGB8(t, img, 75, 25, 0, 255, 0, 2)   // top-right: green
	assertRGB8(t, img, 25, 75, 0, 0, 255, 2)   // bottom-left: blue
	assertRGB8(t, img, 75, 75, 255, 255, 0, 2) // bottom-right: yellow
}

// TestRenderImageMaskUsesFillColor confirms a referenced /ImageMask
// stencil image (buildImageMask: a 2x1 mask, left pixel painted, right
// pixel masked out) paints its left half with the current fill color
// (red) and leaves its right half as the page's own white background.
func TestRenderImageMaskUsesFillColor(t *testing.T) {
	img := renderPage(t, "image-mask.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	assertRGB8(t, img, 25, 50, 255, 0, 0, 2)     // left half: painted red
	assertRGB8(t, img, 75, 50, 255, 255, 255, 2) // right half: untouched background
}

// TestRenderImageSMaskBlendsWithBackground confirms an image's /SMask
// (buildImageSMask: a solid red image with a 50%-gray soft mask) blends
// with the page's white background rather than painting fully opaque -
// the green and blue channels should land roughly halfway between the
// foreground's 0 and the background's 255, while red (255 in both
// layers) stays saturated.
func TestRenderImageSMaskBlendsWithBackground(t *testing.T) {
	img := renderPage(t, "image-smask.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	r, g, b, _ := img.At(50, 50).RGBA()
	r, g, b = r>>8, g>>8, b>>8
	if r < 250 {
		t.Errorf("red channel = %d, want ~255 (both layers agree on full red)", r)
	}
	if g < 100 || g > 155 || b < 100 || b > 155 {
		t.Errorf("green/blue = (%d,%d), want both roughly 127 (halfway between 0 and 255 background)", g, b)
	}
}

// TestRenderJPEGImage confirms a DCTDecode (JPEG)-filtered image
// (buildImageJPEG: a solid dark-blue 4x4 JPEG) decodes and paints with
// only the small color error expected from lossy JPEG compression.
func TestRenderJPEGImage(t *testing.T) {
	img := renderPage(t, "image-jpeg.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	assertRGB8(t, img, 50, 50, 20, 40, 200, 20)
}

// TestRenderJBIG2Image confirms a JBIG2Decode-filtered bilevel image
// (buildImageJBIG2: a 32x32 bitmap, black top-left quadrant and black
// one-pixel border, otherwise white) decodes and paints correctly end to
// end - through the real cross-reference/object-resolution pipeline and
// internal/image's 1-bit sample handling, not just internal/filter's own
// unit tests.
//
// The asserted points are chosen to pin down the two things a bilevel
// pipeline most easily gets wrong. Black and white being the right way
// round: JBIG2 defines 1 as black while a 1-bit DeviceGray sample's 0 is
// black, so the decoder inverts (see jbig2.go's packInverted), and an
// inversion bug would swap every expectation below. And the image's
// orientation: only the *top-left* quadrant is black, so a horizontal or
// vertical flip would move it to a quadrant this test expects to be
// white.
func TestRenderJBIG2Image(t *testing.T) {
	img := renderPage(t, "image-jbig2.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	assertRGB8(t, img, 25, 25, 0, 0, 0, 2)       // top-left quadrant: black
	assertRGB8(t, img, 75, 25, 255, 255, 255, 2) // top-right quadrant: white
	assertRGB8(t, img, 25, 75, 255, 255, 255, 2) // bottom-left quadrant: white
	assertRGB8(t, img, 75, 75, 255, 255, 255, 2) // bottom-right quadrant: white
	assertRGB8(t, img, 50, 1, 0, 0, 0, 2)        // top border
	assertRGB8(t, img, 1, 50, 0, 0, 0, 2)        // left border
	assertRGB8(t, img, 98, 98, 0, 0, 0, 2)       // bottom-right border corner
}

// TestRenderInlineImage confirms an inline ("BI"/"ID"/"EI") image
// (buildInlineImage: a 2x1 red/green DeviceRGB image, no /Resources
// /XObject entry at all) paints correctly - the inline-image counterpart
// to TestRenderReferencedImageRGB.
func TestRenderInlineImage(t *testing.T) {
	img := renderPage(t, "inline-image.pdf")
	if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	assertRGB8(t, img, 25, 50, 255, 0, 0, 2) // left half: red
	assertRGB8(t, img, 75, 50, 0, 255, 0, 2) // right half: green
}

// TestRenderRotatedPage confirms Page.Render's /Rotate handling (see
// page.go's pageDeviceGeometry) end to end: rotated-page.pdf declares
// /Rotate 90 on a 100x200 MediaBox with a 20x20 red square filled at its
// own user-space origin, which pageDeviceGeometry's rotation math (see
// buildRotatedPage's doc comment for the hand-worked-out expected
// result) places at the top-left corner of a 200x100 rendered image.
func TestRenderRotatedPage(t *testing.T) {
	img := renderPage(t, "rotated-page.pdf")
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("Render size = %dx%d, want 200x100 (MediaBox 100x200 with /Rotate 90 swaps dimensions)", b.Dx(), b.Dy())
	}
	assertRGB8(t, img, 5, 5, 255, 0, 0, 2)       // inside the rotated red square
	assertRGB8(t, img, 50, 50, 255, 255, 255, 2) // outside it: background white
}
