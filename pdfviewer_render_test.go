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

func renderFixture(t *testing.T, name string, opts ...pdfviewer.OpenOption) image.Image {
	t.Helper()
	doc, err := pdfviewer.OpenFile(fixturePath(name), opts...)
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

// TestRenderStrokeJoins exercises Phase 15a's real miter/bevel join
// geometry (internal/graphics's addJoin) through the full content-stream
// pipeline ("j" operator -> graphics.State.LineJoin -> StrokeToFill),
// not just at the internal/graphics unit-test level - see
// tools/genfixtures's buildStrokeJoins doc comment for exactly how this
// fixture's two corners (one miter, one bevel, otherwise identical) were
// derived, and internal/graphics/stroke_test.go's
// TestStrokeToFillMiterJoinReachesComputedTip for the same hand-derived
// geometry checked directly against StrokeToFill's output.
//
// The one PDF-space point that distinguishes the two - (40,44), safely
// inside the miter join's extra "spike" triangle but safely outside
// where a bevel join would have stopped - becomes device pixel (40,56)
// after this 100x100 page's standard y-flip (device_y = 100 - pdf_y);
// its mirror image 40 points to the right, (80,56), lands in the
// corresponding spot on the bevel-joined copy.
func TestRenderStrokeJoins(t *testing.T) {
	img := renderFixture(t, "stroke-joins.pdf")
	assertPixel(t, img, 40, 56, 0, 0, 255)     // inside the miter join's spike: blue
	assertPixel(t, img, 80, 56, 255, 255, 255) // same relative spot, bevel join: no spike, white
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

// TestRenderEncryptedMatchesPlainContent is this package's end-to-end,
// whole-page regression test for Phase 7a (see docs/PLAN-V2.md): opening
// and rendering a page from a Standard Security Handler-encrypted
// document (empty user password) must produce pixel-identical output to
// rendering the same content unencrypted, since decryption happens
// transparently, deep inside internal/parser.Document.Resolve, long
// before internal/content or internal/graphics ever see a byte of page
// content. Each of the three fixtures below (see
// tools/genfixtures/main.go's buildEncryptedRC4_40bit, buildEncryptedAES128,
// and buildEncryptedAES256) encrypts the exact same content
// filled-rect.pdf draws unencrypted, using a different revision/cipher
// combination - RC4 40-bit, AES-128, and AES-256 respectively - so this
// one test exercises every decryption code path this package added for
// Phase 7a via the same rendering pipeline a real caller uses, not just
// internal/parser's own lower-level object-resolution tests
// (TestOpenEncryptedDocumentEmptyPasswordDecrypts in
// internal/parser/parser_test.go).
func TestRenderEncryptedMatchesPlainContent(t *testing.T) {
	plain := renderFixture(t, "filled-rect.pdf")
	for _, name := range []string{"encrypted-rc4-40bit.pdf", "encrypted-aes128.pdf", "encrypted-aes256.pdf"} {
		t.Run(name, func(t *testing.T) {
			compareImages(t, renderFixture(t, name), plain)
		})
	}
}

// TestRenderEncryptedWithPasswordMatchesPlainContent is
// TestRenderEncryptedMatchesPlainContent's Phase 7b counterpart (see
// docs/PLAN-V2.md): a document whose user password is genuinely
// non-empty must, given the correct password via WithPassword, render
// pixel-identically to the same unencrypted content too - decryption
// happening correctly is not by itself enough evidence that the right
// *key* was used unless the actually-decrypted content is verified, and
// full-page rendering is the most end-to-end way this project has to
// verify that.
func TestRenderEncryptedWithPasswordMatchesPlainContent(t *testing.T) {
	plain := renderFixture(t, "filled-rect.pdf")
	for _, name := range []string{"encrypted-password-aes128.pdf", "encrypted-password-aes256.pdf"} {
		t.Run(name, func(t *testing.T) {
			got := renderFixture(t, name, pdfviewer.WithPassword(encryptedPasswordFixturePassword))
			compareImages(t, got, plain)
		})
	}
}

// TestRenderTilingPatternFill exercises a tiling pattern used as a fill
// paint source: tools/genfixtures/main.go's buildTilingPatternFill's own
// doc comment works out, by hand, that the cell spanning pattern-space
// (60,60)-(80,80) places its own 10x10 red square at device
// (60,30)-(70,40), while a point elsewhere in that same cell (still
// within the filled 80x80 square, but outside any cell's own red
// square) must remain the untouched white background.
func TestRenderTilingPatternFill(t *testing.T) {
	img := renderFixture(t, "tiling-pattern-fill.pdf")
	assertPixel(t, img, 65, 35, 255, 0, 0)     // inside a cell's red square
	assertPixel(t, img, 78, 38, 255, 255, 255) // same cell, outside the red square
	assertPixel(t, img, 5, 5, 255, 255, 255)   // outside the filled shape entirely
}

// TestRenderType4TintTransformFill exercises Phase 19's (see
// docs/PLAN-V3.md) Type 4 (PostScript calculator) function support through
// the "cs"/"scn" content-stream call site: tools/genfixtures's
// buildType4TintTransformFill doc comment works out, by hand, the exact
// RGB each of its two rectangles' DeviceN tints must produce once passed
// through the fixture's "{ 1 exch sub dup dup dup }" tint transform and
// then this project's baseline CMYK->RGB formula. Repeating that math here
// (rather than just trusting the fixture comment) is deliberate: it is the
// same "derive the expected pixel by hand, independently of the code under
// test" discipline every other direct-pixel test in this file already
// follows (see, for example, TestRenderFilledRect's comment above).
//
//   - Left rectangle, tint 0.75: transform gives C=M=Y=K=1-0.75=0.25, so
//     CMYK->RGB's r=1-min(1,C+K) is 1-min(1,0.5)=0.5 -> RGB (128,128,128).
//     A tint-guessing fallback (treating 0.75 as if it were already a
//     DeviceGray value) would instead produce a much lighter
//     (191,191,191), so this point alone is enough to prove the real
//     Type 4 program ran.
//   - Right rectangle, tint 0.2: C=M=Y=K=1-0.2=0.8, so C+K=1.6 clamps to
//     1, giving r=1-1=0 -> flat black (0,0,0) - again distinguishable from
//     a naive gray-equals-tint guess, which would produce a dim
//     (51,51,51) instead of true black.
func TestRenderType4TintTransformFill(t *testing.T) {
	img := renderFixture(t, "type4-tint-transform-fill.pdf")
	assertPixel(t, img, 30, 50, 128, 128, 128) // left rectangle (tint 0.75): mid-gray
	assertPixel(t, img, 75, 50, 0, 0, 0)       // right rectangle (tint 0.2): flat black
	assertPixel(t, img, 5, 5, 255, 255, 255)   // outside both rectangles: untouched white background
}

// TestRenderType4AxialShading exercises Phase 19's other Type 4 call site,
// a shading's own /Function (internal/content/shading.go's doShading),
// using tools/genfixtures's buildType4AxialShading fixture: a black-white-
// black "hump" gradient (via the Type 4 program "{ 180 mul sin dup dup
// }") that only a genuine per-point function evaluation - not a Type 2/3
// function's fixed monotonic curve shapes - can produce, since the color
// must turn around partway through the gradient. See that fixture's own
// doc comment for the by-hand sin() derivation of each sampled device
// column; briefly, the shading's /Coords [0 0 100 0] maps this 100-wide
// page's device x coordinate onto the function's own /Domain [0 1] input
// t as t = (x+0.5)/100 (Page.Render samples each pixel's center, not its
// left edge), and the y coordinate does not affect the color at all (an
// axial shading varies only along its axis), so any row works for
// sampling.
func TestRenderType4AxialShading(t *testing.T) {
	img := renderFixture(t, "type4-axial-shading.pdf")
	assertPixel(t, img, 0, 50, 4, 4, 4)        // t~0.005: sin(0.9 deg)~0.0157 -> near black
	assertPixel(t, img, 50, 50, 255, 255, 255) // t~0.505: sin(90.9 deg)~0.9999 -> white, the "hump" peak
	assertPixel(t, img, 90, 50, 75, 75, 75)    // t~0.905: sin(162.9 deg)~0.2940 -> a distinct dark gray
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
		"stroke-joins.pdf",
		"clipped-rect.pdf",
		"transformed-rect.pdf",
		"image-rgb.pdf",
		"image-mask.pdf",
		"image-smask.pdf",
		"image-jpeg.pdf",
		"image-jbig2.pdf",
		"image-jbig2-text.pdf",
		"image-jpx.pdf",
		"image-jpx-rgb.pdf",
		"inline-image.pdf",
		"rotated-page.pdf",
		"page-boxes.pdf",
		"pdf20-classic-xref.pdf",
		"pdf20-xref-stream.pdf",
		"pdf20-encrypted-aes256.pdf",
		"text-simple-truetype.pdf",
		"text-simple-type1.pdf",
		"text-scaled.pdf",
		"text-type0-identity.pdf",
		"text-type0-embedded-cmap.pdf",
		"text-type0-predefined-encoding.pdf",
		"text-notdef-fallback.pdf",
		"text-rotated-page.pdf",
		"text-tounicode-simple.pdf",
		"text-tounicode-type0.pdf",
		"separation-fill.pdf",
		"type4-tint-transform-fill.pdf",
		"lab-fill.pdf",
		"axial-shading.pdf",
		"type4-axial-shading.pdf",
		"radial-shading.pdf",
		"function-based-shading.pdf",
		"mesh-shading-type4.pdf",
		"mesh-shading-type5.pdf",
		"mesh-shading-type6.pdf",
		"mesh-shading-type7.pdf",
		"shading-pattern-fill.pdf",
		"form-xobject.pdf",
		"annotation-appearance.pdf",
		"annotation-hidden.pdf",
		"form-filled-no-appearance.pdf",
		"alpha-fill.pdf",
		"blend-multiply.pdf",
		"tiling-pattern-fill.pdf",
		"softmask-luminosity.pdf",
		"softmask-alpha.pdf",
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
