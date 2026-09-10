package filter

import (
	"fmt"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements decodeJBIG2, reversing PDF's JBIG2Decode filter,
// and the segment walk every other jbig2*.go file hangs off - see
// docs/PLAN2.md's Phase 8 for the scope decisions behind it.
//
// # How the pieces fit together
//
// A JBIG2Decode stream is a sequence of self-describing "segments" (see
// the embedded-organization section below), and each segment type has
// its own file here:
//
//   - jbig2mq.go is the MQ arithmetic coder every other piece rides on:
//     it turns coded bytes into a stream of yes/no decisions, each
//     against an adaptive probability estimate selected by context.
//   - jbig2arith.go reads whole *numbers* out of that same decision
//     stream (T.88 Annex A), which everything but a generic region needs.
//   - jbig2generic.go decodes a generic region: a bitmap coded pixel by
//     pixel from its own already-decoded neighborhood. This is what a
//     scanner emitting a plain black-and-white page image produces.
//   - jbig2symbol.go and jbig2text.go decode a symbol dictionary and a
//     text region: JBIG2's highest-compression mode, where each distinct
//     glyph shape is coded once and each occurrence becomes an index
//     plus a position. Most real scan-to-PDF and OCR output uses this.
//   - jbig2refine.go decodes a bitmap as a *correction* of one already
//     decoded, which is how lossy encoders fix up an individual symbol
//     instance whose dictionary shape was not quite right.
//
// A feature this package does not implement (see the scope section at
// the end) is reported as pdferror.ErrUnsupported naming that specific
// feature, rather than silently producing a blank or partially-wrong
// page.
//
// # JBIG2's "embedded organization"
//
// JBIG2 was originally designed as a standalone file format (with its
// own file header identifying it, page defaults, and so on), but PDF
// embeds JBIG2 data using a defined variant - ITU-T T.88 Annex D.3, "the
// embedded organisation" - that strips the file header entirely: a
// JBIG2Decode stream's bytes are simply a sequence of "segments" (see
// jbig2segment.go's segmentHeader type) back to back, each
// self-describing its own type and length. This file only ever
// parses that embedded form, never the standalone file format (which
// this project has no PDF-level use for).
//
// # What a "generic region" bitmap is
//
// A generic region is exactly what CCITTFaxDecode (ccitt.go) also
// produces: one bit per pixel, arranged as rows. Where CCITT predicts
// each row from Huffman-coded run lengths (relative to the row itself
// and, for 2D coding, the previous row), JBIG2's generic region instead
// predicts each individual *pixel* from a small neighborhood of already-
// decoded pixels around it (its "context" - up to 12 fixed neighboring
// positions plus up to 4 further "adaptive template", AT, positions the
// encoder may relocate), feeding that context into the MQ arithmetic
// coder (jbig2mq.go) to decode one bit per pixel. This context-modeling
// approach compresses text-heavy scans noticeably better than CCITT's
// row-based runs, which is JBIG2's whole reason for existing.
//
// # Provenance
//
// Unlike ccitt.go (a close port of a specific existing implementation -
// see that file's own doc comment), this file's segment-parsing and
// context-template logic is a from-scratch implementation written
// directly against the ITU-T T.88 specification's own field layouts and
// figures (clauses 7.2 "segment header syntax", 7.4.1 "region segment
// information field", 7.4.6 "generic region segment", and 6.2 "generic
// region decoding procedure"), matching this project's existing
// precedent (this project's stated dependency policy - documented in
// the repository README - of writing an unsupported filter from scratch
// when porting a good existing implementation is not readily
// practical). The MQ arithmetic coder it decodes generic regions with
// (jbig2mq.go) is, however, the identical standard algorithm essentially
// every independent implementation shares - see that file's own doc
// comment for the fuller provenance discussion, including the
// round-trip-against-this-package's-own-encoder testing strategy this
// code was originally written under.
//
// That strategy's one structural weakness - an encoder and a decoder
// written by the same project can agree with each other and both be
// wrong about the standard - is now partly covered: this package is
// validated against one real scanner's output as well, via
// testdata/fixtures/real-world/pdf-with-jbig2.pdf (see that corpus's
// entry in FIXTURES.md for why carrying it is worth the trouble). That
// file exercises symbol mode thoroughly and generic regions not at all,
// so the two kinds of test complement rather than replace each other.
//
// # Scope limitations, deliberately not implemented
//
// Each of these reports pdferror.ErrUnsupported naming the specific
// feature:
//
//   - Huffman-coded symbol dictionaries and text regions (SDHUFF /
//     SBHUFF), along with the custom table segments and MMR-coded
//     collective bitmaps they imply. Real encoders overwhelmingly choose
//     arithmetic coding, which is the whole reason to use JBIG2's symbol
//     mode over CCITTFaxDecode in the first place.
//   - MMR (Modified Modified READ, i.e. plain CCITT Group 4) coding of a
//     generic region, for the same reason.
//   - Halftone regions and pattern dictionaries (T.88 6.6/6.7), which
//     code dithered greyscale imagery rather than text and are rare in
//     the scanned documents this filter exists to render.
//   - A symbol dictionary that imports another segment's adapted
//     arithmetic contexts (the "bitmap coding context used" flag), a
//     saving that only pays off for dictionaries split across many
//     segments.
//   - A segment whose data length is the "unknown" sentinel, which is
//     only discoverable by scanning for a terminating marker; real PDF
//     producers give every segment a known length.
func decodeJBIG2(data, globals []byte) ([]byte, error) {
	d := &jbig2Decoder{symbolDicts: make(map[uint32][]*jbig2Bitmap)}

	// A /JBIG2Globals stream is a sequence of segments in exactly the
	// same form as the image's own, decoded first so that the symbol
	// dictionaries it defines are in the registry by the time the
	// image's text regions refer to them by number. It contains no
	// region segments of its own (there is no page for them to paint
	// onto), so it contributes nothing to the page bitmap.
	if len(globals) > 0 {
		if err := d.processSegments(globals); err != nil {
			return nil, err
		}
	}
	if err := d.processSegments(data); err != nil {
		return nil, err
	}

	if d.page == nil {
		return nil, pdferror.Malformedf("JBIG2Decode: no region segment found")
	}
	return d.page.packInverted(), nil
}

// jbig2Globals reads a JBIG2Decode stream's /JBIG2Globals parameter: an
// indirect reference to a second stream of JBIG2 segments (see
// decodeJBIG2's own handling of the bytes this returns). Returns nil,
// nil when the parameter is absent, which is the common case - a stream
// that carries its own symbol dictionary, or uses none at all, needs no
// globals.
func jbig2Globals(parms syntax.Dictionary, resolver StreamResolver) ([]byte, error) {
	if parms == nil {
		return nil, nil
	}
	v, ok := parms["JBIG2Globals"]
	if !ok {
		return nil, nil
	}
	if _, isNull := v.(syntax.Null); isNull {
		return nil, nil
	}

	if resolver == nil {
		// Reaching another stream needs a cross-reference table this
		// package does not have; saying so is better than decoding an
		// image whose symbols are missing and silently producing a blank
		// or partial page. In practice every JBIG2 image this project
		// renders arrives through internal/parser.Document.DecodeStream,
		// which does supply a resolver.
		return nil, pdferror.Unsupportedf("JBIG2Decode: /JBIG2Globals cannot be read in this context")
	}

	globals, err := resolver.DecodeReferencedStream(v)
	if err != nil {
		return nil, fmt.Errorf("JBIG2Decode: /JBIG2Globals: %w", err)
	}
	return globals, nil
}

// jbig2Decoder holds the state one JBIG2Decode stream's segments build
// up between them: the page bitmap regions are composited onto, and the
// symbols each symbol dictionary segment exported, kept by segment
// number because that is how a later text region asks for them (see
// segmentHeader.referredTo).
type jbig2Decoder struct {
	page        *jbig2Bitmap
	symbolDicts map[uint32][]*jbig2Bitmap
}

// jbig2Bitmap is a simple, one-byte-per-pixel bitmap (1 = JBIG2
// "foreground", conventionally black; 0 = background, conventionally
// white) - deliberately not bit-packed while it is being built up, since
// generic region decoding, and compositing one region onto the page
// canvas, both need cheap random-access reads and writes of individual
// pixels far more often than they need a compact in-memory
// representation. packInverted (called once, right at the end) is what
// produces the tightly bit-packed, MSB-first output this package's
// caller (internal/image, via internal/parser.Document.DecodeStream)
// actually expects.
type jbig2Bitmap struct {
	width, height int
	// pix holds one byte per pixel, row-major (pix[y*width+x]), each
	// either 0 or 1 - not a packed bitmap; see the type doc comment.
	pix []byte
}

func newJBIG2Bitmap(width, height int, fill byte) *jbig2Bitmap {
	b := &jbig2Bitmap{width: width, height: height, pix: make([]byte, width*height)}
	if fill != 0 {
		for i := range b.pix {
			b.pix[i] = 1
		}
	}
	return b
}

func (b *jbig2Bitmap) get(x, y int) byte {
	if x < 0 || x >= b.width || y < 0 || y >= b.height {
		return 0
	}
	return b.pix[y*b.width+x]
}

func (b *jbig2Bitmap) set(x, y int, v byte) {
	b.pix[y*b.width+x] = v
}

// packInverted packs b into PDF's expected 1-bit-per-pixel, MSB-first,
// byte-aligned-per-row sample format (the same format ccitt.go's
// packCCITTRow produces and internal/image's bitReader expects for any
// /BitsPerComponent 1 image), inverting every pixel along the way: JBIG2
// itself defines 1 = black (foreground) and 0 = white (background,
// clause 5.2.2 of T.88), the *opposite* of the convention this project's
// downstream image pipeline assumes for a DeviceGray image with the
// default (and, for JBIG2Decode, only - unlike CCITTFaxDecode's
// /BlackIs1, there is no equivalent JBIG2Decode parameter) /Decode array
// [0 1], where a 0 sample bit means black. Real-world JBIG2 decoders
// (pdf.js, mupdf, ghostscript) all perform this same inversion for
// exactly this reason.
func (b *jbig2Bitmap) packInverted() []byte {
	rowBytes := (b.width + 7) / 8
	out := make([]byte, rowBytes*b.height)
	for y := 0; y < b.height; y++ {
		row := out[y*rowBytes : (y+1)*rowBytes]
		for x := 0; x < b.width; x++ {
			if b.get(x, y) == 0 { // JBIG2 white -> output bit 1 (PDF white).
				row[x/8] |= 0x80 >> uint(x%8)
			}
			// JBIG2 black (1) leaves the output bit 0 (PDF black) - rows
			// start zeroed by make(), so there is nothing to do for that
			// case.
		}
	}
	return out
}

// combineOp is a generic region segment's combination operator (T.88
// Table 12), saying how a decoded region's pixels are merged onto the
// page canvas at the position the region declares - almost always
// combOpReplace or combOpOr in practice for a single full-page region,
// but a JBIG2 stream may in principle paint several regions onto one
// page (e.g. a scanner emitting the page in horizontal strips).
type combineOp int

const (
	combOpOr combineOp = iota
	combOpAnd
	combOpXor
	combOpXnor
	combOpReplace
)

func (b *jbig2Bitmap) composite(region *jbig2Bitmap, x0, y0 int, op combineOp) {
	for y := 0; y < region.height; y++ {
		py := y0 + y
		if py < 0 || py >= b.height {
			continue
		}
		for x := 0; x < region.width; x++ {
			px := x0 + x
			if px < 0 || px >= b.width {
				continue
			}
			s := region.get(x, y)
			d := b.get(px, py)
			var r byte
			switch op {
			case combOpAnd:
				r = s & d
			case combOpXor:
				r = s ^ d
			case combOpXnor:
				r = 1 - (s ^ d)
			case combOpReplace:
				r = s
			default: // combOpOr, and any reserved value per T.88's guidance to treat as OR.
				r = s | d
			}
			b.set(px, py, r)
		}
	}
}

// processSegments walks every segment in data (JBIG2's PDF-embedded
// organization - see this file's doc comment), compositing every region
// segment onto d.page (sized to the largest extent any region actually
// painted) and recording every symbol dictionary segment's exported
// symbols in d.symbolDicts.
//
// Note that the page size comes from the regions themselves rather than
// from a page information segment's own declared page width and height.
// That is deliberate: in PDF's embedded organization the image's real
// dimensions are the ones the image XObject's own /Width and /Height give
// (internal/image is what consumes this output and enforces them), a page
// information segment need not be present at all, and when one is present
// its declared height is explicitly allowed to be the "unknown" sentinel
// 0xFFFFFFFF for a page whose height is only settled by a later
// end-of-stripe segment. Sizing to what was actually painted needs none
// of that, and cannot disagree with the pixels it returns.
func (d *jbig2Decoder) processSegments(data []byte) error {
	pos := 0
	for pos < len(data) {
		hdr, headerLen, err := parseSegmentHeader(data[pos:])
		if err != nil {
			return err
		}
		pos += headerLen
		if hdr.dataLength == unknownSegmentLength {
			return pdferror.Unsupportedf("JBIG2Decode: segment %d has an unknown data length (streaming generic region)", hdr.number)
		}
		if uint64(pos)+hdr.dataLength > uint64(len(data)) {
			return pdferror.Malformedf("JBIG2Decode: segment %d's declared data length runs past the end of the stream", hdr.number)
		}
		segData := data[pos : uint64(pos)+hdr.dataLength]
		pos += int(hdr.dataLength)

		switch hdr.segmentType {
		case segTypeIntermediateGenericRegion, segTypeImmediateGenericRegion, segTypeImmediateLosslessGenericRegion:
			region, x0, y0, op, err := decodeGenericRegionSegment(segData)
			if err != nil {
				return err
			}
			if err := d.compositeOntoPage(hdr, region, x0, y0, op); err != nil {
				return err
			}

		case segTypeSymbolDictionary:
			symbols, err := decodeSymbolDictSegment(segData, d.referredSymbols(hdr))
			if err != nil {
				return err
			}
			d.symbolDicts[hdr.number] = symbols

		case segTypeTextRegionIntermediate, segTypeTextRegionImmediate, segTypeTextRegionImmediateLossless:
			region, x0, y0, op, err := decodeTextRegionSegment(segData, d.referredSymbols(hdr))
			if err != nil {
				return err
			}
			if err := d.compositeOntoPage(hdr, region, x0, y0, op); err != nil {
				return err
			}

		case segTypeRefinementRegionIntermediate, segTypeRefinementRegionImmediate, segTypeRefinementRegionImmediateLossless:
			region, x0, y0, op, err := decodeRefinementRegionSegment(segData, d.page)
			if err != nil {
				return err
			}
			if err := d.compositeOntoPage(hdr, region, x0, y0, op); err != nil {
				return err
			}

		case segTypeHalftoneRegionIntermediate, segTypeHalftoneRegionImmediate, segTypeHalftoneRegionImmediateLossless,
			segTypePatternDictionary:
			return pdferror.Unsupportedf("JBIG2Decode: segment type %d (halftone region or pattern dictionary) is not implemented", hdr.segmentType)

		default:
			// Page info, end-of-page, end-of-stripe, end-of-file, table,
			// extension, and any other segment type this package does
			// not need to interpret carry no information the page bitmap
			// needs; segData has already been skipped over above
			// regardless of what this switch does with it.
		}
	}
	return nil
}

// referredSymbols gathers the symbols available to hdr's segment: every
// symbol exported by each symbol dictionary it refers to, concatenated
// in the order the referred-to list gives them, since that concatenation
// is exactly what the segment's coded symbol IDs index into.
//
// A referred-to segment that is not a symbol dictionary contributes
// nothing and is skipped rather than rejected - a text region may also
// refer to the custom Huffman table segments this package does not read,
// and a symbol dictionary refers to the page it belongs to.
func (d *jbig2Decoder) referredSymbols(hdr segmentHeader) []*jbig2Bitmap {
	var symbols []*jbig2Bitmap
	for _, ref := range hdr.referredTo {
		symbols = append(symbols, d.symbolDicts[ref]...)
	}
	return symbols
}

// compositeOntoPage paints a decoded region onto d.page at the position
// the region declared, growing (or first allocating) the page as needed.
func (d *jbig2Decoder) compositeOntoPage(hdr segmentHeader, region *jbig2Bitmap, x0, y0 int, op combineOp) error {
	// The page has to be big enough to hold this region at the position
	// the region itself declares. Both the region's size and its
	// position were already bounded per-axis by parseRegionInfo, but
	// their *sum* still needs checking before being used as an
	// allocation size: a modest region positioned near the far end of
	// the allowed coordinate range would otherwise ask for a page far
	// larger than any real image.
	needW, needH := x0+region.width, y0+region.height
	if needW > maxGenericRegionDimension || needH > maxGenericRegionDimension || needW*needH > maxGenericRegionPixels {
		return pdferror.Unsupportedf("JBIG2Decode: segment %d's region at (%d, %d) would need a %dx%d page, exceeding this package's limits", hdr.number, x0, y0, needW, needH)
	}

	switch {
	case d.page == nil:
		d.page = newJBIG2Bitmap(needW, needH, 0)
	case needW > d.page.width || needH > d.page.height:
		d.page = growJBIG2Bitmap(d.page, needW, needH)
	}
	d.page.composite(region, x0, y0, op)
	return nil
}

// growJBIG2Bitmap returns a copy of b enlarged to at least w x h
// (never shrinking either dimension), with every new pixel defaulting to
// background (0) - needed only for the rare case of a multi-region
// JBIG2 stream whose regions are not all the same size as the first one
// encountered.
func growJBIG2Bitmap(b *jbig2Bitmap, w, h int) *jbig2Bitmap {
	if w < b.width {
		w = b.width
	}
	if h < b.height {
		h = b.height
	}
	grown := newJBIG2Bitmap(w, h, 0)
	for y := 0; y < b.height; y++ {
		for x := 0; x < b.width; x++ {
			grown.set(x, y, b.get(x, y))
		}
	}
	return grown
}
