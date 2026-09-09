package crypt

// This file implements encodePassword, which turns the Go string a
// caller types (via the root package's WithPassword option - see
// internal/parser's setupEncryption, this function's only real caller)
// into the raw bytes the Standard Security Handler's algorithms
// actually hash. The PDF specification requires a specific text
// encoding for a password before it is used cryptographically - simply
// hashing a Go string's raw UTF-8 bytes would silently fail to open a
// real-world file for any password containing a non-ASCII character,
// since the file was encrypted using a different encoding's bytes for
// that same character.

// encodePassword converts password into the byte sequence
// ComputeFileKey (revisions 2-4) or ComputeFileKeyR56/HashRevision
// (revisions 5-6) expect, per whichever encoding ISO 32000 specifies
// for r. An empty password string always encodes to an empty (zero
// length) byte slice for every revision, matching this package's
// Phase 7a behavior (an empty password) exactly - Phase 7b (a
// caller-supplied, possibly non-empty password) only changes what
// happens for a *non-empty* password string.
//
// # A deliberately scoped simplification
//
// Revisions 2-4 (see standardPasswordBytes) call for "PDFDocEncoding",
// a single-byte encoding almost, but not quite, identical to Latin-1 -
// full PDFDocEncoding requires an event-numbered mapping table
// diverging from Latin-1 only in the rarely-typed control-code range
// (0x18-0x1F, some of 0x80-0x9F). Revisions 5-6 call for the UTF-8
// bytes of the password after applying SASLprep (RFC 4013) Unicode
// normalization - a real implementation of stringprep, which this
// project's dependency policy (no third-party dependency, no CGO)
// would otherwise have to hand-roll from Unicode tables in full.
//
// This function implements the identity case of each: ordinary
// printable ASCII and Latin-1 text passes through completely
// correctly for revisions 2-4 (byte value equals code point, which is
// exactly what PDFDocEncoding and Latin-1 agree on for every character
// a person is likely to actually type in a password), and any password
// that is already in SASLprep-normalized form - true of ordinary ASCII
// text, which SASLprep does not alter at all - passes through correctly
// for revisions 5-6 too. A password using an exotic PDFDocEncoding-only
// control character, or Unicode text SASLprep would normalize
// differently (composed vs. decomposed accents, for example), is the
// only case this simplification does not handle exactly - the same
// kind of narrow, documented scope decision this project makes
// elsewhere (see, for example, internal/filter's LZWDecode
// /EarlyChange 0 gap). Revisit if a real-world password actually hits
// this gap.
func encodePassword(password string, r int) []byte {
	if password == "" {
		return nil
	}
	if r >= 5 {
		return utf8PasswordBytes(password)
	}
	return standardPasswordBytes(password)
}

// standardPasswordBytes approximates ISO 32000-1 §7.6.3.3's
// PDFDocEncoding, for revisions 2-4, as described in encodePassword's
// doc comment: each Unicode code point in password becomes one byte,
// its own value, when that code point fits in a byte (covering
// ordinary ASCII and Latin-1 Supplement text exactly); a code point
// that does not fit becomes '?' (0x3F, PDFDocEncoding's own standard
// substitute for an unrepresentable character) instead of silently
// corrupting or truncating the password.
func standardPasswordBytes(password string) []byte {
	runes := []rune(password)
	out := make([]byte, len(runes))
	for i, r := range runes {
		if r > 0xFF {
			r = '?'
		}
		out[i] = byte(r)
	}
	return out
}

// maxAES256PasswordBytes is ISO 32000-2 §7.6.4.3.4's limit: "Truncate
// the UTF-8 representation to 127 bytes if it is longer than 127
// bytes."
const maxAES256PasswordBytes = 127

// utf8PasswordBytes implements the revision 5-6 half of encodePassword:
// password's UTF-8 bytes (which is how Go already stores a string in
// memory - see the language specification - so no conversion is needed
// beyond a byte-slice reinterpretation), truncated to
// maxAES256PasswordBytes if longer. Truncation happens on a full UTF-8
// encoded byte boundary as written here (a mid-rune cut is
// specification-permitted per §7.6.4.3.4's own wording, which truncates
// bytes, not Unicode code points), matching what any other
// specification-following implementation would produce for the same
// over-length password.
func utf8PasswordBytes(password string) []byte {
	b := []byte(password)
	if len(b) > maxAES256PasswordBytes {
		b = b[:maxAES256PasswordBytes]
	}
	return b
}
