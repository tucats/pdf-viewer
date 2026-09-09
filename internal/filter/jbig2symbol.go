package filter

import (
	"encoding/binary"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements JBIG2's symbol dictionary segment (ITU-T T.88
// clause 7.4.3 for the segment's own header fields, 6.5 for the decoding
// procedure), plus the matching encoding direction this package's tests
// and fixtures use (see jbig2mq.go's mqEncoder doc comment for why an
// encoder lives here at all).
//
// # What a symbol dictionary is for
//
// A generic region (jbig2generic.go) codes a scanned page pixel by
// pixel. That already compresses text well, but it spends bits on every
// single occurrence of the letter "e" on the page as though it had never
// seen one before. A symbol dictionary instead codes each distinct shape
// *once* - a small bitmap per glyph - and a text region (jbig2text.go)
// then places instances of those shapes on the page by index and
// position, which is where JBIG2's largest compression wins over CCITT
// Group 4 come from on text-heavy scans.
//
// The dictionary is just an ordered list of small bitmaps. What makes
// its coding worth a whole clause of the standard is the ordering: the
// symbols are grouped into "height classes", runs of symbols that all
// share one height, and only the *differences* between consecutive
// heights and widths are coded (through the arithmetic integer
// procedures in jbig2arith.go). Real glyph sets cluster tightly in size,
// so those differences are small numbers that cost very little.
//
// # Sharing state across the whole dictionary
//
// Every symbol bitmap in a dictionary is decoded from one continuing
// MQ-coded stream, through one continuing generic-region context set
// (newGenericContexts) that keeps adapting from one symbol to the next.
// That sharing is deliberate and is most of why a dictionary compresses
// so well: after a few glyphs, the coder has learned what letter-shaped
// pixel neighborhoods look like in this particular scan, and every later
// glyph is cheaper for it.
//
// # Scope
//
// Only the arithmetic-coded form is implemented (SDHUFF = 0). A
// Huffman-coded symbol dictionary is reported as unsupported rather than
// mis-decoded; see jbig2.go's doc comment for this package's overall
// JBIG2 scope.

// maxSymbolsPerDictionary bounds how many symbols one dictionary
// segment may define or export. A dictionary holds one bitmap per
// distinct glyph shape on the page (or, for a globals stream, across a
// document): real ones run from a few dozen to a few thousand, so this
// is far above anything legitimate while keeping a hostile stream from
// asking this package to track millions of bitmaps.
const maxSymbolsPerDictionary = 100_000

// maxSymbolPixelsPerDictionary bounds the total area of every symbol in
// one dictionary, for the same reason a single region's size is bounded
// (see maxGenericRegionPixels): each symbol is held as one byte per
// pixel while it is being decoded, and a stream that declares a few
// thousand enormous symbols would otherwise be asking for unbounded
// memory. Sized to match the single-region limit, since a dictionary's
// symbols are ultimately drawn onto a page no larger than that.
const maxSymbolPixelsPerDictionary = maxGenericRegionPixels

// symbolDictParams is a symbol dictionary segment's parsed header (T.88
// 7.4.3), separated from the coded data that follows it.
type symbolDictParams struct {
	template  int
	at        []jbig2Point
	numExSyms int
	numNewSym int
	// dataStart is where the arithmetic-coded symbol data begins within
	// the segment's data bytes.
	dataStart int
}

// parseSymbolDictHeader parses the fixed fields at the start of a symbol
// dictionary segment's data (T.88 7.4.3.1).
func parseSymbolDictHeader(segData []byte) (symbolDictParams, error) {
	var p symbolDictParams

	if len(segData) < 2 {
		return p, pdferror.Malformedf("JBIG2Decode: truncated symbol dictionary flags")
	}
	flags := binary.BigEndian.Uint16(segData[0:2])
	pos := 2

	huff := flags&0x0001 != 0
	refAgg := flags&0x0002 != 0
	contextUsed := flags&0x0100 != 0
	p.template = int(flags>>10) & 0x03
	refinementTemplate := int(flags>>12) & 0x01

	if huff {
		return p, pdferror.Unsupportedf("JBIG2Decode: Huffman-coded symbol dictionaries are not implemented, only arithmetic coding")
	}
	if contextUsed {
		// "Bitmap coding context used" means this dictionary starts from
		// the adapted arithmetic contexts a previously decoded dictionary
		// ended with, rather than from fresh ones. Carrying that state
		// between segments is rare in practice (it only pays off for a
		// document whose dictionaries are split across many segments), and
		// getting it silently wrong would corrupt every symbol here.
		return p, pdferror.Unsupportedf("JBIG2Decode: symbol dictionaries that import another segment's arithmetic coding contexts are not implemented")
	}

	var err error
	p.at, pos, err = parseATPixels(segData, pos, atSlotCount(p.template))
	if err != nil {
		return p, err
	}
	if refAgg {
		if refinementTemplate == 0 {
			// The refinement template's own two AT pixels, which this
			// package skips past rather than uses - see the refAgg check
			// below.
			if _, pos, err = parseATPixels(segData, pos, 2); err != nil {
				return p, err
			}
		}
		return p, pdferror.Unsupportedf("JBIG2Decode: refinement/aggregate symbol coding (SDREFAGG) is not implemented")
	}

	if len(segData) < pos+8 {
		return p, pdferror.Malformedf("JBIG2Decode: truncated symbol dictionary symbol counts")
	}
	numEx := binary.BigEndian.Uint32(segData[pos : pos+4])
	numNew := binary.BigEndian.Uint32(segData[pos+4 : pos+8])
	pos += 8

	if numEx > maxSymbolsPerDictionary || numNew > maxSymbolsPerDictionary {
		return p, pdferror.Unsupportedf("JBIG2Decode: symbol dictionary declares %d new and %d exported symbols, beyond this package's limit of %d", numNew, numEx, maxSymbolsPerDictionary)
	}
	p.numExSyms = int(numEx)
	p.numNewSym = int(numNew)
	p.dataStart = pos
	return p, nil
}

// decodeSymbolDictSegment decodes one symbol dictionary segment,
// returning the symbols it exports: some mixture of the symbols it
// imported from the dictionaries it refers to (inputSymbols, already
// concatenated in referred-to order by the caller) and the new ones it
// defines itself. A dictionary re-exporting its inputs is how a chain of
// dictionaries accumulates - each one passes on what it was given plus
// what it added.
func decodeSymbolDictSegment(segData []byte, inputSymbols []*jbig2Bitmap) ([]*jbig2Bitmap, error) {
	p, err := parseSymbolDictHeader(segData)
	if err != nil {
		return nil, err
	}
	if len(inputSymbols)+p.numNewSym > maxSymbolsPerDictionary {
		return nil, pdferror.Unsupportedf("JBIG2Decode: symbol dictionary would hold %d symbols, beyond this package's limit of %d", len(inputSymbols)+p.numNewSym, maxSymbolsPerDictionary)
	}

	dec := newMQDecoder(segData[p.dataStart:])
	// One context set, shared by every symbol bitmap this dictionary
	// decodes - see this file's doc comment.
	gbContexts := newGenericContexts(p.template)
	iadh, iadw, iaex := newArithIntCtx(), newArithIntCtx(), newArithIntCtx()

	newSymbols := make([]*jbig2Bitmap, 0, p.numNewSym)
	totalPixels := 0

	// 6.5.5: symbols are decoded in height classes. Each pass of the
	// outer loop starts a new class by adding a signed delta to the
	// running height; the inner loop then reads one symbol per width
	// delta until an OOB marks the end of the class.
	height := 0
	for len(newSymbols) < p.numNewSym {
		dh, ok, bad := dec.decodeInt(iadh)
		if !ok || bad {
			return nil, pdferror.Malformedf("JBIG2Decode: symbol dictionary height class delta is missing or out of range")
		}
		height += dh
		if height <= 0 || height > maxGenericRegionDimension {
			return nil, pdferror.Malformedf("JBIG2Decode: symbol dictionary height class height %d is out of range", height)
		}

		width := 0
		for {
			dw, ok, bad := dec.decodeInt(iadw)
			if bad {
				return nil, pdferror.Malformedf("JBIG2Decode: symbol dictionary width delta is out of range")
			}
			if !ok {
				break // OOB: this height class has no more symbols.
			}
			width += dw
			if width <= 0 || width > maxGenericRegionDimension {
				return nil, pdferror.Malformedf("JBIG2Decode: symbol width %d is out of range", width)
			}
			if len(newSymbols) >= p.numNewSym {
				return nil, pdferror.Malformedf("JBIG2Decode: symbol dictionary defines more symbols than the %d it declared", p.numNewSym)
			}
			totalPixels += width * height
			if totalPixels > maxSymbolPixelsPerDictionary {
				return nil, pdferror.Unsupportedf("JBIG2Decode: symbol dictionary's symbols exceed this package's %d-pixel limit", maxSymbolPixelsPerDictionary)
			}

			// Symbol bitmaps never use typical prediction: TPGDON codes a
			// row identical to the one above it as a single bit, which pays
			// off over a whole page but not over a bitmap a dozen rows tall.
			newSymbols = append(newSymbols, decodeGenericBitmap(dec, gbContexts, width, height, p.template, p.at, false))
		}
	}

	return exportSymbols(dec, iaex, inputSymbols, newSymbols, p.numExSyms)
}

// exportSymbols runs T.88's export flag procedure (6.5.10). A dictionary
// does not necessarily export everything it holds: the input symbols it
// imported and the new ones it just decoded form one combined list, and
// the stream then codes alternating run lengths over that list - so many
// not exported, so many exported, so many not, and so on, starting with
// "not exported". That lets a dictionary decode a shape it only needed
// as an intermediate step (a base glyph later refined into several
// variants) without passing it on.
func exportSymbols(dec *mqDecoder, iaex arithIntCtx, inputSymbols, newSymbols []*jbig2Bitmap, numExSyms int) ([]*jbig2Bitmap, error) {
	all := make([]*jbig2Bitmap, 0, len(inputSymbols)+len(newSymbols))
	all = append(all, inputSymbols...)
	all = append(all, newSymbols...)

	exported := make([]*jbig2Bitmap, 0, numExSyms)
	index := 0
	currentlyExported := false
	// A zero-length run is legal - a dictionary exporting everything
	// starts with an empty "not exported" run - so runs cannot be
	// required to make progress individually. Bounding their *number*
	// instead still terminates on a hostile stream of nothing but empty
	// runs, while leaving every stream a real encoder would produce
	// (which needs at most one run per symbol, plus the leading empty
	// one) comfortably inside the limit.
	maxRuns := 2*len(all) + 4
	for runs := 0; index < len(all); runs++ {
		if runs >= maxRuns {
			return nil, pdferror.Malformedf("JBIG2Decode: symbol dictionary export flags do not account for all %d symbols", len(all))
		}
		runLength, ok, bad := dec.decodeInt(iaex)
		if !ok || bad || runLength < 0 || index+runLength > len(all) {
			return nil, pdferror.Malformedf("JBIG2Decode: symbol dictionary export run length is missing or out of range")
		}
		if currentlyExported {
			exported = append(exported, all[index:index+runLength]...)
		}
		index += runLength
		currentlyExported = !currentlyExported
	}

	// The declared export count is a cross-check on the runs, not a
	// second source of truth: disagreeing means one of the two was
	// misread, so this is malformed rather than something to paper over.
	if len(exported) != numExSyms {
		return nil, pdferror.Malformedf("JBIG2Decode: symbol dictionary's export flags select %d symbols but it declared %d", len(exported), numExSyms)
	}
	return exported, nil
}

// symbolCodeLength returns how many bits a text region spends on each
// symbol ID when choosing between numSymbols symbols (T.88 6.4.5's
// SBSYMCODELEN, and the same quantity a refinement/aggregate symbol
// dictionary needs).
//
// The value is ceil(log2(numSymbols)), with one wrinkle: a
// single-symbol dictionary needs no bits at all by that formula, but
// T.88's published erratum (and every real-world decoder, pdf.js and
// jbig2dec included) uses one bit there instead. Following the erratum
// is what matters for reading real files; this package's own encoder
// uses the same helper, so the two directions cannot disagree.
func symbolCodeLength(numSymbols int) int {
	length := 1
	for numSymbols > 1<<uint(length) {
		length++
	}
	return length
}

// encodeSymbolDictSegment builds a complete symbol dictionary segment
// (header plus arithmetic-coded data) defining symbols, for this
// package's tests and fixtures. It imports nothing and exports every
// symbol it defines, which is the shape a self-contained fixture wants.
func encodeSymbolDictSegment(number uint32, symbols []*jbig2Bitmap, template int) []byte {
	return encodeSymbolDictImporting(number, nil, 0, symbols, template)
}

// encodeSymbolDictImporting is encodeSymbolDictSegment's general form,
// additionally importing numInputSymbols symbols from the dictionaries
// named in referredTo and re-exporting them ahead of its own - the
// chaining a document's dictionaries use to accumulate across segments.
// The caller is responsible for referredTo naming dictionaries that
// really do export that many symbols between them.
//
// newSymbols must be ordered by non-decreasing height, since T.88's
// coding groups symbols into runs that share one height and codes only
// the change from one run's height to the next; a caller that hands over
// an unsorted list is asking for a stream no decoder could read back, so
// this panics rather than producing one.
func encodeSymbolDictImporting(number uint32, referredTo []uint32, numInputSymbols int, symbols []*jbig2Bitmap, template int) []byte {
	for i := 1; i < len(symbols); i++ {
		if symbols[i].height < symbols[i-1].height {
			panic("filter: encodeSymbolDictSegment: symbols must be ordered by non-decreasing height")
		}
	}

	at := defaultATPixels[template]
	enc := newMQEncoder()
	gbContexts := newGenericContexts(template)
	iadh, iadw, iaex := newArithIntCtx(), newArithIntCtx(), newArithIntCtx()

	height := 0
	for i := 0; i < len(symbols); {
		// One height class: this symbol and every following one of the
		// same height.
		enc.encodeInt(iadh, symbols[i].height-height)
		height = symbols[i].height

		width := 0
		for ; i < len(symbols) && symbols[i].height == height; i++ {
			s := symbols[i]
			enc.encodeInt(iadw, s.width-width)
			width = s.width
			encodeGenericBitmap(enc, gbContexts, s.width, s.height, template, at, false, s.get)
		}
		enc.encodeOOB(iadw)
	}

	// Export every symbol, imported ones included: an empty "not
	// exported" run, then one run covering the whole combined list.
	numExported := numInputSymbols + len(symbols)
	enc.encodeInt(iaex, 0)
	enc.encodeInt(iaex, numExported)

	coded := enc.flush()

	data := make([]byte, 0, 2+2*len(at)+8+len(coded))
	data = append(data, byte(template<<2), 0x00) // Flags: SDHUFF and SDREFAGG clear.
	for _, p := range at {
		data = append(data, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	data = appendBE32(data, uint32(numExported))  // SDNUMEXSYMS.
	data = appendBE32(data, uint32(len(symbols))) // SDNUMNEWSYMS.
	data = append(data, coded...)

	header := buildSegmentHeader(number, segTypeSymbolDictionary, referredTo, uint32(len(data)))
	return append(header, data...)
}
