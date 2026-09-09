package filter

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// The tests in this file round-trip a whole symbol-mode JBIG2 stream:
// build a handful of small glyph bitmaps, encode them as a symbol
// dictionary segment, encode a text region segment placing instances of
// them, and require the decoded page to be pixel-for-pixel what placing
// those same glyphs at those same positions produces.
//
// That is a stronger check than it looks. The expected page is built
// here by plain compositing (paintSymbols below), with no shared code
// with the text region decoder at all - so a decoder that misread a
// position, a symbol ID, or a strip boundary produces a page this
// comparison catches, even though the encoder and decoder do share the
// coding tables between them.

// jbig2TestSymbols returns a small set of distinguishable glyph-like
// bitmaps, ordered by non-decreasing height as encodeSymbolDictSegment
// requires. Each shape is asymmetric in both axes so that a symbol drawn
// flipped, transposed, or at the wrong offset does not accidentally
// match.
func jbig2TestSymbols() []*jbig2Bitmap {
	shapes := []struct {
		width, height int
		rows          []string
	}{
		{3, 4, []string{"##.", "#..", "#..", "#.."}},
		{4, 4, []string{"####", "...#", "..#.", ".#.."}},
		{5, 6, []string{"#....", "##...", "#.#..", "#..#.", "#...#", "#####"}},
		{2, 6, []string{"#.", "#.", "#.", "#.", "#.", "##"}},
		{6, 7, []string{"######", "#.....", "#.....", "####..", "#.....", "#.....", "#....."}},
	}

	symbols := make([]*jbig2Bitmap, len(shapes))
	for i, s := range shapes {
		b := newJBIG2Bitmap(s.width, s.height, 0)
		for y, row := range s.rows {
			for x, c := range row {
				if c == '#' {
					b.set(x, y, 1)
				}
			}
		}
		symbols[i] = b
	}
	return symbols
}

// paintSymbols builds the page a text region is expected to produce, by
// simply OR-ing each instance's symbol onto a blank bitmap at its
// top-left corner - deliberately independent of jbig2text.go's own
// placement arithmetic.
func paintSymbols(width, height int, symbols []*jbig2Bitmap, instances []textInstance) []byte {
	page := newJBIG2Bitmap(width, height, 0)
	for _, inst := range instances {
		page.composite(symbols[inst.symbol], inst.x, inst.y, combOpOr)
	}
	return page.pix
}

// encodeSymbolModeStream builds a complete two-segment JBIG2 stream: a
// symbol dictionary followed by a text region referring to it, the shape
// a real symbol-mode scan takes once its globals are prepended.
func encodeSymbolModeStream(width, height int, symbols []*jbig2Bitmap, instances []textInstance) []byte {
	stream := encodeSymbolDictSegment(0, symbols, 0)
	return append(stream, encodeTextRegionSegment(1, []uint32{0}, width, height, symbols, instances, 0, 0, combOpOr)...)
}

func TestJBIG2SymbolTextRoundTrip(t *testing.T) {
	const width, height = 60, 40
	symbols := jbig2TestSymbols()
	instances := []textInstance{
		// Three "lines" of text, each a strip in the coded form: several
		// instances at one y, left to right, with varying gaps.
		{symbol: 0, x: 1, y: 2},
		{symbol: 2, x: 6, y: 2},
		{symbol: 1, x: 14, y: 2},
		{symbol: 0, x: 30, y: 2},
		{symbol: 4, x: 0, y: 15},
		{symbol: 3, x: 9, y: 15},
		{symbol: 2, x: 20, y: 15},
		{symbol: 1, x: 50, y: 28},
	}

	stream := encodeSymbolModeStream(width, height, symbols, instances)
	out, err := decodeJBIG2(stream, nil)
	if err != nil {
		t.Fatalf("decodeJBIG2: %v", err)
	}

	got := unpackJBIG2Output(t, out, width, height)
	want := paintSymbols(width, height, symbols, instances)
	if !bytes.Equal(got, want) {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("pixel (%d, %d) decoded as %d, want %d", i%width, i/width, got[i], want[i])
			}
		}
	}
}

func TestJBIG2SymbolTextRoundTripRandom(t *testing.T) {
	// Many instances at random positions, in the y-then-x order the
	// coding requires - enough strips and enough gap sizes to exercise
	// the integer coder's ranges and the per-strip OOB terminator far
	// past what a hand-written case covers.
	const width, height = 300, 200
	symbols := jbig2TestSymbols()
	rng := rand.New(rand.NewSource(3))

	var instances []textInstance
	for y := 0; y < height-10; y += 1 + rng.Intn(9) {
		x := rng.Intn(20)
		for x < width-10 {
			instances = append(instances, textInstance{symbol: rng.Intn(len(symbols)), x: x, y: y})
			x += 6 + rng.Intn(40)
		}
	}

	stream := encodeSymbolModeStream(width, height, symbols, instances)
	out, err := decodeJBIG2(stream, nil)
	if err != nil {
		t.Fatalf("decodeJBIG2: %v", err)
	}
	if got, want := unpackJBIG2Output(t, out, width, height), paintSymbols(width, height, symbols, instances); !bytes.Equal(got, want) {
		t.Fatalf("decoded page does not match the %d placed symbol instances", len(instances))
	}
}

func TestJBIG2SymbolTextAllTemplates(t *testing.T) {
	// The symbol dictionary's GBTEMPLATE choice affects only how each
	// symbol bitmap is coded, so every template must produce the same
	// page - real encoders pick among them by which compresses best
	// (the sample scan this package was developed against uses
	// GBTEMPLATE 2).
	const width, height = 40, 30
	symbols := jbig2TestSymbols()
	instances := []textInstance{
		{symbol: 4, x: 2, y: 1},
		{symbol: 1, x: 12, y: 1},
		{symbol: 3, x: 20, y: 12},
	}
	want := paintSymbols(width, height, symbols, instances)

	for template := 0; template < 4; template++ {
		stream := encodeSymbolDictSegment(0, symbols, template)
		stream = append(stream, encodeTextRegionSegment(1, []uint32{0}, width, height, symbols, instances, 0, 0, combOpOr)...)

		out, err := decodeJBIG2(stream, nil)
		if err != nil {
			t.Fatalf("template %d: decodeJBIG2: %v", template, err)
		}
		if got := unpackJBIG2Output(t, out, width, height); !bytes.Equal(got, want) {
			t.Errorf("template %d: decoded page does not match the placed symbol instances", template)
		}
	}
}

func TestJBIG2SymbolsFromGlobalsStream(t *testing.T) {
	// A PDF's /JBIG2Globals stream carries segments - in practice a
	// symbol dictionary shared by several images - that the image's own
	// stream then refers to by segment number. The two streams are
	// decoded as one continuing sequence, so a text region in the second
	// can name a dictionary defined in the first.
	const width, height = 50, 20
	symbols := jbig2TestSymbols()
	instances := []textInstance{
		{symbol: 2, x: 3, y: 4},
		{symbol: 0, x: 15, y: 4},
	}

	globals := encodeSymbolDictSegment(0, symbols, 0)
	page := encodeTextRegionSegment(1, []uint32{0}, width, height, symbols, instances, 0, 0, combOpOr)

	out, err := decodeJBIG2(page, globals)
	if err != nil {
		t.Fatalf("decodeJBIG2: %v", err)
	}
	if got, want := unpackJBIG2Output(t, out, width, height), paintSymbols(width, height, symbols, instances); !bytes.Equal(got, want) {
		t.Error("decoded page does not match the symbols placed from the globals stream")
	}

	// Without the globals, the same image stream has no symbols to draw
	// with and must say so rather than drawing a blank page.
	if _, err := decodeJBIG2(page, nil); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("decoding without globals: got %v, want an error wrapping %v", err, pdferror.ErrMalformed)
	}
}

// stubStreamResolver stands in for internal/parser.Document in tests of
// the /JBIG2Globals plumbing: it returns fixed bytes for whatever object
// it is handed, or an error, without needing a real document.
type stubStreamResolver struct {
	data []byte
	err  error
}

func (r stubStreamResolver) DecodeReferencedStream(syntax.Object) ([]byte, error) {
	return r.data, r.err
}

func TestJBIG2GlobalsThroughDecodeWith(t *testing.T) {
	// The same globals-stream case as TestJBIG2SymbolsFromGlobalsStream,
	// but reached the way a real document does: through the filter
	// dispatch, with /JBIG2Globals named in /DecodeParms and fetched via
	// a StreamResolver.
	const width, height = 50, 20
	symbols := jbig2TestSymbols()
	instances := []textInstance{{symbol: 2, x: 3, y: 4}, {symbol: 0, x: 15, y: 4}}

	globals := encodeSymbolDictSegment(0, symbols, 0)
	page := encodeTextRegionSegment(1, []uint32{0}, width, height, symbols, instances, 0, 0, combOpOr)

	dict := syntax.Dictionary{
		"Filter": syntax.Name("JBIG2Decode"),
		// A real file names an indirect reference here; the resolver is
		// what turns whatever it is into bytes, so any object will do.
		"DecodeParms": syntax.Dictionary{"JBIG2Globals": syntax.Reference{Number: 5}},
	}

	out, err := DecodeWith(dict, page, stubStreamResolver{data: globals})
	if err != nil {
		t.Fatalf("DecodeWith: %v", err)
	}
	if got, want := unpackJBIG2Output(t, out, width, height), paintSymbols(width, height, symbols, instances); !bytes.Equal(got, want) {
		t.Error("decoded page does not match the symbols placed from the resolved globals stream")
	}

	// Without a resolver there is no way to reach the globals stream, and
	// saying so beats decoding an image whose symbols are missing.
	if _, err := Decode(dict, page); !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Decode with no resolver: got %v, want an error wrapping %v", err, pdferror.ErrUnsupported)
	}

	// A resolver that cannot produce the stream fails the whole decode
	// rather than falling back to decoding without it.
	resolverErr := errors.New("no such object")
	if _, err := DecodeWith(dict, page, stubStreamResolver{err: resolverErr}); !errors.Is(err, resolverErr) {
		t.Fatalf("DecodeWith with a failing resolver: got %v, want it to wrap %v", err, resolverErr)
	}

	// An absent or null /JBIG2Globals is the common case and must not
	// reach the resolver at all - here the image carries its own
	// dictionary, so it decodes standalone.
	standalone := append(encodeSymbolDictSegment(0, symbols, 0), page...)
	for _, parms := range []syntax.Object{syntax.Dictionary{}, syntax.Dictionary{"JBIG2Globals": syntax.Null{}}} {
		d := syntax.Dictionary{"Filter": syntax.Name("JBIG2Decode"), "DecodeParms": parms}
		if _, err := Decode(d, standalone); err != nil {
			t.Errorf("Decode with /DecodeParms %v: %v", parms, err)
		}
	}
}

func TestJBIG2SymbolDictionaryChaining(t *testing.T) {
	// A symbol dictionary may import another's exported symbols and
	// re-export them alongside its own, which is how a document's
	// dictionaries accumulate across segments. The text region then sees
	// one combined list, in referred-to order.
	const width, height = 40, 20
	all := jbig2TestSymbols()
	first, second := all[:2], all[2:]

	// The second dictionary imports the first's two symbols, so IDs 0-1
	// are the first dictionary's and 2-4 the second's own.
	stream := encodeSymbolDictSegment(0, first, 0)
	stream = append(stream, encodeSymbolDictImporting(1, []uint32{0}, len(first), second, 0)...)

	instances := []textInstance{
		{symbol: 0, x: 1, y: 1},
		{symbol: 4, x: 10, y: 1},
		{symbol: 2, x: 25, y: 2},
	}
	stream = append(stream, encodeTextRegionSegment(2, []uint32{1}, width, height, all, instances, 0, 0, combOpOr)...)

	out, err := decodeJBIG2(stream, nil)
	if err != nil {
		t.Fatalf("decodeJBIG2: %v", err)
	}
	if got, want := unpackJBIG2Output(t, out, width, height), paintSymbols(width, height, all, instances); !bytes.Equal(got, want) {
		t.Error("decoded page does not match the symbols placed from a chained dictionary")
	}
}

func TestJBIG2TextRegionRefCornersAndTransposed(t *testing.T) {
	// Every reference corner and both transposed settings place a symbol
	// somewhere different for the same coded (S, T). This drives
	// decodeTextRegionBitmap directly, since this package's encoder only
	// ever emits the untransposed, top-left form, and checks the placed
	// symbol's actual bounding box against what T.88's corner definition
	// requires.
	symbol := jbig2TestSymbols()[2] // 5 wide, 6 tall.
	symbols := []*jbig2Bitmap{symbol}

	const s, tt = 10, 12
	tests := []struct {
		name       string
		refCorner  int
		transposed bool
		wantX      int
		wantY      int
	}{
		// Untransposed: the left edge sits at S either way, because for a
		// right-hand corner the standard advances S past the symbol
		// before placing it. Only the vertical placement differs.
		{"top-left", refCornerTopLeft, false, s, tt},
		{"top-right", refCornerTopRight, false, s, tt},
		{"bottom-left", refCornerBottomLeft, false, s, tt - symbol.height + 1},
		{"bottom-right", refCornerBottomRight, false, s, tt - symbol.height + 1},
		// Transposed: the axes exchange roles, so the top edge sits at S
		// and the corner decides the horizontal placement.
		{"transposed top-left", refCornerTopLeft, true, tt, s},
		{"transposed bottom-left", refCornerBottomLeft, true, tt, s},
		{"transposed top-right", refCornerTopRight, true, tt - symbol.width + 1, s},
		{"transposed bottom-right", refCornerBottomRight, true, tt - symbol.width + 1, s},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &textRegionParams{
				width: 40, height: 40,
				stripSize: 1, refCorner: tc.refCorner, transposed: tc.transposed,
				combOp: combOpOr, numInstances: 1,
			}

			// One instance: initial strip position 0, one strip at T, one
			// symbol at S, then the strip's terminating OOB.
			enc := newMQEncoder()
			iadt, iafs, iads := newArithIntCtx(), newArithIntCtx(), newArithIntCtx()
			iaid := newArithIAIDCtx(symbolCodeLength(1))
			enc.encodeInt(iadt, 0)
			enc.encodeInt(iadt, tt)
			enc.encodeInt(iafs, s)
			enc.encodeIAID(iaid, 0)
			enc.encodeOOB(iads)

			region, err := decodeTextRegionBitmap(newMQDecoder(enc.flush()), p, symbols)
			if err != nil {
				t.Fatalf("decodeTextRegionBitmap: %v", err)
			}

			want := newJBIG2Bitmap(p.width, p.height, 0)
			want.composite(symbol, tc.wantX, tc.wantY, combOpOr)
			if !bytes.Equal(region.pix, want.pix) {
				t.Errorf("symbol was not placed with its %s corner at (%d, %d)", tc.name, s, tt)
			}
		})
	}
}

func TestJBIG2TextRegionMultiRowStrips(t *testing.T) {
	// A strip taller than one row codes each instance's exact row as an
	// extra per-instance value, which is how a scan whose baseline
	// wobbles slightly still groups a line of text into one strip. The
	// sample scan this package was developed against uses 4- and 8-row
	// strips.
	symbols := jbig2TestSymbols()
	for _, stripSize := range []int{2, 4, 8} {
		p := &textRegionParams{
			width: 60, height: 40,
			stripSize: stripSize, refCorner: refCornerTopLeft,
			combOp: combOpOr, numInstances: 3,
		}

		// One strip based at row 8, with instances 0, 1 and stripSize-1
		// rows into it.
		const stripBase = 8
		offsets := []int{0, 1, stripSize - 1}
		xs := []int{2, 14, 30}

		enc := newMQEncoder()
		iadt, iafs, iads, iait := newArithIntCtx(), newArithIntCtx(), newArithIntCtx(), newArithIntCtx()
		iaid := newArithIAIDCtx(symbolCodeLength(len(symbols)))
		enc.encodeInt(iadt, 0)
		enc.encodeInt(iadt, stripBase/stripSize)
		enc.encodeInt(iafs, xs[0])
		curS := xs[0]
		for i := range xs {
			if i > 0 {
				enc.encodeInt(iads, xs[i]-curS)
				curS = xs[i]
			}
			enc.encodeInt(iait, offsets[i])
			enc.encodeIAID(iaid, i)
			curS += symbols[i].width - 1
		}
		enc.encodeOOB(iads)

		region, err := decodeTextRegionBitmap(newMQDecoder(enc.flush()), p, symbols)
		if err != nil {
			t.Fatalf("strip size %d: decodeTextRegionBitmap: %v", stripSize, err)
		}

		want := newJBIG2Bitmap(p.width, p.height, 0)
		for i := range xs {
			want.composite(symbols[i], xs[i], stripBase+offsets[i], combOpOr)
		}
		if !bytes.Equal(region.pix, want.pix) {
			t.Errorf("strip size %d: symbols were not placed at their per-instance rows", stripSize)
		}
	}
}

func TestJBIG2SymbolTextMalformedStreams(t *testing.T) {
	const width, height = 40, 20
	symbols := jbig2TestSymbols()
	instances := []textInstance{{symbol: 1, x: 2, y: 3}}

	// Offsets within a text region segment built by
	// encodeTextRegionSegment: an 11-byte segment header plus one
	// referred-to segment number, then the 17-byte region information
	// field, then the two flags bytes.
	const (
		textHeaderLen  = 12
		textFlagsHigh  = textHeaderLen + 17
		textFlagsLow   = textFlagsHigh + 1
		textInstOffset = textFlagsLow + 1
	)

	tests := []struct {
		name    string
		wantErr error
		edit    func(dict, text []byte) []byte
	}{
		{
			name:    "Huffman-coded symbol dictionary",
			wantErr: pdferror.ErrUnsupported,
			edit: func(dict, text []byte) []byte {
				// The dictionary's flags are the two bytes after its
				// 11-byte segment header; SDHUFF is the low byte's bit 0.
				dict[12] |= 0x01
				return append(dict, text...)
			},
		},
		{
			name:    "refinement/aggregate symbol dictionary",
			wantErr: pdferror.ErrUnsupported,
			edit: func(dict, text []byte) []byte {
				dict[12] |= 0x02 // SDREFAGG.
				return append(dict, text...)
			},
		},
		{
			name:    "Huffman-coded text region",
			wantErr: pdferror.ErrUnsupported,
			edit: func(dict, text []byte) []byte {
				text[textFlagsLow] |= 0x01 // SBHUFF.
				return append(dict, text...)
			},
		},
		{
			name:    "refined text region",
			wantErr: pdferror.ErrUnsupported,
			edit: func(dict, text []byte) []byte {
				text[textFlagsLow] |= 0x02 // SBREFINE.
				return append(dict, text...)
			},
		},
		{
			name:    "text region with no symbol dictionary",
			wantErr: pdferror.ErrMalformed,
			edit: func(dict, text []byte) []byte {
				return text // The dictionary it refers to is simply absent.
			},
		},
		{
			name:    "text region declaring more instances than it codes",
			wantErr: pdferror.ErrMalformed,
			edit: func(dict, text []byte) []byte {
				text[textInstOffset+3] += 4
				return append(dict, text...)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dict := encodeSymbolDictSegment(0, symbols, 0)
			text := encodeTextRegionSegment(1, []uint32{0}, width, height, symbols, instances, 0, 0, combOpOr)
			if _, err := decodeJBIG2(tc.edit(dict, text), nil); !errors.Is(err, tc.wantErr) {
				t.Fatalf("decodeJBIG2: got %v, want an error wrapping %v", err, tc.wantErr)
			}
		})
	}
}

func TestJBIG2SymbolCodeLength(t *testing.T) {
	// ceil(log2(n)), except that a single-symbol dictionary still costs
	// one bit - see symbolCodeLength's doc comment on T.88's erratum.
	tests := []struct{ symbols, want int }{
		{1, 1}, {2, 1}, {3, 2}, {4, 2}, {5, 3}, {8, 3}, {9, 4}, {255, 8}, {256, 8}, {257, 9},
	}
	for _, tc := range tests {
		if got := symbolCodeLength(tc.symbols); got != tc.want {
			t.Errorf("symbolCodeLength(%d) = %d, want %d", tc.symbols, got, tc.want)
		}
	}
}
