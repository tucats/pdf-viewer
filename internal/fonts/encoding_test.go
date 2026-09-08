package fonts

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestStandardEncoding_ASCIIRange(t *testing.T) {
	t.Parallel()
	table := standardEncoding()
	if table['A'] != 'A' || table['z'] != 'z' || table['0'] != '0' || table[' '] != ' ' {
		t.Errorf("StandardEncoding ASCII range not identity-mapped: A=%q z=%q 0=%q space=%q",
			table['A'], table['z'], table['0'], table[' '])
	}
	if table[0] != 0 {
		t.Errorf("code 0 should be unmapped, got %q", table[0])
	}
}

func TestWinAnsiEncoding_UpperRange(t *testing.T) {
	t.Parallel()
	table := winAnsiEncoding()
	if table[0x80] != '€' {
		t.Errorf("WinAnsiEncoding[0x80] = %q, want €", table[0x80])
	}
	if table[0x92] != '’' {
		t.Errorf("WinAnsiEncoding[0x92] = %q, want right single quote", table[0x92])
	}
	// 0xA9 (Latin-1 Supplement copyright sign) should equal its own
	// Unicode code point directly, per winAnsiEncoding's doc comment.
	if table[0xA9] != 0xA9 {
		t.Errorf("WinAnsiEncoding[0xA9] = %U, want U+00A9", table[0xA9])
	}
}

func TestMacRomanEncoding_UpperRange(t *testing.T) {
	t.Parallel()
	table := macRomanEncoding()
	if table[0x80] != 'Ä' {
		t.Errorf("MacRomanEncoding[0x80] = %q, want Ä", table[0x80])
	}
	if table[0xE7] != 'Á' {
		t.Errorf("MacRomanEncoding[0xE7] = %q, want Á", table[0xE7])
	}
}

func TestBuildSimpleEncoding_DefaultsToStandard(t *testing.T) {
	t.Parallel()
	dict := syntax.Dictionary{}
	table := BuildSimpleEncoding(dict)
	if table['A'] != 'A' {
		t.Errorf("default encoding did not map 'A' to itself")
	}
}

func TestBuildSimpleEncoding_NamedBaseEncoding(t *testing.T) {
	t.Parallel()
	dict := syntax.Dictionary{"Encoding": syntax.Name("WinAnsiEncoding")}
	table := BuildSimpleEncoding(dict)
	if table[0x92] != '’' {
		t.Errorf("named WinAnsiEncoding not applied: got %q at 0x92", table[0x92])
	}
}

func TestBuildSimpleEncoding_Differences(t *testing.T) {
	t.Parallel()
	// Code 65 ('A' under StandardEncoding) is overridden to "B"; code 90
	// is set from a fresh integer entry to "eacute".
	dict := syntax.Dictionary{
		"Encoding": syntax.Dictionary{
			"BaseEncoding": syntax.Name("StandardEncoding"),
			"Differences": syntax.Array{
				syntax.Integer(65), syntax.Name("B"),
				syntax.Integer(90), syntax.Name("eacute"),
			},
		},
	}
	table := BuildSimpleEncoding(dict)
	if table[65] != 'B' {
		t.Errorf("code 65 = %q, want 'B' (from /Differences)", table[65])
	}
	// Only one name ("B") followed the integer 65 before the next
	// integer (90) appeared, so code 66 must be untouched from
	// StandardEncoding - i.e. still 'B' (StandardEncoding's own ASCII
	// mapping for code 66), not advanced further by the single-name
	// assignment to code 65.
	if table[66] != 'B' {
		t.Errorf("code 66 = %q, want 'B' (StandardEncoding's own mapping, untouched by /Differences)", table[66])
	}
	if table[90] != 'é' {
		t.Errorf("code 90 = %q, want 'é' (from /Differences)", table[90])
	}
}

func TestBuildSimpleEncoding_DifferencesSequentialNames(t *testing.T) {
	t.Parallel()
	// A single leading integer followed by several names assigns
	// consecutive codes: 24=breve is skipped (not in the small glyph
	// table) but 25=space and 26=exclam should both land, proving the
	// "advance to next code after each name" rule.
	dict := syntax.Dictionary{
		"Encoding": syntax.Dictionary{
			"Differences": syntax.Array{
				syntax.Integer(24), syntax.Name("space"), syntax.Name("exclam"),
			},
		},
	}
	table := BuildSimpleEncoding(dict)
	if table[24] != ' ' {
		t.Errorf("code 24 = %q, want space", table[24])
	}
	if table[25] != '!' {
		t.Errorf("code 25 = %q, want '!'", table[25])
	}
}

func TestGlyphNameToRune(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want rune
		ok   bool
	}{
		{"A", 'A', true},
		{"z", 'z', true},
		{"space", ' ', true},
		{"ampersand", '&', true},
		{"seven", '7', true},
		{"uni00E9", 'é', true},
		{"totally-bogus-name", 0, false},
	}
	for _, c := range cases {
		got, ok := glyphNameToRune(c.name)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("glyphNameToRune(%q) = (%q,%v), want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}
