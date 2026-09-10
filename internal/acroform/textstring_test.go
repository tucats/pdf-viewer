package acroform

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestDecodeTextStringLatin1(t *testing.T) {
	got := decodeTextString([]byte("Hello"))
	if got != "Hello" {
		t.Fatalf("decodeTextString = %q, want %q", got, "Hello")
	}
}

func TestDecodeTextStringUTF16BE(t *testing.T) {
	// U+FEFF BOM followed by "Hi" as big-endian UTF-16 code units.
	raw := []byte{0xFE, 0xFF, 0x00, 'H', 0x00, 'i'}
	got := decodeTextString(raw)
	if got != "Hi" {
		t.Fatalf("decodeTextString = %q, want %q", got, "Hi")
	}
}

func TestDecodeTextStringUTF16BENonASCII(t *testing.T) {
	// U+00E9 (é) as big-endian UTF-16.
	raw := []byte{0xFE, 0xFF, 0x00, 0xE9}
	got := decodeTextString(raw)
	if got != "é" {
		t.Fatalf("decodeTextString = %q, want %q", got, "é")
	}
}

func TestDecodeTextStringUTF16BEOddTrailingByteDropped(t *testing.T) {
	raw := []byte{0xFE, 0xFF, 0x00, 'H', 0x00}
	got := decodeTextString(raw)
	if got != "H" {
		t.Fatalf("decodeTextString = %q, want %q (trailing odd byte dropped)", got, "H")
	}
}

func winAnsiFontDict() syntax.Dictionary {
	return syntax.Dictionary{
		"Subtype":  syntax.Name("Type1"),
		"BaseFont": syntax.Name("Helvetica"),
		"Encoding": syntax.Name("WinAnsiEncoding"),
	}
}

func TestEncodeForFontASCIIRoundTrips(t *testing.T) {
	got := encodeForFont(winAnsiFontDict(), "AB1")
	want := []byte{'A', 'B', '1'}
	if string(got) != string(want) {
		t.Fatalf("encodeForFont = %v, want %v", got, want)
	}
}

// TestEncodeForFontUnmappableRuneFallsBackToQuestionMark confirms a rune
// with no code at all in the font's encoding table degrades to '?'
// rather than being dropped or panicking - WinAnsiEncoding's own ASCII
// range already maps code 0x3F to '?' itself, so this exercises the
// fallback path actually finding it via the reverse table.
func TestEncodeForFontUnmappableRuneFallsBackToQuestionMark(t *testing.T) {
	got := encodeForFont(winAnsiFontDict(), "A☃B") // U+2603 SNOWMAN
	want := []byte{'A', '?', 'B'}
	if string(got) != string(want) {
		t.Fatalf("encodeForFont = %v, want %v", got, want)
	}
}

func TestEncodeForFontEmptyString(t *testing.T) {
	got := encodeForFont(winAnsiFontDict(), "")
	if len(got) != 0 {
		t.Fatalf("encodeForFont(\"\") = %v, want empty", got)
	}
}
