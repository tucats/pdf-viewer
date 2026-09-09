package filter

import (
	"bytes"
	"math/rand"
	"testing"
)

// jbig2BitmapFromPattern wraps jbig2TestPattern's flat pixel slice in a
// jbig2Bitmap, which is what the refinement procedures work with.
func jbig2BitmapFromPattern(shape string, width, height int) *jbig2Bitmap {
	return &jbig2Bitmap{width: width, height: height, pix: jbig2TestPattern(shape, width, height)}
}

// perturb returns a copy of b with roughly one pixel in every `rate`
// flipped - a stand-in for the small corrections a real refinement
// carries, which is what makes this a meaningful test of the procedure:
// a refinement whose reference is identical to its target would decode
// correctly even from a context layout that was wrong in some way the
// reference half never exposed.
func perturb(b *jbig2Bitmap, rate int, seed int64) *jbig2Bitmap {
	rng := rand.New(rand.NewSource(seed))
	out := newJBIG2Bitmap(b.width, b.height, 0)
	copy(out.pix, b.pix)
	for i := range out.pix {
		if rng.Intn(rate) == 0 {
			out.pix[i] ^= 1
		}
	}
	return out
}

func TestJBIG2RefinementRoundTrip(t *testing.T) {
	// Both refinement templates, at several offsets between the
	// reference and the bitmap being decoded (a real refinement's
	// reference is often shifted, since the refined bitmap may be a
	// different size than the symbol it refines).
	const width, height = 33, 27
	reference := jbig2BitmapFromPattern("diagonalsAndBlock", width, height)

	offsets := []struct{ dx, dy int }{{0, 0}, {1, 0}, {0, 1}, {-2, 3}, {4, -1}}
	for template := 0; template < 2; template++ {
		for _, off := range offsets {
			source := perturb(reference, 12, int64(template*10)+int64(off.dx))

			enc := newMQEncoder()
			encodeRefinementBitmap(enc, make([]mqContext, refinementContextSize(template)), width, height,
				template, defaultRefinementAT, reference, off.dx, off.dy, source)

			got := decodeRefinementBitmap(newMQDecoder(enc.flush()), make([]mqContext, refinementContextSize(template)),
				width, height, template, defaultRefinementAT, reference, off.dx, off.dy, false)

			if !bytes.Equal(got.pix, source.pix) {
				t.Fatalf("template %d, offset (%d, %d): refined bitmap does not match the encoded one", template, off.dx, off.dy)
			}
		}
	}
}

func TestJBIG2RefinementCompressesSmallCorrections(t *testing.T) {
	// Refinement's whole premise is that coding a bitmap against a
	// nearly-identical reference costs far less than coding it from
	// nothing. If this ever stops holding, the reference half of the
	// context is probably not being consulted at all - which a round
	// trip alone would not notice, since both directions would ignore it
	// together.
	const width, height = 64, 64
	reference := jbig2BitmapFromPattern("diagonalsAndBlock", width, height)
	source := perturb(reference, 200, 5)

	enc := newMQEncoder()
	encodeRefinementBitmap(enc, make([]mqContext, refinementContextSize(0)), width, height,
		0, defaultRefinementAT, reference, 0, 0, source)
	refined := len(enc.flush())

	fromScratch := len(EncodeJBIG2GenericRegion(width, height, source.pix, false))
	if refined >= fromScratch {
		t.Errorf("refining against a near-identical reference took %d bytes, no less than the %d bytes of coding it from scratch", refined, fromScratch)
	}
}

func TestJBIG2RefinementTypicalPrediction(t *testing.T) {
	// With typical prediction on, a pixel whose 3x3 reference
	// neighborhood is uniform is not coded at all - it simply takes that
	// value. This drives the decoder with a stream in which the
	// per-row prediction bit is set and *no* pixel decisions follow for
	// the predictable pixels, and checks that the decoded bitmap
	// reproduces the reference's uniform areas.
	//
	// "solid" and "blank" are uniform everywhere except within one pixel
	// of an edge, where the out-of-bounds reference reads as background;
	// a solid reference therefore predicts its own interior and codes
	// only its border.
	const width, height = 24, 16
	reference := jbig2BitmapFromPattern("solid", width, height)

	// Encode: the per-row SLTP bit (1 on the first row, 0 after, so LTP
	// stays 1 throughout), then a decision only for each pixel whose
	// reference neighborhood is not uniform.
	contexts := make([]mqContext, refinementContextSize(0))
	enc := newMQEncoder()
	coding := resolveTemplateSlots(refinementTemplates[0].coding, defaultRefinementAT)
	ref := resolveTemplateSlots(refinementTemplates[0].reference, defaultRefinementAT)
	out := newJBIG2Bitmap(width, height, 0)
	for y := 0; y < height; y++ {
		sltp := 0
		if y == 0 {
			sltp = 1
		}
		enc.encodeBit(&contexts[refinementReusedContext[0]], sltp)
		for x := 0; x < width; x++ {
			if value, uniform := uniformReferenceNeighborhood(reference, x, y); uniform {
				out.set(x, y, value)
				continue
			}
			// Not predictable: code this pixel as the reference has it.
			ctx := 0
			for _, p := range coding {
				ctx = ctx<<1 | int(out.get(x+p.dx, y+p.dy))
			}
			for _, p := range ref {
				ctx = ctx<<1 | int(reference.get(x+p.dx, y+p.dy))
			}
			enc.encodeBit(&contexts[ctx], int(reference.get(x, y)))
			out.set(x, y, reference.get(x, y))
		}
	}

	got := decodeRefinementBitmap(newMQDecoder(enc.flush()), make([]mqContext, refinementContextSize(0)),
		width, height, 0, defaultRefinementAT, reference, 0, 0, true)
	if !bytes.Equal(got.pix, reference.pix) {
		t.Error("typical prediction did not reproduce the reference bitmap")
	}
}

func TestJBIG2RefinementRegionSegment(t *testing.T) {
	// A refinement region segment refines part of the page that an
	// earlier segment painted. This builds exactly that: a generic
	// region, then a refinement region covering it whose decoded content
	// differs from what is already there, and checks the page ends up
	// with the refined content rather than a merge of the two (a
	// refinement replaces what it refined).
	const width, height = 40, 24
	original := jbig2TestPattern("bands", width, height)
	refinedTo := perturb(&jbig2Bitmap{width: width, height: height, pix: original}, 6, 99)

	stream := EncodeJBIG2GenericRegion(width, height, original, false)

	enc := newMQEncoder()
	encodeRefinementBitmap(enc, make([]mqContext, refinementContextSize(0)), width, height,
		0, defaultRefinementAT, &jbig2Bitmap{width: width, height: height, pix: original}, 0, 0, refinedTo)
	coded := enc.flush()

	region := make([]byte, 0, 17+1+4+len(coded))
	region = appendBE32(region, uint32(width))
	region = appendBE32(region, uint32(height))
	region = appendBE32(region, 0) // X.
	region = appendBE32(region, 0) // Y.
	region = append(region, byte(combOpReplace))
	region = append(region, 0x00) // Flags: GRTEMPLATE 0, TPGRON off.
	for _, p := range defaultRefinementAT {
		region = append(region, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	region = append(region, coded...)

	stream = append(stream, buildSegmentHeader(1, segTypeRefinementRegionImmediate, nil, uint32(len(region)))...)
	stream = append(stream, region...)

	out, err := decodeJBIG2(stream, nil)
	if err != nil {
		t.Fatalf("decodeJBIG2: %v", err)
	}
	if got := unpackJBIG2Output(t, out, width, height); !bytes.Equal(got, refinedTo.pix) {
		t.Error("the page does not hold the refinement region's content")
	}
}

func TestJBIG2TextRegionWithRefinedInstances(t *testing.T) {
	// A text region may correct an individual instance against the
	// dictionary's copy of that symbol - what a lossy encoder does where
	// substituting a similar glyph would be too visible. This drives the
	// decoder with a hand-built stream placing three instances, the
	// middle one refined into a different shape.
	symbols := jbig2TestSymbols()
	symbol := symbols[4] // 6 wide, 7 tall.
	refined := perturb(symbol, 3, 17)

	p := &textRegionParams{
		width: 40, height: 20,
		stripSize: 1, refCorner: refCornerTopLeft, combOp: combOpOr,
		numInstances:   3,
		refine:         true,
		refineTemplate: 0,
		refineAT:       defaultRefinementAT,
	}

	cx := newTextContexts(symbolCodeLength(len(symbols)))
	enc := newMQEncoder()
	enc.encodeInt(cx.iadt, 0)
	enc.encodeInt(cx.iadt, 4) // The strip's row.
	enc.encodeInt(cx.iafs, 2) // The first instance's column.

	xs := []int{2, 12, 24}
	curS := xs[0]
	for i, x := range xs {
		if i > 0 {
			enc.encodeInt(cx.iads, x-curS)
			curS = x
		}
		enc.encodeIAID(cx.iaid, 4)

		// Only the middle instance is refined; the others carry a zero
		// refinement flag and are drawn as the dictionary has them.
		if i != 1 {
			enc.encodeInt(cx.iari, 0)
		} else {
			enc.encodeInt(cx.iari, 1)
			// Same size, no offset: RDW, RDH, RDX, RDY all zero.
			for _, ctx := range []arithIntCtx{cx.iardw, cx.iardh, cx.iardx, cx.iardy} {
				enc.encodeInt(ctx, 0)
			}
			encodeRefinementBitmap(enc, cx.refinementContexts(0), symbol.width, symbol.height,
				0, defaultRefinementAT, symbol, 0, 0, refined)
		}
		curS += symbol.width - 1
	}
	enc.encodeOOB(cx.iads)

	decCx := newTextContexts(symbolCodeLength(len(symbols)))
	region, err := decodeTextRegionBitmap(newMQDecoder(enc.flush()), p, symbols, decCx)
	if err != nil {
		t.Fatalf("decodeTextRegionBitmap: %v", err)
	}

	want := newJBIG2Bitmap(p.width, p.height, 0)
	for i, x := range xs {
		drawn := symbol
		if i == 1 {
			drawn = refined
		}
		want.composite(drawn, x, 4, combOpOr)
	}
	if !bytes.Equal(region.pix, want.pix) {
		t.Error("the refined instance was not drawn as its refinement, or the unrefined ones were")
	}
}

func TestJBIG2SymbolDictionaryAggregateSymbol(t *testing.T) {
	// The other aggregate form: a new symbol built as a little collage
	// of several existing ones, coded as a miniature text region drawn
	// onto the new symbol's own bitmap. Two imported symbols are placed
	// side by side to make one wider symbol.
	//
	// This shares the dictionary's coded stream and contexts with that
	// text region, which is the part worth testing - the two procedures
	// have to agree on exactly whose contexts are whose.
	parts := jbig2TestSymbols()[:2] // Both 4 rows tall, 3 and 4 wide.
	const newWidth, newHeight = 9, 4

	enc := newMQEncoder()
	iadh, iadw, iaex, iaai := newArithIntCtx(), newArithIntCtx(), newArithIntCtx(), newArithIntCtx()
	textCx := newTextContexts(symbolCodeLength(len(parts) + 1))

	enc.encodeInt(iadh, newHeight)
	enc.encodeInt(iadw, newWidth)
	enc.encodeInt(iaai, 2) // Two instances: a collage, not a refinement.

	// The miniature text region: one strip at row 0 holding both parts,
	// each with a zero refinement flag (SBREFINE is always on in this
	// mode, so every instance carries the flag even when unrefined).
	xs := []int{0, 5}
	enc.encodeInt(textCx.iadt, 0)
	enc.encodeInt(textCx.iadt, 0)
	enc.encodeInt(textCx.iafs, xs[0])
	curS := xs[0]
	for i, x := range xs {
		if i > 0 {
			enc.encodeInt(textCx.iads, x-curS)
			curS = x
		}
		enc.encodeIAID(textCx.iaid, i)
		enc.encodeInt(textCx.iari, 0)
		curS += parts[i].width - 1
	}
	enc.encodeOOB(textCx.iads)

	enc.encodeOOB(iadw)
	enc.encodeInt(iaex, 0)
	enc.encodeInt(iaex, 3)
	coded := enc.flush()

	data := []byte{0x00, 0x02} // SDREFAGG set.
	for _, p := range defaultATPixels[0] {
		data = append(data, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	for _, p := range defaultRefinementAT {
		data = append(data, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	data = appendBE32(data, 3) // SDNUMEXSYMS.
	data = appendBE32(data, 1) // SDNUMNEWSYMS.
	data = append(data, coded...)

	got, err := decodeSymbolDictSegment(data, parts)
	if err != nil {
		t.Fatalf("decodeSymbolDictSegment: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("dictionary exported %d symbols, want 3", len(got))
	}

	want := newJBIG2Bitmap(newWidth, newHeight, 0)
	for i, x := range xs {
		want.composite(parts[i], x, 0, combOpOr)
	}
	if !bytes.Equal(got[2].pix, want.pix) {
		t.Error("the aggregate symbol is not the collage of the two symbols it was built from")
	}
}

func TestJBIG2SymbolDictionaryRefinedSymbol(t *testing.T) {
	// A symbol dictionary may define a new symbol as a refinement of one
	// it already holds - "the same glyph, but bolder". This builds a
	// dictionary that imports one symbol and defines a second as that
	// symbol refined, then checks it decodes to the refined shape rather
	// than the original.
	//
	// The imported symbol is what makes this well-formed: with SDREFAGG
	// set, *every* new symbol is coded as a refinement or aggregate of
	// symbols the dictionary already holds, so the first one has to
	// refine something that came from elsewhere.
	base := jbig2TestSymbols()[4] // 6 wide, 7 tall.
	refined := perturb(base, 4, 23)
	inputSymbols := []*jbig2Bitmap{base}

	enc := newMQEncoder()
	iadh, iadw, iaex, iaai := newArithIntCtx(), newArithIntCtx(), newArithIntCtx(), newArithIntCtx()
	textCx := newTextContexts(symbolCodeLength(len(inputSymbols) + 1))

	enc.encodeInt(iadh, refined.height)
	enc.encodeInt(iadw, refined.width)
	// One aggregate instance: refine imported symbol 0, at no offset.
	enc.encodeInt(iaai, 1)
	enc.encodeIAID(textCx.iaid, 0)
	enc.encodeInt(textCx.iardx, 0)
	enc.encodeInt(textCx.iardy, 0)
	encodeRefinementBitmap(enc, textCx.refinementContexts(0), refined.width, refined.height,
		0, defaultRefinementAT, base, 0, 0, refined)
	enc.encodeOOB(iadw)
	// Export both the imported symbol and the new one.
	enc.encodeInt(iaex, 0)
	enc.encodeInt(iaex, 2)

	coded := enc.flush()

	// Flags: SDHUFF clear, SDREFAGG set, SDTEMPLATE 0, SDRTEMPLATE 0.
	data := []byte{0x00, 0x02}
	for _, p := range defaultATPixels[0] {
		data = append(data, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	for _, p := range defaultRefinementAT {
		data = append(data, byte(int8(p.dx)), byte(int8(p.dy)))
	}
	data = appendBE32(data, 2) // SDNUMEXSYMS: the imported one and the new one.
	data = appendBE32(data, 1) // SDNUMNEWSYMS.
	data = append(data, coded...)

	got, err := decodeSymbolDictSegment(data, inputSymbols)
	if err != nil {
		t.Fatalf("decodeSymbolDictSegment: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("dictionary exported %d symbols, want 2", len(got))
	}
	if !bytes.Equal(got[0].pix, base.pix) {
		t.Error("the dictionary's first symbol is not the one encoded")
	}
	if !bytes.Equal(got[1].pix, refined.pix) {
		t.Error("the dictionary's refined symbol is not its refinement")
	}
}
