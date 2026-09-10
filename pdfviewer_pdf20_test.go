package pdfviewer_test

import (
	"image"
	"testing"
)

// This file is Phase 16's PDF 2.0 verification (docs/PLAN2.md): three
// fixtures declaring a genuine "%PDF-2.0" header (see
// tools/genfixtures/main.go's buildPDF20ClassicXref, buildPDF20XrefStream
// and buildPDF20EncryptedAES256 doc comments), each otherwise byte-for-
// byte identical to an existing 1.7-headered fixture this project's
// tests already prove renders correctly. Passing here confirms what
// docs/PLAN.md's "Supported PDF versions" section predicted but had not
// actually checked against a real 2.0-labeled file: this project's
// parsing and rendering code never branches on the header's declared
// version number at all (only its "%PDF-" prefix - see
// internal/parser.validateHeader's own doc comment), so a 2.0 file
// exercising both of this project's structural cross-reference
// mechanisms (a classic table, and an object-stream-plus-cross-
// reference-stream pair) and its newest encryption revision (Standard
// Security Handler /V 5 /R 6 - itself a PDF 2.0-era addition) all work
// exactly as their 1.7 counterparts already do.

// TestRenderPDF20ClassicXref confirms a "%PDF-2.0"-headered, classic-
// cross-reference-table file renders identically to filled-rect.pdf,
// which it is byte-for-byte identical to apart from the header.
func TestRenderPDF20ClassicXref(t *testing.T) {
	compareImages(t, renderFixture(t, "pdf20-classic-xref.pdf"), renderFixture(t, "filled-rect.pdf"))
}

// TestRenderPDF20XrefStream confirms a "%PDF-2.0"-headered file whose
// page tree is packed into a PDF 1.5+ object stream, described by a
// cross-reference stream (the structural form PDF 2.0 producers
// commonly favor), opens and renders correctly: a solid blue square
// from (20,20) to (180,180) in PDF user space, which after the standard
// y-flip covers image rows/columns [20,180) on this 200x200 page.
func TestRenderPDF20XrefStream(t *testing.T) {
	img := renderFixture(t, "pdf20-xref-stream.pdf")
	if img.Bounds() != image.Rect(0, 0, 200, 200) {
		t.Fatalf("bounds = %v, want (0,0)-(200,200)", img.Bounds())
	}
	assertPixel(t, img, 100, 100, 0, 0, 255) // deep interior: blue
	assertPixel(t, img, 5, 5, 255, 255, 255) // outside the square: white
	assertPixel(t, img, 179, 179, 0, 0, 255) // just inside the far edge: blue
}

// TestRenderPDF20EncryptedAES256 confirms a "%PDF-2.0"-headered file
// using this project's newest Standard Security Handler revision
// (/V 5 /R 6, AES-256 - added to the specification by ISO 32000-2
// itself, so a genuinely 2.0-labeled fixture is the most honest way to
// check it) decrypts and renders identically to the same unencrypted
// content, exactly like TestRenderEncryptedMatchesPlainContent already
// confirms for its 1.7-headered counterpart, encrypted-aes256.pdf.
func TestRenderPDF20EncryptedAES256(t *testing.T) {
	compareImages(t, renderFixture(t, "pdf20-encrypted-aes256.pdf"), renderFixture(t, "filled-rect.pdf"))
}
