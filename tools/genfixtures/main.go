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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
