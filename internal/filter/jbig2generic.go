package filter

import (
	"encoding/binary"

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

// templateSlot is one bit position in a GBTEMPLATE's context, in the
// order T.88's own template figures number them (most significant bit
// first). A slot is either a fixed neighboring pixel or one of the
// segment's adaptive-template (AT) pixels, whose *bit position* is fixed
// by the figure even though the pixel it reads from is chosen by the
// encoder.
type templateSlot struct {
	// point is the neighbor offset this slot reads, for a fixed slot.
	point jbig2Point
	// atIndex is -1 for a fixed slot, or the index into the segment's AT
	// pixel list (A1 is index 0) for an adaptive one.
	atIndex int
}

// fixedSlot and atSlot are small constructors that keep the template
// tables below readable.
func fixedSlot(dx, dy int) templateSlot { return templateSlot{point: jbig2Point{dx, dy}, atIndex: -1} }
func atSlot(i int) templateSlot         { return templateSlot{atIndex: i} }

// codingTemplates lists each GBTEMPLATE's context bit layout - ITU-T
// T.88 Figures 4-7, "template used when GBTEMPLATE is 0/1/2/3" - in the
// order those figures number the bits: raster order (top row first, left
// to right within a row) *with each AT pixel counted at its nominal
// position*, most significant bit first.
//
// That "nominal position" is the subtlety worth spelling out. Each AT
// pixel's default position (defaultATPixels below) is exactly where the
// figure draws it, so for a stream using the defaults these tables
// describe plain raster order over the whole neighborhood. An encoder
// may move an AT pixel somewhere else entirely to help compression on
// unusual content, and when it does, the moved pixel keeps the *bit
// position* the figure gave it while reading from its new location -
// which is why the layout is written out as an explicit ordered list
// here rather than derived by sorting the combined points.
var codingTemplates = [4][]templateSlot{
	0: {
		atSlot(3), fixedSlot(-1, -2), fixedSlot(0, -2), fixedSlot(1, -2), atSlot(2),
		atSlot(1), fixedSlot(-2, -1), fixedSlot(-1, -1), fixedSlot(0, -1), fixedSlot(1, -1), fixedSlot(2, -1), atSlot(0),
		fixedSlot(-4, 0), fixedSlot(-3, 0), fixedSlot(-2, 0), fixedSlot(-1, 0),
	},
	1: {
		fixedSlot(-1, -2), fixedSlot(0, -2), fixedSlot(1, -2), fixedSlot(2, -2),
		fixedSlot(-2, -1), fixedSlot(-1, -1), fixedSlot(0, -1), fixedSlot(1, -1), fixedSlot(2, -1), atSlot(0),
		fixedSlot(-3, 0), fixedSlot(-2, 0), fixedSlot(-1, 0),
	},
	2: {
		fixedSlot(-1, -2), fixedSlot(0, -2), fixedSlot(1, -2),
		fixedSlot(-2, -1), fixedSlot(-1, -1), fixedSlot(0, -1), fixedSlot(1, -1), atSlot(0),
		fixedSlot(-2, 0), fixedSlot(-1, 0),
	},
	3: {
		fixedSlot(-3, -1), fixedSlot(-2, -1), fixedSlot(-1, -1), fixedSlot(0, -1), fixedSlot(1, -1), atSlot(0),
		fixedSlot(-4, 0), fixedSlot(-3, 0), fixedSlot(-2, 0), fixedSlot(-1, 0),
	},
}

// defaultATPixels gives each GBTEMPLATE's default adaptive-template (AT)
// pixel offset(s) - the positions T.88's template figures draw them at,
// which are also what real encoders overwhelmingly use. A segment states
// its own AT positions explicitly, so these are needed only by this
// package's encoder (which always uses the defaults) and by tests.
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

// genericContextTemplate resolves GBTEMPLATE t's bit layout against the
// AT pixel positions at (as read from a real segment, or
// defaultATPixels[t] for this package's own encoder) into a flat list of
// neighbor offsets, most significant context bit first - the form
// decodeGenericBitmap walks once per pixel.
//
// at must have at least as many entries as template t has AT slots (4
// for GBTEMPLATE 0, 1 for the others); callers read that count straight
// out of the segment, so a short list is a caller bug rather than
// malformed input.
func genericContextTemplate(t int, at []jbig2Point) []jbig2Point {
	slots := codingTemplates[t]
	points := make([]jbig2Point, len(slots))
	for i, s := range slots {
		if s.atIndex < 0 {
			points[i] = s.point
			continue
		}
		points[i] = at[s.atIndex]
	}
	return points
}

// atSlotCount returns how many adaptive-template pixels GBTEMPLATE t
// uses: 4 for GBTEMPLATE 0, 1 for every other template.
func atSlotCount(t int) int {
	if t == 0 {
		return 4
	}
	return 1
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

	at, pos, err := parseATPixels(segData, pos, atSlotCount(template))
	if err != nil {
		return nil, 0, 0, 0, err
	}

	bitmap = decodeGenericBitmap(newMQDecoder(segData[pos:]), newGenericContexts(template), width, height, template, at, tpgdon)
	return bitmap, x, y, op, nil
}

// parseATPixels reads count adaptive-template pixel positions starting
// at data[pos], returning them and the position just past them. AT
// coordinates are signed bytes (T.88 7.4.6.3), one x/y pair each.
func parseATPixels(data []byte, pos, count int) ([]jbig2Point, int, error) {
	if len(data) < pos+2*count {
		return nil, 0, pdferror.Malformedf("JBIG2Decode: truncated adaptive-template (AT) pixel positions")
	}
	at := make([]jbig2Point, count)
	for i := 0; i < count; i++ {
		at[i] = jbig2Point{dx: int(int8(data[pos])), dy: int(int8(data[pos+1]))}
		pos += 2
	}
	return at, pos, nil
}

// newGenericContexts allocates the context set GBTEMPLATE template's
// bit layout needs: one per possible value of its context bits.
//
// A symbol dictionary decodes many small bitmaps from one coded stream
// and must carry a single such set across all of them (that shared,
// continuously-adapting probability state is most of why a dictionary of
// similar-looking glyphs compresses so well), which is why allocating
// the set is separate from decoding a bitmap with it.
func newGenericContexts(template int) []mqContext {
	return make([]mqContext, 1<<uint(len(codingTemplates[template])))
}

// decodeGenericBitmap runs T.88's generic region decoding procedure
// (6.2), reading from dec and producing a width x height jbig2Bitmap.
// Every pixel is decoded in raster order (top-to-bottom, left-to-right),
// building each one's arithmetic-coding context from its already-decoded
// neighbors per genericContextTemplate(template, at) - a pixel outside
// the bitmap (the first couple of rows' "row -1"/"row -2" neighbors, or
// a few pixels' worth of "column -1..-4" on the left edge) reads as 0
// (background), exactly matching jbig2Bitmap.get's own out-of-bounds
// behavior.
//
// dec and contexts are passed in rather than created here because a
// symbol dictionary decodes every one of its symbol bitmaps from a
// single continuing coded stream with a single continuing context set;
// a standalone generic region segment just hands in a decoder over its
// own data and a fresh newGenericContexts(template).
//
// When tpgdon is set, each row first decodes one extra "typical
// prediction" bit (against the fixed reusedContext pseudo-pixel context,
// not a real neighborhood) that, cumulatively XORed together row by
// row, says whether this row turned out to be pixel-for-pixel identical
// to the row above it - extremely common in real scanned text, where
// many consecutive rows of a text line's or a wide margin's white space
// really are identical - letting the encoder skip coding that row's
// pixels individually at all.
func decodeGenericBitmap(dec *mqDecoder, contexts []mqContext, width, height, template int, at []jbig2Point, tpgdon bool) *jbig2Bitmap {
	tmpl := genericContextTemplate(template, at)
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
	return encodeGenericRegionSegmentAT(width, height, pix, tpgdon, originX, originY, op, 0, defaultATPixels[0])
}

// encodeGenericRegionSegmentAT is encodeGenericRegionSegment's fully
// general form, additionally choosing the GBTEMPLATE and its AT pixel
// positions - which this package's fixtures never need (they always use
// GBTEMPLATE 0 at its defaults) but its tests do, to cover the decoder's
// other three templates and the case of an encoder that has moved an AT
// pixel away from its default position.
func encodeGenericRegionSegmentAT(width, height int, pix []byte, tpgdon bool, originX, originY int, op combineOp, template int, at []jbig2Point) []byte {
	if len(pix) != width*height {
		panic("filter: encodeGenericRegionSegment: len(pix) does not match width*height")
	}
	enc := newMQEncoder()
	encodeGenericBitmap(enc, newGenericContexts(template), width, height, template, at, tpgdon, func(x, y int) byte {
		if x < 0 || x >= width || y < 0 || y >= height {
			return 0
		}
		return pix[y*width+x]
	})
	return buildGenericRegionSegment(width, height, template, tpgdon, originX, originY, op, at, enc.flush())
}

// encodeGenericBitmap is decodeGenericBitmap's mirror image: it encodes
// a width x height bitmap's pixels (read through get, which must return
// 0 for any out-of-bounds coordinate, exactly as jbig2Bitmap.get does)
// as the same sequence of decisions, in the same order, against the same
// contexts the decoder will replay them with.
//
// Like the decoder, it takes enc and contexts from its caller rather
// than creating them, so a symbol dictionary can encode many small
// bitmaps into one continuing stream.
//
// Every context here comes purely from the source bitmap's own known
// pixel values - for an encoder, a pixel's "already-decoded neighbors"
// are simply the same input pixels - never from anything the arithmetic
// coder produces.
func encodeGenericBitmap(enc *mqEncoder, contexts []mqContext, width, height, template int, at []jbig2Point, tpgdon bool, get func(x, y int) byte) {
	tmpl := genericContextTemplate(template, at)

	rowsIdentical := func(y0, y1 int) bool {
		for x := 0; x < width; x++ {
			if get(x, y0) != get(x, y1) {
				return false
			}
		}
		return true
	}

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
}

// buildGenericRegionSegment wraps coded (already-MQ-encoded generic
// region bitmap data) in a generic region segment's own header fields
// (region info + generic region flags + AT pixels - the mirror image of
// decodeGenericRegionSegment's parsing) and then in a full JBIG2 segment
// header (segmentHeader's on-the-wire form - the mirror image of
// parseSegmentHeader), producing a complete, self-contained embedded-
// organization JBIG2 byte stream ready to use as a JBIG2Decode stream's
// raw bytes.
func buildGenericRegionSegment(width, height, template int, tpgdon bool, originX, originY int, op combineOp, at []jbig2Point, coded []byte) []byte {
	region := make([]byte, 0, 17+1+2*len(at)+len(coded))
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
	for _, p := range at {
		region = append(region, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	region = append(region, coded...)

	header := buildSegmentHeader(0, segTypeImmediateGenericRegion, nil, uint32(len(region)))
	return append(header, region...)
}

// buildSegmentHeader builds one JBIG2 segment header (T.88 7.2) in its
// simplest legal form: the short-form referred-to count (so at most 4
// referred-to segments) and a 1-byte page association, always page 1 -
// everything this package's fixtures need and nothing more.
//
// Every segment number this package emits is small, so each referred-to
// number is written as a single byte, which is what parseSegmentHeader
// will read back for a referring segment numbered 256 or below.
func buildSegmentHeader(number uint32, segType int, referredTo []uint32, dataLength uint32) []byte {
	if len(referredTo) > 4 {
		panic("filter: buildSegmentHeader: more referred-to segments than the short form can hold")
	}
	if number > 256 {
		panic("filter: buildSegmentHeader: segment number too large for 1-byte referred-to numbers")
	}

	h := make([]byte, 0, 11+len(referredTo))
	h = appendBE32(h, number)
	h = append(h, byte(segType))            // Bits 6-7 (page assoc size, deferred) both 0.
	h = append(h, byte(len(referredTo))<<5) // Referred-to count in the top 3 bits; retention flags 0.
	for _, ref := range referredTo {
		h = append(h, byte(ref))
	}
	h = append(h, 0x01) // Page association: page 1.
	h = appendBE32(h, dataLength)
	return h
}

func appendBE32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
