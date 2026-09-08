package parser

import (
	"bytes"
	"compress/zlib"
	"errors"
	"strconv"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// TestOpenXrefStream exercises loadXrefStream against the
// xref-stream.pdf fixture (see tools/genfixtures's buildXrefStream doc
// comment): the same page tree as minimal-blank-page.pdf, but described
// by a Flate-compressed PDF 1.5+ cross-reference stream (with
// deliberately non-default /W field widths) instead of a classic table.
func TestOpenXrefStream(t *testing.T) {
	d := openFixture(t, "xref-stream.pdf")

	rootRef, ok := d.Trailer["Root"].(syntax.Reference)
	if !ok {
		t.Fatalf("trailer /Root = %#v, want syntax.Reference", d.Trailer["Root"])
	}
	catalog := resolveDict(t, d, rootRef)
	if got := catalog["Type"]; got != syntax.Name("Catalog") {
		t.Errorf("catalog /Type = %#v, want /Catalog", got)
	}

	pagesRef := catalog["Pages"].(syntax.Reference)
	pages := resolveDict(t, d, pagesRef)
	kids := pages["Kids"].(syntax.Array)
	pageRef := kids[0].(syntax.Reference)
	page := resolveDict(t, d, pageRef)

	wantBox := syntax.Array{syntax.Integer(0), syntax.Integer(0), syntax.Integer(200), syntax.Integer(200)}
	if got := page["MediaBox"]; !equalArray(got, wantBox) {
		t.Errorf("page /MediaBox = %#v, want %#v", got, wantBox)
	}

	// The xref stream object itself (5) must resolve to a Stream whose
	// own cross-reference entry - describing its own offset - was
	// correctly read from within its own table.
	obj, err := d.Resolve(5)
	if err != nil {
		t.Fatalf("Resolve(5) (the xref stream itself): %v", err)
	}
	if _, ok := obj.(syntax.Stream); !ok {
		t.Fatalf("Resolve(5) = %#v (%T), want syntax.Stream", obj, obj)
	}
}

// TestOpenObjectStream exercises loadObjectStream and resolveCompressed
// against the object-stream.pdf fixture: the Pages and Page dictionaries
// are packed inside a single object stream and described by type-2
// ("compressed") entries in a cross-reference stream, while the catalog
// and content stream remain ordinary top-level objects.
func TestOpenObjectStream(t *testing.T) {
	d := openFixture(t, "object-stream.pdf")

	rootRef := d.Trailer["Root"].(syntax.Reference)
	catalog := resolveDict(t, d, rootRef)

	pagesRef := catalog["Pages"].(syntax.Reference)
	if e := d.xref[pagesRef.Number]; !e.Compressed {
		t.Fatalf("xref entry for object %d (Pages) is not marked Compressed; fixture or loadXrefStream is broken", pagesRef.Number)
	}
	pages := resolveDict(t, d, pagesRef)
	if got := pages["Type"]; got != syntax.Name("Pages") {
		t.Errorf("pages /Type = %#v, want /Pages", got)
	}

	kids := pages["Kids"].(syntax.Array)
	if len(kids) != 1 {
		t.Fatalf("pages /Kids = %#v, want a 1-element Array", kids)
	}
	pageRef := kids[0].(syntax.Reference)
	page := resolveDict(t, d, pageRef)
	wantBox := syntax.Array{syntax.Integer(0), syntax.Integer(0), syntax.Integer(200), syntax.Integer(200)}
	if got := page["MediaBox"]; !equalArray(got, wantBox) {
		t.Errorf("page /MediaBox = %#v, want %#v", got, wantBox)
	}

	// Resolving the same object stream a second time (via the Page's
	// own object, packed alongside Pages in the same stream) must reuse
	// the cached objStreamContents rather than decoding it twice - this
	// doesn't directly observe the cache, but does confirm resolving
	// more than one compressed object from the same stream works.
	contentsRef := page["Contents"].(syntax.Reference)
	if _, err := d.Resolve(contentsRef.Number); err != nil {
		t.Fatalf("Resolve(%d) (content stream): %v", contentsRef.Number, err)
	}
}

// TestResolveCompressedObjectOutOfRangeIndex constructs a cross-reference
// stream entry claiming an object lives at an index beyond its object
// stream's actual object count, and confirms Resolve reports this as
// malformed rather than panicking on an out-of-range slice access.
func TestResolveCompressedObjectOutOfRangeIndex(t *testing.T) {
	b := newXrefStreamTestBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 2 0 R >>", nil)

	// A minimal one-object object stream (object 3), then a
	// cross-reference stream claiming object 2 is packed at index 5 of
	// it - which does not exist (the stream only has one object, at
	// index 0).
	header := "2 0 "
	body := "<< /Type /Pages /Kids [] /Count 0 >>"
	compressed := deflateForTest(t, []byte(header+body))
	objStmDict := "<< /Type /ObjStm /N 1 /First " + itoaTest(len(header)) + " /Filter /FlateDecode /Length " + itoaTest(len(compressed)) + " >>"
	b.addObject(3, 0, objStmDict, compressed)

	var raw bytes.Buffer
	writeXrefStreamRecordForTest(&raw, 0, 0, 65535)
	writeXrefStreamRecordForTest(&raw, 1, int(b.offsets[1]), 0)
	writeXrefStreamRecordForTest(&raw, 2, 3, 5) // object 2: claims index 5 in object stream 3
	writeXrefStreamRecordForTest(&raw, 1, int(b.offsets[3]), 0)

	compressedXref := deflateForTest(t, raw.Bytes())
	xrefOffset := b.buf.Len()
	xrefDict := "<< /Type /XRef /Size 4 /W [1 4 2] /Root 1 0 R /Filter /FlateDecode /Length " + itoaTest(len(compressedXref)) + " >>"
	b.addObject(4, 0, xrefDict, compressedXref)
	b.buf.WriteString("startxref\n" + itoaTest(xrefOffset) + "\n%%EOF\n")

	d, err := openBytes(t, b.buf.Bytes())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := d.Resolve(2); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Resolve(2) error = %v, want ErrMalformed", err)
	}
}

// TestXrefStreamMissingWIsMalformed confirms a cross-reference stream
// lacking the required /W entry is rejected rather than panicking on
// the resulting zero-width record math.
func TestXrefStreamMissingWIsMalformed(t *testing.T) {
	b := newXrefStreamTestBuilder()
	b.addObject(1, 0, "<< /Type /Catalog /Pages 1 0 R >>", nil)

	xrefOffset := b.buf.Len()
	compressed := deflateForTest(t, []byte{0, 0, 0, 0, 0, 0, 0})
	dict := "<< /Type /XRef /Size 2 /Root 1 0 R /Filter /FlateDecode /Length " + itoaTest(len(compressed)) + " >>"
	b.addObject(2, 0, dict, compressed)
	b.buf.WriteString("startxref\n" + itoaTest(xrefOffset) + "\n%%EOF\n")

	if _, err := openBytes(t, b.buf.Bytes()); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Open error = %v, want ErrMalformed", err)
	}
}

// --- shared test helpers for building ad hoc xref-stream input --------

// xrefStreamTestBuilder is a minimal stand-in for genfixtures' builder
// type (which lives in package main and cannot be imported here): it
// only needs to track object offsets while assembling a small,
// deliberately malformed cross-reference stream inline, matching the
// style of this package's other ad hoc byte-buffer tests (see
// parser_test.go's doc comment on TestOpenMissingHeaderIsMalformed).
type xrefStreamTestBuilder struct {
	buf     bytes.Buffer
	offsets map[int]int64
}

func newXrefStreamTestBuilder() *xrefStreamTestBuilder {
	b := &xrefStreamTestBuilder{offsets: make(map[int]int64)}
	b.buf.WriteString("%PDF-1.7\n")
	return b
}

func (b *xrefStreamTestBuilder) addObject(num, gen int, dict string, data []byte) {
	b.offsets[num] = int64(b.buf.Len())
	b.buf.WriteString(itoaTest(num) + " " + itoaTest(gen) + " obj\n" + dict + "\n")
	if data != nil {
		b.buf.WriteString("stream\n")
		b.buf.Write(data)
		b.buf.WriteString("\nendstream\n")
	}
	b.buf.WriteString("endobj\n")
}

func writeXrefStreamRecordForTest(buf *bytes.Buffer, typ byte, field2, field3 int) {
	buf.WriteByte(typ)
	buf.WriteByte(byte(field2 >> 24))
	buf.WriteByte(byte(field2 >> 16))
	buf.WriteByte(byte(field2 >> 8))
	buf.WriteByte(byte(field2))
	buf.WriteByte(byte(field3 >> 8))
	buf.WriteByte(byte(field3))
}

func deflateForTest(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("deflate: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("deflate: %v", err)
	}
	return buf.Bytes()
}

func itoaTest(n int) string {
	return strconv.Itoa(n)
}
