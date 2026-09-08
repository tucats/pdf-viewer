package parser

import (
	"fmt"

	"github.com/tucats/pdf-viewer/internal/filter"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements loadXrefStream, the PDF 1.5+ counterpart to
// loadClassicXrefTable in parser.go: a cross-reference stream packs the
// same information a classic table does (object N is free, or lives at
// some byte offset - plus a third possibility classic tables cannot
// express at all, "object N is packed inside object stream S at index
// I"; see objstream.go) into a compact, filterable binary stream instead
// of human-readable text, and folds what would otherwise be a separate
// "trailer" dictionary directly into the stream's own dictionary.

// loadXrefStream parses a cross-reference stream, having already peeked
// (and pushed back onto lex) far enough into the section at offset to
// know it does not begin with the "xref" keyword - so it is expected to
// be "N G obj << ... /Type /XRef ... >> stream ... endstream endobj"
// instead. It merges the stream's entries into d.xref, exactly as
// loadClassicXrefTable does for a classic table, and returns the
// stream's own dictionary to serve as this section's trailer (an /XRef
// stream's dictionary is defined by the specification to fill the same
// role a separate trailer dictionary does for a classic table, carrying
// /Root, /Prev, /Size, and so on directly).
func (d *Document) loadXrefStream(lex *syntax.Lexer, offset int64) (syntax.Dictionary, error) {
	_, obj, err := parseIndirectObject(lex)
	if err != nil {
		return nil, fmt.Errorf("cross-reference stream at offset %d: %w", offset, err)
	}
	stream, ok := obj.(syntax.Stream)
	if !ok {
		return nil, pdferror.Malformedf("cross-reference section at offset %d is neither an \"xref\" table nor a stream (found %T)", offset, obj)
	}
	if t, ok := stream.Dict["Type"].(syntax.Name); ok && t != "XRef" {
		return nil, pdferror.Malformedf("stream at offset %d has /Type %q, expected /XRef", offset, t)
	}

	// A cross-reference stream's own dictionary entries are required by
	// the specification to be direct values, never indirect references:
	// resolving a reference requires the very cross-reference table this
	// stream is in the middle of contributing to, which would be
	// circular. filter.Decode is therefore called directly on
	// stream.Dict here, rather than through Document.DecodeStream (which
	// resolves indirect /Filter and /DecodeParms entries) - that method
	// is for ordinary streams encountered after the document is already
	// open.
	decoded, err := filter.Decode(stream.Dict, stream.Raw)
	if err != nil {
		return nil, fmt.Errorf("decoding cross-reference stream at offset %d: %w", offset, err)
	}

	widths, err := xrefStreamWidths(stream.Dict)
	if err != nil {
		return nil, err
	}
	size, err := xrefStreamSize(stream.Dict)
	if err != nil {
		return nil, err
	}
	index, err := xrefStreamIndex(stream.Dict, size)
	if err != nil {
		return nil, err
	}

	recordWidth := widths[0] + widths[1] + widths[2]
	if recordWidth == 0 {
		return nil, pdferror.Malformedf("cross-reference stream /W entries are all zero")
	}

	pos := 0
	for _, sub := range index {
		for i := 0; i < sub.count; i++ {
			if pos+recordWidth > len(decoded) {
				return nil, pdferror.Malformedf("cross-reference stream at offset %d ends before its /Index entries are satisfied", offset)
			}
			record := decoded[pos : pos+recordWidth]
			pos += recordWidth

			entry, err := parseXrefStreamRecord(record, widths)
			if err != nil {
				return nil, err
			}
			objNum := sub.start + i
			if _, exists := d.xref[objNum]; !exists {
				d.xref[objNum] = entry
			}
		}
	}

	return stream.Dict, nil
}

// xrefIndexRange is one (first object number, count) pair from a
// cross-reference stream's /Index entry - see xrefStreamIndex.
type xrefIndexRange struct {
	start, count int
}

// xrefStreamWidths reads and validates the stream dictionary's required
// /W entry: an array of exactly three non-negative integers giving the
// byte width of a record's three fields (type, field2, field3), in that
// order. A width of 0 for a field means that field is simply not stored
// - every record omits it - and it takes its type-specific default value
// instead; see parseXrefStreamRecord. Widths are capped at 8 bytes,
// comfortably covering any byte offset or object number a real file
// needs, so that a record's fields fit in a uint64 without needing
// arbitrary-precision arithmetic.
func xrefStreamWidths(dict syntax.Dictionary) ([3]int, error) {
	arr, ok := dict["W"].(syntax.Array)
	if !ok || len(arr) != 3 {
		return [3]int{}, pdferror.Malformedf("cross-reference stream /W must be a 3-element array")
	}
	var widths [3]int
	for i, v := range arr {
		n, ok := v.(syntax.Integer)
		if !ok || n < 0 || n > 8 {
			return [3]int{}, pdferror.Malformedf("cross-reference stream /W[%d] = %#v, want an integer in [0,8]", i, v)
		}
		widths[i] = int(n)
	}
	return widths, nil
}

// xrefStreamSize reads the stream dictionary's required /Size entry:
// one greater than the highest object number this revision of the
// document defines. It is used only as the default range for /Index
// when /Index itself is absent - see xrefStreamIndex.
func xrefStreamSize(dict syntax.Dictionary) (int, error) {
	n, ok := dict["Size"].(syntax.Integer)
	if !ok || n < 0 {
		return 0, pdferror.Malformedf("cross-reference stream /Size must be a non-negative integer")
	}
	return int(n), nil
}

// xrefStreamIndex reads the stream dictionary's optional /Index entry -
// pairs of (first object number, count) describing which object numbers
// this stream's records cover, and in what order - defaulting to the
// single pair [0, size] (every object number from 0 through size-1) when
// /Index is absent, per the specification.
func xrefStreamIndex(dict syntax.Dictionary, size int) ([]xrefIndexRange, error) {
	v, ok := dict["Index"]
	if !ok {
		return []xrefIndexRange{{start: 0, count: size}}, nil
	}
	arr, ok := v.(syntax.Array)
	if !ok || len(arr)%2 != 0 {
		return nil, pdferror.Malformedf("cross-reference stream /Index must be an array with an even number of elements")
	}
	ranges := make([]xrefIndexRange, 0, len(arr)/2)
	for i := 0; i < len(arr); i += 2 {
		start, ok1 := arr[i].(syntax.Integer)
		count, ok2 := arr[i+1].(syntax.Integer)
		if !ok1 || !ok2 || start < 0 || count < 0 {
			return nil, pdferror.Malformedf("cross-reference stream /Index pair at position %d is not two non-negative integers", i)
		}
		ranges = append(ranges, xrefIndexRange{start: int(start), count: int(count)})
	}
	return ranges, nil
}

// parseXrefStreamRecord interprets one fixed-width record (already
// sliced to exactly widths[0]+widths[1]+widths[2] bytes) as an xrefEntry,
// per the three record types the specification defines:
//
//   - type 0: free entry. field2 (the next free object number) and
//     field3 (its generation) are read but not retained beyond the
//     generation, matching how a classic table's 'f' entries are
//     handled - this project never reuses freed object numbers.
//   - type 1: "in use" entry, exactly like a classic table's 'n' entry:
//     field2 is the byte offset, field3 the generation.
//   - type 2: compressed entry (only possible in a stream-based
//     cross-reference section; classic tables have no way to express
//     this). field2 is the object number of the object stream
//     containing this object, field3 the index within it - see
//     objstream.go.
//
// A missing type field (width 0) defaults to type 1, per the
// specification - this lets a stream omit an entirely regular run of
// "in use" entries' type field to save space, since 1 is by far the
// most common entry type in practice.
func parseXrefStreamRecord(record []byte, widths [3]int) (xrefEntry, error) {
	typ := readBigEndianDefault(record[:widths[0]], 1)
	field2 := readBigEndianDefault(record[widths[0]:widths[0]+widths[1]], 0)
	field3 := readBigEndianDefault(record[widths[0]+widths[1]:], 0)

	switch typ {
	case 0:
		return xrefEntry{Free: true, Generation: int(field3)}, nil
	case 1:
		return xrefEntry{Offset: int64(field2), Generation: int(field3)}, nil
	case 2:
		return xrefEntry{Compressed: true, StreamNum: int(field2), StreamIdx: int(field3)}, nil
	default:
		return xrefEntry{}, pdferror.Malformedf("cross-reference stream record has unknown type %d", typ)
	}
}

// readBigEndianDefault interprets b as a big-endian unsigned integer,
// or returns def if b is empty (meaning this field's /W width was 0 -
// "not stored; use the type-specific default").
func readBigEndianDefault(b []byte, def uint64) uint64 {
	if len(b) == 0 {
		return def
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}
