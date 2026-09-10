package fonts

import (
	"bytes"
	"fmt"
	"strings"
)

// This file is type1.go's forward direction: building a complete,
// working Type 1 font program byte-for-byte, the same "round-trip
// against an encoder this package also owns" testing strategy
// internal/filter/jbig2mq.go's mqEncoder and cff_test.go's buildTestCFF
// use, for the same reason - this project has no independently-produced,
// freely-redistributable real-world Type 1 font sample to validate
// parseType1Font against (the sample this phase was given for local
// testing turned out to embed CFF/Type1C programs under a /Subtype
// /Type1 font *dictionary*, not an actual /FontFile Type 1 charstring
// program at all - see docs/PLAN2.md's Phase 11 progress log). Encoding
// and decoding here are written from opposite ends of the specification
// (Program 7.1 describes decryption; encryptType1 is this package's own
// derivation of its inverse) wherever the format allows that
// independence - though, unlike CFF's Type 2 charstring format,
// Adobe's own eexec cipher is a synchronous stream cipher whose
// encrypt and decrypt procedures are structurally identical either way
// (see decryptType1's doc comment), so this one piece cannot be made
// independent even in principle.
//
// EncodeType1FontProgram is exported - despite existing only for tests
// and fixture generation - because tools/genfixtures (a separate
// module) needs to build a synthetic embedded Type 1 font fixture too,
// the same reason internal/filter exports EncodeJBIG2GenericRegion.

// encryptType1 is the forward direction of decryptType1's eexec stream
// cipher - see that function's doc comment for why this is, for this
// particular cipher, literally the same procedure run with plaintext
// and ciphertext swapped (the running key r is always re-derived from
// whichever byte was just produced as ciphertext, which encryptType1
// computes before decryptType1 would ever see it, rather than from
// plaintext).
func encryptType1(plain []byte, r uint16) []byte {
	cipher := make([]byte, len(plain))
	for i, p := range plain {
		c := p ^ byte(r>>8)
		cipher[i] = c
		r = (uint16(c)+r)*type1C1 + type1C2
	}
	return cipher
}

// encryptType1Charstring is decryptType1Charstring's forward direction:
// prepends lenIV arbitrary padding bytes (this package always uses
// zero bytes - the specification does not require any particular
// value, only that decryptType1Charstring's caller discard exactly
// this many) to cs and encrypts the result with the charstring key.
func encryptType1Charstring(cs []byte, lenIV int) []byte {
	padded := make([]byte, lenIV+len(cs))
	copy(padded[lenIV:], cs)
	return encryptType1(padded, type1CharstringKey)
}

// EncodeType1Int encodes v using Type 1 Charstring's compact
// variable-length integer operand forms (decodeType1Number's forward
// direction) - whichever of the four forms is shortest for v. Exported
// so tools/genfixtures can build charstring operand bytes by value
// instead of hand-computing the encoded bytes for each one, the same
// convenience cff_test.go's unexported encodeCFFInt gives this
// package's own tests (not reused directly here since a _test.go file's
// declarations are not visible outside its own package - see this
// file's own doc comment on why EncodeType1FontProgram has to be
// exported for the same reason).
func EncodeType1Int(v int) []byte {
	switch {
	case v >= -107 && v <= 107:
		return []byte{byte(v + 139)}
	case v >= 108 && v <= 1131:
		v -= 108
		return []byte{byte(v/256 + 247), byte(v % 256)}
	case v >= -1131 && v <= -108:
		v = -v - 108
		return []byte{byte(v/256 + 251), byte(v % 256)}
	default:
		return []byte{255, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
}

// Type1TestGlyph names one glyph (by PostScript glyph name) together
// with its already op-encoded, not-yet-encrypted Type 1 charstring
// body - the input EncodeType1FontProgram needs to build a complete,
// loadable embedded Type 1 font program. See this file's doc comment
// for why it is exported.
type Type1TestGlyph struct {
	Name       string
	Charstring []byte
}

// EncodeType1FontProgram assembles a complete, minimal, working Type 1
// font program suitable for a PDF /FontFile stream, returning its bytes
// together with the /Length1 (cleartext prefix length) and /Length2
// (eexec-encrypted portion length) values that stream's dictionary
// needs (see simple.go's readType1FontFileStream) - /Length3 (the
// trailing zeros-plus-cleartomark convention, see this function's own
// trailer-writing code below) is always 0 in what this function builds,
// which the PDF specification explicitly permits when that trailer is
// simply omitted.
//
// glyphs' charstrings are encrypted with the default lenIV (4); subrs
// (each already an op-encoded, not-yet-encrypted Type 1 charstring
// body, indexed by its own position in this slice) may be nil for a
// font that uses no local subroutines at all. unitsPerEm, when nonzero,
// is written out as this font's own /FontMatrix (see
// findType1UnitsPerEm); pass 0 to use the specification's own default
// of 1000.
func EncodeType1FontProgram(glyphs []Type1TestGlyph, subrs [][]byte, unitsPerEm uint16) (data []byte, length1, length2 int) {
	var header bytes.Buffer
	header.WriteString("%!PS-AdobeFont-1.0: GenfixturesType1 001.000\n")
	header.WriteString("/FontName /GenfixturesType1 def\n")
	scale := 0.001
	if unitsPerEm != 0 {
		scale = 1.0 / float64(unitsPerEm)
	}
	fmt.Fprintf(&header, "/FontMatrix [%g 0 0 %g 0 0] readonly def\n", scale, scale)
	header.WriteString("currentfile eexec\n")

	var private bytes.Buffer
	private.WriteString("dup /Private 15 dict dup begin\n")
	private.WriteString("/lenIV 4 def\n")
	if len(subrs) > 0 {
		fmt.Fprintf(&private, "/Subrs %d array\n", len(subrs))
		for i, s := range subrs {
			enc := encryptType1Charstring(s, 4)
			fmt.Fprintf(&private, "dup %d %d RD ", i, len(enc))
			private.Write(enc)
			private.WriteString(" NP\n")
		}
	}
	fmt.Fprintf(&private, "/CharStrings %d dict dup begin\n", len(glyphs))
	for _, g := range glyphs {
		enc := encryptType1Charstring(g.Charstring, 4)
		fmt.Fprintf(&private, "/%s %d RD ", g.Name, len(enc))
		private.Write(enc)
		private.WriteString(" ND\n")
	}
	private.WriteString("end\n")
	private.WriteString("end\n")

	// The eexec cipher's own leading 4-byte discard (splitType1EexecSection)
	// - arbitrary padding bytes, so zero is as good as any other choice.
	plainWithLeadIn := make([]byte, 4+private.Len())
	copy(plainWithLeadIn[4:], private.Bytes())
	cipher := encryptType1(plainWithLeadIn, type1EexecKey)

	var out bytes.Buffer
	out.Write(header.Bytes())
	length1 = out.Len()
	out.Write(cipher)
	length2 = len(cipher)

	// A conventional (though, per this function's own doc comment, not
	// functionally required by this package's own parser) trailer -
	// included only so this fixture's byte layout resembles a real Type
	// 1 font program as closely as costs nothing to do.
	for i := 0; i < 8; i++ {
		out.WriteString(strings.Repeat("0", 64))
		out.WriteByte('\n')
	}
	out.WriteString("cleartomark\n")

	return out.Bytes(), length1, length2
}
