package content

import (
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements parseInlineImage, which reads a "BI...ID...EI"
// inline image directly against the Lexer, having already consumed the
// "BI" keyword. See Parse's doc comment for why this cannot go through
// the ordinary "operands then keyword" loop the way every other operator
// does: an inline image's dictionary portion uses its own abbreviated
// key names, and its raw sample data is not tokenizable PDF object
// syntax at all.

// inlineKeyAliases maps every abbreviated key name the PDF specification
// permits inside an inline image's dictionary (section 8.9.7, Table 93)
// to the full name an ordinary image XObject's dictionary would use for
// the same thing. Normalizing here means internal/image.Decode (and
// interpret.go's "Do" handling, for a referenced XObject image) can
// share one code path that only ever looks for full names - it does not
// need its own copy of this table, or to special-case inline images at
// all.
var inlineKeyAliases = map[syntax.Name]syntax.Name{
	"BPC": "BitsPerComponent",
	"CS":  "ColorSpace",
	"D":   "Decode",
	"DP":  "DecodeParms",
	"F":   "Filter",
	"H":   "Height",
	"IM":  "ImageMask",
	"W":   "Width",
	"I":   "Interpolate",
	"L":   "Length",
}

func normalizeInlineKey(key syntax.Name) syntax.Name {
	if full, ok := inlineKeyAliases[key]; ok {
		return full
	}
	return key
}

// parseInlineImage reads an inline image's dictionary and raw sample
// data, having already consumed the "BI" keyword that introduces it.
func parseInlineImage(lex *syntax.Lexer) (*InlineImage, error) {
	dict := syntax.Dictionary{}
	for {
		tok, err := lex.Next()
		if err != nil {
			return nil, err
		}
		if tok.Kind == syntax.KindKeyword && tok.Text == "ID" {
			break
		}
		if tok.Kind == syntax.KindEOF {
			return nil, pdferror.Malformedf("inline image (\"BI\") has no \"ID\" operator before end of input")
		}
		if tok.Kind != syntax.KindName {
			return nil, pdferror.Malformedf("inline image dictionary key must be a name, found %v", tok)
		}
		key := normalizeInlineKey(syntax.Name(tok.Bytes))
		val, err := syntax.ParseValue(lex, 0)
		if err != nil {
			return nil, err
		}
		dict[key] = val
	}

	// The specification requires exactly one whitespace byte between
	// "ID" and the raw data that follows it (see SkipOneWhitespaceByte's
	// doc comment) - unlike an ordinary token, this must be read at the
	// raw byte level, since a lexer that always skips whitespace ahead of
	// the next token could not tell "no data, then a byte that happens to
	// be whitespace" apart from "the mandatory separator, then data that
	// starts with more whitespace".
	if err := lex.SkipOneWhitespaceByte(); err != nil {
		return nil, err
	}

	raw, err := readInlineImageData(lex, dict)
	if err != nil {
		return nil, err
	}
	return &InlineImage{Dict: dict, Raw: raw}, nil
}

// readInlineImageData reads an inline image's raw sample bytes, having
// already consumed the dictionary and the single mandatory whitespace
// byte following "ID". It prefers an exact byte count whenever one can
// be determined without decoding anything - either the non-standard but
// unambiguous /L (Length) key, or (when the image has no /Filter at all)
// a length computed directly from /Width, /Height, /BitsPerComponent,
// and the color space's component count - and falls back to scanning for
// a whitespace-delimited "EI" otherwise (a filtered image with no /L:
// its true encoded length cannot be known without decoding it first, and
// decoding it requires first knowing where it ends - see
// Lexer.ScanForInlineImageEnd's doc comment for the same tension spelled
// out in more detail).
func readInlineImageData(lex *syntax.Lexer, dict syntax.Dictionary) ([]byte, error) {
	if n, ok := explicitInlineLength(dict); ok {
		return readExactInlineBytes(lex, n)
	}
	if _, hasFilter := dict["Filter"]; !hasFilter {
		if n, ok := computedInlineLength(dict); ok {
			return readExactInlineBytes(lex, n)
		}
	}
	return lex.ScanForInlineImageEnd()
}

func readExactInlineBytes(lex *syntax.Lexer, n int64) ([]byte, error) {
	raw, err := lex.ReadRawBytes(n)
	if err != nil {
		return nil, err
	}
	tok, err := lex.Next()
	if err != nil {
		return nil, err
	}
	if tok.Kind != syntax.KindKeyword || tok.Text != "EI" {
		return nil, pdferror.Malformedf("inline image data is not followed by an \"EI\" operator")
	}
	return raw, nil
}

// explicitInlineLength reports the byte count named by dict's /Length
// (originally /L) entry, if present - the one unambiguous way to know an
// inline image's raw data length regardless of whether it is filtered.
func explicitInlineLength(dict syntax.Dictionary) (int64, bool) {
	v, ok := dict["Length"]
	if !ok {
		return 0, false
	}
	n, ok := v.(syntax.Integer)
	if !ok || n < 0 {
		return 0, false
	}
	return int64(n), true
}

// computedInlineLength computes an unfiltered inline image's exact raw
// byte count from its declared dimensions, bit depth, and color space
// component count, mirroring the row-padded packing internal/image.
// Decode expects (see that package's bitReader). It reports ok=false
// whenever any of those are missing or the component count cannot be
// determined without resolving a named or array color space resource
// (which this package deliberately avoids doing just to find a length -
// see readInlineImageData's doc comment for the fallback this triggers).
func computedInlineLength(dict syntax.Dictionary) (int64, bool) {
	w, ok1 := dict["Width"].(syntax.Integer)
	h, ok2 := dict["Height"].(syntax.Integer)
	if !ok1 || !ok2 || w <= 0 || h <= 0 {
		return 0, false
	}

	isMask, _ := dict["ImageMask"].(syntax.Boolean)
	bpc := int64(8)
	if bool(isMask) {
		bpc = 1
	} else if v, ok := dict["BitsPerComponent"].(syntax.Integer); ok {
		bpc = int64(v)
	}

	comps, ok := inlineRawComponentCount(dict, bool(isMask))
	if !ok {
		return 0, false
	}

	rowBits := int64(w) * bpc * int64(comps)
	rowBytes := (rowBits + 7) / 8
	return rowBytes * int64(h), true
}

// inlineRawComponentCount reports how many raw samples one pixel carries
// for a color space named directly (a bare Name, one of the three Device
// families or their inline abbreviations - see internal/image's
// colorspace.go for the full set this project supports once
// /Resources /ColorSpace resolution is available), or 1 for an
// /ImageMask. Any other /ColorSpace form (an array such as /Indexed, or
// a name that must be looked up in /Resources) returns ok=false, since
// resolving those needs the machinery internal/image already owns and
// this package deliberately does not duplicate here - see
// computedInlineLength's doc comment.
func inlineRawComponentCount(dict syntax.Dictionary, isMask bool) (int, bool) {
	if isMask {
		return 1, true
	}
	name, ok := dict["ColorSpace"].(syntax.Name)
	if !ok {
		return 0, false
	}
	switch name {
	case "DeviceGray", "G":
		return 1, true
	case "DeviceRGB", "RGB":
		return 3, true
	case "DeviceCMYK", "CMYK":
		return 4, true
	default:
		return 0, false
	}
}
