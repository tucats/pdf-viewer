package fonts

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// This file tests Font.Width's substituteWidths precedence rule (see
// that field's own doc comment in font.go and docs/FONTS.md's "Widths
// vs. outlines" section) directly against a small fake glyphOutlineSource
// implementation, independent of any real font format - simple.go's
// applySubstituteWidths (which decides *when* to set substituteWidths in
// the first place) is tested separately, in
// substitute_wiring_test.go, against a real synthetic sfnt.

// fakeAdvanceSource is a minimal glyphOutlineSource that also implements
// advanceWidthSource (font.go), standing in for a real *sfntFont with
// parsed "hmtx" data.
type fakeAdvanceSource struct {
	unitsPerEm uint16
	widths     map[uint16]uint16 // gid -> advance width, in this fake's own design units
}

func (f fakeAdvanceSource) GlyphOutline(gid uint16) (*graphics.Path, bool) {
	return &graphics.Path{}, true
}
func (f fakeAdvanceSource) UnitsPerEm() uint16 { return f.unitsPerEm }
func (f fakeAdvanceSource) AdvanceWidth(gid uint16) (uint16, bool) {
	w, ok := f.widths[gid]
	return w, ok
}

// fakeOutlineOnlySource implements glyphOutlineSource but *not*
// advanceWidthSource - standing in for a real *cffFont, which never
// carries advance-width data (see advanceWidthSource's own doc comment).
type fakeOutlineOnlySource struct{}

func (fakeOutlineOnlySource) GlyphOutline(gid uint16) (*graphics.Path, bool) {
	return &graphics.Path{}, true
}
func (fakeOutlineOnlySource) UnitsPerEm() uint16 { return 1000 }

func TestFont_Width_PerCodeEntryAlwaysWinsOverSubstitute(t *testing.T) {
	f := &Font{
		widths:           map[int]float64{65: 42},
		defaultWidth:     500,
		substituteWidths: true,
		glyphSource:      fakeAdvanceSource{unitsPerEm: 1000, widths: map[uint16]uint16{1: 999}},
		lookupGID:        func(code int) (uint16, bool) { return 1, true },
	}
	if w := f.Width(65); w != 42 {
		t.Errorf("Width(65) = %v, want 42 (an explicit /Widths entry, never overridden)", w)
	}
}

func TestFont_Width_SubstituteWidthUsedWhenNoPerCodeEntry(t *testing.T) {
	f := &Font{
		defaultWidth:     500,
		substituteWidths: true,
		// unitsPerEm 2000 (double PDF's fixed 1000/em convention) and a
		// raw advance of 1000 should scale down to 500 in glyph space.
		glyphSource: fakeAdvanceSource{unitsPerEm: 2000, widths: map[uint16]uint16{7: 1000}},
		lookupGID:   func(code int) (uint16, bool) { return 7, true },
	}
	if w := f.Width(65); w != 500 {
		t.Errorf("Width(65) = %v, want 500 (scaled substitute advance width)", w)
	}
}

func TestFont_Width_FallsBackToDefaultWhenSubstituteHasNoAdvanceWidthData(t *testing.T) {
	f := &Font{
		defaultWidth:     500,
		substituteWidths: true,
		glyphSource:      fakeOutlineOnlySource{}, // does not implement advanceWidthSource
		lookupGID:        func(code int) (uint16, bool) { return 1, true },
	}
	if w := f.Width(65); w != 500 {
		t.Errorf("Width(65) = %v, want 500 (defaultWidth, since the substitute has no advance-width data)", w)
	}
}

func TestFont_Width_FallsBackToDefaultWhenCodeHasNoGlyph(t *testing.T) {
	f := &Font{
		defaultWidth:     500,
		substituteWidths: true,
		glyphSource:      fakeAdvanceSource{unitsPerEm: 1000, widths: map[uint16]uint16{1: 999}},
		lookupGID:        func(code int) (uint16, bool) { return 0, false },
	}
	if w := f.Width(65); w != 500 {
		t.Errorf("Width(65) = %v, want 500 (defaultWidth, since lookupGID found nothing for this code)", w)
	}
}

func TestFont_Width_SubstituteWidthsFalseNeverConsultsGlyphSource(t *testing.T) {
	f := &Font{
		defaultWidth:     500,
		substituteWidths: false, // the default - no PDF-supplied /Widths gap being filled
		glyphSource:      fakeAdvanceSource{unitsPerEm: 1000, widths: map[uint16]uint16{1: 999}},
		lookupGID:        func(code int) (uint16, bool) { return 1, true },
	}
	if w := f.Width(65); w != 500 {
		t.Errorf("Width(65) = %v, want 500 (defaultWidth; substituteWidths is false, so the substitute's own width must never be consulted)", w)
	}
}
