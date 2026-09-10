package fonts

import "testing"

// This file tests tounicode.go's /ToUnicode CMap parsing in isolation,
// the same way cidcmap_test.go tests cidcmap.go's CMap parsing: a plain
// []byte in, a *ToUnicodeMap with plain methods out, no PDF document
// structure or Font involved at all - see font_test.go for the
// higher-level tests confirming Font.TextForCode actually uses this
// correctly once a real font dictionary is loaded.

// TestParseToUnicodeCMap_BFChar confirms the simplest possible shape: a
// single beginbfchar entry mapping one code directly to one Unicode
// character.
func TestParseToUnicodeCMap_BFChar(t *testing.T) {
	t.Parallel()
	data := []byte(`
/CMapName /Test-ToUnicode def
1 begincodespacerange
<00> <FF>
endcodespacerange
1 beginbfchar
<41> <0041>
endbfchar
endcmap
`)
	u := parseToUnicodeCMap(data)

	if s, ok := u.TextForCode(0x41); !ok || s != "A" {
		t.Errorf("TextForCode(0x41) = (%q,%v), want (\"A\",true)", s, ok)
	}
	if _, ok := u.TextForCode(0x42); ok {
		t.Errorf("TextForCode(0x42) reported found, want not found (no entry at all)")
	}
}

// TestParseToUnicodeCMap_BFCharLigature confirms a single code can map
// to more than one Unicode character - the common real-world case of a
// ligature glyph (one glyph, several characters, "ffi" here) that
// TestParseToUnicodeCMap_BFChar's one-rune-in-one-rune-out case does not
// exercise.
func TestParseToUnicodeCMap_BFCharLigature(t *testing.T) {
	t.Parallel()
	// "ffi" as three UTF-16BE code units: 0066 0066 0069.
	data := []byte(`
1 beginbfchar
<AB> <006600660069>
endbfchar
endcmap
`)
	u := parseToUnicodeCMap(data)
	if s, ok := u.TextForCode(0xAB); !ok || s != "ffi" {
		t.Errorf("TextForCode(0xAB) = (%q,%v), want (\"ffi\",true)", s, ok)
	}
}

// TestParseToUnicodeCMap_BFRangeIncrement confirms the common
// "increment a single destination value" beginbfrange shape: a
// contiguous run of codes maps to a contiguous run of Unicode
// characters, without needing one bfchar entry per code.
func TestParseToUnicodeCMap_BFRangeIncrement(t *testing.T) {
	t.Parallel()
	data := []byte(`
1 beginbfrange
<0041> <0043> <0041>
endbfrange
endcmap
`)
	u := parseToUnicodeCMap(data)

	tests := []struct {
		code uint32
		want string
	}{
		{0x41, "A"},
		{0x42, "B"},
		{0x43, "C"},
	}
	for _, tt := range tests {
		if s, ok := u.TextForCode(tt.code); !ok || s != tt.want {
			t.Errorf("TextForCode(0x%X) = (%q,%v), want (%q,true)", tt.code, s, ok, tt.want)
		}
	}
	if _, ok := u.TextForCode(0x44); ok {
		t.Errorf("TextForCode(0x44) reported found, want not found (outside the declared range)")
	}
}

// TestParseToUnicodeCMap_BFRangeArray confirms beginbfrange's other
// shape: an explicit array of destination strings, one per code in the
// range, with no arithmetic relationship between them at all (unlike
// the increment shape, this works even when consecutive codes mean
// unrelated characters).
func TestParseToUnicodeCMap_BFRangeArray(t *testing.T) {
	t.Parallel()
	data := []byte(`
1 beginbfrange
<10> <12> [<0041> <00660066> <0043>]
endbfrange
endcmap
`)
	u := parseToUnicodeCMap(data)

	if s, ok := u.TextForCode(0x10); !ok || s != "A" {
		t.Errorf("TextForCode(0x10) = (%q,%v), want (\"A\",true)", s, ok)
	}
	if s, ok := u.TextForCode(0x11); !ok || s != "ff" {
		t.Errorf("TextForCode(0x11) = (%q,%v), want (\"ff\",true)", s, ok)
	}
	if s, ok := u.TextForCode(0x12); !ok || s != "C" {
		t.Errorf("TextForCode(0x12) = (%q,%v), want (\"C\",true)", s, ok)
	}
}

// TestParseToUnicodeCMap_CharsTakePriorityOverRange confirms a
// beginbfchar entry is consulted before any beginbfrange that also
// happens to cover the same code - the same "specific beats general"
// precedence cidcmap.go's CMap.CIDForCode documents for CID lookups,
// applied here to /ToUnicode text.
func TestParseToUnicodeCMap_CharsTakePriorityOverRange(t *testing.T) {
	t.Parallel()
	data := []byte(`
1 beginbfrange
<0041> <0043> <0058>
endbfrange
1 beginbfchar
<42> <005A>
endbfchar
endcmap
`)
	u := parseToUnicodeCMap(data)
	if s, ok := u.TextForCode(0x42); !ok || s != "Z" {
		t.Errorf("TextForCode(0x42) = (%q,%v), want (\"Z\",true) - the bfchar entry should win", s, ok)
	}
	// 0x41 and 0x43 are only covered by the range, so they should still
	// use its own (unrelated) arithmetic: base 0x58 ('X') + offset.
	if s, ok := u.TextForCode(0x41); !ok || s != "X" {
		t.Errorf("TextForCode(0x41) = (%q,%v), want (\"X\",true)", s, ok)
	}
	if s, ok := u.TextForCode(0x43); !ok || s != "Z" {
		t.Errorf("TextForCode(0x43) = (%q,%v), want (\"Z\",true)", s, ok)
	}
}

// TestParseToUnicodeCMap_SurrogatePair confirms a destination hex string
// longer than 2 bytes decodes as a full UTF-16 surrogate pair rather
// than two separate (and individually meaningless) 16-bit values - a
// real requirement for any character outside the Basic Multilingual
// Plane (emoji being the most familiar modern example): U+1F600
// ("grinning face") encodes as the UTF-16BE surrogate pair D83D DE00.
func TestParseToUnicodeCMap_SurrogatePair(t *testing.T) {
	t.Parallel()
	data := []byte(`
1 beginbfchar
<01> <D83DDE00>
endbfchar
endcmap
`)
	u := parseToUnicodeCMap(data)
	s, ok := u.TextForCode(0x01)
	if !ok {
		t.Fatalf("TextForCode(0x01) not found")
	}
	runes := []rune(s)
	if len(runes) != 1 || runes[0] != 0x1F600 {
		t.Errorf("TextForCode(0x01) = %q (runes %v), want a single U+1F600", s, runes)
	}
}

// TestParseToUnicodeCMap_MalformedTolerated confirms parseToUnicodeCMap
// never panics or returns a nil *ToUnicodeMap for truncated or otherwise
// malformed input - see parseToUnicodeCMap's own doc comment for the
// "skip what can't be parsed" policy this exercises. It does not assert
// anything about *which* entries survive, only that parsing completes
// and the result is safe to query.
func TestParseToUnicodeCMap_MalformedTolerated(t *testing.T) {
	t.Parallel()
	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("beginbfchar"),                    // no matching endbfchar
		[]byte("1 beginbfchar <41"),              // truncated mid hex string
		[]byte("1 beginbfrange <00> <FF"),        // truncated mid hi bound
		[]byte("1 beginbfrange <FF> <00> <41>"),  // hi < lo
		[]byte("1 beginbfrange <00> <01> [<41>"), // unterminated array
		[]byte("garbage garbage garbage"),
	}
	for i, data := range cases {
		u := parseToUnicodeCMap(data)
		if u == nil {
			t.Errorf("case %d: parseToUnicodeCMap returned nil", i)
			continue
		}
		if _, ok := u.TextForCode(0x41); ok {
			// Not necessarily wrong for every case, but worth a second
			// look if it ever happens - most of these cases declare no
			// complete, well-formed entry at all.
			t.Logf("case %d: TextForCode(0x41) unexpectedly found something", i)
		}
	}
}

// TestToUnicodeMap_NilReceiver confirms every method on a nil
// *ToUnicodeMap is safe to call - the same "a Font is always usable"
// nil-tolerance cidcmap.go's CMap documents, needed here because a font
// with no /ToUnicode entry at all (loadToUnicode never having set
// Font.toUnicode) leaves that field nil, and Font.TextForCode calls
// straight into it with no nil check of its own.
func TestToUnicodeMap_NilReceiver(t *testing.T) {
	t.Parallel()
	var u *ToUnicodeMap
	if _, ok := u.TextForCode(0x41); ok {
		t.Errorf("nil *ToUnicodeMap.TextForCode reported found, want not found")
	}
}
