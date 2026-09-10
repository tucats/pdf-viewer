package acroform

import "github.com/tucats/pdf-viewer/internal/fonts"

// measureWidth returns the text-space width (in unscaled PDF units, the
// same units a "Td"/"Tm" position is expressed in) of codes - already
// font-byte-encoded text (see encodeForFont) - shown at size, using
// exactly the formula internal/content/text.go's showText advances the
// text matrix by for each glyph (w0 * size, summed - ignoring character/
// word spacing and horizontal scaling, which a generated appearance
// never sets to anything other than their defaults, so they would only
// ever contribute zero/no-op adjustments here anyway).
func measureWidth(font *fonts.Font, codes []byte, size float64) float64 {
	total := 0.0
	for _, dc := range font.DecodeCodes(codes) {
		total += font.Width(dc.Code) / 1000 * size
	}
	return total
}

// clampFloat returns v clamped to [lo, hi].
func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// autoFontSize implements this package's documented simplification of
// 12.7.3.3's "auto-sized" text (a /DA "Tf" size of 0, meaning "the
// producer leaves the actual size up to whatever renders this field"):
// a size proportional to the field's own height, clamped to a sensible
// reading-size range. Acrobat's own auto-sizing algorithm additionally
// accounts for the specific font's cap-height/descent metrics and
// iterates to find the largest size that still fits every line of
// wrapped text within both dimensions of the field - considerably more
// than this package attempts to reproduce; this heuristic instead
// always returns a plausible, legible size given only the field's
// geometry; buildTextLines (textfield.go) then shrinks it further via
// shrinkToFitWidth below to at least avoid the specific, especially
// visible failure mode where a single-line field's text overflows its
// own /Rect horizontally.
func autoFontSize(rectHeight float64) float64 {
	const minSize, maxSize = 4.0, 12.0
	return clampFloat(rectHeight*0.65, minSize, maxSize)
}

// shrinkToFitWidth returns a font size no larger than size that keeps
// codes' measured width within maxWidth, given that measureWidth is
// exactly linear in size (doubling size exactly doubles width, since it
// is nothing more than a per-glyph width sum multiplied by size) - so a
// single division gives the exact largest size that fits, unlike
// autoFontSize's own height-based estimate, which has no equivalently
// exact formula to fall back on. Used only for single-line fields (see
// textfield.go); a multiline field wraps instead of shrinking, per this
// package's doc comment on scope.
func shrinkToFitWidth(font *fonts.Font, codes []byte, size, maxWidth float64) float64 {
	if maxWidth <= 0 || len(codes) == 0 {
		return size
	}
	width := measureWidth(font, codes, size)
	if width <= maxWidth {
		return size
	}
	const minSize = 4.0
	shrunk := size * maxWidth / width
	if shrunk < minSize {
		return minSize
	}
	return shrunk
}
