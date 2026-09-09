package model

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tucats/pdf-viewer/internal/parser"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/source"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

const fixturesDir = "../../testdata/fixtures/handmade"

func openFixture(t *testing.T, name string) *Document {
	t.Helper()
	d, err := openFixtureErr(t, name)
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	return d
}

func openFixtureErr(t *testing.T, name string) (*Document, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixturesDir, name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	src, err := source.New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("source.New: %v", err)
	}
	p, err := parser.Open(src)
	if err != nil {
		t.Fatalf("parser.Open(%s): %v", name, err)
	}
	return Open(p)
}

func TestOpenMinimalBlankPage(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")

	if got, want := d.PageCount(), 1; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}
	got := d.Page(0).MediaBox
	want := Rect{LLX: 0, LLY: 0, URX: 200, URY: 200}
	if got != want {
		t.Errorf("Page(0).MediaBox = %+v, want %+v", got, want)
	}
}

func TestOpenTwoPages(t *testing.T) {
	d := openFixture(t, "two-pages.pdf")

	if got, want := d.PageCount(), 2; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}

	want := []Rect{
		{LLX: 0, LLY: 0, URX: 100, URY: 200},
		{LLX: 0, LLY: 0, URX: 300, URY: 400},
	}
	for i, w := range want {
		if got := d.Page(i).MediaBox; got != w {
			t.Errorf("Page(%d).MediaBox = %+v, want %+v", i, got, w)
		}
	}
}

func TestOpenIncrementalUpdate(t *testing.T) {
	d := openFixture(t, "incremental-update.pdf")

	if got, want := d.PageCount(), 2; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}

	want := []Rect{
		{LLX: 0, LLY: 0, URX: 200, URY: 200},
		{LLX: 0, LLY: 0, URX: 250, URY: 350},
	}
	for i, w := range want {
		if got := d.Page(i).MediaBox; got != w {
			t.Errorf("Page(%d).MediaBox = %+v, want %+v", i, got, w)
		}
	}
}

func TestOpenMalformedBadXrefOffsetRecovers(t *testing.T) {
	d := openFixture(t, "malformed-bad-xref-offset.pdf")

	if got, want := d.PageCount(), 1; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}
	want := Rect{LLX: 0, LLY: 0, URX: 200, URY: 200}
	if got := d.Page(0).MediaBox; got != want {
		t.Errorf("Page(0).MediaBox = %+v, want %+v", got, want)
	}
}

// TestMediaBoxIsInherited constructs a small document by hand (rather
// than via a checked-in fixture; see the equivalent note in
// internal/parser/parser_test.go) where /MediaBox is set only on the
// Pages tree root, not on the page itself, to confirm the specific
// inheritance rule this package exists to implement.
func TestMediaBoxIsInherited(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)

	if got, want := d.PageCount(), 1; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}
	want := Rect{LLX: 0, LLY: 0, URX: 612, URY: 792}
	if got := d.Page(0).MediaBox; got != want {
		t.Errorf("Page(0).MediaBox = %+v, want %+v (should have been inherited)", got, want)
	}
}

// TestPageMediaBoxOverridesInherited confirms a page's own /MediaBox
// takes precedence over one inherited from an ancestor, rather than the
// other way around or the two somehow merging.
func TestPageMediaBoxOverridesInherited(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] >> endobj
`)
	d := openTestDocument(t, data)
	want := Rect{LLX: 0, LLY: 0, URX: 100, URY: 100}
	if got := d.Page(0).MediaBox; got != want {
		t.Errorf("Page(0).MediaBox = %+v, want %+v (page's own box should win)", got, want)
	}
}

// TestResourcesAreInherited exercises the same inheritance rule as
// TestMediaBoxIsInherited, but for /Resources, and checks it via
// RawResources rather than MediaBox.
func TestResourcesAreInherited(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] /Resources << /Font << /F1 4 0 R >> >> >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
4 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj
`)
	d := openTestDocument(t, data)
	res := d.Page(0).RawResources
	if res == nil {
		t.Fatal("Page(0).RawResources = nil, want inherited /Resources dictionary")
	}
	if _, ok := res["Font"]; !ok {
		t.Errorf("Page(0).RawResources = %#v, want a /Font entry", res)
	}
}

// TestRotateIsInheritedAndNormalized exercises the same inheritance rule
// as TestMediaBoxIsInherited, but for /Rotate, and additionally checks
// that a negative rotation value is normalized into [0,360).
func TestRotateIsInheritedAndNormalized(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] /Rotate -90 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	if got, want := d.Page(0).Rotate, 270; got != want {
		t.Errorf("Page(0).Rotate = %d, want %d (-90 normalized into [0,360))", got, want)
	}
}

func TestRotateDefaultsToZero(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	if got := d.Page(0).Rotate; got != 0 {
		t.Errorf("Page(0).Rotate = %d, want 0", got)
	}
}

// TestPageRotateOverridesInherited confirms a page's own /Rotate takes
// precedence over one inherited from an ancestor.
func TestPageRotateOverridesInherited(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] /Rotate 90 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /Rotate 180 >> endobj
`)
	d := openTestDocument(t, data)
	if got, want := d.Page(0).Rotate, 180; got != want {
		t.Errorf("Page(0).Rotate = %d, want %d (page's own value should win)", got, want)
	}
}

// TestRotateNotAMultipleOf90FallsBackToDefault confirms a malformed
// /Rotate value (not a multiple of 90, which the specification requires)
// does not propagate as a nonsensical rotation - it falls back to
// whatever was otherwise inherited (here, nothing, so 0).
func TestRotateNotAMultipleOf90FallsBackToDefault(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 10 10] /Rotate 45 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	if got := d.Page(0).Rotate; got != 0 {
		t.Errorf("Page(0).Rotate = %d, want 0 (invalid /Rotate 45 should not propagate)", got)
	}
}

// TestMultiLevelPageTree confirms traversal works through more than one
// level of intermediate Pages nodes, not just a single flat Kids array,
// and that document order is preserved.
func TestMultiLevelPageTree(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R 6 0 R] /Count 2 /MediaBox [0 0 10 10] >> endobj
3 0 obj << /Type /Pages /Parent 2 0 R /Kids [4 0 R 5 0 R] /Count 2 >> endobj
4 0 obj << /Type /Page /Parent 3 0 R /MediaBox [0 0 1 1] >> endobj
5 0 obj << /Type /Page /Parent 3 0 R /MediaBox [0 0 2 2] >> endobj
6 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 3 3] >> endobj
`)
	d := openTestDocument(t, data)
	if got, want := d.PageCount(), 3; got != want {
		t.Fatalf("PageCount() = %d, want %d", got, want)
	}
	wantWidths := []float64{1, 2, 3}
	for i, w := range wantWidths {
		if got := d.Page(i).MediaBox.URX; got != w {
			t.Errorf("Page(%d).MediaBox.URX = %v, want %v (wrong document order)", i, got, w)
		}
	}
}

// TestPageTreeCycleIsRejected constructs a Pages node that lists itself
// as its own kid, which would otherwise send collectPages into infinite
// recursion; see collectPages' cycle guard.
func TestPageTreeCycleIsRejected(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [2 0 R] /Count 1 /MediaBox [0 0 10 10] >> endobj
`)
	if _, err := openTestDocumentErr(t, data); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

// TestPageWithNoMediaBoxIsMalformed confirms that a page with no
// /MediaBox anywhere in its ancestry - which the PDF specification does
// not permit - is reported as malformed rather than silently defaulting
// to some made-up size.
func TestPageWithNoMediaBoxIsMalformed(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	if _, err := openTestDocumentErr(t, data); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

// TestCropBoxDefaultsToMediaBox confirms a page with no /CropBox
// anywhere in its ancestry reports CropBox equal to MediaBox - the
// specification's documented default, and this package's fallback for
// every document this project's own test corpus (fixture PDFs above) has
// used until now, which is why none of those cases needed a CropBox
// field at all before it existed.
func TestCropBoxDefaultsToMediaBox(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	page := d.Page(0)
	if page.CropBox != page.MediaBox {
		t.Errorf("CropBox = %+v, MediaBox = %+v, want them equal when /CropBox is absent", page.CropBox, page.MediaBox)
	}
}

// TestCropBoxIsInherited exercises the same inheritance rule
// TestMediaBoxIsInherited does, for /CropBox instead of /MediaBox.
func TestCropBoxIsInherited(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] /CropBox [10 10 600 780] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	want := Rect{LLX: 10, LLY: 10, URX: 600, URY: 780}
	if got := d.Page(0).CropBox; got != want {
		t.Errorf("Page(0).CropBox = %+v, want %+v (should have been inherited)", got, want)
	}
}

// TestPageCropBoxOverridesInherited confirms a page's own /CropBox takes
// precedence over one inherited from an ancestor.
func TestPageCropBoxOverridesInherited(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 612 792] /CropBox [10 10 600 780] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /CropBox [0 0 100 100] >> endobj
`)
	d := openTestDocument(t, data)
	want := Rect{LLX: 0, LLY: 0, URX: 100, URY: 100}
	if got := d.Page(0).CropBox; got != want {
		t.Errorf("Page(0).CropBox = %+v, want %+v (page's own box should win)", got, want)
	}
}

// TestCropBoxIsClippedToMediaBox is this package's regression test for
// the real-world case that motivated adding CropBox support in the
// first place: a print-production PDF (crop marks, bleed, and color
// calibration bars scanned or laid out around the actual trimmed page)
// whose /MediaBox spans the whole physical sheet and whose /CropBox
// names the smaller trimmed region within it - see resolveCropBox's own
// doc comment for the specification citation. This uses a /CropBox that
// extends beyond /MediaBox on every side to confirm the clipping happens
// (a well-formed real-world file's CropBox is normally already within
// its MediaBox, as in the case above, so this specifically exercises
// the "producer got it wrong" path).
func TestCropBoxIsClippedToMediaBox(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 100 100] /CropBox [-50 -50 150 150] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	want := Rect{LLX: 0, LLY: 0, URX: 100, URY: 100}
	if got := d.Page(0).CropBox; got != want {
		t.Errorf("Page(0).CropBox = %+v, want %+v (should be clipped to MediaBox)", got, want)
	}
}

// TestCropBoxDegenerateFallsBackToMediaBox confirms a /CropBox that does
// not overlap /MediaBox at all (which real producers should never write,
// but nothing in the file format actually forbids) falls back to the
// whole MediaBox rather than producing a zero-size or negative-size
// page - see resolveCropBox's own doc comment for the rationale.
func TestCropBoxDegenerateFallsBackToMediaBox(t *testing.T) {
	data := buildTestDocument(t, `
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 /MediaBox [0 0 100 100] /CropBox [200 200 300 300] >> endobj
3 0 obj << /Type /Page /Parent 2 0 R >> endobj
`)
	d := openTestDocument(t, data)
	page := d.Page(0)
	if page.CropBox != page.MediaBox {
		t.Errorf("CropBox = %+v, MediaBox = %+v, want them equal when /CropBox does not overlap MediaBox at all", page.CropBox, page.MediaBox)
	}
}

// --- test helpers -----------------------------------------------------

// buildTestDocument assembles a minimal, syntactically valid PDF file
// from a fragment of already-written indirect object definitions (each
// "N G obj ... endobj", one per line as passed in objects), computing a
// correct classic cross-reference table for them the same way
// tools/genfixtures does for the checked-in fixture corpus. This lets
// model package tests describe the exact object graph they want to
// exercise (page tree shape, inheritance) far more directly than
// composing full fixture files would allow, without ever hand-computing
// a byte offset.
func buildTestDocument(t *testing.T, objects string) []byte {
	t.Helper()

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")

	// Split the fragment into individual "N G obj ... endobj" pieces on
	// the "endobj" keyword, appending each to buf and recording the
	// byte offset it landed at - exactly the information a
	// cross-reference table needs - as it goes. This mirrors, in
	// miniature, what tools/genfixtures/main.go's builder type does for
	// the checked-in fixture corpus.
	offsets := make(map[int]int)
	var maxNum int
	remaining := objects
	for strings.TrimSpace(remaining) != "" {
		remaining = strings.TrimSpace(remaining)
		idx := strings.Index(remaining, "endobj")
		if idx < 0 {
			t.Fatalf("test document fragment has an object with no endobj: %q", remaining)
		}
		piece := remaining[:idx+len("endobj")]
		remaining = remaining[idx+len("endobj"):]

		num := firstObjectNumber(t, piece)
		offsets[num] = buf.Len()
		if num > maxNum {
			maxNum = num
		}
		buf.WriteString(piece)
		buf.WriteString("\n")
	}

	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", maxNum+1)
	for n := 1; n <= maxNum; n++ {
		if off, ok := offsets[n]; ok {
			fmt.Fprintf(&buf, "%010d 00000 n \n", off)
		} else {
			buf.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\n", maxNum+1)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)

	return buf.Bytes()
}

// firstObjectNumber reads just the leading object number off a "N G obj
// ..." fragment, using the real tokenizer so the test helper stays in
// sync with how the module itself parses object headers.
func firstObjectNumber(t *testing.T, piece string) int {
	t.Helper()
	lex := syntax.NewLexer(strings.NewReader(piece))
	tok, err := lex.Next()
	if err != nil || tok.Kind != syntax.KindNumber {
		t.Fatalf("expected test document fragment to start with an object number: %q", piece)
	}
	num, err := strconv.Atoi(tok.Text)
	if err != nil {
		t.Fatalf("object number %q is not a valid integer: %v", tok.Text, err)
	}
	return num
}

func openTestDocument(t *testing.T, data []byte) *Document {
	t.Helper()
	d, err := openTestDocumentErr(t, data)
	if err != nil {
		t.Fatalf("opening test document: %v", err)
	}
	return d
}

func openTestDocumentErr(t *testing.T, data []byte) (*Document, error) {
	t.Helper()
	src, err := source.New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("source.New: %v", err)
	}
	p, err := parser.Open(src)
	if err != nil {
		t.Fatalf("parser.Open: %v", err)
	}
	return Open(p)
}
