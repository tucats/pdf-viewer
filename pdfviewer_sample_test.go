package pdfviewer_test

import (
	"context"
	"path/filepath"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// realWorldFixturesDir holds PDFs produced by other software, as opposed
// to the hand-authored, byte-exact corpus tools/genfixtures writes to
// testdata/fixtures/handmade. Every file here is documented - with its
// provenance and license - in testdata/fixtures/FIXTURES.md.
//
// The distinction matters for what a test may assume. A handmade
// fixture's every byte is chosen by this project, so a test can assert
// exactly what should come out of it. A sample was produced by a real
// encoder whose choices this project does not control, which is exactly
// what makes it valuable: it exercises combinations of features no
// hand-authored fixture would think to produce.
const realWorldFixturesDir = "testdata/fixtures/real-world"

func realWorldPath(name string) string {
	return filepath.Join(realWorldFixturesDir, name)
}

// TestRenderRealWorldJBIG2Sample renders pdf-with-jbig2.pdf, a scan from
// a Xerox WorkCentre copier, and compares it against a checked-in
// reference image exactly as TestRenderMatchesReferenceImages does for
// the generated fixtures.
//
// This is the only end-to-end check in this project against JBIG2 bytes
// produced by a real encoder rather than by internal/filter's own
// encoder, which makes it the one test that could catch this package and
// its encoder agreeing with each other on something ITU-T T.88 does not
// actually say. The file uses JBIG2's symbol mode throughout: a
// 55-symbol dictionary in a /JBIG2Globals stream, a second 114-symbol
// dictionary in the image's own stream, and two text regions - one per
// horizontal stripe of the page - placing 1025 symbol instances between
// them from both dictionaries at once. It also exercises 4- and 8-row
// strips, a non-zero SBDSOFFSET, and a page whose /Rotate is 270.
func TestRenderRealWorldJBIG2Sample(t *testing.T) {
	doc, err := pdfviewer.OpenFile(realWorldPath("pdf-with-jbig2.pdf"))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	if got := doc.PageCount(); got != 1 {
		t.Fatalf("PageCount() = %d, want 1", got)
	}
	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}

	img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The page's MediaBox is 792x612, but /Rotate 270 swaps the rendered
	// dimensions.
	if b := img.Bounds(); b.Dx() != 612 || b.Dy() != 792 {
		t.Fatalf("Render size = %dx%d, want 612x792", b.Dx(), b.Dy())
	}

	refPath := filepath.Join(renderRefsDir, "sample-jbig2.png")
	if *update {
		writePNG(t, refPath, img)
		return
	}
	compareImages(t, img, readPNG(t, refPath))
}
