// Package filter decodes PDF stream filters: the encodings a stream's
// /Filter dictionary entry names, which must be reversed before a
// stream's bytes mean anything to a higher layer. This is Phase 2 work
// per the repository README's phased plan ("Content streams and a
// minimal raster backend"); Phase 1 deliberately stopped short of this
// package, since PDF 1.5+ cross-reference streams and object streams -
// carried forward into this phase - are themselves normally
// Flate-compressed and so could not be supported without it.
//
// # Supported filters
//
// ASCII85Decode, ASCIIHexDecode, RunLengthDecode, LZWDecode, and
// FlateDecode (with PNG and TIFF predictors) are implemented. Any other
// filter name - DCTDecode (JPEG, Phase 3), CCITTFaxDecode, JBIG2Decode,
// JPXDecode, and Crypt - returns an error wrapping
// pdferror.ErrUnsupported naming the filter, rather than being silently
// skipped or misread; see docs/capability-matrix.md for the up-to-date
// status of each.
//
// # Filter chains
//
// PDF allows a stream's /Filter entry to name more than one filter, in
// which case they are applied in the order listed (the same order the
// stream was encoded in, e.g. compressed with FlateDecode and then
// additionally encoded with ASCII85Decode so the result is safe to embed
// in a text-only transport). Decode reverses the chain in that same
// order, since decoding is meant to exactly undo encoding, not reverse
// it. /DecodeParms carries filter-specific parameters (in particular,
// the PNG/TIFF predictor for FlateDecode and LZWDecode) and, when more
// than one filter is named, is expected to line up with /Filter
// entry-for-entry.
//
// # Bounded decompression
//
// Every decoder in this package enforces maxDecodedSize on its output,
// so a maliciously crafted small input claiming to expand into gigabytes
// of output (a "decompression bomb") fails with a classified error
// instead of exhausting memory - see the repository README's
// "Dependency and safety policy" section, which specifically calls out
// bounding decompression.
//
// # Indirect references within a stream's own dictionary
//
// This package works entirely with already-resolved syntax.Object
// values: it does not know how to follow a syntax.Reference, since doing
// so requires a cross-reference table (internal/parser's job, not this
// package's). A stream's /Filter, /DecodeParms, or a /DecodeParms
// dictionary's own entries are, in practice, always written directly
// rather than as indirect references, so this is not a real-world
// limitation - but a caller working with a stream whose dictionary might
// still contain unresolved references should resolve them first; see
// internal/parser.Document.DecodeStream, which does exactly that before
// calling into this package.
package filter

import (
	"fmt"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// maxDecodedSize bounds how many bytes any single filter's decoded
// output - or, for a filter chain, each individual step's output - may
// contain. 256 MiB is far larger than any legitimate single content
// stream, image, or object stream this project's fixture corpus or
// real-world documents are expected to need, while still keeping a
// hostile input's worst-case memory use bounded and predictable; see the
// package doc comment's "Bounded decompression" section.
const maxDecodedSize = 256 << 20

// Decode returns the fully-decoded bytes of a stream whose dictionary is
// dict and whose still-encoded bytes are raw, applying every filter
// named in dict's /Filter entry in order. If dict has no /Filter entry
// at all, raw is returned unchanged - not every stream is filtered.
func Decode(dict syntax.Dictionary, raw []byte) ([]byte, error) {
	names, err := filterNames(dict)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return raw, nil
	}

	parmsList, err := decodeParmsList(dict, len(names))
	if err != nil {
		return nil, err
	}

	data := raw
	for i, name := range names {
		data, err = decodeOne(name, parmsList[i], data)
		if err != nil {
			return nil, fmt.Errorf("filter %q: %w", name, err)
		}
		if len(data) > maxDecodedSize {
			return nil, pdferror.Malformedf("filter %q output exceeds %d bytes", name, maxDecodedSize)
		}
	}
	return data, nil
}

// filterNames normalizes dict's /Filter entry - which PDF allows to be
// either a single Name or an Array of Names - into a slice, returning a
// nil (empty) slice if the entry is absent.
func filterNames(dict syntax.Dictionary) ([]syntax.Name, error) {
	v, ok := dict["Filter"]
	if !ok {
		return nil, nil
	}
	switch f := v.(type) {
	case syntax.Name:
		return []syntax.Name{f}, nil
	case syntax.Array:
		names := make([]syntax.Name, len(f))
		for i, e := range f {
			n, ok := e.(syntax.Name)
			if !ok {
				return nil, pdferror.Malformedf("/Filter array element %d is not a name (found %T)", i, e)
			}
			names[i] = n
		}
		return names, nil
	default:
		return nil, pdferror.Malformedf("/Filter is neither a name nor an array (found %T)", v)
	}
}

// decodeParmsList normalizes dict's /DecodeParms entry (also accepting
// the /DP abbreviation PDF permits in a few contexts) into a slice of
// length n, one per filter named in /Filter, in the same order. A filter
// with no corresponding parameters (entry absent, or explicitly null)
// gets a nil syntax.Dictionary, which every decodeOne case below treats
// as "use this filter's defaults".
func decodeParmsList(dict syntax.Dictionary, n int) ([]syntax.Dictionary, error) {
	out := make([]syntax.Dictionary, n)

	v, ok := dict["DecodeParms"]
	if !ok {
		v, ok = dict["DP"]
	}
	if !ok {
		return out, nil
	}

	switch p := v.(type) {
	case syntax.Dictionary:
		if n != 1 {
			return nil, pdferror.Malformedf("/DecodeParms is a single dictionary but /Filter names %d filters", n)
		}
		out[0] = p
		return out, nil
	case syntax.Array:
		if len(p) != n {
			return nil, pdferror.Malformedf("/DecodeParms array has %d entries but /Filter names %d filters", len(p), n)
		}
		for i, e := range p {
			switch d := e.(type) {
			case syntax.Dictionary:
				out[i] = d
			case syntax.Null:
				// Leave out[i] nil: this filter uses its defaults.
			default:
				return nil, pdferror.Malformedf("/DecodeParms array element %d is neither a dictionary nor null (found %T)", i, e)
			}
		}
		return out, nil
	case syntax.Null:
		return out, nil
	default:
		return nil, pdferror.Malformedf("/DecodeParms is neither a dictionary, an array, nor null (found %T)", v)
	}
}

// decodeOne applies the single filter named name (with parameters parms,
// which is nil if none were given) to data.
func decodeOne(name syntax.Name, parms syntax.Dictionary, data []byte) ([]byte, error) {
	switch name {
	case "ASCII85Decode", "A85":
		return decodeASCII85(data)
	case "ASCIIHexDecode", "AHx":
		return decodeASCIIHex(data)
	case "RunLengthDecode", "RL":
		return decodeRunLength(data)
	case "LZWDecode", "LZW":
		return decodeLZW(data, parms)
	case "FlateDecode", "Fl":
		return decodeFlate(data, parms)
	default:
		return nil, pdferror.Unsupportedf("stream filter %q", name)
	}
}

// intParm reads an integer-valued entry from parms (which may be nil,
// meaning no parameters were given at all), returning def if the key is
// absent, parms itself is nil, or the entry is not a syntax.Integer.
// Every /DecodeParms field this package reads (/Predictor, /Colors,
// /BitsPerComponent, /Columns, /EarlyChange) is defined by the PDF
// specification to be a plain integer, never a real number or indirect
// reference (see the package doc comment's note on indirect references).
func intParm(parms syntax.Dictionary, key syntax.Name, def int) int {
	if parms == nil {
		return def
	}
	v, ok := parms[key]
	if !ok {
		return def
	}
	n, ok := v.(syntax.Integer)
	if !ok {
		return def
	}
	return int(n)
}
