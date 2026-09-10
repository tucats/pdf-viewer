package acroform

import (
	"fmt"
	"strings"

	"github.com/tucats/pdf-viewer/internal/fonts"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file builds a generated appearance's content-stream bytes for a
// text field (/FT /Tx) or a choice field shown as plain text (/FT /Ch -
// a combo box's current text, or a list box's first selected entry;
// see this package's doc comment on why only the first selection is
// shown for a multi-select list). Both field types share one code path
// (buildTextAppearance) since, once /V has been reduced to a single
// display string, there is nothing left that differs between them for
// this package's purposes.

// textPadding is the fixed inset (in PDF points) kept between a
// generated appearance's text and its field's own /Rect edges on every
// side - a small, deliberately simple stand-in for Acrobat's own more
// elaborate layout, but enough to keep text from visually touching (or,
// for a field with a drawn border, overlapping) the field's boundary.
const textPadding = 2.0

// lineLeadingFactor scales a chosen font size into the vertical distance
// between successive lines of a multiline field - 1.15 is a common,
// unremarkable single-spacing multiplier (comparable to most word
// processors' own "single spacing" default), not a value the
// specification itself prescribes.
const lineLeadingFactor = 1.15

// daFontBaseNames maps the handful of font resource names Acrobat's own
// form-creation tools conventionally use in a generated /DR (e.g.
// "Helv") to the standard PostScript font name font substitution's
// standard-14 table (internal/fonts/substitute.go) actually recognizes -
// used only by the fallback path in resolveDAFont, when a field's /DR
// does not actually define the name its own /DA asks for (a malformed,
// but not vanishingly rare, real-world file). A name not found here
// falls back to "Helvetica", the single most common form field font by
// far.
var daFontBaseNames = map[syntax.Name]syntax.Name{
	"Helv": "Helvetica",
	"HeBo": "Helvetica-Bold",
	"Cour": "Courier",
	"CoBo": "Courier-Bold",
	"TiRo": "Times-Roman",
	"TiBo": "Times-Bold",
	"ZaDb": "ZapfDingbats",
	"Symb": "Symbol",
}

// buildTextAppearance returns the content-stream bytes and /Resources
// /Font entry for a generated Tx/Ch field appearance sized to
// width x height (the widget's own /Rect dimensions), or ok=false if
// there is nothing to paint - an empty or absent value, a password
// field (see this package's doc comment), a /DA this package cannot
// parse, or a /DA font this package cannot resolve at all (not even to
// this function's own synthetic fallback, which in practice never
// actually fails).
func buildTextAppearance(r Resolver, f Field, width, height float64) ([]byte, syntax.Dictionary, bool) {
	if f.FT == "Tx" && f.Ff&ffPassword != 0 {
		return nil, nil, false
	}

	text, ok := fieldDisplayText(f)
	if !ok || text == "" {
		return nil, nil, false
	}

	da, ok := ParseDA(f.DA)
	if !ok || da.FontName == "" {
		return nil, nil, false
	}

	fontDict, fontEntry := resolveDAFont(r, f, da.FontName)
	if subtype, _ := fontDict["Subtype"].(syntax.Name); subtype == "Type0" {
		// A /DA naming a composite font is out of this package's scope -
		// see encodeForFont's doc comment.
		return nil, nil, false
	}
	font, err := fonts.Load(fontDict, r)
	if err != nil || font == nil {
		return nil, nil, false
	}

	multiline := f.FT == "Tx" && f.Ff&ffMultiline != 0
	size := da.FontSize
	if size <= 0 {
		size = autoFontSize(height)
	}
	maxWidth := width - 2*textPadding
	if maxWidth < 0 {
		maxWidth = 0
	}

	var lines [][]byte
	if multiline {
		lines = wrapText(font, fontDict, text, size, maxWidth)
	} else {
		codes := encodeForFont(fontDict, singleLineText(text))
		size = shrinkToFitWidth(font, codes, size, maxWidth)
		lines = [][]byte{codes}
	}

	content := renderLines(font, fontDict, f, lines, da, size, width, height, multiline)
	resources := syntax.Dictionary{"Font": syntax.Dictionary{da.FontName: fontEntry}}
	return content, resources, true
}

// renderLines assembles the actual content-stream bytes: /DA's own
// non-"Tf" operators (almost always just a color operator) first, this
// package's own re-derived "Tf" (see nonFontOperatorsSource's doc
// comment for why /DA's original "Tf" is never reused directly), then
// one "Td"/"Tj" pair per line, positioned per q (quadding) horizontally
// and stacked top-down by lineLeadingFactor*size vertically for a
// multiline field. A line that would fall below the field's bottom edge
// is silently omitted rather than painted overflowing the field - a
// deliberate truncation, not a bug, matching this package's doc
// comment on scope.
func renderLines(font *fonts.Font, fontDict syntax.Dictionary, f Field, lines [][]byte, da DefaultAppearance, size, width, height float64, multiline bool) []byte {
	var b strings.Builder
	b.WriteString("q\n")
	if extra := nonFontOperatorsSource(f.DA); len(extra) > 0 {
		b.Write(extra)
	} else {
		b.WriteString("0 g\n")
	}
	fmt.Fprintf(&b, "/%s %s Tf\n", da.FontName, formatNumber(size))
	b.WriteString("BT\n")

	leading := size * lineLeadingFactor
	firstY := (height - size) / 2
	if multiline {
		firstY = height - textPadding - size
	}
	if firstY < 0 {
		firstY = 0
	}

	prevX, prevY := 0.0, 0.0
	for i, codes := range lines {
		y := firstY - float64(i)*leading
		if multiline && y < 0 {
			break
		}
		x := lineX(font, codes, size, width, f.Q)
		if i == 0 {
			fmt.Fprintf(&b, "%s %s Td\n", formatNumber(x), formatNumber(y))
		} else {
			fmt.Fprintf(&b, "%s %s Td\n", formatNumber(x-prevX), formatNumber(y-prevY))
		}
		prevX, prevY = x, y
		fmt.Fprintf(&b, "(%s) Tj\n", escapeLiteral(codes))
	}

	b.WriteString("ET\nQ\n")
	return []byte(b.String())
}

// lineX returns one line's left-edge x position in text space, per
// this field's quadding (Q: 0 left, 1 center, 2 right - 12.7.3.3),
// clamped so it never falls left of textPadding.
func lineX(font *fonts.Font, codes []byte, size, width float64, q int) float64 {
	switch q {
	case 1:
		w := measureWidth(font, codes, size)
		x := (width - w) / 2
		return clampFloat(x, textPadding, width)
	case 2:
		w := measureWidth(font, codes, size)
		x := width - textPadding - w
		return clampFloat(x, textPadding, width)
	default:
		return textPadding
	}
}

// fieldDisplayText reduces f.V to the single Go string this package
// should paint: a text field's own string, a choice field's current
// (first, for a multi-select list) selection, or ok=false for anything
// else (no value at all, or a value shape this field type should never
// actually have).
func fieldDisplayText(f Field) (string, bool) {
	switch v := f.V.(type) {
	case syntax.String:
		return decodeTextString([]byte(v)), true
	case syntax.Array:
		if len(v) == 0 {
			return "", true
		}
		if s, ok := v[0].(syntax.String); ok {
			return decodeTextString([]byte(s)), true
		}
		return "", true
	case syntax.Null, nil:
		return "", true
	default:
		return "", false
	}
}

// singleLineText replaces any literal newline in s with a space - a
// single-line field's /V should never contain one, but a malformed file
// might, and folding it into a space keeps the rest of this package's
// single-line layout math (which assumes exactly one line) valid rather
// than needing a separate "what if there's a stray newline" branch.
func singleLineText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)
}

// splitNewlines splits s on any of the three newline conventions text
// strings may use (bare CR, bare LF, or CRLF) into paragraphs - the
// units wrapText further word-wraps to fit maxWidth.
func splitNewlines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// wrapText splits text into paragraphs (splitNewlines) and greedily
// word-wraps each to fit maxWidth at size, returning every resulting
// line already encoded into font byte codes (encodeForFont) - ready for
// renderLines to measure and paint directly, with no further text
// processing needed downstream.
func wrapText(font *fonts.Font, fontDict syntax.Dictionary, text string, size, maxWidth float64) [][]byte {
	var lines [][]byte
	for _, para := range splitNewlines(text) {
		lines = append(lines, wrapParagraph(font, fontDict, para, size, maxWidth)...)
	}
	return lines
}

// wrapParagraph greedily packs para's whitespace-separated words onto
// as few lines as fit within maxWidth, always producing at least one
// (possibly empty, for an empty paragraph - preserving a deliberate
// blank line in the original text) line. This is an ordinary greedy
// word-wrap, not full Knuth-Plass-style paragraph breaking - simple,
// deterministic, and entirely sufficient for a form field's short
// values.
func wrapParagraph(font *fonts.Font, fontDict syntax.Dictionary, para string, size, maxWidth float64) [][]byte {
	words := strings.Fields(para)
	if len(words) == 0 {
		return [][]byte{encodeForFont(fontDict, "")}
	}

	var lines [][]byte
	current := words[0]
	for _, w := range words[1:] {
		candidate := current + " " + w
		if measureWidth(font, encodeForFont(fontDict, candidate), size) > maxWidth {
			lines = append(lines, encodeForFont(fontDict, current))
			current = w
			continue
		}
		current = candidate
	}
	lines = append(lines, encodeForFont(fontDict, current))
	return lines
}

// resolveDAFont resolves name within f.DR's /Font subdictionary,
// returning both the font dictionary itself (already one reference
// deep, exactly as internal/fonts.Load and internal/fonts.
// BuildSimpleEncoding both expect - see this package's doc comment) and
// the raw, still-possibly-indirect entry a generated appearance's own
// /Resources /Font should reuse verbatim (preserving its object
// identity, so internal/content's cross-call FontCache still recognizes
// it as the same font it may already have loaded for the page's own
// content - see content.FontCache's doc comment).
//
// When f.DR is absent, or does not define name at all (a malformed
// file - the specification requires every /DA font name to resolve
// through /DR), this falls back to a synthetic Type1 font dictionary
// naming a standard-14 PostScript font (via daFontBaseNames) so
// generation can still proceed - rendering through font substitution if
// the caller enabled it, or this project's existing notdefGlyph
// placeholder-box fallback otherwise, exactly like any other
// non-embedded font (see docs/capability-matrix.md's "Non-embedded font
// fallback" row). This fallback path always succeeds; resolveDAFont has
// no failure return.
func resolveDAFont(r Resolver, f Field, name syntax.Name) (syntax.Dictionary, syntax.Object) {
	if f.DR != nil {
		if fontsDict, ok := dictEntry(r, f.DR, "Font"); ok {
			if entry, ok := fontsDict[name]; ok {
				if resolved, err := resolveIfRef(r, entry); err == nil {
					if dict, ok := resolved.(syntax.Dictionary); ok {
						return dict, entry
					}
				}
			}
		}
	}

	baseFont, ok := daFontBaseNames[name]
	if !ok {
		baseFont = "Helvetica"
	}
	fallback := syntax.Dictionary{
		"Subtype":  syntax.Name("Type1"),
		"BaseFont": baseFont,
		"Encoding": syntax.Name("WinAnsiEncoding"),
	}
	return fallback, fallback
}
