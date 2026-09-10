package content

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file tests text_extract.go's ExtractText - the Phase 10
// counterpart to text_test.go's Interpret-based text-painting tests.
// Most fixtures here reuse nonEmbeddedSimpleFontDict-style font
// dictionaries (text_test.go) since extraction never touches a font's
// glyph outline data at all - only its /Widths, /Encoding, and
// /ToUnicode - so a font with no embedded program at all is just as
// good a test fixture here as one with a real TrueType program (see
// pdfviewer_text_test.go, root package, for end-to-end coverage against
// real fixtures via the public Page.Text API).

func mustExtractWithResources(t *testing.T, src string, resources syntax.Dictionary, resolver *fakeResolver) []TextGlyph {
	t.Helper()
	ops, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	glyphs, err := ExtractText(ops, graphics.Identity(), resources, resolver, nil)
	if err != nil {
		t.Fatalf("ExtractText(%q): %v", src, err)
	}
	return glyphs
}

// simpleFontWithToUnicodeDict is nonEmbeddedSimpleFontDict (text_test.go)
// plus a /ToUnicode entry pointing at object number toUnicodeObjNum.
func simpleFontWithToUnicodeDict(toUnicodeObjNum int) syntax.Dictionary {
	dict := make(syntax.Dictionary, len(nonEmbeddedSimpleFontDict)+1)
	for k, v := range nonEmbeddedSimpleFontDict {
		dict[k] = v
	}
	dict["ToUnicode"] = syntax.Reference{Number: toUnicodeObjNum}
	return dict
}

// TestExtractText_SimpleFontPositionsAndText confirms the core case: two
// codes shown by one "Tj", each recovered as its own TextGlyph with the
// Unicode text a /ToUnicode CMap declares (not any ASCII coincidence -
// codes 0x80/0x81 are deliberately outside StandardEncoding's mapped
// range, so a passing test here can only mean the /ToUnicode CMap was
// actually consulted, not TextForCode's /Encoding fallback - see
// TestExtractText_FallsBackToEncodingWithoutToUnicode below for that
// fallback exercised on its own) and correct positions/widths (each code
// is 1000 units wide per wideWidthsArray - text_test.go - so at font
// size 10 each glyph should advance the pen by exactly 10 units).
func TestExtractText_SimpleFontPositionsAndText(t *testing.T) {
	// 0x80 -> U+00E9 ('é'), 0x81 -> U+00EA ('ê') via the "increment"
	// beginbfrange shape.
	toUnicode := []byte("1 beginbfrange\n<80> <81> <00E9>\nendbfrange\nendcmap\n")
	resolver := &fakeResolver{objects: map[int]syntax.Object{
		5: simpleFontWithToUnicodeDict(6),
		6: syntax.Stream{Raw: toUnicode},
	}}
	glyphs := mustExtractWithResources(t, "BT\n/F1 10 Tf\n0 0 Td\n<8081> Tj\nET\n", fakeFontResources(5), resolver)

	if len(glyphs) != 2 {
		t.Fatalf("len(glyphs) = %d, want 2", len(glyphs))
	}
	if glyphs[0].Text != "é" || glyphs[1].Text != "ê" {
		t.Errorf("glyph text = %q, %q, want \"é\", \"ê\"", glyphs[0].Text, glyphs[1].Text)
	}
	if glyphs[0].X != 0 || glyphs[0].Y != 0 {
		t.Errorf("glyphs[0] position = (%v,%v), want (0,0)", glyphs[0].X, glyphs[0].Y)
	}
	if glyphs[0].Width != 10 {
		t.Errorf("glyphs[0].Width = %v, want 10", glyphs[0].Width)
	}
	if glyphs[1].X != 10 || glyphs[1].Y != 0 {
		t.Errorf("glyphs[1] position = (%v,%v), want (10,0)", glyphs[1].X, glyphs[1].Y)
	}
	if glyphs[0].FontSize != 10 || glyphs[1].FontSize != 10 {
		t.Errorf("FontSize = %v, %v, want 10, 10", glyphs[0].FontSize, glyphs[1].FontSize)
	}
}

// TestExtractText_FallsBackToEncodingWithoutToUnicode confirms a simple
// font with no /ToUnicode entry at all still recovers text via
// TextForCode's /Encoding fallback (fonts.BuildSimpleEncoding) - here,
// nonEmbeddedSimpleFontDict itself (no /Encoding entry either, so
// StandardEncoding applies), showing plain ASCII "A".
func TestExtractText_FallsBackToEncodingWithoutToUnicode(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	glyphs := mustExtractWithResources(t, "BT\n/F1 10 Tf\n0 0 Td\n(A) Tj\nET\n", fakeFontResources(5), resolver)
	if len(glyphs) != 1 || glyphs[0].Text != "A" {
		t.Fatalf("glyphs = %+v, want one glyph with Text \"A\"", glyphs)
	}
}

// TestExtractText_CTMAppliesToPosition confirms a "cm" before "BT"
// shifts every glyph's reported position exactly like it would shift a
// painted glyph's device-space position under Render - ExtractText's
// initialCTM/"cm" handling is the same CTM math Interpret itself uses
// (graphics.Matrix.Mul), just applied to glyph-space (0,0) instead of an
// outline's own points.
func TestExtractText_CTMAppliesToPosition(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	glyphs := mustExtractWithResources(t, "1 0 0 1 50 25 cm\nBT\n/F1 10 Tf\n0 0 Td\n(A) Tj\nET\n", fakeFontResources(5), resolver)
	if len(glyphs) != 1 {
		t.Fatalf("len(glyphs) = %d, want 1", len(glyphs))
	}
	if glyphs[0].X != 50 || glyphs[0].Y != 25 {
		t.Errorf("glyphs[0] position = (%v,%v), want (50,25)", glyphs[0].X, glyphs[0].Y)
	}
}

// TestExtractText_QQRestoresCTM confirms "q"/"Q" save and restore the
// CTM around text the same way they do for painting - a glyph shown
// after a "q cm ... Q" sequence should land back at the position it
// would have without that sequence at all.
func TestExtractText_QQRestoresCTM(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	glyphs := mustExtractWithResources(t,
		"q\n1 0 0 1 500 500 cm\nQ\nBT\n/F1 10 Tf\n0 0 Td\n(A) Tj\nET\n",
		fakeFontResources(5), resolver)
	if len(glyphs) != 1 {
		t.Fatalf("len(glyphs) = %d, want 1", len(glyphs))
	}
	if glyphs[0].X != 0 || glyphs[0].Y != 0 {
		t.Errorf("glyphs[0] position = (%v,%v), want (0,0) - the \"cm\" inside q/Q should not have leaked out", glyphs[0].X, glyphs[0].Y)
	}
}

// TestExtractText_NoFontSelectedYieldsNoGlyphs confirms showing text
// with no successful "Tf" beforehand is silently skipped - the same
// tolerance text.go's own showText documents for painting - rather than
// panicking on a nil font.
func TestExtractText_NoFontSelectedYieldsNoGlyphs(t *testing.T) {
	glyphs := mustExtractWithResources(t, "BT\n0 0 Td\n(A) Tj\nET\n", nil, &fakeResolver{})
	if len(glyphs) != 0 {
		t.Errorf("glyphs = %+v, want none (no font was ever selected)", glyphs)
	}
}

// TestExtractText_TJArrayAdjustmentDoesNotEmitAGlyph confirms a "TJ"
// array's numeric elements (kerning/spacing adjustments) only move the
// text matrix - exactly like text.go's showAdjustment - and never
// themselves produce a TextGlyph, only the string elements around them
// do.
func TestExtractText_TJArrayAdjustmentDoesNotEmitAGlyph(t *testing.T) {
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}
	glyphs := mustExtractWithResources(t, "BT\n/F1 10 Tf\n0 0 Td\n[(A) -500 (B)] TJ\nET\n", fakeFontResources(5), resolver)
	if len(glyphs) != 2 {
		t.Fatalf("len(glyphs) = %d, want 2 (one per string element, none for the numeric adjustment)", len(glyphs))
	}
	// The -500 adjustment (in thousandths of an em) at font size 10 moves
	// the pen forward by 0.5*10 = 5 units *in addition* to the first
	// glyph's own 10-unit advance (see showAdjustment's doc comment on
	// the sign convention: negative moves forward).
	if want := 10.0 + 5.0; glyphs[1].X != want {
		t.Errorf("glyphs[1].X = %v, want %v", glyphs[1].X, want)
	}
}

// TestExtractText_UnsupportedContentDoesNotError confirms ExtractText's
// central design property (see text_extract.go's own doc comment): a
// content stream that would make Interpret fail outright (here, an
// image XObject with an unsupported /ColorSpace - the exact fixture
// TestInterpretDoWithUnsupportedImageFeaturePropagatesError,
// image_test.go, uses to prove Interpret itself *does* fail on it)
// still extracts its perfectly good text cleanly, since "Do" plays no
// role in what text exists or where it sits.
func TestExtractText_UnsupportedContentDoesNotError(t *testing.T) {
	imgDict := syntax.Dictionary{
		"Subtype": syntax.Name("Image"),
		"Width":   syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8),
		"ColorSpace":       syntax.Array{syntax.Name("Pattern")},
	}
	resources := syntax.Dictionary{
		"Font":    syntax.Dictionary{"F1": syntax.Reference{Number: 5}},
		"XObject": syntax.Dictionary{"Im0": syntax.Stream{Dict: imgDict, Raw: []byte{0, 0, 0}}},
	}
	resolver := &fakeResolver{objects: map[int]syntax.Object{5: nonEmbeddedSimpleFontDict}}

	src := "/Im0 Do\nBT\n/F1 10 Tf\n0 0 Td\n(A) Tj\nET\n"
	ops, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Confirm the premise: Interpret really does fail on this content.
	if _, err := Interpret(ops, graphics.Identity(), resources, resolver); !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Interpret: got %v, want an error wrapping ErrUnsupported (the premise this test depends on)", err)
	}

	glyphs, err := ExtractText(ops, graphics.Identity(), resources, resolver, nil)
	if err != nil {
		t.Fatalf("ExtractText: %v, want no error despite the unsupported image", err)
	}
	if len(glyphs) != 1 || glyphs[0].Text != "A" {
		t.Fatalf("glyphs = %+v, want one glyph with Text \"A\"", glyphs)
	}
}

// TestExtractText_MalformedTfStillErrors confirms ExtractText, unlike
// its tolerance for unsupported *painting* features, still reports a
// genuine error for malformed content among the operators it does
// itself interpret - here, "Tf" with the wrong operand count.
func TestExtractText_MalformedTfStillErrors(t *testing.T) {
	ops, err := Parse([]byte("BT\n/F1 Tf\nET\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := ExtractText(ops, graphics.Identity(), nil, &fakeResolver{}, nil); err == nil {
		t.Fatalf("ExtractText with malformed \"Tf\": got no error, want one")
	}
}
