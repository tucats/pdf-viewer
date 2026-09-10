package acroform

import (
	"strings"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestFieldDisplayTextString(t *testing.T) {
	f := Field{V: syntax.String("hello")}
	got, ok := fieldDisplayText(f)
	if !ok || got != "hello" {
		t.Fatalf("fieldDisplayText = %q, %v", got, ok)
	}
}

func TestFieldDisplayTextArrayUsesFirst(t *testing.T) {
	f := Field{V: syntax.Array{syntax.String("first"), syntax.String("second")}}
	got, ok := fieldDisplayText(f)
	if !ok || got != "first" {
		t.Fatalf("fieldDisplayText = %q, %v", got, ok)
	}
}

func TestFieldDisplayTextNoValue(t *testing.T) {
	got, ok := fieldDisplayText(Field{})
	if !ok || got != "" {
		t.Fatalf("fieldDisplayText = %q, %v, want (\"\", true)", got, ok)
	}
}

func TestFieldDisplayTextWrongTypeIsNotOK(t *testing.T) {
	f := Field{V: syntax.Integer(5)}
	if _, ok := fieldDisplayText(f); ok {
		t.Fatalf("fieldDisplayText accepted a non-text /V")
	}
}

func TestSingleLineTextReplacesNewlines(t *testing.T) {
	got := singleLineText("a\nb\r\nc")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("singleLineText left a newline: %q", got)
	}
}

func TestSplitNewlinesHandlesAllThreeConventions(t *testing.T) {
	for _, s := range []string{"a\nb", "a\rb", "a\r\nb"} {
		got := splitNewlines(s)
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Fatalf("splitNewlines(%q) = %v", s, got)
		}
	}
}

func TestWrapParagraphPreservesEmptyLine(t *testing.T) {
	font := mustLoadUniformWidthFont(t, ' ', 'z', 500)
	lines := wrapParagraph(font, winAnsiFontDict(), "", 12, 100)
	if len(lines) != 1 || len(lines[0]) != 0 {
		t.Fatalf("wrapParagraph(\"\") = %v, want one empty line", lines)
	}
}

// TestWrapParagraphBreaksOnWidth confirms a paragraph too wide for
// maxWidth to fit on one line is split into more than one, and that
// every resulting line individually fits.
func TestWrapParagraphBreaksOnWidth(t *testing.T) {
	font := mustLoadUniformWidthFont(t, ' ', 'z', 500) // 0.5em/char
	// At size 10, each char is 5 units wide; "one two three four" is 19
	// chars - comfortably wider than maxWidth=40 (8 chars) on one line.
	lines := wrapParagraph(font, winAnsiFontDict(), "one two three four", 10, 40)
	if len(lines) < 2 {
		t.Fatalf("wrapParagraph produced %d line(s), want more than 1", len(lines))
	}
	for _, l := range lines {
		if w := measureWidth(font, l, 10); w > 40 {
			t.Fatalf("wrapped line %q measures %v, wider than maxWidth 40", l, w)
		}
	}
}

func TestResolveDAFontUsesDR(t *testing.T) {
	fontDict := syntax.Dictionary{"Subtype": syntax.Name("Type1"), "BaseFont": syntax.Name("Helvetica")}
	f := Field{DR: syntax.Dictionary{"Font": syntax.Dictionary{"Helv": fontDict}}}
	dict, entry := resolveDAFont(&fakeResolver{}, f, "Helv")
	if dict["BaseFont"] != syntax.Name("Helvetica") {
		t.Fatalf("resolveDAFont dict = %+v", dict)
	}
	if entryDict, ok := entry.(syntax.Dictionary); !ok || entryDict["BaseFont"] != syntax.Name("Helvetica") {
		t.Fatalf("resolveDAFont entry = %#v", entry)
	}
}

func TestResolveDAFontFallsBackWhenDRMissing(t *testing.T) {
	dict, _ := resolveDAFont(&fakeResolver{}, Field{}, "Helv")
	if dict["BaseFont"] != syntax.Name("Helvetica") {
		t.Fatalf("resolveDAFont fallback BaseFont = %v, want Helvetica", dict["BaseFont"])
	}
	if dict["Subtype"] != syntax.Name("Type1") {
		t.Fatalf("resolveDAFont fallback Subtype = %v, want Type1", dict["Subtype"])
	}
}

func TestResolveDAFontFallsBackWhenNameNotInDR(t *testing.T) {
	f := Field{DR: syntax.Dictionary{"Font": syntax.Dictionary{}}}
	dict, _ := resolveDAFont(&fakeResolver{}, f, "Cour")
	if dict["BaseFont"] != syntax.Name("Courier") {
		t.Fatalf("resolveDAFont fallback for /Cour BaseFont = %v, want Courier", dict["BaseFont"])
	}
}

// TestBuildTextAppearanceProducesTfAndTj is an integration-style test at
// the Go-value level (no PDF file involved): given a plain text field
// with a resolvable /DR font, buildTextAppearance's output should
// select that font by name and show the field's own value.
func TestBuildTextAppearanceProducesTfAndTj(t *testing.T) {
	fontDict := winAnsiFontDict()
	f := Field{
		FT: "Tx",
		V:  syntax.String("Hi"),
		DA: []byte("/Helv 12 Tf 0 g"),
		DR: syntax.Dictionary{"Font": syntax.Dictionary{"Helv": fontDict}},
	}
	content, resources, ok := buildTextAppearance(&fakeResolver{}, f, 100, 20)
	if !ok {
		t.Fatalf("buildTextAppearance reported ok=false")
	}
	src := string(content)
	if !strings.Contains(src, "/Helv 12 Tf") {
		t.Fatalf("content missing expected Tf operator: %q", src)
	}
	if !strings.Contains(src, "(Hi) Tj") {
		t.Fatalf("content missing expected Tj operator: %q", src)
	}
	fontsEntry, ok := resources["Font"].(syntax.Dictionary)
	if !ok || fontsEntry["Helv"] == nil {
		t.Fatalf("resources = %+v, want a /Font /Helv entry", resources)
	}
}

func TestBuildTextAppearanceNoValueIsNotOK(t *testing.T) {
	f := Field{FT: "Tx", DA: []byte("/Helv 12 Tf 0 g")}
	if _, _, ok := buildTextAppearance(&fakeResolver{}, f, 100, 20); ok {
		t.Fatalf("buildTextAppearance with no /V reported ok=true")
	}
}

func TestBuildTextAppearancePasswordFieldIsNotOK(t *testing.T) {
	f := Field{FT: "Tx", Ff: ffPassword, V: syntax.String("secret"), DA: []byte("/Helv 12 Tf 0 g")}
	if _, _, ok := buildTextAppearance(&fakeResolver{}, f, 100, 20); ok {
		t.Fatalf("buildTextAppearance for a password field reported ok=true")
	}
}

func TestBuildTextAppearanceNoDAIsNotOK(t *testing.T) {
	f := Field{FT: "Tx", V: syntax.String("hi")}
	if _, _, ok := buildTextAppearance(&fakeResolver{}, f, 100, 20); ok {
		t.Fatalf("buildTextAppearance with no /DA reported ok=true")
	}
}

// TestBuildTextAppearanceType0DAFontIsNotOK confirms a /DA naming a
// composite font (out of scope - see encodeForFont's doc comment) is
// rejected rather than producing mis-encoded output.
func TestBuildTextAppearanceType0DAFontIsNotOK(t *testing.T) {
	f := Field{
		FT: "Tx",
		V:  syntax.String("hi"),
		DA: []byte("/C0 12 Tf 0 g"),
		DR: syntax.Dictionary{"Font": syntax.Dictionary{"C0": syntax.Dictionary{"Subtype": syntax.Name("Type0")}}},
	}
	if _, _, ok := buildTextAppearance(&fakeResolver{}, f, 100, 20); ok {
		t.Fatalf("buildTextAppearance accepted a Type0 /DA font")
	}
}

// TestBuildTextAppearanceQuaddingAffectsX confirms center/right quadding
// actually changes the emitted horizontal position relative to left
// (the default), rather than being silently ignored.
func TestBuildTextAppearanceQuaddingAffectsX(t *testing.T) {
	fontDict := winAnsiFontDict()
	base := Field{
		FT: "Tx",
		V:  syntax.String("Hi"),
		DA: []byte("/Helv 12 Tf 0 g"),
		DR: syntax.Dictionary{"Font": syntax.Dictionary{"Helv": fontDict}},
	}
	left, _, _ := buildTextAppearance(&fakeResolver{}, base, 100, 20)

	centered := base
	centered.Q = 1
	center, _, _ := buildTextAppearance(&fakeResolver{}, centered, 100, 20)

	if string(left) == string(center) {
		t.Fatalf("left- and center-quadded content are identical")
	}
}
