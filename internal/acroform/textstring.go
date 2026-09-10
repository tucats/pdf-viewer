package acroform

import (
	"unicode/utf16"

	"github.com/tucats/pdf-viewer/internal/fonts"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements the two small text-encoding conversions
// generating a text field's appearance needs: decoding a field's /V
// (a PDF "text string" - 7.9.2.2) into a Go string, and the reverse
// direction - encoding a Go string into the single-byte character codes
// a specific simple font's own /Encoding expects, so the bytes this
// package writes into a generated appearance's "Tj" operand mean what
// they are supposed to mean once internal/content's own text-showing
// code (unmodified, and unaware this appearance was not present in the
// original file) reads them back.

// decodeTextString decodes s - the raw bytes of a PDF text string object
// (a field's /V, most commonly) - into a Go string, per 7.9.2.2's two
// possible encodings: UTF-16BE with a leading byte-order-mark (0xFE
// 0xFF), or PDFDocEncoding otherwise.
//
// Full PDFDocEncoding is not implemented here - like internal/crypt's
// password.go (encodePassword's doc comment, which documents the exact
// same simplification for the same encoding in the opposite direction),
// this package treats a non-UTF-16 text string's bytes as Latin-1,
// which PDFDocEncoding agrees with for every ASCII and Latin-1
// character; only PDFDocEncoding's small set of specifically-remapped
// bytes (mostly typographic punctuation in the 0x18-0x1F and 0x80-0x9F
// ranges) would decode to the wrong rune, a rare and cosmetic
// divergence for the vast majority of real-world field values.
func decodeTextString(s []byte) string {
	if len(s) >= 2 && s[0] == 0xFE && s[1] == 0xFF {
		return utf16BEToString(s[2:])
	}
	runes := make([]rune, len(s))
	for i, b := range s {
		runes[i] = rune(b)
	}
	return string(runes)
}

// utf16BEToString decodes b as big-endian UTF-16 code units into a Go
// string. A trailing odd byte (not valid UTF-16 at all) is simply
// dropped rather than treated as an error, matching this project's
// general tolerance for minor malformed input elsewhere (e.g.
// internal/fonts/tounicode.go's identically named function, which this
// mirrors rather than imports - see internal/acroform.Resolver's doc
// comment on why this package declares its own small copies of
// utilities other packages already have private versions of, rather
// than depending on them).
func utf16BEToString(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	return string(utf16.Decode(units))
}

// encodeForFont converts text into the raw single-byte character codes
// fontDict's own /Encoding (as internal/fonts.BuildSimpleEncoding
// resolves it) maps each of text's runes to, so that a "Tj" operand
// built from the result shows the intended characters once decoded
// through that same font. A rune with no code in the font's encoding
// table becomes '?' (0x3F) if the encoding itself maps some code to
// '?' (true of every predefined encoding this package's font.go
// resolves), or is silently dropped otherwise - the same "best effort,
// never fail outright" tolerance this project applies to every other
// unmappable-input case (e.g. internal/crypt's encodePassword, which
// substitutes '?' for the identical reason).
//
// This only produces correct results for a *simple* (one-byte-code)
// font; see appearance.go's Type0-DA-font check for why a composite
// font here is rejected before this function is ever reached.
func encodeForFont(fontDict syntax.Dictionary, text string) []byte {
	enc := fonts.BuildSimpleEncoding(fontDict)

	reverse := make(map[rune]byte, 256)
	questionMark := byte(0)
	haveQuestionMark := false
	for code := 0; code < 256; code++ {
		r := enc[code]
		if r == 0 {
			continue
		}
		if _, exists := reverse[r]; !exists {
			reverse[r] = byte(code)
		}
		if r == '?' && !haveQuestionMark {
			questionMark = byte(code)
			haveQuestionMark = true
		}
	}

	out := make([]byte, 0, len(text))
	for _, r := range text {
		if b, ok := reverse[r]; ok {
			out = append(out, b)
			continue
		}
		if haveQuestionMark {
			out = append(out, questionMark)
			continue
		}
		out = append(out, '?')
	}
	return out
}
