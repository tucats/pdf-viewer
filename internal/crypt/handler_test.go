package crypt

import (
	"bytes"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file tests internal/crypt end to end, the same way
// internal/parser will actually use it: build a syntax.Dictionary that
// looks exactly like a real /Encrypt dictionary a PDF-writing
// application would have produced (using this package's own exported
// "forward direction" functions - ComputeOwnerHash, ComputeFileKey,
// ComputeUserHash, ComputeAES256UserStrings, and EncryptForFixture - see
// the package doc comment for why those exist), then feed it to New and
// check that decryption recovers the original plaintext.
//
// There is no independently-sourced "known answer" test vector here
// (no real encrypted PDF ships as a fixture for this project to check
// against - see tools/genfixtures's own doc comment on why this project
// prefers hand-authored fixtures) - instead, every test below is a
// round trip: encrypt with the forward functions, decrypt with Handler,
// and check the result matches what was encrypted. This does not catch
// a bug that is present identically in both directions (for example, if
// both ComputeUserHash and validateUserPassword agreed on a wrong
// algorithm), but it does catch the far more likely mistake of the two
// directions disagreeing with each other, and it fully exercises every
// code path New, DecryptString, and DecryptStream have.

// buildR234Dict returns a syntax.Dictionary equivalent to a real /Encrypt
// dictionary for revision r (2, 3, or 4), key length keyLenBytes, using
// method (RC4 or AESV2, only meaningful for r == 4) for both streams and
// strings, with both the owner and user passwords empty. It also
// returns the resulting file key, so a test can independently encrypt
// sample data to verify Handler decrypts it correctly.
func buildR234Dict(t *testing.T, r, keyLenBytes int, method Method) (syntax.Dictionary, []byte, []byte) {
	t.Helper()

	id0 := []byte("0123456789ABCDEF")
	var p int32 = -44

	o := ComputeOwnerHash(nil, nil, r, keyLenBytes)
	fileKey := ComputeFileKey(nil, o, p, id0, r, keyLenBytes, true)
	u := ComputeUserHash(fileKey, r, id0)

	dict := syntax.Dictionary{
		"Filter": syntax.Name("Standard"),
		"R":      syntax.Integer(r),
		"O":      syntax.String(o),
		"U":      syntax.String(u),
		"P":      syntax.Integer(p),
		"Length": syntax.Integer(keyLenBytes * 8),
	}
	switch {
	case r == 2:
		dict["V"] = syntax.Integer(1)
	case r == 3:
		dict["V"] = syntax.Integer(2)
	default: // r == 4
		dict["V"] = syntax.Integer(4)
		cfm := syntax.Name("V2")
		if method == MethodAESV2 {
			cfm = syntax.Name("AESV2")
		}
		dict["CF"] = syntax.Dictionary{
			"StdCF": syntax.Dictionary{"CFM": cfm},
		}
		dict["StmF"] = syntax.Name("StdCF")
		dict["StrF"] = syntax.Name("StdCF")
	}
	return dict, fileKey, id0
}

func TestNewR234RoundTrip(t *testing.T) {
	cases := []struct {
		name        string
		r           int
		keyLenBytes int
		method      Method
	}{
		{"R2-RC4-40bit", 2, 5, MethodRC4},
		{"R3-RC4-128bit", 3, 16, MethodRC4},
		{"R4-RC4-128bit", 4, 16, MethodRC4},
		{"R4-AESV2-128bit", 4, 16, MethodAESV2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dict, fileKey, id0 := buildR234Dict(t, tc.r, tc.keyLenBytes, tc.method)

			h, err := New(dict, id0)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			const num, gen = 7, 0
			plainString := []byte("The quick brown fox jumps over the lazy dog")
			plainStream := bytes.Repeat([]byte("stream content "), 5) // spans several AES blocks

			iv := bytes.Repeat([]byte{0x42}, 16)
			encString, err := EncryptForFixture(fileKey, tc.method, num, gen, iv, plainString)
			if err != nil {
				t.Fatalf("EncryptForFixture(string): %v", err)
			}
			encStream, err := EncryptForFixture(fileKey, tc.method, num, gen, iv, plainStream)
			if err != nil {
				t.Fatalf("EncryptForFixture(stream): %v", err)
			}

			gotString, err := h.DecryptString(num, gen, encString)
			if err != nil {
				t.Fatalf("DecryptString: %v", err)
			}
			if !bytes.Equal(gotString, plainString) {
				t.Errorf("DecryptString = %q, want %q", gotString, plainString)
			}

			gotStream, err := h.DecryptStream(num, gen, encStream)
			if err != nil {
				t.Fatalf("DecryptStream: %v", err)
			}
			if !bytes.Equal(gotStream, plainStream) {
				t.Errorf("DecryptStream = %q, want %q", gotStream, plainStream)
			}

			// Encrypting the exact same plaintext for a *different*
			// object number must not produce the same ciphertext -
			// otherwise Algorithm 1's whole point (see key.go) would be
			// defeated.
			encStringOtherObj, err := EncryptForFixture(fileKey, tc.method, num+1, gen, iv, plainString)
			if err != nil {
				t.Fatalf("EncryptForFixture(other object): %v", err)
			}
			if tc.method != MethodIdentity && bytes.Equal(encString, encStringOtherObj) {
				t.Errorf("ciphertext for object %d and %d unexpectedly matched", num, num+1)
			}
		})
	}
}

func TestNewR234WrongPassword(t *testing.T) {
	// A document whose /U was computed for a *different* file key (as if
	// a real, non-empty user password had been used) must be rejected
	// with ErrWrongPassword when this package tries the empty password
	// against it - this is exactly the "document needs a password we
	// don't have" case Phase 7a explicitly leaves unhandled (see the
	// package doc comment).
	const r, keyLenBytes = 3, 16
	id0 := []byte("0123456789ABCDEF")
	var p int32 = -44

	o := ComputeOwnerHash(nil, nil, r, keyLenBytes)
	// Compute U against a file key derived from a non-empty password,
	// simulating a document that really does require one.
	wrongFileKey := ComputeFileKey([]byte("secret"), o, p, id0, r, keyLenBytes, true)
	u := ComputeUserHash(wrongFileKey, r, id0)

	dict := syntax.Dictionary{
		"Filter": syntax.Name("Standard"),
		"V":      syntax.Integer(2),
		"R":      syntax.Integer(r),
		"O":      syntax.String(o),
		"U":      syntax.String(u),
		"P":      syntax.Integer(p),
		"Length": syntax.Integer(keyLenBytes * 8),
	}

	_, err := New(dict, id0)
	if err != ErrWrongPassword {
		t.Fatalf("New error = %v, want ErrWrongPassword", err)
	}
}

func TestNewRejectsUnsupportedFilterAndRevision(t *testing.T) {
	id0 := []byte("0123456789ABCDEF")

	t.Run("public-key filter", func(t *testing.T) {
		dict := syntax.Dictionary{
			"Filter": syntax.Name("Adobe.PubSec"),
			"V":      syntax.Integer(1),
			"R":      syntax.Integer(2),
		}
		if _, err := New(dict, id0); err == nil {
			t.Fatal("New: want an error for a non-Standard security handler")
		}
	})

	t.Run("out of range revision", func(t *testing.T) {
		dict := syntax.Dictionary{
			"Filter": syntax.Name("Standard"),
			"V":      syntax.Integer(1),
			"R":      syntax.Integer(1),
		}
		if _, err := New(dict, id0); err == nil {
			t.Fatal("New: want an error for revision 1")
		}
	})
}

func TestNewR234IdentityMethodPassesThrough(t *testing.T) {
	// A V4 document that names /Identity for /StrF but a real cipher for
	// /StmF (or vice versa) is unusual but legal - strings pass through
	// completely unencrypted in that case.
	const r, keyLenBytes = 4, 16
	id0 := []byte("0123456789ABCDEF")
	var p int32 = -44

	o := ComputeOwnerHash(nil, nil, r, keyLenBytes)
	fileKey := ComputeFileKey(nil, o, p, id0, r, keyLenBytes, true)
	u := ComputeUserHash(fileKey, r, id0)

	dict := syntax.Dictionary{
		"Filter": syntax.Name("Standard"),
		"V":      syntax.Integer(4),
		"R":      syntax.Integer(r),
		"O":      syntax.String(o),
		"U":      syntax.String(u),
		"P":      syntax.Integer(p),
		"Length": syntax.Integer(keyLenBytes * 8),
		"CF": syntax.Dictionary{
			"StdCF": syntax.Dictionary{"CFM": syntax.Name("V2")},
		},
		"StmF": syntax.Name("StdCF"),
		"StrF": syntax.Name("Identity"),
	}

	h, err := New(dict, id0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plain := []byte("not actually encrypted")
	got, err := h.DecryptString(9, 0, plain)
	if err != nil {
		t.Fatalf("DecryptString: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("DecryptString with /Identity /StrF = %q, want unchanged %q", got, plain)
	}
}

// buildR56Dict is buildR234Dict's revision 5/6 (AES-256) equivalent.
func buildR56Dict(t *testing.T, r int) (syntax.Dictionary, []byte, []byte) {
	t.Helper()

	id0 := []byte("0123456789ABCDEF")
	fileKey := bytes.Repeat([]byte{0x11}, 32) // arbitrary, fixed 256-bit file key
	validationSalt := bytes.Repeat([]byte{0x22}, 8)
	keySalt := bytes.Repeat([]byte{0x33}, 8)

	u, ue, err := ComputeAES256UserStrings(fileKey, r, validationSalt, keySalt)
	if err != nil {
		t.Fatalf("ComputeAES256UserStrings: %v", err)
	}
	o, oe, err := ComputeAES256OwnerStrings(fileKey, r, u, validationSalt, keySalt)
	if err != nil {
		t.Fatalf("ComputeAES256OwnerStrings: %v", err)
	}

	dict := syntax.Dictionary{
		"Filter": syntax.Name("Standard"),
		"V":      syntax.Integer(5),
		"R":      syntax.Integer(r),
		"O":      syntax.String(o),
		"U":      syntax.String(u),
		"OE":     syntax.String(oe),
		"UE":     syntax.String(ue),
		"P":      syntax.Integer(-4),
		"Length": syntax.Integer(256),
	}
	return dict, fileKey, id0
}

func TestNewR56RoundTrip(t *testing.T) {
	for _, r := range []int{5, 6} {
		t.Run(map[int]string{5: "R5", 6: "R6"}[r], func(t *testing.T) {
			dict, fileKey, id0 := buildR56Dict(t, r)

			h, err := New(dict, id0)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			const num, gen = 12, 0
			plain := bytes.Repeat([]byte("AES-256 payload "), 4)
			iv := bytes.Repeat([]byte{0x99}, 16)

			enc, err := EncryptForFixture(fileKey, MethodAESV3, num, gen, iv, plain)
			if err != nil {
				t.Fatalf("EncryptForFixture: %v", err)
			}
			got, err := h.DecryptStream(num, gen, enc)
			if err != nil {
				t.Fatalf("DecryptStream: %v", err)
			}
			if !bytes.Equal(got, plain) {
				t.Errorf("DecryptStream = %q, want %q", got, plain)
			}

			// Revision 5-6 uses the file key directly - encrypting the
			// same plaintext for a different object number must produce
			// *identical* ciphertext (given the same IV), unlike the
			// R2-4 case above, confirming ObjectKey is correctly skipped.
			encOtherObj, err := EncryptForFixture(fileKey, MethodAESV3, num+1, gen, iv, plain)
			if err != nil {
				t.Fatalf("EncryptForFixture(other object): %v", err)
			}
			if !bytes.Equal(enc, encOtherObj) {
				t.Errorf("AESV3 ciphertext unexpectedly differed by object number")
			}
		})
	}
}

func TestNewR56WrongPassword(t *testing.T) {
	dict, _, id0 := buildR56Dict(t, 6)
	// Corrupt the validation hash portion of /U so it no longer matches
	// an empty password, simulating a document with a real user
	// password.
	u := append(syntax.String{}, dict["U"].(syntax.String)...)
	u[0] ^= 0xFF
	dict["U"] = u

	_, err := New(dict, id0)
	if err != ErrWrongPassword {
		t.Fatalf("New error = %v, want ErrWrongPassword", err)
	}
}

func TestDecryptObjectRecursesThroughDictionariesAndArrays(t *testing.T) {
	dict, fileKey, id0 := buildR234Dict(t, 3, 16, MethodRC4)
	h, err := New(dict, id0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const num, gen = 3, 0
	title := []byte("Confidential Report")
	iv := bytes.Repeat([]byte{0x01}, 16)
	encTitle, err := EncryptForFixture(fileKey, MethodRC4, num, gen, iv, title)
	if err != nil {
		t.Fatalf("EncryptForFixture: %v", err)
	}
	encBody, err := EncryptForFixture(fileKey, MethodRC4, num, gen, iv, []byte("stream body"))
	if err != nil {
		t.Fatalf("EncryptForFixture: %v", err)
	}

	obj := syntax.Dictionary{
		"Title": syntax.String(encTitle),
		"Kids": syntax.Array{
			syntax.String(encTitle),
			syntax.Reference{Number: 99}, // must pass through unchanged
		},
		"Attachment": syntax.Stream{
			Dict: syntax.Dictionary{"Length": syntax.Integer(len(encBody))},
			Raw:  encBody,
		},
	}

	decrypted, err := h.DecryptObject(num, gen, obj)
	if err != nil {
		t.Fatalf("DecryptObject: %v", err)
	}
	out, ok := decrypted.(syntax.Dictionary)
	if !ok {
		t.Fatalf("DecryptObject returned %T, want syntax.Dictionary", decrypted)
	}

	if got := out["Title"].(syntax.String); !bytes.Equal(got, title) {
		t.Errorf("Title = %q, want %q", got, title)
	}
	kids := out["Kids"].(syntax.Array)
	if got := kids[0].(syntax.String); !bytes.Equal(got, title) {
		t.Errorf("Kids[0] = %q, want %q", got, title)
	}
	if ref, ok := kids[1].(syntax.Reference); !ok || ref.Number != 99 {
		t.Errorf("Kids[1] = %#v, want an untouched Reference{Number: 99}", kids[1])
	}
	attachment := out["Attachment"].(syntax.Stream)
	if !bytes.Equal(attachment.Raw, []byte("stream body")) {
		t.Errorf("Attachment.Raw = %q, want %q", attachment.Raw, "stream body")
	}
}

func TestDecryptObjectSkipsUnencryptedMetadataStream(t *testing.T) {
	dict, _, id0 := buildR234Dict(t, 3, 16, MethodRC4)
	dict["EncryptMetadata"] = syntax.Boolean(false)
	// Because EncryptMetadata was false when computing the file key
	// (see buildR234Dict's "true" argument), rebuild the dictionary with
	// a matching file key so New's own validation still succeeds.
	id0Copy := append([]byte{}, id0...)
	o := dict["O"].(syntax.String)
	p := int32(dict["P"].(syntax.Integer))
	fileKey := ComputeFileKey(nil, []byte(o), p, id0Copy, 3, 16, false)
	dict["U"] = syntax.String(ComputeUserHash(fileKey, 3, id0Copy))

	h, err := New(dict, id0Copy)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plaintextMetadata := []byte("<xmp>unencrypted metadata bytes</xmp>")
	stream := syntax.Stream{
		Dict: syntax.Dictionary{"Type": syntax.Name("Metadata")},
		Raw:  plaintextMetadata, // stored as plaintext, per /EncryptMetadata false
	}

	decrypted, err := h.DecryptObject(5, 0, stream)
	if err != nil {
		t.Fatalf("DecryptObject: %v", err)
	}
	got := decrypted.(syntax.Stream)
	if !bytes.Equal(got.Raw, plaintextMetadata) {
		t.Errorf("Metadata stream Raw = %q, want untouched %q", got.Raw, plaintextMetadata)
	}
}

func TestPadPassword(t *testing.T) {
	empty := PadPassword(nil)
	if len(empty) != 32 || !bytes.Equal(empty, padBytes[:]) {
		t.Errorf("PadPassword(nil) = %x, want the full 32-byte pad string", empty)
	}

	short := PadPassword([]byte("abc"))
	if len(short) != 32 {
		t.Fatalf("PadPassword(\"abc\") length = %d, want 32", len(short))
	}
	if !bytes.Equal(short[:3], []byte("abc")) || !bytes.Equal(short[3:], padBytes[:29]) {
		t.Errorf("PadPassword(\"abc\") = %x, want \"abc\" followed by the pad string's first 29 bytes", short)
	}

	long := PadPassword(bytes.Repeat([]byte{0x01}, 40))
	if len(long) != 32 || !bytes.Equal(long, bytes.Repeat([]byte{0x01}, 32)) {
		t.Errorf("PadPassword of a 40-byte password should truncate to its first 32 bytes")
	}
}
