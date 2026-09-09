package pdfviewer_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file tests WithFontSubstitution (docs/FONTS.md's Phase 4) at the
// public-API level: opening a document, attaching the option, and
// rendering/inspecting diagnostics - as opposed to internal/fonts' own
// tests (substitute_test.go, substitute_wiring_test.go), which exercise
// the matching algorithm and its wiring into loadSimpleFont more
// directly and with finer-grained assertions. TestSubstitutedGlyphIsUsedInsteadOfNotdefFallback
// below is this project's Phase 4 "end-to-end" test per docs/FONTS.md's
// Testing section: a real candidate ".ttf" file written to a temporary
// directory (never a real system font, and never asserting anything
// about what happens to be installed on the machine running `go test` -
// see that section's explicit "do not" for why), reusing
// pdfviewer_text_test.go's existing text-notdef-fallback.pdf fixture (a
// non-embedded /BaseFont /Helvetica simple font, previously always
// falling back to notdefGlyph - see that fixture's own doc comment in
// tools/genfixtures/text.go).

// TestRenderWithFontSubstitutionOptionDoesNotBreakOrdinaryRendering
// confirms that simply opting into font substitution - even pointed at a
// directory with nothing useful in it - does not change the outcome for
// a document whose fonts are all embedded and already resolvable, and
// does not error or panic.
func TestRenderWithFontSubstitutionOptionDoesNotBreakOrdinaryRendering(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("two-pages.pdf"), pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{
		Directories:           []string{t.TempDir()},
		DisableSystemDefaults: true,
	}))
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if _, err := page.Render(context.Background(), pdfviewer.RenderOptions{}); err != nil {
		t.Fatalf("Render: %v", err)
	}
}

// TestFontSubstitutionZeroValueScansSystemDefaults confirms the
// documented zero-value behavior of FontSubstitution.
// DisableSystemDefaults: an unset (false) value means platform defaults
// ARE scanned, so FontSubstitution{} (setting nothing at all) is a
// useful, non-empty configuration rather than silently doing nothing -
// this only proves opening succeeds and the option is accepted, since a
// CI machine's actual font directories are unpredictable (see
// docs/FONTS.md's Testing section on not depending on real installed
// fonts).
func TestFontSubstitutionZeroValueScansSystemDefaults(t *testing.T) {
	doc, err := pdfviewer.OpenFile(fixturePath("minimal-blank-page.pdf"), pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{}))
	if err != nil {
		t.Fatalf("OpenFile with a zero-value FontSubstitution: %v", err)
	}
	defer doc.Close()
}

// --- a minimal, hand-built candidate ".ttf" file, written to disk for
// DirectorySource to find ---
//
// This mirrors tools/genfixtures/truetype.go's buildTestFontProgram (an
// embedded /FontFile2 stream fixture) byte-for-byte in spirit: the
// smallest sfnt this package's own parser (internal/fonts/truetype.go)
// can read, with exactly one visible glyph (a filled square) reached via
// the cmap entry for 'A' (0x41) - the one character
// text-notdef-fallback.pdf's content stream actually shows. It is kept
// local to this file (rather than exported from tools/genfixtures, an
// unrelated `package main`) since nothing else in this module needs a
// standalone candidate font *file* - every other fixture builder in this
// project produces bytes meant to be embedded inside a PDF stream, not
// written to disk as its own file the way a real candidate font would
// be found.

func candidateTTFBytes() []byte {
	notdef := ttGlyph(nil)
	square := ttGlyph([]ttPoint{
		{150, 150}, {850, 150}, {850, 850}, {150, 850},
	})
	glyphs := [][]byte{notdef, square}

	var glyf bytes.Buffer
	loca := make([]uint32, len(glyphs)+1)
	for i, g := range glyphs {
		loca[i] = uint32(glyf.Len())
		glyf.Write(g)
	}
	loca[len(glyphs)] = uint32(glyf.Len())

	var locaBuf bytes.Buffer
	for _, off := range loca {
		_ = binary.Write(&locaBuf, binary.BigEndian, off)
	}

	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:20], 1000) // unitsPerEm
	binary.BigEndian.PutUint16(head[50:52], 1)    // indexToLocFormat: long

	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:6], uint16(len(glyphs)))

	var cmapSub bytes.Buffer
	_ = binary.Write(&cmapSub, binary.BigEndian, uint16(0))   // format 0
	_ = binary.Write(&cmapSub, binary.BigEndian, uint16(262)) // length
	_ = binary.Write(&cmapSub, binary.BigEndian, uint16(0))   // language
	glyphIDs := make([]byte, 256)
	glyphIDs['A'] = 1
	cmapSub.Write(glyphIDs)

	var cmap bytes.Buffer
	_ = binary.Write(&cmap, binary.BigEndian, uint16(0)) // version
	_ = binary.Write(&cmap, binary.BigEndian, uint16(1)) // numTables
	_ = binary.Write(&cmap, binary.BigEndian, uint16(3)) // platformID: Windows
	_ = binary.Write(&cmap, binary.BigEndian, uint16(1)) // encodingID: Unicode BMP
	_ = binary.Write(&cmap, binary.BigEndian, uint32(12))
	cmap.Write(cmapSub.Bytes())

	return assembleCandidateSfnt(map[string][]byte{
		"head": head,
		"maxp": maxp,
		"loca": locaBuf.Bytes(),
		"glyf": glyf.Bytes(),
		"cmap": cmap.Bytes(),
	})
}

// ttPoint is one on-curve point of ttGlyph's single contour.
type ttPoint struct{ x, y int16 }

// ttGlyph encodes a "glyf" table entry for a single-contour glyph with
// every point on-curve (points nil builds an intentionally empty/blank
// glyph, used for glyph 0/.notdef here) - the same simplest-possible
// encoding truetype_test.go's encodeSimpleGlyph uses, reimplemented
// locally since that helper lives in internal/fonts's own test-only
// files, not something this external test package can import.
func ttGlyph(points []ttPoint) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, int16(len(points)))
	_ = binary.Write(&buf, binary.BigEndian, [4]int16{}) // xMin/yMin/xMax/yMax: unused by this package's reader
	if len(points) > 0 {
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(points)-1))
	}
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // instructionLength
	for range points {
		buf.WriteByte(0x01) // on-curve flag
	}
	var prevX, prevY int16
	for _, p := range points {
		_ = binary.Write(&buf, binary.BigEndian, p.x-prevX)
		prevX = p.x
	}
	for _, p := range points {
		_ = binary.Write(&buf, binary.BigEndian, p.y-prevY)
		prevY = p.y
	}
	return buf.Bytes()
}

// assembleCandidateSfnt writes a complete sfnt table directory (version
// tag, table count, three unused "binary search helper" fields, one
// 16-byte record per table, then the table bytes themselves) from a set
// of already-encoded tables - the on-disk container format both an
// embedded /FontFile2 stream and a standalone candidate font file share
// (see internal/fonts/truetype.go's parseTableDirectory doc comment).
func assembleCandidateSfnt(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	var out bytes.Buffer
	const sfntVersionTrueType = 0x00010000
	_ = binary.Write(&out, binary.BigEndian, uint32(sfntVersionTrueType))
	_ = binary.Write(&out, binary.BigEndian, uint16(len(tags)))
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // searchRange
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // entrySelector
	_ = binary.Write(&out, binary.BigEndian, uint16(0)) // rangeShift

	headerLen := 12 + len(tags)*16
	offset := uint32(headerLen)
	type placed struct {
		tag            string
		offset, length uint32
	}
	placements := make([]placed, 0, len(tags))
	for _, tag := range tags {
		placements = append(placements, placed{tag, offset, uint32(len(tables[tag]))})
		offset += uint32(len(tables[tag]))
	}
	for _, p := range placements {
		out.WriteString(p.tag)
		_ = binary.Write(&out, binary.BigEndian, uint32(0)) // checksum: not verified by this package's reader
		_ = binary.Write(&out, binary.BigEndian, p.offset)
		_ = binary.Write(&out, binary.BigEndian, p.length)
	}
	for _, tag := range tags {
		out.Write(tables[tag])
	}
	return out.Bytes()
}

// TestSubstitutedGlyphIsUsedInsteadOfNotdefFallback is this project's
// Phase 4 end-to-end confirmation (docs/FONTS.md's Testing section):
// opening text-notdef-fallback.pdf (a non-embedded /BaseFont /Helvetica
// simple font - previously *always* falling back to notdefGlyph's
// placeholder box, per that fixture's own doc comment) with
// WithFontSubstitution pointed at a temporary directory containing one
// real, hand-built candidate ".ttf" file should now record a
// "substituted" diagnostic instead of the old "placeholder boxes" one -
// the candidate has no "name"/"OS/2" table at all (see candidateTTFBytes,
// which - like tools/genfixtures' own embedded-font fixtures - only
// writes the tables internal/fonts's parser reads), so it is found via
// matchFace's standard-14 category fallback (Helvetica -> sans-serif;
// see substitute.go's standard14Categories and categoryOf, which treats
// a candidate with no family/serif signal at all as sans-serif by
// default) rather than an exact family-name match.
func TestSubstitutedGlyphIsUsedInsteadOfNotdefFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "candidate.ttf"), candidateTTFBytes(), 0o644); err != nil {
		t.Fatalf("writing candidate font file: %v", err)
	}

	diags := &pdfviewer.Diagnostics{}
	doc, err := pdfviewer.OpenFile(
		fixturePath("text-notdef-fallback.pdf"),
		pdfviewer.WithDiagnostics(diags),
		pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{
			Directories:           []string{dir},
			DisableSystemDefaults: true,
		}),
	)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()

	page, err := doc.Page(0)
	if err != nil {
		t.Fatalf("Page(0): %v", err)
	}
	if _, err := page.Render(context.Background(), pdfviewer.RenderOptions{}); err != nil {
		t.Fatalf("Render: %v", err)
	}

	messages := diags.Messages()
	foundSubstituted, foundPlaceholder := false, false
	for _, msg := range messages {
		if strings.Contains(msg, "substituted") {
			foundSubstituted = true
		}
		if strings.Contains(msg, "placeholder boxes") {
			foundPlaceholder = true
		}
	}
	if !foundSubstituted {
		t.Errorf("Messages() = %v, want a message mentioning a substituted font", messages)
	}
	if foundPlaceholder {
		t.Errorf("Messages() = %v, want no \"placeholder boxes\" fallback message now that a substitute was found", messages)
	}
}
