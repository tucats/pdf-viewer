package filter

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

func TestDecodeNoFilter(t *testing.T) {
	raw := []byte("plain bytes")
	got, err := Decode(syntax.Dictionary{}, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("Decode with no /Filter = %v, want unchanged %v", got, raw)
	}
}

func TestDecodeSingleFilter(t *testing.T) {
	raw := []byte("48656C6C6F>")
	dict := syntax.Dictionary{"Filter": syntax.Name("ASCIIHexDecode")}
	got, err := Decode(dict, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, []byte("Hello")) {
		t.Fatalf("Decode = %q, want %q", got, "Hello")
	}
}

func TestDecodeFilterChain(t *testing.T) {
	// A stream encoded as Flate, then ASCII85 - the order a producer
	// would encode in (compress, then make transport-safe). /Filter
	// lists them in the order to *decode*, so ASCII85Decode runs first.
	original := []byte("chained filter content, twice over")
	flated := encodeFlateForTest(t, original)
	encoded := encodeASCII85ForTest(flated)

	dict := syntax.Dictionary{
		"Filter": syntax.Array{syntax.Name("ASCII85Decode"), syntax.Name("FlateDecode")},
	}
	got, err := Decode(dict, encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("Decode chain = %q, want %q", got, original)
	}
}

func TestDecodeParmsArrayMatchesFilterArray(t *testing.T) {
	// One row, three 8-bit samples [5, 8, 2], TIFF-predicted (each
	// sample stored as its difference, mod 256, from the one before).
	predicted := []byte{5, 3, 250} // 5, 8-5=3, 2-8=-6 = 250 mod 256
	encoded := encodeLZWForTest(t, predicted)

	dict := syntax.Dictionary{
		"Filter": syntax.Array{syntax.Name("LZWDecode")},
		"DecodeParms": syntax.Array{
			syntax.Dictionary{
				"Predictor":        syntax.Integer(2),
				"Colors":           syntax.Integer(1),
				"BitsPerComponent": syntax.Integer(8),
				"Columns":          syntax.Integer(3),
			},
		},
	}
	got, err := Decode(dict, encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []byte{5, 8, 2}
	if !bytes.Equal(got, want) {
		t.Fatalf("Decode with array /DecodeParms = %v, want %v", got, want)
	}
}

func TestDecodeUnsupportedFilter(t *testing.T) {
	// Crypt is the one remaining unimplemented filter name now that
	// Phase 14f has wired up JPXDecode (JPEG 2000) - see jpx.go and
	// jpx_test.go.
	dict := syntax.Dictionary{"Filter": syntax.Name("Crypt")}
	_, err := Decode(dict, []byte("whatever"))
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Decode with unsupported filter: got %v, want an error wrapping ErrUnsupported", err)
	}
}

func TestDecodeMalformedFilterEntry(t *testing.T) {
	dict := syntax.Dictionary{"Filter": syntax.Integer(5)}
	_, err := Decode(dict, []byte("whatever"))
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with non-name /Filter: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestDecodeParmsCountMismatch(t *testing.T) {
	dict := syntax.Dictionary{
		"Filter":      syntax.Array{syntax.Name("ASCIIHexDecode"), syntax.Name("FlateDecode")},
		"DecodeParms": syntax.Array{syntax.Dictionary{}},
	}
	_, err := Decode(dict, []byte("whatever"))
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Decode with mismatched /DecodeParms length: got %v, want an error wrapping ErrMalformed", err)
	}
}
