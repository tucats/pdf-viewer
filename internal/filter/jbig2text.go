package filter

import (
	"encoding/binary"

	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// This file implements JBIG2's text region segment (ITU-T T.88 clause
// 7.4.4 for the segment's own header fields, 6.4 for the decoding
// procedure), plus the matching encoding direction this package's tests
// and fixtures use.
//
// # What a text region is
//
// A text region paints instances of symbols from a symbol dictionary
// (jbig2symbol.go) onto a region bitmap: "draw symbol 41 here, symbol 7
// twelve pixels to its right, ...". For a scanned page of text this is
// where nearly all the page's content lives, and coding each occurrence
// as an index plus a small positional delta is what makes JBIG2's
// symbol mode compress so much better than coding the same glyph's
// pixels over and over.
//
// # Strips, S and T
//
// Rather than a plain (x, y) per instance, T.88 organizes instances into
// horizontal "strips". A strip has one coarse vertical position (T);
// instances within it are placed left to right, each coded as the gap
// since the previous instance's right edge (S). Text on a scanned line
// shares a baseline, so a strip's instances nearly all sit at the same
// T, and the gaps between neighboring glyphs are small numbers - both
// cheap for the arithmetic integer coder (jbig2arith.go).
//
// When a strip is more than one row tall (SBSTRIPS above 1, which is
// what a scan with slightly uneven baselines uses), each instance also
// carries a small "how far into the strip" refinement, CURT.
//
// The naming stays close to the standard's - STRIPT, FIRSTS, CURS, IDS -
// because that is what makes this readable next to the clause it
// implements.
//
// # Transposed regions and reference corners
//
// A region may be TRANSPOSED, in which case S runs vertically and T
// horizontally - how vertically-set text (some CJK layouts) is coded.
// Independently, REFCORNER says which corner of a symbol's bitmap the
// coded position refers to. Both are implemented here (they cost only
// the coordinate arithmetic in drawTextSymbol), even though a Latin
// scan almost always uses the untransposed, bottom-left-referenced
// form.
//
// # Scope
//
// Only the arithmetic-coded form is implemented (SBHUFF = 0), and
// refinement of individual instances (SBREFINE) is reported as
// unsupported rather than mis-decoded; see jbig2.go's doc comment for
// this package's overall JBIG2 scope.

// Reference corner codes (T.88 Table 34): which corner of a symbol's
// bitmap the instance's coded (S, T) position names.
const (
	refCornerBottomLeft  = 0
	refCornerTopLeft     = 1
	refCornerBottomRight = 2
	refCornerTopRight    = 3
)

// maxTextRegionInstances bounds how many symbol instances one text
// region may place. Each instance costs a bounded amount of work
// (compositing one symbol bitmap), so this is really a bound on total
// decoding time against a stream that declares an absurd instance count;
// a full page of dense text runs to a few thousand.
const maxTextRegionInstances = 10_000_000

// textRegionParams is a text region segment's parsed header (T.88
// 7.4.4), separated from the coded data that follows it.
type textRegionParams struct {
	width, height int
	x, y          int
	// regionOp composites the finished region onto the page; combOp
	// composites each individual symbol onto the region.
	regionOp combineOp
	combOp   combineOp

	stripSize    int // SBSTRIPS: how many rows tall one strip is.
	refCorner    int
	transposed   bool
	defaultPixel byte
	dsOffset     int // SBDSOFFSET: a constant added to every inter-symbol gap.
	numInstances int

	// refine (SBREFINE) allows an individual symbol instance to be
	// corrected against the dictionary's copy of that symbol - see
	// jbig2refine.go. refineTemplate and refineAT are that refinement's
	// GRTEMPLATE and adaptive pixels.
	refine         bool
	refineTemplate int
	refineAT       []jbig2Point

	dataStart int
}

// textContexts is every adaptive context set one text region decoding
// pass needs. It is a named group rather than a dozen locals because a
// symbol dictionary using aggregate coding (jbig2symbol.go) decodes
// several text regions that must all *share* one set of these, along
// with the dictionary's own coded stream - each aggregate symbol
// continues adapting where the previous one left off.
type textContexts struct {
	iadt, iafs, iads, iait           arithIntCtx
	iari, iardw, iardh, iardx, iardy arithIntCtx
	iaid                             *arithIAIDCtx
	// refinement holds the pixel-level contexts an SBREFINE instance's
	// refinement decodes against, allocated lazily since most regions
	// never refine anything.
	refinement []mqContext
}

func newTextContexts(symbolCodeLen int) *textContexts {
	return &textContexts{
		iadt: newArithIntCtx(), iafs: newArithIntCtx(), iads: newArithIntCtx(), iait: newArithIntCtx(),
		iari: newArithIntCtx(), iardw: newArithIntCtx(), iardh: newArithIntCtx(),
		iardx: newArithIntCtx(), iardy: newArithIntCtx(),
		iaid: newArithIAIDCtx(symbolCodeLen),
	}
}

// refinementContexts returns the refinement context set, allocating it
// on first use.
func (c *textContexts) refinementContexts(template int) []mqContext {
	if c.refinement == nil {
		c.refinement = make([]mqContext, refinementContextSize(template))
	}
	return c.refinement
}

// parseTextRegionHeader parses the fixed fields at the start of a text
// region segment's data (T.88 7.4.4.1).
func parseTextRegionHeader(segData []byte) (textRegionParams, error) {
	var p textRegionParams

	var err error
	p.width, p.height, p.x, p.y, p.regionOp, err = parseRegionInfo(segData)
	if err != nil {
		return p, err
	}
	pos := 17

	if len(segData) < pos+2 {
		return p, pdferror.Malformedf("JBIG2Decode: truncated text region flags")
	}
	flags := binary.BigEndian.Uint16(segData[pos : pos+2])
	pos += 2

	huff := flags&0x0001 != 0
	p.refine = flags&0x0002 != 0
	logStripSize := int(flags>>2) & 0x03
	p.refCorner = int(flags>>4) & 0x03
	p.transposed = flags&0x0040 != 0
	p.combOp = combineOp(int(flags>>7) & 0x03)
	p.defaultPixel = byte(flags>>9) & 0x01
	p.refineTemplate = int(flags>>15) & 0x01

	// SBDSOFFSET is a 5-bit *signed* field (T.88 7.4.4.1.1): a constant
	// nudge applied to every inter-symbol gap, letting an encoder shift
	// the whole distribution of gaps toward zero where the coder spends
	// fewest bits on it.
	p.dsOffset = int(flags>>10) & 0x1F
	if p.dsOffset > 15 {
		p.dsOffset -= 32
	}

	p.stripSize = 1 << uint(logStripSize)

	if huff {
		return p, pdferror.Unsupportedf("JBIG2Decode: Huffman-coded text regions are not implemented, only arithmetic coding")
	}
	p.refineAT = defaultRefinementAT
	if p.refine && p.refineTemplate == 0 {
		if p.refineAT, pos, err = parseATPixels(segData, pos, 2); err != nil {
			return p, err
		}
	}

	if len(segData) < pos+4 {
		return p, pdferror.Malformedf("JBIG2Decode: truncated text region instance count")
	}
	numInstances := binary.BigEndian.Uint32(segData[pos : pos+4])
	pos += 4
	if numInstances > maxTextRegionInstances {
		return p, pdferror.Unsupportedf("JBIG2Decode: text region declares %d symbol instances, beyond this package's limit of %d", numInstances, maxTextRegionInstances)
	}
	p.numInstances = int(numInstances)
	p.dataStart = pos
	return p, nil
}

// decodeTextRegionSegment decodes one text region segment, returning the
// region bitmap it paints and where/how jbig2.go should composite it
// onto the page. symbols is the concatenation, in referred-to order, of
// the symbols exported by every symbol dictionary this segment refers
// to - the list its coded symbol IDs index into.
func decodeTextRegionSegment(segData []byte, symbols []*jbig2Bitmap) (bitmap *jbig2Bitmap, x, y int, op combineOp, err error) {
	p, err := parseTextRegionHeader(segData)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if len(symbols) == 0 {
		return nil, 0, 0, 0, pdferror.Malformedf("JBIG2Decode: text region refers to no symbol dictionary, so it has no symbols to draw")
	}

	dec := newMQDecoder(segData[p.dataStart:])
	region, err := decodeTextRegionBitmap(dec, &p, symbols, nil)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return region, p.x, p.y, p.regionOp, nil
}

// decodeTextRegionBitmap runs T.88's text region decoding procedure
// (6.4.5) against an already-positioned decoder, producing the region's
// bitmap.
//
// cx may be nil, in which case fresh contexts sized for this symbol list
// are used - the right thing for a standalone text region segment. A
// symbol dictionary's aggregate coding instead passes its own shared
// set; see textContexts.
func decodeTextRegionBitmap(dec *mqDecoder, p *textRegionParams, symbols []*jbig2Bitmap, cx *textContexts) (*jbig2Bitmap, error) {
	if cx == nil {
		cx = newTextContexts(symbolCodeLength(len(symbols)))
	}
	iadt, iafs, iads, iait := cx.iadt, cx.iafs, cx.iads, cx.iait

	region := newJBIG2Bitmap(p.width, p.height, p.defaultPixel)

	// 6.4.5 step 1: the first strip's T coordinate is coded as a
	// negative multiple of the strip height, so that the running strip
	// position below can start by adding to it.
	stripT, ok, bad := dec.decodeInt(iadt)
	if !ok || bad {
		return nil, pdferror.Malformedf("JBIG2Decode: text region initial strip position is missing or out of range")
	}
	stripT *= -p.stripSize

	firstS := 0
	instances := 0
	for instances < p.numInstances {
		// A new strip: its T position is a delta from the previous one.
		dt, ok, bad := dec.decodeInt(iadt)
		if !ok || bad {
			return nil, pdferror.Malformedf("JBIG2Decode: text region strip delta is missing or out of range")
		}
		stripT += dt * p.stripSize

		// The strip's first instance is placed relative to the *previous
		// strip's* first instance, so that a column of text starting at
		// the same left margin costs almost nothing per line.
		dfs, ok, bad := dec.decodeInt(iafs)
		if !ok || bad {
			return nil, pdferror.Malformedf("JBIG2Decode: text region first-symbol delta is missing or out of range")
		}
		firstS += dfs
		curS := firstS

		for first := true; ; first = false {
			if !first {
				ids, ok, bad := dec.decodeInt(iads)
				if bad {
					return nil, pdferror.Malformedf("JBIG2Decode: text region symbol gap is out of range")
				}
				if !ok {
					break // OOB: this strip has no more instances.
				}
				curS += ids + p.dsOffset
			}
			if instances >= p.numInstances {
				return nil, pdferror.Malformedf("JBIG2Decode: text region places more symbol instances than the %d it declared", p.numInstances)
			}

			// Within a strip taller than one row, each instance says how
			// far into the strip it actually sits.
			curT := 0
			if p.stripSize > 1 {
				curT, ok, bad = dec.decodeInt(iait)
				if !ok || bad {
					return nil, pdferror.Malformedf("JBIG2Decode: text region symbol strip offset is missing or out of range")
				}
			}

			id := dec.decodeIAID(cx.iaid)
			if id < 0 || id >= len(symbols) {
				return nil, pdferror.Malformedf("JBIG2Decode: text region refers to symbol %d, but only %d symbols are available", id, len(symbols))
			}

			symbol := symbols[id]
			if p.refine {
				refined, err := refineTextSymbol(dec, p, cx, symbol)
				if err != nil {
					return nil, err
				}
				symbol = refined
			}

			curS = drawTextSymbol(region, symbol, p, curS, stripT+curT)
			instances++
		}
	}
	return region, nil
}

// refineTextSymbol handles one instance's optional refinement (T.88
// 6.4.11): a leading flag says whether this instance is refined at all,
// and if it is, four more values give the refined bitmap's size change
// and the reference's offset within it. The dictionary's symbol is
// returned unchanged when the flag says no refinement, which is the
// common case even in a stream that enables refinement at all.
func refineTextSymbol(dec *mqDecoder, p *textRegionParams, cx *textContexts, symbol *jbig2Bitmap) (*jbig2Bitmap, error) {
	ri, ok, bad := dec.decodeInt(cx.iari)
	if !ok || bad {
		return nil, pdferror.Malformedf("JBIG2Decode: text region refinement flag is missing or out of range")
	}
	if ri == 0 {
		return symbol, nil
	}

	values := make([]int, 4)
	for i, ctx := range []arithIntCtx{cx.iardw, cx.iardh, cx.iardx, cx.iardy} {
		v, ok, bad := dec.decodeInt(ctx)
		if !ok || bad {
			return nil, pdferror.Malformedf("JBIG2Decode: text region refinement parameter is missing or out of range")
		}
		values[i] = v
	}
	rdw, rdh, rdx, rdy := values[0], values[1], values[2], values[3]

	width, height := symbol.width+rdw, symbol.height+rdh
	if width <= 0 || height <= 0 || width > maxGenericRegionDimension || height > maxGenericRegionDimension || width*height > maxGenericRegionPixels {
		return nil, pdferror.Malformedf("JBIG2Decode: refined symbol size %dx%d is out of range", width, height)
	}

	// The reference sits centered in whatever extra space the size
	// change created, then shifted by RDX/RDY. The halving rounds toward
	// negative infinity (an arithmetic shift, not a division), which is
	// what T.88 specifies and what matters when a refinement makes a
	// symbol smaller.
	return decodeRefinementBitmap(dec, cx.refinementContexts(p.refineTemplate), width, height,
		p.refineTemplate, p.refineAT, symbol, (rdw>>1)+rdx, (rdh>>1)+rdy, false), nil
}

// drawTextSymbol composites one symbol instance onto the region at the
// position (s, t) names, returning the updated S coordinate - which
// advances past the symbol so the next instance's coded gap is measured
// from this one's far edge.
//
// The two coordinate systems are worth stating plainly, because they are
// what makes the four reference corners and the transposed flag more
// than bookkeeping. Untransposed, S is horizontal and T vertical; the
// symbol's *left* edge sits at S either way, because for a right-hand
// reference corner T.88 advances S past the symbol's width before
// placing it (6.4.5 step 3c vi) rather than after (step 3c x). So only
// the vertical placement actually differs: a top corner puts the
// symbol's first row at T, a bottom corner puts its last row there.
// Transposed, the same argument holds with the axes exchanged: the top
// edge sits at S, and a left or right corner decides whether T names the
// symbol's first or last column.
func drawTextSymbol(region, symbol *jbig2Bitmap, p *textRegionParams, s, t int) int {
	top := p.refCorner == refCornerTopLeft || p.refCorner == refCornerTopRight
	left := p.refCorner == refCornerTopLeft || p.refCorner == refCornerBottomLeft

	var x, y int
	if p.transposed {
		y = s
		x = t
		if !left {
			x -= symbol.width - 1
		}
	} else {
		x = s
		y = t
		if !top {
			y -= symbol.height - 1
		}
	}

	region.composite(symbol, x, y, p.combOp)

	if p.transposed {
		return s + symbol.height - 1
	}
	return s + symbol.width - 1
}

// textInstance is one symbol placement for this package's text region
// encoder: which symbol, and where its top-left corner goes.
type textInstance struct {
	symbol int
	x, y   int
}

// encodeTextRegionSegment builds a complete text region segment (header
// plus arithmetic-coded data) placing instances of symbols on a
// width x height region, for this package's tests and fixtures.
//
// It always emits the simplest legal shape: one-row strips, top-left
// reference corner, untransposed, no gap offset, symbols OR-ed onto a
// blank region. The decoder's other combinations are reachable from
// tests through decodeTextRegionBitmap directly.
//
// instances must be ordered by non-decreasing y, and by non-decreasing x
// within one y, since the coding is a walk over strips top to bottom and
// then left to right within each strip. As in encodeSymbolDictSegment,
// an unordered list is a caller bug rather than something to sort
// silently, since it would produce a stream that decodes to different
// positions than the caller asked for.
func encodeTextRegionSegment(number uint32, referredTo []uint32, width, height int, symbols []*jbig2Bitmap, instances []textInstance, originX, originY int, regionOp combineOp) []byte {
	for i := 1; i < len(instances); i++ {
		prev, cur := instances[i-1], instances[i]
		if cur.y < prev.y || (cur.y == prev.y && cur.x < prev.x) {
			panic("filter: encodeTextRegionSegment: instances must be ordered by y, then x")
		}
	}

	enc := newMQEncoder()
	iadt, iafs, iads, iait := newArithIntCtx(), newArithIntCtx(), newArithIntCtx(), newArithIntCtx()
	iaid := newArithIAIDCtx(symbolCodeLength(len(symbols)))
	_ = iait // One-row strips code no per-instance strip offset.

	// The decoder starts by reading a negated initial strip position;
	// starting from 0 keeps the first strip's own delta equal to its T.
	enc.encodeInt(iadt, 0)

	stripT := 0
	firstS := 0
	for i := 0; i < len(instances); {
		t := instances[i].y
		enc.encodeInt(iadt, t-stripT)
		stripT = t

		enc.encodeInt(iafs, instances[i].x-firstS)
		firstS = instances[i].x
		curS := firstS

		for first := true; i < len(instances) && instances[i].y == t; i, first = i+1, false {
			if !first {
				// The coded gap is measured from where the previous symbol
				// left CURS - one pixel short of its right edge.
				enc.encodeInt(iads, instances[i].x-curS)
				curS = instances[i].x
			}
			enc.encodeIAID(iaid, instances[i].symbol)
			curS += symbols[instances[i].symbol].width - 1
		}
		enc.encodeOOB(iads)
	}

	coded := enc.flush()

	data := make([]byte, 0, 17+2+4+len(coded))
	data = appendBE32(data, uint32(width))
	data = appendBE32(data, uint32(height))
	data = appendBE32(data, uint32(originX))
	data = appendBE32(data, uint32(originY))
	data = append(data, byte(regionOp))

	// Flags: SBHUFF and SBREFINE clear, LOGSBSTRIPS 0, REFCORNER
	// TOPLEFT, untransposed, SBCOMBOP OR, SBDEFPIXEL 0, SBDSOFFSET 0,
	// SBRTEMPLATE 0.
	data = append(data, 0x00, byte(refCornerTopLeft<<4))
	data = appendBE32(data, uint32(len(instances)))
	data = append(data, coded...)

	header := buildSegmentHeader(number, segTypeTextRegionImmediate, referredTo, uint32(len(data)))
	return append(header, data...)
}
