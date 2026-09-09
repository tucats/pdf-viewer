package filter

import (
	"encoding/binary"
	"sort"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements JBIG2's "generic region" coding procedure (ITU-T
// T.88 clause 6.2 for decoding, clause 6.2.6/Annex E for the arithmetic
// coding it rides on top of - see jbig2mq.go): both the decoding side
// (decodeGenericRegionSegment, used by jbig2.go against real PDF input)
// and, for this package's own tests and tools/genfixtures to build
// known-correct input with (see jbig2mq.go's mqEncoder doc comment for
// why this project's own encoder exists at all), the matching encoding
// side (EncodeJBIG2GenericRegion).

// jbig2Point is one neighboring-pixel offset (relative to the pixel
// currently being decoded or encoded) contributing one bit to that
// pixel's arithmetic-coding context.
type jbig2Point struct{ dx, dy int }

// codingTemplates lists each GBTEMPLATE's fixed (non-adaptive) context
// pixel offsets - ITU-T T.88 Figures 7-10, "context used for coding the
// generic region" - in the order the specification's own figures show
// them (not yet sorted into final bit order - see
// genericContextTemplate).
var codingTemplates = [4][]jbig2Point{
	0: {
		{-1, -2}, {0, -2}, {1, -2},
		{-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1},
		{-4, 0}, {-3, 0}, {-2, 0}, {-1, 0},
	},
	1: {
		{-1, -2}, {0, -2}, {1, -2}, {2, -2},
		{-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1},
		{-3, 0}, {-2, 0}, {-1, 0},
	},
	2: {
		{-1, -2}, {0, -2}, {1, -2},
		{-2, -1}, {-1, -1}, {0, -1}, {1, -1},
		{-2, 0}, {-1, 0},
	},
	3: {
		{-3, -1}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1},
		{-4, 0}, {-3, 0}, {-2, 0}, {-1, 0},
	},
}

// defaultATPixels gives each GBTEMPLATE's default adaptive-template (AT)
// pixel offset(s) - T.88's own recommended defaults, and, per this
// package's documented scope limitation (see jbig2.go's doc comment),
// the *only* AT positions this decoder accepts; a real encoder is
// allowed to move these to help compression on unusual content, but
// doing so is rare in practice.
var defaultATPixels = [4][]jbig2Point{
	0: {{3, -1}, {-3, -1}, {2, -2}, {-2, -2}},
	1: {{3, -1}},
	2: {{2, -1}},
	3: {{2, -1}},
}

// reusedContext is the fixed "pseudo-pixel" context value (T.88 6.2.5.7)
// each GBTEMPLATE uses to decode its per-row "SLTP" (typical prediction)
// bit when TPGDON is set. This is an arbitrary but standard-fixed
// context index that does not correspond to any real pixel neighborhood
// - it exists purely so encoder and decoder have an agreed-upon extra
// "channel" through the same context-indexed arithmetic coder to signal
// "this row is identical to the previous one" cheaply.
var reusedContext = [4]int{0x9B25, 0x0795, 0x00E5, 0x0195}

// genericContextTemplate returns GBTEMPLATE t's combined (fixed + AT)
// context points, sorted into raster order (top row to bottom, and left
// to right within a row). This sort is not itself part of the
// specification - T.88 assigns each context point a fixed bit position
// directly from its own figures - but produces the identical bit
// assignment those figures show whenever the AT pixels are at their
// default positions (the only case this package supports, see
// defaultATPixels' doc comment): the default AT positions were chosen
// specifically so that they already fall into raster order among the
// fixed points, which is what makes this sort-based construction and
// the specification's own fixed diagram produce the same context number
// for the same neighborhood.
func genericContextTemplate(t int) []jbig2Point {
	fixed := codingTemplates[t]
	at := defaultATPixels[t]
	combined := make([]jbig2Point, 0, len(fixed)+len(at))
	combined = append(combined, fixed...)
	combined = append(combined, at...)
	sort.Slice(combined, func(i, j int) bool {
		if combined[i].dy != combined[j].dy {
			return combined[i].dy < combined[j].dy
		}
		return combined[i].dx < combined[j].dx
	})
	return combined
}

// atPixelsAreDefault reports whether at (as read from a real generic
// region segment's own AT pixel fields) exactly matches GBTEMPLATE
// template's default positions - see defaultATPixels' doc comment for
// why a non-default arrangement is rejected as unsupported rather than
// decoded (potentially incorrectly).
func atPixelsAreDefault(template int, at []jbig2Point) bool {
	def := defaultATPixels[template]
	if len(at) != len(def) {
		return false
	}
	for i := range at {
		if at[i] != def[i] {
			return false
		}
	}
	return true
}

// maxGenericRegionPixels bounds a single generic region's width*height -
// this package's jbig2Bitmap deliberately trades memory for simplicity
// by using one whole byte per pixel while a region is being decoded
// (see that type's doc comment), so this limit exists for the same
// "bounded work against a hostile or merely oversized input" reason
// ccitt.go's own maxCCITTDimension does, sized to match
// internal/image's own maxImagePixels since a JBIG2 region is not
// expected to legitimately need more pixels than any other image this
// project renders.
const maxGenericRegionPixels = 64_000_000

// maxGenericRegionDimension bounds /Width and /Height individually,
// checked before they are multiplied together (a multiplication that
// could otherwise overflow, or at least produce a misleadingly small
// product, if only the product itself were bounded - the same
// "check each axis before checking their product" pattern ccitt.go's
// decodeCCITT uses for /Columns and /Rows).
const maxGenericRegionDimension = 1 << 20

// parseRegionInfo parses a region segment's "region segment information
// field" (T.88 7.4.1), the fixed 17-byte header every region-shaped
// segment type (not just generic regions) begins with: the region's own
// bitmap dimensions and its position and combination operator for
// painting onto the page.
func parseRegionInfo(data []byte) (width, height, x, y int, op combineOp, err error) {
	const regionInfoLen = 17
	if len(data) < regionInfoLen {
		return 0, 0, 0, 0, 0, pdferror.Malformedf("JBIG2Decode: truncated region segment information field")
	}
	width = int(binary.BigEndian.Uint32(data[0:4]))
	height = int(binary.BigEndian.Uint32(data[4:8]))
	x = int(binary.BigEndian.Uint32(data[8:12]))
	y = int(binary.BigEndian.Uint32(data[12:16]))
	op = combineOp(data[16] & 0x07)

	if width <= 0 || width > maxGenericRegionDimension || height <= 0 || height > maxGenericRegionDimension {
		return 0, 0, 0, 0, 0, pdferror.Malformedf("JBIG2Decode: region dimensions %dx%d out of range", width, height)
	}
	// The position is bounded on the same axes and by the same limit as
	// the size, since jbig2.go composites the region onto a page bitmap
	// that has to be large enough to contain it *at* this position - so an
	// absurd position costs page memory exactly as an absurd size would.
	// (The values are read as unsigned 32-bit, so on a platform where int
	// is 32 bits wide anything above 2^31 arrives here already negative;
	// the lower bound catches that rather than trusting the conversion.)
	if x < 0 || x > maxGenericRegionDimension || y < 0 || y > maxGenericRegionDimension {
		return 0, 0, 0, 0, 0, pdferror.Malformedf("JBIG2Decode: region position (%d, %d) out of range", x, y)
	}
	if width*height > maxGenericRegionPixels {
		return 0, 0, 0, 0, 0, pdferror.Unsupportedf("JBIG2Decode: region is %dx%d pixels, exceeding this package's %d-pixel limit", width, height, maxGenericRegionPixels)
	}
	return width, height, x, y, op, nil
}

// decodeGenericRegionSegment decodes one generic region segment's data
// (the bytes following its segment header - T.88 7.4.6, "generic region
// segment data header" followed by the arithmetic-coded bitmap itself),
// returning the decoded bitmap and where/how jbig2.go's caller should
// composite it onto the page.
func decodeGenericRegionSegment(segData []byte) (bitmap *jbig2Bitmap, x, y int, op combineOp, err error) {
	width, height, x, y, op, err := parseRegionInfo(segData)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	pos := 17

	if len(segData) < pos+1 {
		return nil, 0, 0, 0, pdferror.Malformedf("JBIG2Decode: truncated generic region segment flags")
	}
	flags := segData[pos]
	pos++
	mmr := flags&0x01 != 0
	template := int((flags >> 1) & 0x03)
	tpgdon := flags&0x08 != 0

	if mmr {
		return nil, 0, 0, 0, pdferror.Unsupportedf("JBIG2Decode: MMR-coded generic regions are not implemented, only arithmetic coding")
	}

	atCount := 1
	if template == 0 {
		atCount = 4
	}
	if len(segData) < pos+2*atCount {
		return nil, 0, 0, 0, pdferror.Malformedf("JBIG2Decode: truncated generic region AT pixels")
	}
	at := make([]jbig2Point, atCount)
	for i := 0; i < atCount; i++ {
		// AT coordinates are signed bytes (T.88 7.4.6.3).
		at[i] = jbig2Point{dx: int(int8(segData[pos])), dy: int(int8(segData[pos+1]))}
		pos += 2
	}
	if !atPixelsAreDefault(template, at) {
		return nil, 0, 0, 0, pdferror.Unsupportedf("JBIG2Decode: non-default adaptive-template (AT) pixel positions are not implemented")
	}

	bitmap = decodeGenericBitmap(segData[pos:], width, height, template, tpgdon)
	return bitmap, x, y, op, nil
}

// decodeGenericBitmap runs T.88's generic region decoding procedure
// (6.2) over coded, producing a width x height jbig2Bitmap. Every pixel
// is decoded in raster order (top-to-bottom, left-to-right), building
// each one's arithmetic-coding context from its already-decoded
// neighbors per genericContextTemplate(template) - a pixel outside the
// bitmap (the first couple of rows' "row -1"/"row -2" neighbors, or a
// few pixels' worth of "column -1..-4" on the left edge) reads as 0
// (background), exactly matching jbig2Bitmap.get's own out-of-bounds
// behavior.
//
// When tpgdon is set, each row first decodes one extra "typical
// prediction" bit (against the fixed reusedContext pseudo-pixel context,
// not a real neighborhood) that, cumulatively XORed together row by
// row, says whether this row turned out to be pixel-for-pixel identical
// to the row above it - extremely common in real scanned text, where
// many consecutive rows of a text line's or a wide margin's white space
// really are identical - letting the encoder skip coding that row's
// pixels individually at all.
func decodeGenericBitmap(coded []byte, width, height, template int, tpgdon bool) *jbig2Bitmap {
	tmpl := genericContextTemplate(template)
	contexts := make([]mqContext, 1<<uint(len(tmpl)))
	dec := newMQDecoder(coded)
	bitmap := newJBIG2Bitmap(width, height, 0)

	ltp := 0
	for y := 0; y < height; y++ {
		if tpgdon {
			sltp := dec.decodeBit(&contexts[reusedContext[template]])
			ltp ^= sltp
			if ltp == 1 {
				if y > 0 {
					copy(bitmap.pix[y*width:(y+1)*width], bitmap.pix[(y-1)*width:y*width])
				}
				continue
			}
		}
		for x := 0; x < width; x++ {
			ctx := 0
			for _, p := range tmpl {
				ctx = ctx<<1 | int(bitmap.get(x+p.dx, y+p.dy))
			}
			bitmap.set(x, y, byte(dec.decodeBit(&contexts[ctx])))
		}
	}
	return bitmap
}

// EncodeJBIG2GenericRegion builds a minimal, single-segment JBIG2 stream
// (PDF's embedded organization - see jbig2.go's doc comment) encoding
// pix (one byte per pixel, row-major, width*height long, each 0 for
// background/white or 1 for foreground/black - the same convention
// jbig2Bitmap uses internally) as a single immediate generic region
// covering the whole page, using GBTEMPLATE 0 at its default AT pixel
// positions - the simplest combination this package's own decoder above
// supports, favoring an easy-to-verify implementation over compression
// efficiency (a real encoder would choose whichever GBTEMPLATE and
// typical-prediction setting compressed a given image best).
//
// tpgdon selects whether to use typical prediction, coding each row that
// is identical to the one above it as a single bit rather than pixel by
// pixel (see decodeGenericBitmap's doc comment). Both settings decode to
// the same bitmap, so it exists to exercise the decoder's two paths and
// to let a fixture resemble the typical-prediction-using output real
// scanners produce.
//
// This function exists only to build known-correct input for this
// package's own round-trip tests and tools/genfixtures - see jbig2mq.go's
// mqEncoder doc comment for the fuller rationale.
func EncodeJBIG2GenericRegion(width, height int, pix []byte, tpgdon bool) []byte {
	return encodeGenericRegionSegment(width, height, pix, tpgdon, 0, 0, combOpOr)
}

// encodeGenericRegionSegment is EncodeJBIG2GenericRegion's general form,
// additionally placing the region at (originX, originY) on the page and
// compositing it with op - which a single full-page region never needs
// (it sits at the origin), but which lets this package's tests build the
// multi-region streams a scanner emitting a page in horizontal strips
// would produce.
func encodeGenericRegionSegment(width, height int, pix []byte, tpgdon bool, originX, originY int, op combineOp) []byte {
	if len(pix) != width*height {
		panic("filter: encodeGenericRegionSegment: len(pix) does not match width*height")
	}
	const template = 0
	tmpl := genericContextTemplate(template)
	contexts := make([]mqContext, 1<<uint(len(tmpl)))
	enc := newMQEncoder()

	// get mirrors jbig2Bitmap.get's out-of-bounds behavior exactly (a
	// neighbor above the first row or left of the first column reads as
	// background), which is what keeps the contexts computed here
	// identical to the ones decodeGenericBitmap will compute. Every
	// context here comes purely from pix's own known pixel values - for
	// an encoder, a pixel's "already-decoded neighbors" are simply the
	// same input pixels - never from anything the arithmetic coder
	// produces.
	get := func(x, y int) byte {
		if x < 0 || x >= width || y < 0 || y >= height {
			return 0
		}
		return pix[y*width+x]
	}

	rowsIdentical := func(y0, y1 int) bool {
		for x := 0; x < width; x++ {
			if pix[y0*width+x] != pix[y1*width+x] {
				return false
			}
		}
		return true
	}

	// Decisions are encoded in exactly the raster order (and, with
	// tpgdon, with exactly the same per-row extra SLTP decision)
	// decodeGenericBitmap replays them in.
	ltp := 0
	for y := 0; y < height; y++ {
		if tpgdon {
			// This row can be skipped entirely when it is identical to the
			// row above. The coded bit is not that fact directly but the
			// *change* in it since the previous row (SLTP), because that is
			// what the decoder XORs into its own running ltp state.
			skip := 0
			if y > 0 && rowsIdentical(y, y-1) {
				skip = 1
			}
			enc.encodeBit(&contexts[reusedContext[template]], skip^ltp)
			ltp = skip
			if ltp == 1 {
				continue
			}
		}
		for x := 0; x < width; x++ {
			ctx := 0
			for _, p := range tmpl {
				ctx = ctx<<1 | int(get(x+p.dx, y+p.dy))
			}
			enc.encodeBit(&contexts[ctx], int(get(x, y)))
		}
	}

	return buildGenericRegionSegment(width, height, template, tpgdon, originX, originY, op, enc.flush())
}

// buildGenericRegionSegment wraps coded (already-MQ-encoded generic
// region bitmap data) in a generic region segment's own header fields
// (region info + generic region flags + AT pixels - the mirror image of
// decodeGenericRegionSegment's parsing) and then in a full JBIG2 segment
// header (segmentHeader's on-the-wire form - the mirror image of
// parseSegmentHeader), producing a complete, self-contained embedded-
// organization JBIG2 byte stream ready to use as a JBIG2Decode stream's
// raw bytes.
func buildGenericRegionSegment(width, height, template int, tpgdon bool, originX, originY int, op combineOp, coded []byte) []byte {
	region := make([]byte, 0, 17+1+2*len(defaultATPixels[template])+len(coded))
	region = appendBE32(region, uint32(width))
	region = appendBE32(region, uint32(height))
	region = appendBE32(region, uint32(originX))
	region = appendBE32(region, uint32(originY))
	region = append(region, byte(op))

	flags := byte(template << 1) // Bit 0 (MMR) is 0: arithmetic coding.
	if tpgdon {
		flags |= 0x08
	}
	region = append(region, flags)
	for _, p := range defaultATPixels[template] {
		region = append(region, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	region = append(region, coded...)

	header := buildSegmentHeader(0, segTypeImmediateGenericRegion, uint32(len(region)))
	return append(header, region...)
}

// buildSegmentHeader builds one JBIG2 segment header (T.88 7.2) in its
// simplest legal form: no referred-to segments, a 1-byte page
// association (always page 1) - everything buildGenericRegionSegment's
// single-segment fixtures need and nothing more.
func buildSegmentHeader(number uint32, segType int, dataLength uint32) []byte {
	h := make([]byte, 0, 11)
	h = appendBE32(h, number)
	h = append(h, byte(segType)) // Bits 6-7 (page assoc size, deferred) both 0.
	h = append(h, 0x00)          // Referred-to count/retention flags: short form, count 0.
	h = append(h, 0x01)          // Page association: page 1.
	h = appendBE32(h, dataLength)
	return h
}

func appendBE32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
