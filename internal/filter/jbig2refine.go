package filter

import (
	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements JBIG2's generic refinement region decoding
// procedure (ITU-T T.88 clause 6.3) and the refinement region segment
// that wraps it (7.4.7), plus the matching encoding direction this
// package's tests use.
//
// # What refinement is for
//
// Everything else in this package's JBIG2 support decodes a bitmap from
// nothing. Refinement instead decodes a bitmap that is a *correction* of
// one already decoded: each pixel's arithmetic-coding context is built
// not only from its own already-decoded neighbors (as in a generic
// region) but also from the corresponding neighborhood of a reference
// bitmap. Where the two agree - which, for a correction, is almost
// everywhere - the coder is highly confident and spends almost nothing.
//
// Two things use it, and both are about lossy coding of scanned text:
//
//   - A text region may refine an individual symbol instance
//     (SBREFINE). A lossy symbol-mode encoder groups glyphs that are
//     merely *similar* into one dictionary entry; where that
//     substitution would be too visible, it refines that one instance
//     back toward the pixels actually scanned.
//   - A symbol dictionary may define a new symbol as a refinement of an
//     existing one (SDREFAGG), so a dictionary can hold "the same 'e',
//     but bolder" without coding a second 'e' from scratch.
//
// A refinement region segment standing on its own (the third user,
// handled by decodeRefinementRegionSegment below) refines part of the
// page itself, using whatever has already been painted there as its
// reference. That is rarer, but costs little once the procedure exists.

// refinementTemplates lists each GRTEMPLATE's context bit layout (T.88
// Figures 12-14), most significant bit first. Unlike a generic region's
// single neighborhood, a refinement context has two halves: pixels from
// the bitmap being decoded ("coding", which can only look at already-
// decoded positions) and pixels from the reference bitmap
// ("reference", which is fully decoded already and so may look in every
// direction, including straight at the pixel being refined).
//
// GRTEMPLATE 0 has one adaptive pixel in each half; GRTEMPLATE 1 has
// none. As in the generic templates, an adaptive slot keeps its bit
// position while reading from wherever the segment says.
var refinementTemplates = [2]struct {
	coding    []templateSlot
	reference []templateSlot
}{
	0: {
		coding:    []templateSlot{fixedSlot(0, -1), fixedSlot(1, -1), fixedSlot(-1, 0), atSlot(0)},
		reference: []templateSlot{fixedSlot(0, -1), fixedSlot(1, -1), fixedSlot(-1, 0), fixedSlot(0, 0), fixedSlot(1, 0), fixedSlot(-1, 1), fixedSlot(0, 1), fixedSlot(1, 1), atSlot(1)},
	},
	1: {
		coding:    []templateSlot{fixedSlot(-1, -1), fixedSlot(0, -1), fixedSlot(1, -1), fixedSlot(-1, 0)},
		reference: []templateSlot{fixedSlot(0, -1), fixedSlot(-1, 0), fixedSlot(0, 0), fixedSlot(1, 0), fixedSlot(0, 1), fixedSlot(1, 1)},
	},
}

// defaultRefinementAT gives the refinement adaptive pixels' default
// positions: A1 in the coding half and A2 in the reference half, both
// at (-1, -1). Only GRTEMPLATE 0 has them.
var defaultRefinementAT = []jbig2Point{{-1, -1}, {-1, -1}}

// refinementReusedContext is the fixed pseudo-context each GRTEMPLATE
// uses for its per-row typical-prediction decision, the refinement
// counterpart of reusedContext (T.88 6.3.5.6).
var refinementReusedContext = [2]int{0x0020, 0x0008}

// refinementContextSize returns how many contexts GRTEMPLATE t's bit
// layout needs.
func refinementContextSize(t int) int {
	return 1 << uint(len(refinementTemplates[t].coding)+len(refinementTemplates[t].reference))
}

// resolveTemplateSlots turns a slot list into plain neighbor offsets
// against the given adaptive pixel positions - the refinement
// counterpart of genericContextTemplate.
func resolveTemplateSlots(slots []templateSlot, at []jbig2Point) []jbig2Point {
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

// decodeRefinementBitmap runs T.88's generic refinement region decoding
// procedure (6.3.5), producing a width x height bitmap refined from
// reference. dx and dy shift the reference under the bitmap being
// decoded: the pixel at (x, y) here is refining reference's pixel at
// (x-dx, y-dy).
//
// When tpgron is set, each row first decodes a typical-prediction bit
// whose running value says whether this row may be predicted from the
// reference. While it is set, any pixel whose entire 3x3 reference
// neighborhood is uniform simply takes that value without being decoded
// at all - a refinement's rows are mostly untouched interior and
// background, so that is the common case and coding nothing for it is
// the point.
func decodeRefinementBitmap(dec *mqDecoder, contexts []mqContext, width, height, template int, at []jbig2Point, reference *jbig2Bitmap, dx, dy int, tpgron bool) *jbig2Bitmap {
	coding := resolveTemplateSlots(refinementTemplates[template].coding, at)
	ref := resolveTemplateSlots(refinementTemplates[template].reference, at)
	bitmap := newJBIG2Bitmap(width, height, 0)

	ltp := 0
	for y := 0; y < height; y++ {
		if tpgron {
			ltp ^= dec.decodeBit(&contexts[refinementReusedContext[template]])
		}
		for x := 0; x < width; x++ {
			if ltp == 1 {
				if value, uniform := uniformReferenceNeighborhood(reference, x-dx, y-dy); uniform {
					bitmap.set(x, y, value)
					continue
				}
			}
			ctx := 0
			for _, p := range coding {
				ctx = ctx<<1 | int(bitmap.get(x+p.dx, y+p.dy))
			}
			for _, p := range ref {
				ctx = ctx<<1 | int(reference.get(x-dx+p.dx, y-dy+p.dy))
			}
			bitmap.set(x, y, byte(dec.decodeBit(&contexts[ctx])))
		}
	}
	return bitmap
}

// uniformReferenceNeighborhood reports whether every pixel of the 3x3
// block of reference centered at (x, y) has the same value, and if so
// what that value is - the test typical prediction uses to decide a
// pixel without decoding it.
func uniformReferenceNeighborhood(reference *jbig2Bitmap, x, y int) (byte, bool) {
	first := reference.get(x-1, y-1)
	for ddy := -1; ddy <= 1; ddy++ {
		for ddx := -1; ddx <= 1; ddx++ {
			if reference.get(x+ddx, y+ddy) != first {
				return 0, false
			}
		}
	}
	return first, true
}

// encodeRefinementBitmap is decodeRefinementBitmap's mirror image, for
// this package's round-trip tests. It always codes every pixel
// (typical prediction is an encoder's optional saving, and leaving it
// out here keeps the one function that has to stay in lockstep with the
// decoder as simple as possible).
func encodeRefinementBitmap(enc *mqEncoder, contexts []mqContext, width, height, template int, at []jbig2Point, reference *jbig2Bitmap, dx, dy int, source *jbig2Bitmap) {
	coding := resolveTemplateSlots(refinementTemplates[template].coding, at)
	ref := resolveTemplateSlots(refinementTemplates[template].reference, at)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			ctx := 0
			for _, p := range coding {
				ctx = ctx<<1 | int(source.get(x+p.dx, y+p.dy))
			}
			for _, p := range ref {
				ctx = ctx<<1 | int(reference.get(x-dx+p.dx, y-dy+p.dy))
			}
			enc.encodeBit(&contexts[ctx], int(source.get(x, y)))
		}
	}
}

// decodeRefinementRegionSegment decodes a standalone refinement region
// segment (T.88 7.4.7), which refines a rectangle of the page that
// earlier segments already painted. page may be nil if nothing has been
// painted yet, in which case the reference is blank - legal, if
// pointless, and better handled than crashed on.
func decodeRefinementRegionSegment(segData []byte, page *jbig2Bitmap) (bitmap *jbig2Bitmap, x, y int, op combineOp, err error) {
	width, height, x, y, op, err := parseRegionInfo(segData)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	pos := 17

	if len(segData) < pos+1 {
		return nil, 0, 0, 0, pdferror.Malformedf("JBIG2Decode: truncated refinement region segment flags")
	}
	flags := segData[pos]
	pos++
	template := int(flags & 0x01)
	tpgron := flags&0x02 != 0

	at := defaultRefinementAT
	if template == 0 {
		if at, pos, err = parseATPixels(segData, pos, 2); err != nil {
			return nil, 0, 0, 0, err
		}
	}

	// The reference is whatever the page already holds at this region's
	// own position, so the refined result can simply replace it.
	reference := newJBIG2Bitmap(width, height, 0)
	if page != nil {
		for ry := 0; ry < height; ry++ {
			for rx := 0; rx < width; rx++ {
				reference.set(rx, ry, page.get(x+rx, y+ry))
			}
		}
	}

	dec := newMQDecoder(segData[pos:])
	contexts := make([]mqContext, refinementContextSize(template))
	bitmap = decodeRefinementBitmap(dec, contexts, width, height, template, at, reference, 0, 0, tpgron)

	// A refinement replaces what it refined rather than being merged
	// with it: the decoded bitmap already *is* the corrected content,
	// so OR-ing it onto the original would keep pixels the refinement
	// deliberately cleared.
	return bitmap, x, y, combOpReplace, nil
}
