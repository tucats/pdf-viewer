package fonts

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// TestParsePostScriptName_RealWorldNames exercises ParsePostScriptName
// against the exact five /BaseFont values the docs/FONTS.md bug report
// was filed against (see "go run ./cmd/pdfpreview -diagnostics ..." in
// that document's audit section) - these are real, unedited PostScript
// names from a real PDF, not made up for this test, so getting them
// right is the actual bar Phase 1 needs to clear.
func TestParsePostScriptName_RealWorldNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		wantFamily string
		wantBold   bool
		wantItalic bool
	}{
		{"TimesNewRomanPSMT", "Times New Roman", false, false},
		{"Arial-BoldMT", "Arial", true, false},
		{"TimesNewRomanPS-BoldMT", "Times New Roman", true, false},
		{"ArialMT", "Arial", false, false},
		{"Arial-BoldItalicMT", "Arial", true, true},
		// "-Roman" is the classic PostScript "plain upright weight"
		// suffix (the fourth member of Family-Roman/-Bold/-Italic/
		// -BoldItalic), and must be stripped just like "-Bold" is -
		// without also matching the unrelated compound family name
		// "TimesNewRoman" above, whose "Roman" is not a suffix at all.
		{"Times-Roman", "Times", false, false},
		{"Palatino-Roman", "Palatino", false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			family, bold, italic := ParsePostScriptName(c.name)
			if family != c.wantFamily || bold != c.wantBold || italic != c.wantItalic {
				t.Errorf("ParsePostScriptName(%q) = (%q, %v, %v), want (%q, %v, %v)",
					c.name, family, bold, italic, c.wantFamily, c.wantBold, c.wantItalic)
			}
		})
	}
}

// TestParsePostScriptName_SubsetTag confirms a subsetted embedded
// font's six-uppercase-letter-plus-plus prefix (PDF specification,
// section 9.6.4.3 - see subsetTagPattern's doc comment) is removed
// before anything else runs, so it never leaks into the reported family
// name or confuses style-token matching.
func TestParsePostScriptName_SubsetTag(t *testing.T) {
	t.Parallel()

	family, bold, italic := ParsePostScriptName("ABCDEF+Verdana-Bold")
	if family != "Verdana" || !bold || italic {
		t.Errorf("ParsePostScriptName(subset-tagged) = (%q, %v, %v), want (\"Verdana\", true, false)", family, bold, italic)
	}

	// A prefix that merely *looks* like a subset tag but is lowercase,
	// or is not exactly six letters, must NOT be stripped - it's simply
	// part of an unusual font name.
	family, _, _ = ParsePostScriptName("abcdef+Verdana")
	if family != "abcdef+Verdana" {
		t.Errorf("ParsePostScriptName(lowercase prefix) = %q, want unchanged", family)
	}
	family, _, _ = ParsePostScriptName("ABCDE+Verdana")
	if family != "ABCDE+Verdana" {
		t.Errorf("ParsePostScriptName(five-letter prefix) = %q, want unchanged", family)
	}
}

// TestParsePostScriptName_CamelCaseWordSpacing exercises the
// word-spacing heuristic (camelBoundaryBeforeUpper and
// camelBoundaryAfterAcronym) on its own, including the trickier
// all-caps-acronym case ("ITC" immediately followed by the ordinary
// word "Avant") that camelBoundaryBeforeUpper alone cannot split
// correctly - see that variable's doc comment for why a second pass is
// needed.
func TestParsePostScriptName_CamelCaseWordSpacing(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, want string }{
		{"Verdana", "Verdana"},
		{"CourierNew", "Courier New"},
		{"ComicSansMS", "Comic Sans MS"},
		{"ITCAvantGardeGothic", "ITC Avant Garde Gothic"},
	}
	for _, c := range cases {
		family, _, _ := ParsePostScriptName(c.name)
		if family != c.want {
			t.Errorf("ParsePostScriptName(%q) family = %q, want %q", c.name, family, c.want)
		}
	}
}

// TestParsePostScriptName_NoStyleTokenLeavesFlagsFalse confirms a name
// with no recognized style substring at all comes back with both flags
// false, rather than, say, panicking or guessing - the documented
// "safe, if sometimes plain" fallback from ParsePostScriptName's doc
// comment.
func TestParsePostScriptName_NoStyleTokenLeavesFlagsFalse(t *testing.T) {
	t.Parallel()

	family, bold, italic := ParsePostScriptName("Georgia")
	if family != "Georgia" || bold || italic {
		t.Errorf("ParsePostScriptName(\"Georgia\") = (%q, %v, %v), want (\"Georgia\", false, false)", family, bold, italic)
	}
}

// TestParsePostScriptName_EmptyName confirms an empty /BaseFont (which
// does happen in malformed real-world files) does not panic and comes
// back as an empty family with no style flags set.
func TestParsePostScriptName_EmptyName(t *testing.T) {
	t.Parallel()

	family, bold, italic := ParsePostScriptName("")
	if family != "" || bold || italic {
		t.Errorf("ParsePostScriptName(\"\") = (%q, %v, %v), want (\"\", false, false)", family, bold, italic)
	}
}

// TestCharacterize_NoDescriptorFallsBackToBaseFont covers the exact
// shape of the reported bug's own fonts: a /BaseFont with no
// /FontDescriptor at all (confirmed against the real file - see this
// package's docs/FONTS.md audit), which is the common case for a
// non-embedded standard font. Characterize must still produce a usable
// result purely from /BaseFont.
func TestCharacterize_NoDescriptorFallsBackToBaseFont(t *testing.T) {
	t.Parallel()

	dict := syntax.Dictionary{
		"Subtype":  syntax.Name("TrueType"),
		"BaseFont": syntax.Name("Arial-BoldItalicMT"),
	}
	got := Characterize(dict, fakeResolver{})

	want := FontCharacteristics{Family: "Arial", Bold: true, Italic: true, Weight: 700}
	if got != want {
		t.Errorf("Characterize(no descriptor) = %+v, want %+v", got, want)
	}
}

// TestCharacterize_FlagsBits confirms every /FontDescriptor /Flags bit
// this file reads (flagFixedPitch, flagSerif, flagItalic,
// flagForceBold - see their doc comments for the PDF specification bit
// numbering) is wired to the right FontCharacteristics field, using a
// /BaseFont ("XYZ") that ParsePostScriptName would not itself recognize
// any style information from, so any Bold/Italic seen in the result can
// only have come from /Flags.
func TestCharacterize_FlagsBits(t *testing.T) {
	t.Parallel()

	dict := syntax.Dictionary{
		"Subtype":  syntax.Name("TrueType"),
		"BaseFont": syntax.Name("XYZ"),
		"FontDescriptor": syntax.Dictionary{
			"Flags": syntax.Integer(flagFixedPitch | flagSerif | flagItalic | flagForceBold),
		},
	}
	got := Characterize(dict, fakeResolver{})

	want := FontCharacteristics{
		Family:     "XYZ",
		Bold:       true,
		Italic:     true,
		Serif:      true,
		FixedPitch: true,
		Weight:     700,
	}
	if got != want {
		t.Errorf("Characterize(all flags set) = %+v, want %+v", got, want)
	}
}

// TestCharacterize_FontWeightAndItalicAngle confirms /FontWeight and
// /ItalicAngle are honored independently of /Flags - a font descriptor
// is allowed to specify these without setting the corresponding /Flags
// bit at all, and real-world files sometimes do exactly that.
func TestCharacterize_FontWeightAndItalicAngle(t *testing.T) {
	t.Parallel()

	t.Run("weight 600 counts as bold", func(t *testing.T) {
		t.Parallel()
		dict := syntax.Dictionary{
			"BaseFont": syntax.Name("XYZ"),
			"FontDescriptor": syntax.Dictionary{
				"FontWeight": syntax.Integer(600),
			},
		}
		got := Characterize(dict, fakeResolver{})
		if !got.Bold || got.Weight != 600 {
			t.Errorf("Characterize(FontWeight 600) = %+v, want Bold=true, Weight=600", got)
		}
	})

	t.Run("weight 400 does not count as bold", func(t *testing.T) {
		t.Parallel()
		dict := syntax.Dictionary{
			"BaseFont": syntax.Name("XYZ"),
			"FontDescriptor": syntax.Dictionary{
				"FontWeight": syntax.Integer(400),
			},
		}
		got := Characterize(dict, fakeResolver{})
		if got.Bold || got.Weight != 400 {
			t.Errorf("Characterize(FontWeight 400) = %+v, want Bold=false, Weight=400", got)
		}
	})

	t.Run("nonzero ItalicAngle counts as italic", func(t *testing.T) {
		t.Parallel()
		dict := syntax.Dictionary{
			"BaseFont": syntax.Name("XYZ"),
			"FontDescriptor": syntax.Dictionary{
				"ItalicAngle": syntax.Real(-12.5),
			},
		}
		got := Characterize(dict, fakeResolver{})
		if !got.Italic {
			t.Errorf("Characterize(ItalicAngle -12.5) = %+v, want Italic=true", got)
		}
	})

	t.Run("zero ItalicAngle does not count as italic", func(t *testing.T) {
		t.Parallel()
		dict := syntax.Dictionary{
			"BaseFont": syntax.Name("XYZ"),
			"FontDescriptor": syntax.Dictionary{
				"ItalicAngle": syntax.Integer(0),
			},
		}
		got := Characterize(dict, fakeResolver{})
		if got.Italic {
			t.Errorf("Characterize(ItalicAngle 0) = %+v, want Italic=false", got)
		}
	})
}

// TestCharacterize_FontFamilyOverridesBaseFontGuess confirms an
// explicit /FontDescriptor /FontFamily entry - the PDF's own stated
// family name - wins over whatever ParsePostScriptName would have
// guessed from /BaseFont, per Characterize's doc comment.
func TestCharacterize_FontFamilyOverridesBaseFontGuess(t *testing.T) {
	t.Parallel()

	dict := syntax.Dictionary{
		"BaseFont": syntax.Name("XYZ123-BoldMT"),
		"FontDescriptor": syntax.Dictionary{
			"FontFamily": syntax.String("Custom Family Name"),
		},
	}
	got := Characterize(dict, fakeResolver{})
	if got.Family != "Custom Family Name" {
		t.Errorf("Characterize(FontFamily override).Family = %q, want %q", got.Family, "Custom Family Name")
	}
	// The style still comes from /BaseFont even though the family name
	// was overridden - /FontFamily only ever supplies a name, per the
	// specification, not a style.
	if !got.Bold {
		t.Errorf("Characterize(FontFamily override).Bold = false, want true (from /BaseFont)")
	}
}

// TestCharacterize_EmptyDictionary confirms a font dictionary with
// neither /BaseFont nor /FontDescriptor - about as degenerate as a
// dictionary Load itself would still accept can get - does not panic
// and returns the all-defaults FontCharacteristics{Weight: 400}.
func TestCharacterize_EmptyDictionary(t *testing.T) {
	t.Parallel()

	got := Characterize(syntax.Dictionary{}, fakeResolver{})
	want := FontCharacteristics{Weight: 400}
	if got != want {
		t.Errorf("Characterize(empty dict) = %+v, want %+v", got, want)
	}
}
