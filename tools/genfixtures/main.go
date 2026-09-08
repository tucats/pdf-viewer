// Command genfixtures writes the hand-authored PDF files used as test
// fixtures under testdata/fixtures/handmade.
//
// # Why a generator instead of PDFs checked in from elsewhere
//
// The project's Phase 0 exit criteria (see the "Phased Plan" section of
// the repository README) call for "small hand-authored PDFs for each
// feature under test, plus permitted real-world fixtures with recorded
// licenses and provenance." A hand-authored PDF still needs byte-exact
// cross-reference offsets to be a *valid* PDF (the xref table records
// exactly where in the file each object starts), and computing those
// offsets by hand is tedious and error-prone. This program builds each
// fixture's bytes programmatically — tracking the offset of every object
// as it is written — so the offsets are always correct by construction,
// and so the fixture corpus is reproducible: anyone can regenerate the
// exact same files by running `go run ./tools/genfixtures`.
//
// The *content* of every fixture is still authored by this project
// (there is no PDF content copied from anywhere else), which is what
// "hand-authored" refers to in the README — the generation is just a
// mechanical way of assembling and byte-counting that content correctly.
//
// # How to run it
//
//	go run ./tools/genfixtures
//
// This overwrites every file in testdata/fixtures/handmade. Run it again
// after editing this file whenever a fixture's content needs to change;
// do not hand-edit the generated .pdf files, since any edit will
// desynchronize their xref offsets from their actual byte layout and
// produce a fixture that is invalid for reasons unrelated to what it is
// supposed to be testing.
package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// outputDir is where every generated fixture is written: the
// testdata/fixtures/handmade directory in the repository, computed as an
// absolute path from this source file's own location rather than from
// the process's current working directory. That matters because this
// package is entered two different ways that have two different working
// directories - `go run ./tools/genfixtures` runs with whatever
// directory the developer happened to be in (typically the repository
// root), while `go test` always runs with the package's own directory
// (tools/genfixtures) as the working directory - and outputDir needs to
// resolve to the same place either way.
//
// runtime.Caller(0) returns the absolute path of *this* source file as
// recorded by the compiler, which is stable regardless of the process's
// working directory; walking up two directories from
// tools/genfixtures/main.go reaches the repository root.
var outputDir = func() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("genfixtures: runtime.Caller failed to report this file's own path")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // tools/genfixtures -> tools -> repo root
	return filepath.Join(repoRoot, "testdata", "fixtures", "handmade")
}()

func main() {
	fixtures := []struct {
		name string
		data []byte
	}{
		{"minimal-blank-page.pdf", buildMinimalBlankPage()},
		{"two-pages.pdf", buildTwoPages()},
		{"incremental-update.pdf", buildIncrementalUpdate()},
		{"malformed-bad-xref-offset.pdf", buildMalformedBadXrefOffset()},
		{"truncated.pdf", buildTruncated()},
		{"xref-stream.pdf", buildXrefStream()},
		{"object-stream.pdf", buildObjectStream()},
		{"filled-rect.pdf", buildFilledRect()},
		{"stroked-line.pdf", buildStrokedLine()},
		{"clipped-rect.pdf", buildClippedRect()},
		{"transformed-rect.pdf", buildTransformedRect()},
		{"flate-content-rect.pdf", buildFlateContentRect()},
		{"image-rgb.pdf", buildImageRGB()},
		{"image-mask.pdf", buildImageMask()},
		{"image-smask.pdf", buildImageSMask()},
		{"image-jpeg.pdf", buildImageJPEG()},
		{"inline-image.pdf", buildInlineImage()},
		{"rotated-page.pdf", buildRotatedPage()},
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "genfixtures: %v\n", err)
		os.Exit(1)
	}

	for _, f := range fixtures {
		path := filepath.Join(outputDir, f.name)
		if err := os.WriteFile(path, f.data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "genfixtures: writing %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d bytes)\n", path, len(f.data))
	}
}

// --- Fixture bodies -------------------------------------------------
//
// Each buildXxx function below describes, in comments, exactly what PDF
// structural feature the fixture exists to exercise. Keep that pattern
// for any fixture added later: a fixture whose purpose isn't written
// down decays into "some PDF file" that nobody dares delete or change.

// buildMinimalBlankPage returns the simplest possible valid PDF this
// project should be able to open: one page, no content, a classic
// (non-compressed) cross-reference table, and a single trailer. This is
// the baseline "does the parser work at all" fixture.
func buildMinimalBlankPage() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})
	return b.finish(1)
}

// buildTwoPages returns a valid PDF whose page tree has two page objects
// under one Pages node, each with distinct MediaBox dimensions so a test
// can assert that page attributes are read per-page rather than
// accidentally shared or swapped. This exercises Kids-array traversal
// and the Count field, both required before Document.PageCount and
// Document.Page(index) can be trusted.
func buildTwoPages() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 200] /Resources << >> /Contents 5 0 R >>", nil)
	b.addObject(4, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 400] /Resources << >> /Contents 6 0 R >>", nil)
	b.addObject(5, 0, "<< /Length 0 >>", []byte{})
	b.addObject(6, 0, "<< /Length 0 >>", []byte{})
	return b.finish(1)
}

// buildIncrementalUpdate returns a PDF that has been "incrementally
// saved" once: the original file (a single blank page, objects 1-4) is
// followed by an appended update that adds a second page (objects 5-6)
// and rewrites the Pages object (object 2) to reference both pages,
// closing with a *second* trailer whose /Prev entry points back at the
// first trailer's xref table. Real-world PDF editors produce files like
// this constantly (every incremental save appends rather than rewriting
// the file), so a parser that only ever reads the last trailer while
// ignoring /Prev will see a Pages object listing a page whose object
// definition it never read. This fixture is the regression test for
// that: a correct reader must end up with two pages, both renderable,
// with object 2's *final* (updated) definition winning over its
// original one.
func buildIncrementalUpdate() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})
	firstXrefOffset := b.writeXrefAndTrailer(1, "")

	// The appended update reuses object number 2 (superseding the
	// original Pages object) and introduces new object numbers 5 and 6
	// for the second page and its content stream. Only the *changed and
	// new* objects need to appear in this update; object 1, 3, and 4 are
	// untouched and are inherited from the previous revision via /Prev.
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>", nil)
	b.addObject(5, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 250 350] /Resources << >> /Contents 6 0 R >>", nil)
	b.addObject(6, 0, "<< /Length 0 >>", []byte{})
	b.writeXrefAndTrailer(1, fmt.Sprintf(" /Prev %d", firstXrefOffset))

	return b.buf.Bytes()
}

// buildMalformedBadXrefOffset returns a file that is byte-for-byte
// identical to buildMinimalBlankPage, except the xref entry for object 3
// (the Page object) has been deliberately corrupted to point at the
// wrong offset. This exercises the error-classification requirement from
// Phase 1's exit criteria: a reader must report this as ErrMalformed
// (the file's own bookkeeping is inconsistent) rather than panicking or
// silently returning wrong data, and ideally should still be able to
// recover by falling back to a linear scan for "N G obj" markers, which
// real-world PDF readers commonly do for exactly this situation.
func buildMalformedBadXrefOffset() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	// Corrupt the recorded offset for object 3 by adding a bogus amount
	// so it no longer points at "3 0 obj". The offset is still a
	// plausible-looking in-range number, which is the more interesting
	// (and more realistic) failure mode compared to an offset that is
	// obviously out of bounds.
	b.offsets[3] += 12345

	return b.finish(1)
}

// buildTruncated returns a minimal-blank-page PDF that has been cut off
// partway through the final content stream object, before its endstream
// / endobj / xref / trailer machinery. This simulates a partial download
// or a copy that was interrupted mid-write. A reader must report this as
// ErrMalformed rather than panicking or blocking forever waiting for
// bytes that will never arrive.
func buildTruncated() []byte {
	full := buildMinimalBlankPage()

	// Cut the file in half. Because buildMinimalBlankPage's earlier
	// objects and the xref/trailer machinery all live in the back half
	// of the file, this reliably removes the xref table, trailer, and
	// startxref entirely - a reader cannot even locate where object
	// data starts, which is deliberately the harshest truncation case.
	return full[:len(full)/2]
}

// buildXrefStream returns a PDF whose page tree is structurally
// identical to buildMinimalBlankPage's, but whose cross-reference
// section is a PDF 1.5+ cross-reference stream (Flate-compressed, per
// how real producers almost always write one) rather than a classic
// "xref" table - the Phase 2 counterpart exercising
// internal/parser.Document.loadXrefStream. The chosen field widths,
// /W [1 4 2], are deliberately not the "natural" 4-byte-everything
// choice some implementations default to, so that a reader which
// silently assumed a fixed record layout instead of reading /W would
// fail on this fixture.
func buildXrefStream() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	const xrefObjNum = 5
	size := xrefObjNum + 1
	// The xref stream object's own "5 0 obj" is about to begin at the
	// buffer's current length; its own entry (below) describes exactly
	// that offset, which is how a real cross-reference stream always
	// includes itself - the table is written as part of the very object
	// it describes.
	xrefOffset := b.buf.Len()

	var raw bytes.Buffer
	writeXrefStreamRecord(&raw, 0, 0, 65535) // object 0: free list head
	for n := 1; n < xrefObjNum; n++ {
		writeXrefStreamRecord(&raw, 1, b.offsets[n], 0)
	}
	writeXrefStreamRecord(&raw, 1, xrefOffset, 0)

	compressed := deflate(raw.Bytes())
	dict := fmt.Sprintf("<< /Type /XRef /Size %d /W [1 4 2] /Root 1 0 R /Filter /FlateDecode /Length %d >>", size, len(compressed))
	b.addObject(xrefObjNum, 0, dict, compressed)

	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)
	return b.buf.Bytes()
}

// buildObjectStream returns a PDF whose Pages and Page dictionaries
// (object numbers 2 and 3) are packed together inside a single PDF 1.5+
// object stream (object 5) instead of each having its own "N G obj ...
// endobj" definition, described via a cross-reference stream (object 6)
// whose entries for 2 and 3 are type-2 ("compressed") records - the
// Phase 2 counterpart exercising internal/parser's
// Document.loadObjectStream and resolveCompressed. The content stream
// (object 4) and the catalog (object 1) remain ordinary top-level
// objects: the PDF specification forbids storing a stream inside an
// object stream, and there is no reason to compress the catalog itself,
// so this fixture exercises a document that mixes both storage forms -
// exactly what real-world PDF 1.5+ producers do (a document's very first
// objects are often left uncompressed for a "fast web view" preview).
func buildObjectStream() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(4, 0, "<< /Length 0 >>", []byte{})

	packed := []struct {
		num  int
		dict string
	}{
		{2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"},
		{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>"},
	}
	var header, body strings.Builder
	for _, p := range packed {
		fmt.Fprintf(&header, "%d %d ", p.num, body.Len())
		body.WriteString(p.dict)
		body.WriteString("\n")
	}
	first := header.Len()
	compressed := deflate([]byte(header.String() + body.String()))

	const objStmNum = 5
	objStmDict := fmt.Sprintf("<< /Type /ObjStm /N %d /First %d /Filter /FlateDecode /Length %d >>", len(packed), first, len(compressed))
	b.addObject(objStmNum, 0, objStmDict, compressed)

	const xrefObjNum = 6
	size := xrefObjNum + 1
	xrefOffset := b.buf.Len()

	var raw bytes.Buffer
	writeXrefStreamRecord(&raw, 0, 0, 65535)                // 0: free list head
	writeXrefStreamRecord(&raw, 1, b.offsets[1], 0)         // 1: Catalog
	writeXrefStreamRecord(&raw, 2, objStmNum, 0)            // 2: Pages, packed at index 0
	writeXrefStreamRecord(&raw, 2, objStmNum, 1)            // 3: Page, packed at index 1
	writeXrefStreamRecord(&raw, 1, b.offsets[4], 0)         // 4: content stream
	writeXrefStreamRecord(&raw, 1, b.offsets[objStmNum], 0) // 5: the object stream itself
	writeXrefStreamRecord(&raw, 1, xrefOffset, 0)           // 6: the xref stream itself

	compressedXref := deflate(raw.Bytes())
	xrefDict := fmt.Sprintf("<< /Type /XRef /Size %d /W [1 4 2] /Root 1 0 R /Filter /FlateDecode /Length %d >>", size, len(compressedXref))
	b.addObject(xrefObjNum, 0, xrefDict, compressedXref)

	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)
	return b.buf.Bytes()
}

// writeXrefStreamRecord appends one fixed-width cross-reference stream
// record matching the /W [1 4 2] layout buildXrefStream and
// buildObjectStream both use: a 1-byte type, a 4-byte big-endian
// field2, and a 2-byte big-endian field3. See
// internal/parser/xrefstream.go's parseXrefStreamRecord for what each
// type/field2/field3 combination means.
func writeXrefStreamRecord(buf *bytes.Buffer, typ byte, field2, field3 int) {
	buf.WriteByte(typ)
	buf.WriteByte(byte(field2 >> 24))
	buf.WriteByte(byte(field2 >> 16))
	buf.WriteByte(byte(field2 >> 8))
	buf.WriteByte(byte(field2))
	buf.WriteByte(byte(field3 >> 8))
	buf.WriteByte(byte(field3))
}

// deflate zlib-compresses data (the wrapping PDF's FlateDecode filter
// expects - see internal/filter's decodeFlate), for use by any fixture
// whose stream declares /Filter /FlateDecode.
func deflate(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		panic(fmt.Sprintf("genfixtures: deflate: %v", err))
	}
	if err := w.Close(); err != nil {
		panic(fmt.Sprintf("genfixtures: deflate: %v", err))
	}
	return buf.Bytes()
}

// buildFilledRect returns a single 100x100-point page whose content
// stream fills an 80x80 red square with an 10-point margin on every
// side, using plain (unfiltered) content stream bytes. This is the
// baseline Phase 2 rendering fixture: solid color, axis-aligned path
// construction ("re"), and nonzero-winding fill ("f") with no transform
// beyond the page's own device mapping.
func buildFilledRect() []byte {
	return buildSinglePageContent(100, 100, "1 0 0 rg\n10 10 80 80 re\nf\n")
}

// buildStrokedLine returns a single 100x100-point page whose content
// stream strokes a diagonal blue line, corner to corner, with a 5-point
// line width - exercising path construction via "m"/"l" and the "S"
// stroke operator together with a non-default line width, distinct from
// buildFilledRect's fill-only content.
func buildStrokedLine() []byte {
	return buildSinglePageContent(100, 100, "0 0 1 RG\n5 w\n10 10 m\n90 90 l\nS\n")
}

// buildClippedRect returns a single 100x100-point page whose content
// stream clips to a 40x40 square in the page's center ("re W n") and
// then fills the entire page green - only the clipped square should
// actually end up green in the rendered output, exercising clipping
// together with fill.
func buildClippedRect() []byte {
	return buildSinglePageContent(100, 100, "30 30 40 40 re\nW\nn\n0 1 0 rg\n0 0 100 100 re\nf\n")
}

// buildTransformedRect returns a single 100x100-point page whose content
// stream translates to the page center and rotates 45 degrees ("cm")
// before filling an axis-aligned (in its own, now-rotated, user space)
// orange square - exercising the current transformation matrix, saved
// and restored with "q"/"Q" around the temporary transform so it does
// not leak into anything painted afterward.
func buildTransformedRect() []byte {
	return buildSinglePageContent(100, 100,
		"q\n1 0 0 1 50 50 cm\n0.70710678 0.70710678 -0.70710678 0.70710678 0 0 cm\n"+
			"1 0.5 0 rg\n-20 -20 40 40 re\nf\nQ\n")
}

// buildFlateContentRect returns a single 100x100-point page identical in
// appearance to buildFilledRect (a red 80x80 square with a 10-point
// margin), but whose content stream is Flate-compressed - exercising
// filter decoding applied to a *page content* stream specifically,
// distinct from buildXrefStream/buildObjectStream's use of Flate for
// structural (cross-reference/object-stream) data. This is what actually
// closes the loop on Phase 2's "decode ... Flate ... streams as needed
// by the fixture corpus" exit criterion for the content-stream pipeline
// (internal/model.PageContentBytes -> internal/parser.DecodeStream ->
// internal/filter.Decode), not just the parser's own bootstrapping use
// of it.
func buildFlateContentRect() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", nil)

	content := deflate([]byte("1 0 0 rg\n10 10 80 80 re\nf\n"))
	dict := fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>", len(content))
	b.addObject(4, 0, dict, content)
	return b.finish(1)
}

// buildImageRGB returns a single 100x100-point page whose content stream
// paints a referenced image XObject ("Do") across the entire page: a 2x2
// DeviceRGB image (red, green / blue, yellow, one solid color per
// quadrant), uncompressed. This is the baseline Phase 3 rendering
// fixture for referenced images - exercising /Resources /XObject
// lookup, image dictionary resolution, and DeviceRGB sample decoding -
// the image counterpart to buildFilledRect's role for Phase 2's vector
// fills.
func buildImageRGB() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	pixels := []byte{
		255, 0, 0, 0, 255, 0, // row 0: red, green
		0, 0, 255, 255, 255, 0, // row 1: blue, yellow
	}
	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 2 "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Length %d >>", len(pixels))
	b.addObject(5, 0, imgDict, pixels)

	return b.finish(1)
}

// buildImageMask returns a single 100x100-point page that sets the fill
// color to red and then paints a referenced /ImageMask stencil image
// across the entire page: a 2x1, 1-bit-per-pixel mask whose left pixel
// is painted (sample 0) and whose right pixel is masked out (sample 1),
// per the default /Decode for image masks (see internal/image's package
// doc comment). The rendered result should be a red left half and a
// white (background, left untouched) right half - exercising
// /ImageMask's "paint using the current fill color" behavior end to end,
// including internal/content threading the graphics state's FillColor
// through to internal/image.Decode.
func buildImageMask() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\nq\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	// Two 1-bit samples packed high-bit-first into a single byte:
	// 0 (paint), then 1 (mask out), then six padding bits - 0b01000000.
	maskData := []byte{0x40}
	maskDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 2 /Height 1 "+
		"/ImageMask true /Length %d >>", len(maskData))
	b.addObject(5, 0, maskDict, maskData)

	return b.finish(1)
}

// buildImageSMask returns a single 100x100-point page that paints a 1x1
// solid red image XObject with an /SMask giving it 50% alpha, across the
// entire page - exercising resolving and decoding a *second* image
// object (the soft mask) referenced from within the first one's own
// dictionary, and internal/image's per-pixel alpha blending. Over this
// project's opaque white page background, the expected rendered color is
// (approximately) red blended at ~50% opacity onto white: full red
// channel (already 255 in both layers), green and blue roughly halved
// from white toward the mask's own gray value.
func buildImageSMask() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	smaskData := []byte{128}
	smaskDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 "+
		"/ColorSpace /DeviceGray /BitsPerComponent 8 /Length %d >>", len(smaskData))
	b.addObject(6, 0, smaskDict, smaskData)

	imgData := []byte{255, 0, 0}
	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /SMask 6 0 R /Length %d >>", len(imgData))
	b.addObject(5, 0, imgDict, imgData)

	return b.finish(1)
}

// buildImageJPEG returns a single 100x100-point page that paints a
// referenced image XObject encoded with DCTDecode (JPEG) - a small,
// solid dark-blue 4x4 image, generated at fixture-build time with the
// standard library's own image/jpeg encoder (quality 100, to keep lossy
// compression artifacts on a flat color small enough for an exact-match
// pixel test with a modest tolerance) rather than any externally sourced
// JPEG file, keeping this fixture's provenance identical to every other
// hand-authored one in this package (see FIXTURES.md). This exercises
// internal/filter's DCTDecode support end to end, through the real
// cross-reference/object-resolution pipeline rather than only
// internal/filter's own unit tests.
func buildImageJPEG() []byte {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.Set(x, y, color.RGBA{R: 20, G: 40, B: 200, A: 255})
		}
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, src, &jpeg.Options{Quality: 100}); err != nil {
		panic(fmt.Sprintf("genfixtures: encoding test JPEG: %v", err))
	}
	jpegData := jpegBuf.Bytes()

	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+
		"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>", nil)

	content := []byte("q\n100 0 0 100 0 0 cm\n/Im0 Do\nQ\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	imgDict := fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width 4 /Height 4 "+
		"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>", len(jpegData))
	b.addObject(5, 0, imgDict, jpegData)

	return b.finish(1)
}

// buildInlineImage returns a single 100x100-point page whose content
// stream paints an inline ("BI"/"ID"/"EI") image directly, with no
// /Resources /XObject entry at all: a 2x1 DeviceRGB image (red, green),
// uncompressed, scaled to cover the whole page. This is the Phase 3
// counterpart to buildImageRGB for the *other* way a PDF can embed image
// data - exercising internal/content's inline-image parsing
// (operator.go/inlineimage.go) end to end, including its computed-exact-
// length read path (this image has no /Filter, so its raw byte count is
// computed from /W, /H, /BPC, and /CS rather than needing an /L key or a
// scan for "EI" - see inlineimage.go's readInlineImageData).
func buildInlineImage() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> /Contents 4 0 R >>", nil)

	var content bytes.Buffer
	content.WriteString("q\n100 0 0 100 0 0 cm\nBI /W 2 /H 1 /BPC 8 /CS /RGB ID ")
	content.Write([]byte{255, 0, 0, 0, 255, 0}) // red, green
	content.WriteString(" EI\nQ\n")

	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", content.Len()), content.Bytes())
	return b.finish(1)
}

// buildRotatedPage returns a page whose MediaBox is 100 (wide) x 200
// (tall) points but which declares /Rotate 90, with a 20x20-point red
// square filled at the origin of its own (unrotated) user space - the
// Phase 3 regression fixture for Page.Render's and Page.Thumbnail's
// /Rotate handling, which previously had no end-to-end rendering test at
// all (only internal/model's inheritance/normalization logic was
// covered - see model_test.go's TestRotateIsInheritedAndNormalized).
//
// Working through pageDeviceGeometry's own math (root package page.go)
// by hand: a 90-degree rotation swaps the rendered image's pixel
// dimensions to 200x100, and maps this fixture's user-space square
// (0,0)-(20,20) to device rectangle (0,0)-(20,20) in the *rotated*
// canvas - i.e. the red square should land at the top-left corner of a
// 200x100 rendered image. A test asserting exactly that (rather than
// only checking the output's pixel dimensions) catches a rotation
// direction or sign error that a dimensions-only check would miss.
func buildRotatedPage() []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 200] /Rotate 90 "+
		"/Resources << >> /Contents 4 0 R >>", nil)

	content := []byte("1 0 0 rg\n0 0 20 20 re\nf\n")
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)

	return b.finish(1)
}

// buildSinglePageContent is the shared skeleton behind the vector
// rendering fixtures above: one page of the given size, one content
// stream holding contentOps verbatim (unfiltered - internal/filter is
// exercised separately by buildXrefStream/buildObjectStream's Flate
// streams, so these rendering fixtures keep their content streams
// plain text for easy reading in a hex/text dump while debugging a
// rendering test failure).
func buildSinglePageContent(width, height int, contentOps string) []byte {
	b := newBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)
	b.addObject(2, 0, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil)
	b.addObject(3, 0, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << >> /Contents 4 0 R >>", width, height), nil)
	content := []byte(contentOps)
	b.addObject(4, 0, fmt.Sprintf("<< /Length %d >>", len(content)), content)
	return b.finish(1)
}

// --- Low-level PDF byte assembly ------------------------------------

// builder accumulates PDF file bytes while tracking the byte offset at
// which each indirect object's "N G obj" line begins, which is exactly
// the information a cross-reference table needs to record. It is not a
// general-purpose PDF writer - it only supports the small set of
// constructs these fixtures need.
type builder struct {
	buf     bytes.Buffer
	offsets map[int]int // object number -> byte offset of "N G obj"
	maxObj  int
}

func newBuilder() *builder {
	b := &builder{offsets: make(map[int]int)}
	b.buf.WriteString("%PDF-1.7\n")
	// A conventional four-byte binary comment marking the file as
	// containing binary data, as recommended by the PDF specification so
	// that naive text-mode file transfers don't mangle it. It has no
	// structural meaning to a parser beyond being a comment line (a line
	// starting with '%').
	b.buf.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})
	return b
}

// addObject records the current buffer length as the object's offset,
// then writes "N G obj\n<dict>\n[stream\n<data>\nendstream\n]endobj\n". If
// data is nil, the object has no stream and dict is written as-is
// (typically a dictionary or array). If data is non-nil (even if empty),
// dict must be a dictionary whose /Length entry matches len(data)
// exactly - the caller is responsible for that, since only the caller
// knows the intended dictionary contents.
func (b *builder) addObject(num, gen int, dict string, data []byte) {
	b.offsets[num] = b.buf.Len()
	if num > b.maxObj {
		b.maxObj = num
	}
	fmt.Fprintf(&b.buf, "%d %d obj\n%s\n", num, gen, dict)
	if data != nil {
		b.buf.WriteString("stream\n")
		b.buf.Write(data)
		b.buf.WriteString("\nendstream\n")
	}
	b.buf.WriteString("endobj\n")
}

// writeXrefAndTrailer appends a classic (non-stream) cross-reference
// table covering every object number from 0 through the highest object
// number seen so far, followed by a trailer whose dictionary is
// "<< /Size <n> /Root <rootObj> 0 R<trailerExtra> >>", and finally the
// startxref/%%EOF footer. It returns the byte offset at which the "xref"
// keyword was written, which a later incremental update's trailer needs
// to record as its /Prev value.
//
// trailerExtra is inserted verbatim just before the trailer dictionary's
// closing ">>" and is expected to already include its own leading space
// (e.g. " /Prev 123"), or to be empty for a file's first (and possibly
// only) trailer.
func (b *builder) writeXrefAndTrailer(rootObj int, trailerExtra string) int {
	xrefOffset := b.buf.Len()

	size := b.maxObj + 1
	fmt.Fprintf(&b.buf, "xref\n0 %d\n", size)

	// Object 0 is always the head of the free-object linked list, always
	// generation 65535, always type 'f'. This project never reuses freed
	// object numbers in its fixtures, so no other object needs a real
	// "next free" chain; object 0 points at itself (offset 0), which is
	// how a PDF with no free objects conventionally represents the end
	// of that chain.
	b.writeXrefEntry(0, 65535, 'f')
	for n := 1; n < size; n++ {
		offset, ok := b.offsets[n]
		if !ok {
			// No object was ever registered for this number in this
			// revision; treat it as free. None of the fixtures above
			// currently exercise this path, but it keeps the table
			// well-formed if a future fixture has a gap in object
			// numbers.
			b.writeXrefEntry(0, 65535, 'f')
			continue
		}
		b.writeXrefEntry(offset, 0, 'n')
	}

	fmt.Fprintf(&b.buf, "trailer\n<< /Size %d /Root %d 0 R%s >>\n", size, rootObj, trailerExtra)
	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)

	return xrefOffset
}

// writeXrefEntry writes exactly one 20-byte cross-reference table entry,
// the fixed width the PDF specification requires: a 10-digit byte
// offset, a space, a 5-digit generation number, a space, the single
// letter 'n' (in use) or 'f' (free), and a mandatory 2-character
// end-of-line sequence. Using a fixed byte count matters here: some
// real-world readers seek by entry index (offset + 20*n) rather than
// scanning line by line, so an entry of the wrong length would silently
// desynchronize every entry after it.
func (b *builder) writeXrefEntry(offset, gen int, kind byte) {
	fmt.Fprintf(&b.buf, "%010d %05d %c \n", offset, gen, kind)
}

// finish writes the xref table and trailer for a single-revision file
// (no /Prev) and returns the completed file bytes.
func (b *builder) finish(rootObj int) []byte {
	b.writeXrefAndTrailer(rootObj, "")
	return b.buf.Bytes()
}
