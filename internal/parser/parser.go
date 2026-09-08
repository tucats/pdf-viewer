// Package parser resolves the object-level structure of a PDF file:
// locating and validating the "%PDF-" header, finding the trailer and
// its cross-reference table (following the "startxref" keyword backward
// from the end of the file, per the PDF specification), walking any
// chain of incremental updates via each trailer's /Prev entry, and
// lazily resolving indirect object references to the internal/syntax
// values they point at.
//
// # What this package supports so far
//
// This initial implementation (Phase 1 of the project's phased plan;
// see the repository README) supports the classic, table-based
// cross-reference format ("xref" followed by subsection headers and
// fixed-width entries) and classic trailers, including files that have
// been incrementally updated one or more times. It deliberately does
// not yet support PDF 1.5+ cross-reference streams or object streams:
// both are normally Flate-compressed, and Flate decoding is scoped to
// Phase 2 ("Content streams and a minimal raster backend") of the
// phased plan, alongside the other stream filters. Opening a file that
// uses a cross-reference stream currently fails with an error wrapping
// pdfviewer.ErrUnsupported rather than silently misreading it; see
// docs/capability-matrix.md for the up-to-date status of this and every
// other capability.
//
// # Recovering from a corrupted cross-reference table
//
// A cross-reference table's whole purpose is to let a reader jump
// straight to any object's bytes without reading the rest of the file -
// but that table is itself just more bytes in the file, and can be
// wrong: truncated transfers, buggy PDF writers, and deliberately
// hostile input can all produce a table whose recorded offsets do not
// actually point at the objects they claim to. When resolving an object
// through its recorded offset fails, this package falls back once
// (Document.recoverByScanning) to a linear scan of the entire file for
// "N G obj" markers, rebuilding a corrected offset table from what it
// actually finds - the same strategy real-world PDF readers use to open
// files that a byte-exact reading of the specification would have to
// reject. See testdata/fixtures/handmade/malformed-bad-xref-offset.pdf
// (documented in testdata/fixtures/FIXTURES.md) for the fixture this
// behavior is tested against.
package parser

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/source"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// xrefEntry is one cross-reference table entry: either "object N,
// generation G, lives at byte offset Offset" (Free == false) or "object
// N is on the free list" (Free == true, meaning the object does not
// currently exist - resolving a reference to it yields the PDF null
// object, per the specification).
type xrefEntry struct {
	Offset     int64
	Generation int
	Free       bool
}

// Document is a parsed PDF file's object-level structure: everything
// needed to resolve any indirect object reference to its value, plus
// the file's trailer dictionary (which is where a caller finds the
// document's root object - see the Trailer field). It does not itself
// know anything about pages, fonts, or content streams; that
// higher-level structure is internal/model's job, built on top of this
// package's Resolve method.
type Document struct {
	src *source.Reader

	// xref maps object number to where that object's definition lives.
	// It is populated once, when the Document is opened, by walking the
	// cross-reference table (and any /Prev-chained earlier revisions);
	// see loadXref.
	xref map[int]xrefEntry

	// Trailer is the document's trailer dictionary: the dictionary
	// following the final (most recent) revision's "trailer" keyword,
	// or - for the cross-reference-stream format this package does not
	// yet support - the stream's own dictionary. Higher layers look up
	// entries such as /Root (the document catalog) and /Info here.
	Trailer syntax.Dictionary

	// cache holds already-resolved objects, keyed by object number, so
	// that resolving the same reference more than once (extremely
	// common - many objects are referenced from several places, e.g. a
	// shared /Resources dictionary) does not re-read and re-parse the
	// file every time.
	cache map[int]syntax.Object

	// resolving detects reference cycles *within a single object's own
	// definition* (for example, an object whose dictionary somehow
	// contains a direct reference back to itself in a position this
	// package itself needs to chase - which does not currently happen
	// anywhere in this package, since Resolve does not recursively
	// follow references it returns, but is kept as a defensive guard
	// for internal/model, which does recursively follow references
	// while walking the page tree and calls Resolve reentrantly while
	// doing so).
	resolving map[int]bool

	// recovered records whether recoverByScanning has already run, so
	// that a file with several genuinely-broken objects does not
	// trigger a full linear file scan once per broken object.
	recovered bool
}

// Open parses src's header, trailer, and cross-reference table (and any
// chain of incremental updates), returning a Document ready to resolve
// object references. It does not read or validate any object's content
// beyond the trailer itself - opening a document is deliberately cheap,
// consistent with the "lazy" rendering model described in the
// repository README's "Pagination and resource ownership" section:
// Document.Resolve does the actual per-object work, only for objects a
// caller actually asks for.
func Open(src *source.Reader) (*Document, error) {
	if err := validateHeader(src); err != nil {
		return nil, err
	}

	startOffset, err := findStartXref(src)
	if err != nil {
		return nil, err
	}

	d := &Document{
		src:       src,
		xref:      make(map[int]xrefEntry),
		cache:     make(map[int]syntax.Object),
		resolving: make(map[int]bool),
	}
	if err := d.loadXref(startOffset); err != nil {
		return nil, err
	}
	if d.Trailer == nil {
		return nil, pdferror.Malformedf("no trailer dictionary found")
	}
	return d, nil
}

// headerScanLimit bounds how far into the file validateHeader will look
// for the "%PDF-" header. The specification places it at the very start
// of the file, but tolerates (and real-world files sometimes contain)
// leading junk bytes before it; scanning a bounded prefix rather than
// the whole file keeps this check cheap and its worst-case cost fixed
// regardless of file size.
const headerScanLimit = 1024

func validateHeader(src *source.Reader) error {
	n := src.Size()
	if n > headerScanLimit {
		n = headerScanLimit
	}
	head, err := src.Bytes(0, n)
	if err != nil {
		return err
	}
	if !bytes.Contains(head, []byte("%PDF-")) {
		return pdferror.Malformedf("missing %%PDF- header in the first %d bytes", headerScanLimit)
	}
	return nil
}

// startxrefScanLimit bounds how far from the end of the file
// findStartXref will look for the "startxref" keyword. The
// specification expects it very near the end (followed only by the
// offset itself and "%%EOF"), so a small, fixed-size tail read is both
// sufficient for conforming files and keeps this lookup's cost bounded
// regardless of file size, per the same "bounded work" policy referenced
// throughout this package.
const startxrefScanLimit = 2048

// findStartXref locates the "startxref" keyword near the end of the
// file and returns the byte offset it specifies - the starting point
// for loadXref.
func findStartXref(src *source.Reader) (int64, error) {
	tail, err := src.Tail(startxrefScanLimit)
	if err != nil {
		return 0, err
	}

	const keyword = "startxref"
	idx := bytes.LastIndex(tail, []byte(keyword))
	if idx < 0 {
		return 0, pdferror.Malformedf("no %q keyword found in the last %d bytes of the file", keyword, startxrefScanLimit)
	}

	lex := syntax.NewLexer(bytes.NewReader(tail[idx+len(keyword):]))
	tok, err := lex.Next()
	if err != nil {
		return 0, err
	}
	if tok.Kind != syntax.KindNumber {
		return 0, pdferror.Malformedf("%q keyword not followed by a byte offset", keyword)
	}
	offset, err := strconv.ParseInt(tok.Text, 10, 64)
	if err != nil || offset < 0 {
		return 0, pdferror.Malformedf("%q offset %q is not a valid non-negative integer", keyword, tok.Text)
	}
	return offset, nil
}

// maxXrefChain bounds how many /Prev-linked revisions loadXref will
// follow. Combined with the cycle check inside loadXref, this keeps a
// file with a corrupted or hostile /Prev chain from causing unbounded
// work - see the repository README's "Dependency and safety policy".
// No legitimate PDF editing workflow this project anticipates supporting
// produces anywhere near this many incremental saves of one file.
const maxXrefChain = 1024

// loadXref walks the chain of cross-reference sections starting at
// startOffset and following each one's trailer /Prev entry, merging
// their object-offset entries into d.xref and recording the first
// (i.e., most recent) trailer dictionary seen as d.Trailer.
//
// Because incremental updates are walked newest-to-oldest, and because
// mergeXrefEntries below only ever fills in object numbers not already
// present, an object number's *first* appearance while walking this
// chain - which is always its most recent revision - is the one that
// wins. This is exactly the semantics an incrementally-updated PDF
// requires: a later revision's redefinition of an object must shadow
// that same object number's definition in an earlier revision.
func (d *Document) loadXref(startOffset int64) error {
	offset := startOffset
	visited := make(map[int64]bool)

	for i := 0; ; i++ {
		if i >= maxXrefChain {
			return pdferror.Malformedf("cross-reference /Prev chain exceeds %d revisions", maxXrefChain)
		}
		if visited[offset] {
			return pdferror.Malformedf("cross-reference /Prev chain contains a cycle at offset %d", offset)
		}
		visited[offset] = true

		trailer, err := d.loadXrefSection(offset)
		if err != nil {
			return fmt.Errorf("reading cross-reference table at offset %d: %w", offset, err)
		}
		if d.Trailer == nil {
			d.Trailer = trailer
		}

		prevValue, hasPrev := trailer["Prev"]
		if !hasPrev {
			return nil
		}
		prevOffset, ok := prevValue.(syntax.Integer)
		if !ok {
			return pdferror.Malformedf("trailer /Prev is not a direct integer")
		}
		offset = int64(prevOffset)
	}
}

// loadXrefSection parses one cross-reference section at offset - either
// a classic "xref" table or (not yet supported; see the package doc
// comment) a cross-reference stream - merging its entries into d.xref
// and returning its trailer dictionary.
func (d *Document) loadXrefSection(offset int64) (syntax.Dictionary, error) {
	r, err := d.src.SectionFrom(offset)
	if err != nil {
		return nil, err
	}
	lex := syntax.NewLexer(r)

	tok, err := lex.Next()
	if err != nil {
		return nil, err
	}

	if tok.Kind == syntax.KindKeyword && tok.Text == "xref" {
		return d.loadClassicXrefTable(lex)
	}

	// Anything else at this offset that isn't the "xref" keyword is
	// expected to be an indirect object definition ("N G obj << ...
	// /Type /XRef ... >> stream ... endstream") - a cross-reference
	// stream. See the package doc comment for why that is not supported
	// yet.
	return nil, pdferror.Unsupportedf("cross-reference streams (PDF 1.5+); this document does not use a classic xref table")
}

// loadClassicXrefTable parses a classic cross-reference table, having
// already consumed the "xref" keyword that introduces it, and returns
// its trailer dictionary. The table is a sequence of one or more
// subsections, each beginning with "<first object number> <count>"
// followed by exactly count fixed-format entries, terminated by the
// "trailer" keyword and the trailer dictionary itself.
func (d *Document) loadClassicXrefTable(lex *syntax.Lexer) (syntax.Dictionary, error) {
	for {
		tok, err := lex.Next()
		if err != nil {
			return nil, err
		}
		if tok.Kind == syntax.KindKeyword && tok.Text == "trailer" {
			break
		}
		if tok.Kind != syntax.KindNumber {
			return nil, pdferror.Malformedf("expected a subsection header or the \"trailer\" keyword, found %q", tok.Text)
		}
		firstObj, ok := parseNonNegativeInt(tok.Text)
		if !ok {
			return nil, pdferror.Malformedf("subsection start object number %q is not a valid non-negative integer", tok.Text)
		}

		countTok, err := lex.Next()
		if err != nil {
			return nil, err
		}
		if countTok.Kind != syntax.KindNumber {
			return nil, pdferror.Malformedf("expected a subsection entry count, found %q", countTok.Text)
		}
		count, ok := parseNonNegativeInt(countTok.Text)
		if !ok {
			return nil, pdferror.Malformedf("subsection entry count %q is not a valid non-negative integer", countTok.Text)
		}

		for i := 0; i < count; i++ {
			entry, err := readXrefTableEntry(lex)
			if err != nil {
				return nil, err
			}
			objNum := firstObj + i
			if _, exists := d.xref[objNum]; !exists {
				d.xref[objNum] = entry
			}
		}
	}

	val, err := syntax.ParseValue(lex, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing trailer dictionary: %w", err)
	}
	dict, ok := val.(syntax.Dictionary)
	if !ok {
		return nil, pdferror.Malformedf("trailer value is not a dictionary")
	}
	return dict, nil
}

// readXrefTableEntry reads one classic cross-reference table entry: an
// offset, a generation number, and a single letter 'n' (in use) or 'f'
// (free). The specification fixes each entry at exactly 20 bytes, but
// this reads the three fields as ordinary Lexer tokens (which already
// skip whitespace, including the entry's own padding and line ending)
// rather than slicing fixed-width bytes - a deliberately more tolerant
// approach that still correctly reads conforming files, but does not
// depend on an entry being padded to exactly the byte width the
// specification calls for.
func readXrefTableEntry(lex *syntax.Lexer) (xrefEntry, error) {
	offsetTok, err := lex.Next()
	if err != nil {
		return xrefEntry{}, err
	}
	genTok, err := lex.Next()
	if err != nil {
		return xrefEntry{}, err
	}
	kindTok, err := lex.Next()
	if err != nil {
		return xrefEntry{}, err
	}

	if offsetTok.Kind != syntax.KindNumber || genTok.Kind != syntax.KindNumber || kindTok.Kind != syntax.KindKeyword {
		return xrefEntry{}, pdferror.Malformedf("malformed cross-reference table entry")
	}
	offset, ok := parseNonNegativeInt(offsetTok.Text)
	if !ok {
		return xrefEntry{}, pdferror.Malformedf("cross-reference entry offset %q is not a valid non-negative integer", offsetTok.Text)
	}
	gen, ok := parseNonNegativeInt(genTok.Text)
	if !ok {
		return xrefEntry{}, pdferror.Malformedf("cross-reference entry generation %q is not a valid non-negative integer", genTok.Text)
	}

	switch kindTok.Text {
	case "n":
		return xrefEntry{Offset: int64(offset), Generation: gen}, nil
	case "f":
		return xrefEntry{Generation: gen, Free: true}, nil
	default:
		return xrefEntry{}, pdferror.Malformedf("cross-reference entry type must be \"n\" or \"f\", found %q", kindTok.Text)
	}
}

func parseNonNegativeInt(text string) (int, bool) {
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil || v < 0 || v > 1<<31 {
		return 0, false
	}
	return int(v), true
}
