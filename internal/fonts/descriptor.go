package fonts

import (
	"regexp"
	"strings"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements Phase 1 of the font-substitution work described
// in docs/FONTS.md: turning a font dictionary's /FontDescriptor and
// /BaseFont entries into a small, structured summary of what the font
// is *supposed to look like* (its family name, and whether it is bold,
// italic, serif, or fixed-pitch/monospace). Later phases (see
// docs/FONTS.md's "Implementation" section) will compare this summary
// against real font files found on disk to pick a substitute for a font
// this package cannot extract real glyph outlines from. This file does
// not do any of that itself - it has no file I/O and does not change
// what Load, loadSimpleFont, or loadType0Font do today. It is pure data
// extraction, safe to build and test entirely on its own.
//
// # A note for readers newer to Go
//
// Go has no "optional field" or "nullable struct field" built in the
// way some languages do - a bool field that is never set is simply
// false, an int field that is never set is simply 0, and so on (this is
// called the field's "zero value"). Several functions in this file
// therefore return an extra "ok bool" alongside a value (for example,
// numberValue - already used elsewhere in this package, see font.go) so
// that a caller can tell "the PDF didn't say" apart from "the PDF said
// zero." FontCharacteristics itself does not bother with that
// distinction for every field - see its own doc comment for exactly
// which fields are best-effort defaults versus real "the PDF told us
// so" values.

// FontCharacteristics is a best-effort description of what a font
// *should* look like, built either from a PDF font dictionary (via
// Characterize) or, in a later phase, from a real font file found on
// disk - both sides are meant to produce values in this same shape so
// they can be compared against each other.
//
// Every field here has a well-defined zero value that means "not bold",
// "not italic", and so on, rather than "unknown" - this package always
// has *some* answer for each trait (even if it's a guess based on
// nothing better than the font's own name), matching the rest of this
// package's existing "Font never fails, it just falls back" philosophy
// (see Font's own doc comment in font.go).
type FontCharacteristics struct {
	// Family is a best-effort human-readable family name, for example
	// "Arial" or "Times New Roman" - not a PDF resource name and not
	// necessarily the exact string a font file's own name table would
	// report, just this package's best guess at what a person would
	// call the font. It may be empty if nothing usable could be
	// determined at all (an empty /BaseFont and no /FontDescriptor
	// /FontFamily entry).
	Family string

	// Bold is true when this font is (or is meant to look like) a bold
	// weight - see Characterize's doc comment for exactly which PDF
	// entries can set this.
	Bold bool

	// Italic is true when this font is (or is meant to look like) an
	// italic or oblique style.
	Italic bool

	// Serif is true when the font descriptor positively says this is a
	// serif font (PDF's /Flags bit 2). This package deliberately does
	// not try to *guess* serif-ness from the family name alone here -
	// see this file's package-level doc comment above and
	// docs/FONTS.md's Phase 1 "Non-goals" section - so a false value
	// here means "not marked serif", not "confirmed sans-serif";
	// telling those apart from the family name (Times New Roman versus
	// Arial, say) is left to a later phase's standard-14 lookup table.
	Serif bool

	// FixedPitch is true when the font descriptor positively says every
	// glyph in this font has the same advance width (PDF's /Flags bit
	// 1) - the traditional "monospace" or "typewriter" trait, as seen
	// in fonts like Courier.
	FixedPitch bool

	// Weight is a numeric font weight on the same 100-900 scale PDF's
	// own /FontWeight entry and OpenType's usWeightClass table use (100
	// = thin, 400 = normal/regular, 700 = bold, 900 = black). When no
	// weight information is available at all, this defaults to 700 if
	// Bold is true or 400 otherwise - a reasonable guess, not a value
	// the PDF actually stated, so it should not be treated as more
	// precise than that.
	Weight int
}

// Font descriptor /Flags bits this file reads, continuing the numbering
// convention simple.go's flagSymbolic already established (PDF
// specification, Table 123 - "Font descriptor flags"; bit position N,
// 1-indexed, corresponds to the value 1<<(N-1)).
const (
	// flagFixedPitch is bit position 1 (value 1): "All glyphs have the
	// same width (as opposed to proportional or variable-pitch fonts,
	// which have different widths)."
	flagFixedPitch = 1 << 0

	// flagSerif is bit position 2 (value 2): "Glyphs have serifs, which
	// are short strokes drawn at an angle on the top and bottom of
	// glyph stems... Sans serif fonts do not have serifs."
	flagSerif = 1 << 1

	// flagItalic is bit position 7 (value 64): "Glyphs have dominant
	// vertical strokes that are slanted."
	flagItalic = 1 << 6

	// flagForceBold is bit position 19 (value 262144): "The font
	// contains characters outside the Adobe standard Latin character
	// set... [and] the appearance of glyphs [should] be set to bold by
	// artificially thickening their outlines" - in practice, real-world
	// PDF producers also just use this bit as a general "treat this
	// font as bold" signal, which is exactly how this file uses it.
	flagForceBold = 1 << 18
)

// Characterize builds a FontCharacteristics for dict, a font dictionary
// in the same shape Load (font.go) accepts - dict's own top-level
// entries (/BaseFont, /FontDescriptor) are read directly, following one
// level of indirect reference through resolver where needed, exactly
// like Load's own helpers (dictValue, numberValue) already do elsewhere
// in this package.
//
// Characterize never fails: a dictionary with no /FontDescriptor at all
// (common for a non-embedded standard font - Times New Roman and Arial,
// for example, frequently appear in real-world PDFs with only a
// /BaseFont entry and no descriptor whatsoever) still produces a usable
// result, falling back to whatever /BaseFont's own name implies via
// ParsePostScriptName.
func Characterize(dict syntax.Dictionary, resolver Resolver) FontCharacteristics {
	descriptor, _ := dictValue(resolver, dict, "FontDescriptor")

	// flags holds the /FontDescriptor /Flags bitmask, or 0 if it is
	// absent - a missing flag simply reads as "bit not set" below,
	// mirroring isSimple.go's isSymbolic, which treats a missing /Flags
	// the same way.
	flags := 0
	if v, ok := numberValue(descriptor["Flags"]); ok {
		flags = int(v)
	}

	baseFont, _ := dict["BaseFont"].(syntax.Name)
	nameFamily, nameBold, nameItalic := ParsePostScriptName(string(baseFont))

	// /FontDescriptor /FontFamily, when present, is the PDF's own
	// explicit statement of the family name and is preferred over
	// whatever ParsePostScriptName guessed from /BaseFont. It is
	// declared by the specification as a PDF "text string" (which can,
	// in general, be UTF-16BE with a leading byte-order mark rather
	// than a simple single-byte encoding); this package only handles
	// the common single-byte case here, matching this project's
	// existing precedent of documenting an intentionally narrow text
	// decoding rather than silently mis-rendering it (see, for example,
	// encoding.go's note on StandardEncoding's own documented gap) -
	// /FontFamily is rare enough in real-world files that this is a
	// reasonable place to draw that line for now.
	family := nameFamily
	if fam, ok := descriptor["FontFamily"].(syntax.String); ok && len(fam) > 0 {
		family = string(fam)
	}

	// A font is treated as bold if any one of three independent signals
	// says so: the descriptor's own "force bold" flag, a numeric
	// /FontWeight of 600 or higher (600 sits between OpenType's
	// "semibold" (600) and "bold" (700) weight classes - deliberately
	// inclusive, since a semibold face is closer in spirit to a bold
	// substitute than a regular one), or a bold-looking /BaseFont name
	// (see ParsePostScriptName). Any single "yes" wins - there is no
	// real-world case where these signals disagree and the disagreement
	// should make this package say "not bold" instead of "bold".
	weightValue, hasWeight := numberValue(descriptor["FontWeight"])
	bold := flags&flagForceBold != 0 || (hasWeight && weightValue >= 600) || nameBold

	// Similarly, italic is true if the descriptor's italic flag is set,
	// /ItalicAngle is nonzero (PDF's own convention for "not slanted" -
	// see the specification's description of /FontDescriptor
	// /ItalicAngle - is exactly 0), or /BaseFont's name looks italic or
	// oblique.
	italicAngle, hasAngle := numberValue(descriptor["ItalicAngle"])
	italic := flags&flagItalic != 0 || (hasAngle && italicAngle != 0) || nameItalic

	weight := int(weightValue)
	if !hasWeight {
		if bold {
			weight = 700
		} else {
			weight = 400
		}
	}

	return FontCharacteristics{
		Family:     family,
		Bold:       bold,
		Italic:     italic,
		Serif:      flags&flagSerif != 0,
		FixedPitch: flags&flagFixedPitch != 0,
		Weight:     weight,
	}
}

// styleToken is one entry in styleTokens below: a substring that, when
// found in a PostScript font name, indicates a style (bold and/or
// italic, or - for "-Roman" - neither) and should be removed from the
// name once recognized (so it doesn't end up looking like part of the
// family name).
type styleToken struct {
	text   string
	bold   bool
	italic bool
}

// styleTokens lists the PostScript style substrings ParsePostScriptName
// recognizes, in the order they are tried. Combined bold+italic tokens
// ("BoldItalic", "BoldOblique") are listed before the plain "Bold" and
// "Italic"/"Oblique" tokens so that, for example, "Arial-BoldItalicMT"
// matches the single combined token "BoldItalic" (setting both Bold and
// Italic from one match) rather than only ever matching "Bold" and
// leaving a stray "Italic" behind in the family name. "Oblique" is
// PostScript's traditional name for a *slanted* (as opposed to a
// separately hand-drawn italic) style, but this package treats the two
// the same way, since the visual distinction does not matter for
// picking a substitute font.
//
// "-Roman" (bold=false, italic=false - already Go's zero value, so
// nothing needs setting) is PostScript's traditional fourth member of
// the classic Family-Roman/Family-Bold/Family-Italic/Family-BoldItalic
// naming convention (e.g. "Times-Roman", "Palatino-Roman"): it marks the
// plain upright weight, exactly the way "Bold"/"Italic" mark theirs, so
// it needs stripping from the family name for the same reason. Unlike
// every other entry here, its token text includes the leading hyphen:
// a bare "Roman" would also match inside a legitimately compound family
// name like "TimesNewRoman" (see
// TestParsePostScriptName_RealWorldNames's "TimesNewRomanPSMT" case,
// which must keep reading as family "Times New Roman", not have "Roman"
// stripped out of the middle) - requiring the hyphen restricts the match
// to the "-Roman" suffix form specifically.
var styleTokens = []styleToken{
	{text: "BoldOblique", bold: true, italic: true},
	{text: "BoldItalic", bold: true, italic: true},
	{text: "Oblique", italic: true},
	{text: "Italic", italic: true},
	{text: "Bold", bold: true},
	{text: "-Roman"},
}

// foundrySuffixes lists trailing substrings ParsePostScriptName strips
// from the end of a name once any style token has already been removed
// - these are foundry/format conventions rather than anything about the
// font's actual family or style. "PSMT" is listed before "MT" and "PS"
// individually so a name ending in all four letters ("TimesNewRomanPSMT")
// has the whole marker removed in one step rather than only "MT" being
// stripped and leaving a stray "PS" that a second pass then removes
// separately (both orders reach the same final answer since
// stripSuffixes below loops until nothing more matches, but trying the
// longer marker first keeps the common case a single step).
//
// "MT" originates from Monotype (a font foundry/vendor); the "PS" in
// "TimesNewRomanPSMT" indicates the PostScript-flavored metrics variant
// of that font. Neither says anything about weight or slant, which is
// why they are handled entirely separately from styleTokens above.
var foundrySuffixes = []string{"PSMT", "MT", "PS"}

// findStyleToken searches name for the first (leftmost) occurrence of any
// entry in styleTokens, in styleTokens' own list order when more than one
// would match at the same position - this is what lets a combined token
// like "BoldItalic" win over the plain "Bold" token it contains, since
// styleTokens deliberately lists the combined tokens first (see
// styleTokens' own doc comment). It returns the byte index of the match
// within name and the matched token itself, or (-1, styleToken{}) if
// nothing in styleTokens occurs in name at all.
func findStyleToken(name string) (int, styleToken) {
	for _, token := range styleTokens {
		if idx := indexFold(name, token.text); idx >= 0 {
			return idx, token
		}
	}
	return -1, styleToken{}
}

// detectStyleTokens reports whether name contains the word "Bold" and/or
// the word "Italic"/"Oblique" anywhere in it (case-insensitively),
// checking for each independently rather than picking a single "first
// match wins" token the way findStyleToken above does. This
// independent-checks approach is what correctly reads a "name" table
// subfamily string like "Bold Italic" (two separate words, space
// -separated) as *both* bold and italic - findStyleToken's combined
// tokens ("BoldItalic", with no space) exist specifically to parse a
// concatenated PostScript-style name such as "Arial-BoldItalicMT", and
// would miss the space-separated case entirely, matching only "Italic"
// and silently losing "Bold" (styleTokens lists "Italic" as its own
// separate entry precisely so a name with only italic and no bold still
// matches something). This function is used by probe.go's
// characterizeFace as a fallback signal for a face with no "OS/2" table
// to read bold/italic from directly.
func detectStyleTokens(name string) (bold, italic bool) {
	bold = indexFold(name, "Bold") >= 0
	italic = indexFold(name, "Italic") >= 0 || indexFold(name, "Oblique") >= 0
	return bold, italic
}

// subsetTagPattern matches a PDF font subset tag: exactly six uppercase
// ASCII letters followed by a "+", which a subsetted embedded font's
// /BaseFont is required (PDF specification, section 9.6.4.3) to be
// prefixed with, for example "ABCDEF+Verdana". The six letters
// themselves are arbitrary (chosen by whatever program created the
// subset) and carry no information about the font's actual name.
var subsetTagPattern = regexp.MustCompile(`^[A-Z]{6}\+`)

// camelBoundaryBeforeUpper matches a lowercase letter or digit
// immediately followed by an uppercase letter - the ordinary "word
// boundary" inside a concatenated name like "TimesNewRoman" (matches
// between "s" and "N", and between "w" and "R").
var camelBoundaryBeforeUpper = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// camelBoundaryAfterAcronym matches the end of a run of two or more
// uppercase letters where the run is followed by another uppercase
// letter that itself starts a new lowercase word - the trickier "word
// boundary" inside a name like "ITCAvantGarde", where "ITC" is an
// all-caps acronym immediately followed by the ordinary word "Avant"
// (matches between "ITC" and "Avant", splitting at the "C"/"A"
// boundary rather than after just one letter).
var camelBoundaryAfterAcronym = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)

// ParsePostScriptName parses name - a font's /BaseFont value, PDF's
// PostScript-style font name such as "Arial-BoldMT" or
// "ABCDEF+TimesNewRomanPS-BoldMT" - into a plain family name plus
// bold/italic flags, on a best-effort basis: real-world PDF producers
// are not perfectly consistent about these naming conventions, so a
// name this function doesn't recognize any style markers in simply
// comes back with bold=false, italic=false, and family set to name
// itself (after subset-tag removal and word-spacing), which is always a
// safe, if sometimes plain, answer.
//
// The steps, in order:
//
//  1. Remove a leading subset tag ("ABCDEF+"), if present.
//  2. Look for the first recognized style token (see styleTokens) and
//     remove it, recording whether it indicated bold and/or italic.
//  3. Repeatedly strip a trailing foundry/format marker (see
//     foundrySuffixes), along with any hyphen or comma left dangling
//     immediately before it.
//  4. Insert spaces at word boundaries within what's left (so
//     "TimesNewRoman" reads as "Times New Roman"), and trim any
//     leftover leading/trailing hyphens, commas, or spaces.
func ParsePostScriptName(name string) (family string, bold, italic bool) {
	name = subsetTagPattern.ReplaceAllString(name, "")

	if idx, token := findStyleToken(name); idx >= 0 {
		name = name[:idx] + name[idx+len(token.text):]
		bold, italic = token.bold, token.italic
	}

	name = stripFoundrySuffixes(name)

	name = camelBoundaryBeforeUpper.ReplaceAllString(name, "$1 $2")
	name = camelBoundaryAfterAcronym.ReplaceAllString(name, "$1 $2")
	name = strings.Trim(name, "-, ")

	return name, bold, italic
}

// indexFold returns the byte index of the first case-insensitive
// occurrence of substr within s, or -1 if substr does not occur at all
// - the same result strings.Index would give if both arguments were
// already the same case. Go's standard library has no direct
// case-insensitive equivalent of strings.Index, so this is a small,
// deliberately simple implementation: it is only ever called with short
// ASCII style tokens (see styleTokens) against font names, which are
// themselves conventionally ASCII, so the straightforward
// lowercase-both-and-compare approach here is more than fast enough and
// needs no Unicode-aware case folding.
func indexFold(s, substr string) int {
	return strings.Index(strings.ToLower(s), strings.ToLower(substr))
}

// stripFoundrySuffixes repeatedly removes a trailing entry of
// foundrySuffixes from name, along with any hyphen or comma left
// immediately before it, stopping either when no suffix matches
// anymore or when removing one would empty the string entirely (a name
// that is *only* "MT" and nothing else is left alone, since stripping
// it would leave no family name at all). The loop is needed because a
// name can end in more than one marker layered on top of each other
// once a style token has already been cut out of the middle - for
// example "TimesNewRomanPS-BoldMT" becomes "TimesNewRomanPS-MT" after
// "Bold" is removed, and reaching the final "TimesNewRoman" takes two
// passes: one to strip "-MT", and a second to strip the "PS" that was
// left exposed at the end.
func stripFoundrySuffixes(name string) string {
	for {
		stripped := false
		for _, suffix := range foundrySuffixes {
			if len(name) <= len(suffix) {
				continue
			}
			tail := name[len(name)-len(suffix):]
			if strings.EqualFold(tail, suffix) {
				name = strings.TrimRight(name[:len(name)-len(suffix)], "-,")
				stripped = true
				break
			}
		}
		if !stripped {
			return name
		}
	}
}
