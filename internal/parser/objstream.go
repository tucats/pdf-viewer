package parser

import (
	"bytes"
	"fmt"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements resolving a compressed cross-reference entry
// (xrefEntry.Compressed - see parser.go and xrefstream.go) into the
// syntax.Object it describes: an object stored inside a PDF 1.5+ object
// stream ("ObjStm") rather than at its own byte offset in the file.
// Grouping many small objects into one shared, filterable stream is what
// actually motivates object streams: many short dictionaries (font
// descriptors, individual page objects, and so on) compress far better
// together than each independently, and none of them need their own
// "N G obj ... endobj" wrapper any more - an object stream's header
// records where each packed object's value begins, and the values
// themselves are written back to back with nothing separating them but
// whitespace.

// objStreamContents holds one already-decoded, already-header-parsed
// object stream, cached by Document.objStreams so that resolving several
// objects packed into the same stream - the common case, since producers
// deliberately group many objects together for better compression -
// decodes and parses that stream's header only once.
type objStreamContents struct {
	// objNums[i] is the object number of the i'th object packed into
	// this stream; offsets[i] is the byte offset (into data, i.e.
	// already relative to the stream's /First) at which that object's
	// value begins. Both slices have the same length (the stream's /N).
	objNums []int
	offsets []int

	// data is the decoded stream's bytes from /First onward - i.e. with
	// the header (the objNums/offsets pairs) already stripped off.
	data []byte
}

// resolveCompressed resolves a compressed xrefEntry (StreamNum,
// StreamIdx - see xrefEntry's doc comment) to its syntax.Object value,
// loading and caching the named object stream first if this Document
// has not already done so for a previous resolve.
func (d *Document) resolveCompressed(entry xrefEntry) (syntax.Object, error) {
	contents, err := d.loadObjectStream(entry.StreamNum)
	if err != nil {
		return nil, fmt.Errorf("object stream %d: %w", entry.StreamNum, err)
	}
	if entry.StreamIdx < 0 || entry.StreamIdx >= len(contents.offsets) {
		return nil, pdferror.Malformedf("object stream %d has no entry at index %d", entry.StreamNum, entry.StreamIdx)
	}

	start := contents.offsets[entry.StreamIdx]
	end := len(contents.data)
	if entry.StreamIdx+1 < len(contents.offsets) {
		end = contents.offsets[entry.StreamIdx+1]
	}
	if start < 0 || start > end || end > len(contents.data) {
		return nil, pdferror.Malformedf("object stream %d has an invalid byte range at index %d", entry.StreamNum, entry.StreamIdx)
	}

	lex := syntax.NewLexer(bytes.NewReader(contents.data[start:end]))
	val, err := syntax.ParseValue(lex, 0)
	if err != nil {
		return nil, fmt.Errorf("object stream %d, index %d: %w", entry.StreamNum, entry.StreamIdx, err)
	}
	return val, nil
}

// maxObjStmObjects bounds how many header entries loadObjectStream will
// allocate space for based on a stream's /N count, so that a hostile
// /N claiming, say, a billion objects cannot force a huge allocation
// before the header has even been read and validated against the
// stream's actual (already size-bounded, via internal/filter's
// maxDecodedSize) decoded length; see the repository README's
// "Dependency and safety policy". No legitimate PDF producer packs
// anywhere near this many objects into a single object stream.
const maxObjStmObjects = 1_000_000

// loadObjectStream resolves object number num - which must itself be an
// ordinary, non-compressed object; object streams cannot contain other
// object streams, so no special cycle guard beyond Resolve's ordinary
// one is needed here - decodes its stream bytes, and parses its header:
// /N pairs of "object-number offset" tokens, after which the packed
// objects' own values begin at byte /First within the decoded data.
func (d *Document) loadObjectStream(num int) (*objStreamContents, error) {
	if cached, ok := d.objStreams[num]; ok {
		return cached, nil
	}

	obj, err := d.Resolve(num)
	if err != nil {
		return nil, err
	}
	stream, ok := obj.(syntax.Stream)
	if !ok {
		return nil, pdferror.Malformedf("object %d is not a stream (found %T)", num, obj)
	}
	if t, ok := stream.Dict["Type"].(syntax.Name); ok && t != "ObjStm" {
		return nil, pdferror.Malformedf("object %d has /Type %q, expected /ObjStm", num, t)
	}

	decoded, err := d.DecodeStream(stream)
	if err != nil {
		return nil, err
	}

	n, ok := stream.Dict["N"].(syntax.Integer)
	if !ok || n < 0 || n > maxObjStmObjects {
		return nil, pdferror.Malformedf("object %d: /N must be an integer in [0, %d]", num, maxObjStmObjects)
	}
	first, ok := stream.Dict["First"].(syntax.Integer)
	if !ok || first < 0 || int(first) > len(decoded) {
		return nil, pdferror.Malformedf("object %d: /First must be a non-negative integer no greater than the decoded stream length", num)
	}

	header := syntax.NewLexer(bytes.NewReader(decoded[:first]))
	objNums := make([]int, 0, n)
	offsets := make([]int, 0, n)
	for i := 0; i < int(n); i++ {
		numTok, err := header.Next()
		if err != nil {
			return nil, err
		}
		offTok, err := header.Next()
		if err != nil {
			return nil, err
		}
		objNum, ok1 := parseNonNegativeInt(numTok.Text)
		off, ok2 := parseNonNegativeInt(offTok.Text)
		if numTok.Kind != syntax.KindNumber || offTok.Kind != syntax.KindNumber || !ok1 || !ok2 {
			return nil, pdferror.Malformedf("object %d: malformed object stream header entry %d", num, i)
		}
		objNums = append(objNums, objNum)
		offsets = append(offsets, off)
	}

	contents := &objStreamContents{
		objNums: objNums,
		offsets: offsets,
		data:    decoded[first:],
	}
	d.objStreams[num] = contents
	return contents, nil
}
