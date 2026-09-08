package model

import (
	"bytes"
	"fmt"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// PageContentBytes returns page's content stream bytes, fully decoded
// through whatever filters each stream's dictionary names (via
// Document.parser.DecodeStream - see internal/parser and
// internal/filter). A page's own /Contents entry may be a single stream
// or an array of several: per the PDF specification, an array of
// content streams is treated as if their decoded bytes were
// concatenated with a single whitespace byte between each pair (so that
// an operator is never accidentally split, or two operators
// accidentally joined, across a stream boundary that a producer chose
// for reasons unrelated to content stream syntax, such as keeping each
// page's incremental edits in their own stream object). A page with no
// /Contents entry at all (a genuinely blank page) returns empty bytes
// and a nil error, not an error - Phase 1's hand-authored fixtures
// already rely on this being valid.
func (d *Document) PageContentBytes(page Page) ([]byte, error) {
	contents, ok := page.dict["Contents"]
	if !ok {
		return nil, nil
	}

	resolved, err := resolveObject(d.parser, contents)
	if err != nil {
		return nil, fmt.Errorf("resolving page /Contents: %w", err)
	}

	switch v := resolved.(type) {
	case syntax.Stream:
		return d.parser.DecodeStream(v)
	case syntax.Array:
		var buf bytes.Buffer
		for i, elem := range v {
			streamObj, err := resolveObject(d.parser, elem)
			if err != nil {
				return nil, fmt.Errorf("resolving page /Contents[%d]: %w", i, err)
			}
			stream, ok := streamObj.(syntax.Stream)
			if !ok {
				return nil, pdferror.Malformedf("page /Contents[%d] is not a stream (found %T)", i, streamObj)
			}
			decoded, err := d.parser.DecodeStream(stream)
			if err != nil {
				return nil, fmt.Errorf("decoding page /Contents[%d]: %w", i, err)
			}
			if i > 0 {
				buf.WriteByte('\n')
			}
			buf.Write(decoded)
		}
		return buf.Bytes(), nil
	case syntax.Null:
		return nil, nil
	default:
		return nil, pdferror.Malformedf("page /Contents is neither a stream nor an array (found %T)", resolved)
	}
}
