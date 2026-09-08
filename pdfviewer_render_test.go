package pdfviewer_test

import (
	"context"
	"flag"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file tests Page.Render's actual rendered output against the
// hand-authored vector fixtures added for Phase 2 (see
// tools/genfixtures/main.go's buildFilledRect, buildStrokedLine,
// buildClippedRect, and buildTransformedRect doc comments) - the whole
// point of those fixtures existing at all. Phase 3's image fixtures
// (buildImageRGB and friends) are exercised with their own direct
// pixel-sampling tests in pdfviewer_image_test.go, but are included in
// this file's TestRenderMatchesReferenceImages below so they get the
// same whole-image regression coverage the vector fixtures do. Two
// complementary styles of check are used:
//
//   - Direct pixel sampling: a handful of specific (x, y) points, each
//     with a known expected color derived by hand from the fixture's own
//     content stream and page geometry (see each test's comment for the
//     derivation) - useful because a failure points immediately at
//     "this specific pixel is wrong" without needing to inspect an
//     image.
//   - Whole-image comparison against a checked-in reference PNG under
//     testdata/renderrefs, within a documented numeric tolerance (see
//     compareImages) - this is Phase 2's exit criterion from the
//     repository README ("compare rendered output against checked-in
//     reference images with a documented tolerance") and catches any
//     regression to pixels the hand-picked sample points above don't
//     happen to cover.

// update regenerates every checked-in reference PNG from the renderer's
// current output instead of comparing against it - the standard Go
// "golden file" testing idiom. Run:
//
//	go test . -run TestRenderMatchesReferenceImages -update
//
// after a deliberate, reviewed change to rendering output, and commit
// the resulting testdata/renderrefs/*.png files alongside the code
// change that caused them to differ.
var update = flag.Bool("update", false, "update golden reference images in testdata/renderrefs instead of comparing against them")

const renderRefsDir = "testdata/renderrefs"

func renderFixture(t *testing.T, name string) image.Image {
	t.Helper()
	doc, err := pdfviewer.OpenFile(fixturePath(name))
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", name, err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
	if err != nil {
		t.Fatalf("Render(%s): %v", name, err)
	}
	return img
}

func rgba8(img image.Image, x, y int) (r, g, b, a int) {
	rr, gg, bb, aa := img.At(x, y).RGBA()
	return int(rr >> 8), int(gg >> 8), int(bb >> 8), int(aa >> 8)
}

func assertPixel(t *testing.T, img image.Image, x, y, wantR, wantG, wantB int) {
	t.Helper()
	r, g, b, a := rgba8(img, x, y)
	if abs(r-wantR) > 2 || abs(g-wantG) > 2 || abs(b-wantB) > 2 || a != 255 {
		t.Errorf("pixel (%d,%d) = (%d,%d,%d,%d), want ~(%d,%d,%d,255)", x, y, r, g, b, a, wantR, wantG, wantB)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestRenderFilledRect exercises the simplest possible painted page: a
// solid red square from (10,10) to (90,90) in PDF user space (y-up),
// which - after the standard y-flip to image space (y-down) on this
// 100x100 page - covers image rows/columns [10,90).
func TestRenderFilledRect(t *testing.T) {
	img := renderFixture(t, "filled-rect.pdf")
	if img.Bounds() != image.Rect(0, 0, 100, 100) {
		t.Fatalf("bounds = %v, want (0,0)-(100,100)", img.Bounds())
	}
	assertPixel(t, img, 50, 50, 255, 0, 0)   // deep interior: red
	assertPixel(t, img, 5, 5, 255, 255, 255) // outside the square: white
	assertPixel(t, img, 89, 89, 255, 0, 0)   // just inside the far edge: red
}

// TestRenderStrokedLine exercises "S" (stroke) with a non-default line
// width: a blue diagonal line from (10,10) to (90,90) in PDF space,
// width 4, which after the y-flip runs anti-diagonally through the
// image's center.
func TestRenderStrokedLine(t *testing.T) {
	img := renderFixture(t, "stroked-line.pdf")
	assertPixel(t, img, 50, 49, 0, 0, 255)   // on the line, near its midpoint: blue
	assertPixel(t, img, 5, 5, 255, 255, 255) // far corner, off the line: white
}

// TestRenderClippedRect exercises "W"/"n" clipping: content clips to a
// 40x40 square centered on the page ([30,70] both axes, symmetric so
// the y-flip does not move it), then fills the *entire* page green - only
// the clipped square should actually end up green.
func TestRenderClippedRect(t *testing.T) {
	img := renderFixture(t, "clipped-rect.pdf")
	assertPixel(t, img, 50, 50, 0, 255, 0)   // center, inside the clip: green
	assertPixel(t, img, 5, 5, 255, 255, 255) // outside the clip: white
	assertPixel(t, img, 31, 68, 0, 255, 0)   // just inside a clip corner: green
	assertPixel(t, img, 69, 32, 0, 255, 0)   // just inside the opposite corner: green
}

// TestRenderTransformedRect exercises "cm" (translate then rotate 45
// degrees) applied before filling a square: the square's center lands
// exactly on the page's center regardless of the rotation (rotation
// around the already-translated origin does not move the origin
// itself), so the page center must be filled, while points well outside
// the rotated square's ~28-unit half-diagonal must not be.
func TestRenderTransformedRect(t *testing.T) {
	img := renderFixture(t, "transformed-rect.pdf")
	assertPixel(t, img, 50, 50, 255, 128, 0) // page center: orange
	assertPixel(t, img, 5, 5, 255, 255, 255)
	assertPixel(t, img, 95, 95, 255, 255, 255)
}

// TestRenderFlateContentMatchesPlainContent confirms
// internal/filter.Decode is correctly wired into the content-stream
// pipeline (internal/model.PageContentBytes ->
// internal/parser.Document.DecodeStream -> internal/filter.Decode):
// flate-content-rect.pdf draws the exact same red square as
// filled-rect.pdf, but with its content stream Flate-compressed rather
// than stored as plain text, so the two must render identically.
func TestRenderFlateContentMatchesPlainContent(t *testing.T) {
	plain := renderFixture(t, "filled-rect.pdf")
	flate := renderFixture(t, "flate-content-rect.pdf")
	compareImages(t, flate, plain)
}

// TestRenderMatchesReferenceImages is Phase 2's "compare rendered output
// against checked-in reference images with a documented tolerance" exit
// criterion (see the repository README's Phase 2 entry): it re-renders
// every vector fixture and compares the result pixel-for-pixel against a
// checked-in PNG under testdata/renderrefs, allowing the tolerance
// documented on compareImages.
func TestRenderMatchesReferenceImages(t *testing.T) {
	names := []string{
		"minimal-blank-page.pdf",
		"filled-rect.pdf",
		"stroked-line.pdf",
		"clipped-rect.pdf",
		"transformed-rect.pdf",
		"image-rgb.pdf",
		"image-mask.pdf",
		"image-smask.pdf",
		"image-jpeg.pdf",
		"inline-image.pdf",
		"rotated-page.pdf",
	}

	if *update {
		if err := os.MkdirAll(renderRefsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", renderRefsDir, err)
		}
	}

	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			img := renderFixture(t, name)
			refPath := filepath.Join(renderRefsDir, refName(name))

			if *update {
				writePNG(t, refPath, img)
				return
			}

			ref := readPNG(t, refPath)
			compareImages(t, img, ref)
		})
	}
}

func refName(fixtureName string) string {
	return fixtureName[:len(fixtureName)-len(filepath.Ext(fixtureName))] + ".png"
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encoding %s: %v", path, err)
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening reference image %s: %v (run with -update to generate it)", path, err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decoding reference image %s: %v", path, err)
	}
	return img
}

// maxMeanChannelDiff is the documented tolerance TestRenderMatchesReferenceImages
// allows: the mean absolute difference, across every color and alpha
// channel of every pixel, between the freshly rendered image and its
// checked-in reference, expressed in 8-bit (0-255) units. This
// project's rasterizer is deterministic pure Go (see internal/raster's
// package doc comment) and is expected to reproduce a reference image
// exactly on the platform it was generated on; this tolerance exists to
// absorb last-bit floating-point differences in math.Sqrt/Cos/Sin
// (used by stroke join/cap circles - see graphics.StrokeToFill) that
// could in principle differ by a rendered pixel or two across CPU
// architectures in this project's multi-platform CI (see
// .github/workflows/ci.yml), without masking an actual rendering
// regression, which would shift far more than a handful of edge pixels
// by far more than one unit.
const maxMeanChannelDiff = 0.5

func compareImages(t *testing.T, got, want image.Image) {
	t.Helper()
	if got.Bounds().Dx() != want.Bounds().Dx() || got.Bounds().Dy() != want.Bounds().Dy() {
		t.Fatalf("image size = %v, want %v", got.Bounds(), want.Bounds())
	}

	gb, wb := got.Bounds(), want.Bounds()
	var total, n float64
	for y := 0; y < gb.Dy(); y++ {
		for x := 0; x < gb.Dx(); x++ {
			gr, gg, gbl, ga := got.At(gb.Min.X+x, gb.Min.Y+y).RGBA()
			wr, wg, wbl, wa := want.At(wb.Min.X+x, wb.Min.Y+y).RGBA()
			total += chanDiff(gr, wr) + chanDiff(gg, wg) + chanDiff(gbl, wbl) + chanDiff(ga, wa)
			n += 4
		}
	}
	mean := total / n
	if mean > maxMeanChannelDiff {
		t.Errorf("mean per-channel difference = %.4f, exceeds tolerance %.4f", mean, maxMeanChannelDiff)
	}
}

func chanDiff(a, b uint32) float64 {
	ai, bi := int(a>>8), int(b>>8)
	d := ai - bi
	if d < 0 {
		d = -d
	}
	return float64(d)
}
