package content

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// TestParseInlineImageExplicitLength confirms the non-standard /L
// (Length) key, when present, is used for an exact byte-count read
// rather than falling back to computing a length or scanning for "EI" -
// in particular, this must work even though the 3 raw bytes below
// happen to contain what could otherwise look like a false "EI" match.
func TestParseInlineImageExplicitLength(t *testing.T) {
	ops, err := Parse([]byte("BI /W 1 /H 1 /L 3 ID \x00EI EI"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	img := ops[0].InlineImage
	if img == nil {
		t.Fatal("InlineImage is nil")
	}
	if string(img.Raw) != "\x00EI" {
		t.Errorf("InlineImage.Raw = %q, want %q", img.Raw, "\x00EI")
	}
	// /L (an inline-image-only abbreviation) is normalized to /Length,
	// matching every other alias - see inlineKeyAliases.
	if img.Dict["Length"] != syntax.Integer(3) {
		t.Errorf("InlineImage.Dict[\"Length\"] = %#v, want Integer(3)", img.Dict["Length"])
	}
}

// TestParseInlineImageComputedLengthForUnfilteredData confirms an
// unfiltered inline image's raw length is computed from its declared
// /Width, /Height, /BitsPerComponent, and /ColorSpace rather than
// requiring an /L key or scanning for "EI" - and that this computed read
// correctly consumes data containing an embedded false "EI"-like byte
// sequence that a heuristic scan would have stopped at prematurely.
func TestParseInlineImageComputedLengthForUnfilteredData(t *testing.T) {
	// 2x1 DeviceRGB, 8 bits/component: 2*1*3 = 6 raw bytes, deliberately
	// containing the bytes 'E','I' in the middle where a naive scan might
	// misfire.
	raw := []byte{1, 2, 'E', 'I', 5, 6}
	data := append([]byte("BI /W 2 /H 1 /CS /RGB ID "), raw...)
	data = append(data, []byte(" EI")...)

	ops, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	img := ops[0].InlineImage
	if img == nil {
		t.Fatal("InlineImage is nil")
	}
	if string(img.Raw) != string(raw) {
		t.Errorf("InlineImage.Raw = %v, want %v", img.Raw, raw)
	}
}

// TestParseInlineImageScansForEIWhenFiltered confirms a filtered inline
// image (one with no computable exact length) falls back to scanning for
// a whitespace-delimited "EI".
func TestParseInlineImageScansForEIWhenFiltered(t *testing.T) {
	ops, err := Parse([]byte("BI /W 1 /H 1 /F /Fl ID somecompresseddata EI"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	img := ops[0].InlineImage
	if img == nil {
		t.Fatal("InlineImage is nil")
	}
	if string(img.Raw) != "somecompresseddata" {
		t.Errorf("InlineImage.Raw = %q, want %q", img.Raw, "somecompresseddata")
	}
	if img.Dict["Filter"] != syntax.Name("Fl") {
		t.Errorf("InlineImage.Dict[\"Filter\"] (from /F) = %#v, want Name(\"Fl\")", img.Dict["Filter"])
	}
}

// TestParseInlineImageImageMaskComputedLength confirms an /ImageMask
// inline image's computed length uses 1 bit per pixel regardless of any
// stray /BitsPerComponent, matching internal/image.Decode's own forced
// bpc=1 for masks.
func TestParseInlineImageImageMaskComputedLength(t *testing.T) {
	// 8x1 pixels at 1 bit/pixel = exactly 1 byte.
	data := []byte("BI /W 8 /H 1 /IM true ID \xAA EI")
	ops, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	img := ops[0].InlineImage
	if len(img.Raw) != 1 || img.Raw[0] != 0xAA {
		t.Errorf("InlineImage.Raw = %v, want [0xAA]", img.Raw)
	}
	if img.Dict["ImageMask"] != syntax.Boolean(true) {
		t.Errorf("InlineImage.Dict[\"ImageMask\"] (from /IM) = %#v, want Boolean(true)", img.Dict["ImageMask"])
	}
}

func TestParseInlineImageMissingIDIsMalformed(t *testing.T) {
	_, err := Parse([]byte("BI /W 1 /H 1"))
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse with no \"ID\": error = %v, want ErrMalformed", err)
	}
}

func TestParseInlineImageMissingEIAfterExplicitLengthIsMalformed(t *testing.T) {
	_, err := Parse([]byte("BI /W 1 /H 1 /L 3 ID abcXX"))
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse with wrong data after explicit /L: error = %v, want ErrMalformed", err)
	}
}

func TestParseInlineImageUnterminatedScanIsMalformed(t *testing.T) {
	_, err := Parse([]byte("BI /W 1 /H 1 /F /Fl ID nodata"))
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Parse with no \"EI\" terminator: error = %v, want ErrMalformed", err)
	}
}
