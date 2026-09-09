package crypt

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodePasswordEmptyIsNil(t *testing.T) {
	for _, r := range []int{2, 3, 4, 5, 6} {
		if got := encodePassword("", r); len(got) != 0 {
			t.Errorf("encodePassword(\"\", %d) = %x, want empty", r, got)
		}
	}
}

func TestEncodePasswordR234ASCIIPassesThrough(t *testing.T) {
	got := encodePassword("Hunter2!", 3)
	want := []byte("Hunter2!")
	if !bytes.Equal(got, want) {
		t.Errorf("encodePassword(%q, 3) = %x, want %x", "Hunter2!", got, want)
	}
}

func TestEncodePasswordR234NonLatin1SubstitutesQuestionMark(t *testing.T) {
	// U+4E2D ('中') does not fit in a single PDFDocEncoding/Latin-1 byte
	// (see encodePassword's doc comment on this package's documented
	// simplification) - it must become '?' rather than silently
	// corrupting or misrepresenting the password.
	got := encodePassword("a中b", 4)
	want := []byte("a?b")
	if !bytes.Equal(got, want) {
		t.Errorf("encodePassword(%q, 4) = %x, want %x", "a中b", got, want)
	}
}

func TestEncodePasswordR56UsesUTF8Bytes(t *testing.T) {
	// Revision 5-6 uses the password's UTF-8 bytes directly (see
	// encodePassword's doc comment) - unlike revisions 2-4, a non-Latin-1
	// character is preserved exactly, not substituted.
	password := "a中b"
	got := encodePassword(password, 6)
	want := []byte(password)
	if !bytes.Equal(got, want) {
		t.Errorf("encodePassword(%q, 6) = %x, want %x", password, got, want)
	}
}

func TestEncodePasswordR56TruncatesTo127Bytes(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := encodePassword(long, 6)
	if len(got) != maxAES256PasswordBytes {
		t.Fatalf("encodePassword(200-byte password, 6) length = %d, want %d", len(got), maxAES256PasswordBytes)
	}
	if !bytes.Equal(got, []byte(long[:maxAES256PasswordBytes])) {
		t.Errorf("encodePassword(200-byte password, 6) = %q, want the first %d bytes of the input", got, maxAES256PasswordBytes)
	}
}
