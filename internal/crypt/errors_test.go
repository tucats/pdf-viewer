package crypt

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file tests the error paths handler_test.go's round-trip-focused
// tests do not exercise: malformed /Encrypt dictionaries, truncated or
// corrupted ciphertext, and a handful of small helper functions' edge
// cases. Constructing a genuinely malformed or corrupted input for each
// of these (rather than just a well-formed one that happens not to
// validate, which handler_test.go's TestNewR234WrongPassword and
// TestNewR56WrongPassword already cover) is what a real, if unusual,
// PDF file could plausibly contain - a truncated download, a corrupted
// copy, or simply a bug in whatever tool produced it - so this package
// must fail on it with an error, never a panic.

func TestNewRejectsMalformedEncryptDict(t *testing.T) {
	id0 := []byte("0123456789ABCDEF")

	cases := []struct {
		name string
		dict syntax.Dictionary
	}{
		{
			"missing /O",
			syntax.Dictionary{"V": syntax.Integer(1), "R": syntax.Integer(2), "U": syntax.String(make([]byte, 32))},
		},
		{
			"missing /U",
			syntax.Dictionary{"V": syntax.Integer(1), "R": syntax.Integer(2), "O": syntax.String(make([]byte, 32))},
		},
		{
			"/O wrong length",
			syntax.Dictionary{
				"V": syntax.Integer(1), "R": syntax.Integer(2),
				"O": syntax.String(make([]byte, 10)), "U": syntax.String(make([]byte, 32)),
			},
		},
		{
			"revision 5 with a 32-byte (not 48-byte) /U",
			syntax.Dictionary{
				"V": syntax.Integer(5), "R": syntax.Integer(5),
				"O": syntax.String(make([]byte, 32)), "U": syntax.String(make([]byte, 32)),
			},
		},
		{
			"revision 6 missing /UE",
			syntax.Dictionary{
				"V": syntax.Integer(5), "R": syntax.Integer(6),
				"O": syntax.String(make([]byte, 48)), "U": syntax.String(make([]byte, 48)),
			},
		},
		{
			"revision 4 with an invalid /Length",
			syntax.Dictionary{
				"V": syntax.Integer(4), "R": syntax.Integer(4),
				"O": syntax.String(make([]byte, 32)), "U": syntax.String(make([]byte, 32)),
				"Length": syntax.Integer(13), // not a multiple of 8
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.dict, id0); err == nil {
				t.Fatalf("New(%v): want an error, got nil", tc.dict)
			}
		})
	}
}

func TestDecryptStreamRejectsCorruptCiphertext(t *testing.T) {
	dict, _, id0 := buildR234Dict(t, 4, 16, MethodAESV2)
	h, err := New(dict, id0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Run("shorter than one AES block", func(t *testing.T) {
		if _, err := h.DecryptStream(7, 0, []byte{0x01, 0x02, 0x03}); err == nil {
			t.Fatal("DecryptStream: want an error for data shorter than the AES block size")
		}
	})

	t.Run("not block-aligned", func(t *testing.T) {
		data := make([]byte, 16+5) // one IV block plus a partial block
		if _, err := h.DecryptStream(7, 0, data); err == nil {
			t.Fatal("DecryptStream: want an error for non-block-aligned ciphertext")
		}
	})

	t.Run("empty stream decrypts to empty, no error", func(t *testing.T) {
		got, err := h.DecryptStream(7, 0, nil)
		if err != nil {
			t.Fatalf("DecryptStream(nil): %v", err)
		}
		if len(got) != 0 {
			t.Errorf("DecryptStream(nil) = %x, want empty", got)
		}
	})
}

func TestDecryptObjectPropagatesErrors(t *testing.T) {
	dict, _, id0 := buildR234Dict(t, 4, 16, MethodAESV2)
	h, err := New(dict, id0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A string too short to contain even one AES block, nested inside a
	// dictionary and an array, must surface as an error from
	// DecryptObject rather than being silently dropped or panicking.
	badString := syntax.String([]byte{0xAA})
	cases := []struct {
		name string
		obj  syntax.Object
	}{
		{"direct string", badString},
		{"nested in dictionary", syntax.Dictionary{"X": badString}},
		{"nested in array", syntax.Array{badString}},
		{"nested in a stream's dictionary", syntax.Stream{Dict: syntax.Dictionary{"X": badString}, Raw: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.DecryptObject(1, 0, tc.obj); err == nil {
				t.Fatalf("DecryptObject(%#v): want an error", tc.obj)
			}
		})
	}
}

func TestResolveCryptFiltersDefaultsToIdentityWhenAbsent(t *testing.T) {
	// A /V 4 document with no /CF, /StmF, or /StrF at all is unusual but
	// legal - per ISO 32000-1 §7.6.5, both default to /Identity, meaning
	// nothing is actually encrypted despite the document declaring
	// /Encrypt.
	stm, str := resolveCryptFilters(syntax.Dictionary{})
	if stm != MethodIdentity || str != MethodIdentity {
		t.Errorf("resolveCryptFilters({}) = (%v, %v), want (Identity, Identity)", stm, str)
	}
}

func TestPkcs7UnpadRejectsInvalidPadding(t *testing.T) {
	cases := [][]byte{
		{0x01, 0x02, 0x00}, // padding length 0 is never valid
		{0x01, 0x02, 0x10}, // padding length (16) larger than the data itself
		bytesN(20, 17),     // padding length larger than one AES block
	}
	for _, data := range cases {
		if _, err := pkcs7Unpad(data); err == nil {
			t.Errorf("pkcs7Unpad(%x): want an error", data)
		}
	}
}

func bytesN(n int, last byte) []byte {
	b := make([]byte, n)
	b[n-1] = last
	return b
}

func TestIntEntryAndBoolEntryDefaults(t *testing.T) {
	dict := syntax.Dictionary{
		"Present":     syntax.Integer(42),
		"WrongType":   syntax.Name("not a number"),
		"BoolPresent": syntax.Boolean(true),
	}
	if got := intEntry(dict, "Present", -1); got != 42 {
		t.Errorf("intEntry(Present) = %d, want 42", got)
	}
	if got := intEntry(dict, "Missing", -1); got != -1 {
		t.Errorf("intEntry(Missing) = %d, want default -1", got)
	}
	if got := intEntry(dict, "WrongType", -1); got != -1 {
		t.Errorf("intEntry(WrongType) = %d, want default -1", got)
	}
	if got := boolEntry(dict, "BoolPresent", false); got != true {
		t.Errorf("boolEntry(BoolPresent) = %v, want true", got)
	}
	if got := boolEntry(dict, "Missing", true); got != true {
		t.Errorf("boolEntry(Missing) = %v, want default true", got)
	}
}
