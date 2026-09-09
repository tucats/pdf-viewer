package filter

import (
	"bytes"
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// jbig2TestPattern builds a width x height one-byte-per-pixel bitmap
// (1 = JBIG2 foreground/black) from a small set of named shapes. The
// shapes are chosen to exercise different parts of the context modeling:
// solid areas keep a single context very confident, fine detail keeps
// many contexts near 50/50, and "bands" produces long runs of rows
// identical to the row above, which is what typical prediction (TPGDON)
// exists to compress.
func jbig2TestPattern(shape string, width, height int) []byte {
	pix := make([]byte, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var on bool
			switch shape {
			case "blank":
				on = false
			case "solid":
				on = true
			case "bands":
				// Horizontal bands: many consecutive identical rows.
				on = (y/4)%2 == 0
			case "checker":
				// Every pixel differs from all four neighbors - close to
				// the worst case for a context-modeling coder.
				on = (x+y)%2 == 0
			case "diagonalsAndBlock":
				on = (x+y)%5 < 2 || (x > width/2 && y > height/2)
			case "border":
				on = x == 0 || y == 0 || x == width-1 || y == height-1
			default:
				panic("unknown jbig2 test pattern shape " + shape)
			}
			if on {
				pix[y*width+x] = 1
			}
		}
	}
	return pix
}

// unpackJBIG2Output reverses jbig2Bitmap.packInverted, turning a decoded
// JBIG2Decode result back into one byte per pixel using JBIG2's own
// convention (1 = black), so a test can compare it against the pixels it
// encoded. It fails the test if the output is not exactly the expected
// size, since a wrong length would otherwise show up as a confusing
// per-pixel mismatch.
func unpackJBIG2Output(t *testing.T, out []byte, width, height int) []byte {
	t.Helper()

	rowBytes := (width + 7) / 8
	if len(out) != rowBytes*height {
		t.Fatalf("decoded output is %d bytes, want %d (%d bytes per row x %d rows)", len(out), rowBytes*height, rowBytes, height)
	}

	pix := make([]byte, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			bit := (out[y*rowBytes+x/8] >> uint(7-x%8)) & 1
			// packInverted maps JBIG2 black (1) to a clear output bit.
			if bit == 0 {
				pix[y*width+x] = 1
			}
		}
	}
	return pix
}

func TestJBIG2ContextTemplatesMatchSpecifiedBitLayout(t *testing.T) {
	// This is the one property this package's round-trip tests
	// structurally cannot check. Round-tripping proves the encoder and
	// decoder agree with each other, but they share genericContextTemplate
	// - so if its bit ordering were a permutation of the one ITU-T T.88
	// actually specifies, every round trip here would still pass while
	// every real-world JBIG2 stream decoded to noise.
	//
	// So this test pins the ordering independently. T.88 numbers each
	// GBTEMPLATE's context bits in raster order, most significant bit
	// first: the top row's leftmost pixel down to the pixel immediately
	// left of the one being coded. The expected point lists below are that
	// ordering, read off the specification's own template figures with the
	// adaptive pixels at their default positions.
	tests := []struct {
		template int
		// perRow is how many context pixels each of the template's rows
		// contributes, top row first - the grouping cross-checked against
		// reusedContext below.
		perRow []int
		points []jbig2Point
	}{
		{
			template: 0,
			perRow:   []int{5, 7, 4},
			points: []jbig2Point{
				{-2, -2}, {-1, -2}, {0, -2}, {1, -2}, {2, -2},
				{-3, -1}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1}, {3, -1},
				{-4, 0}, {-3, 0}, {-2, 0}, {-1, 0},
			},
		},
		{
			template: 1,
			perRow:   []int{4, 6, 3},
			points: []jbig2Point{
				{-1, -2}, {0, -2}, {1, -2}, {2, -2},
				{-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1}, {3, -1},
				{-3, 0}, {-2, 0}, {-1, 0},
			},
		},
		{
			template: 2,
			perRow:   []int{3, 5, 2},
			points: []jbig2Point{
				{-1, -2}, {0, -2}, {1, -2},
				{-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1},
				{-2, 0}, {-1, 0},
			},
		},
		{
			template: 3,
			perRow:   []int{6, 4},
			points: []jbig2Point{
				{-3, -1}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1},
				{-4, 0}, {-3, 0}, {-2, 0}, {-1, 0},
			},
		},
	}

	for _, tc := range tests {
		got := genericContextTemplate(tc.template)
		if len(got) != len(tc.points) {
			t.Errorf("GBTEMPLATE %d: %d context pixels, want %d", tc.template, len(got), len(tc.points))
			continue
		}
		for i := range got {
			if got[i] != tc.points[i] {
				t.Errorf("GBTEMPLATE %d: context bit %d (counting from the most significant) is pixel %+v, want %+v",
					tc.template, i, got[i], tc.points[i])
			}
		}

		// Independent cross-check on the same ordering: T.88 fixes the
		// pseudo-context each template uses for its typical-prediction
		// (SLTP) decision, and those constants are only meaningful in the
		// bit layout above. Their widths must therefore match the total
		// number of context pixels, and the row grouping asserted above
		// must account for all of them.
		total := 0
		for _, n := range tc.perRow {
			total += n
		}
		if total != len(tc.points) {
			t.Errorf("GBTEMPLATE %d: per-row counts %v sum to %d, but the template has %d pixels", tc.template, tc.perRow, total, len(tc.points))
		}
		if reusedContext[tc.template] >= 1<<uint(len(tc.points)) {
			t.Errorf("GBTEMPLATE %d: typical-prediction context %#x does not fit in %d bits, so it cannot belong to this bit layout",
				tc.template, reusedContext[tc.template], len(tc.points))
		}
	}
}

func TestJBIG2GenericRegionRoundTrip(t *testing.T) {
	sizes := [][2]int{
		{1, 1},   // Degenerate: every context neighbor is out of bounds.
		{8, 8},   // Exactly one output byte per row.
		{13, 7},  // Width not a multiple of 8: packInverted pads each row.
		{37, 41}, // Both dimensions odd and unrelated to any byte boundary.
		{64, 64},
	}
	shapes := []string{"blank", "solid", "bands", "checker", "diagonalsAndBlock", "border"}

	for _, size := range sizes {
		width, height := size[0], size[1]
		for _, shape := range shapes {
			// Both typical-prediction settings must decode to the same
			// bitmap - that is the whole point of TPGDON being an encoder's
			// free choice.
			for _, tpgdon := range []bool{false, true} {
				want := jbig2TestPattern(shape, width, height)
				stream := EncodeJBIG2GenericRegion(width, height, want, tpgdon)

				out, err := decodeJBIG2(stream)
				if err != nil {
					t.Fatalf("%s %dx%d (tpgdon=%v): decodeJBIG2: %v", shape, width, height, tpgdon, err)
				}
				got := unpackJBIG2Output(t, out, width, height)
				if !bytes.Equal(got, want) {
					for i := range want {
						if got[i] != want[i] {
							t.Fatalf("%s %dx%d (tpgdon=%v): pixel (%d, %d) decoded as %d, want %d",
								shape, width, height, tpgdon, i%width, i/width, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

func TestJBIG2TypicalPredictionCompressesIdenticalRows(t *testing.T) {
	// Typical prediction's entire purpose is coding a row identical to the
	// one above it as a single bit instead of pixel by pixel. A tall image
	// of wide horizontal bands is almost entirely such rows, so enabling
	// it should shrink the stream noticeably; if this ever stops holding,
	// the TPGDON encode path has probably stopped actually being taken
	// (and the round-trip test above would still pass, since both paths
	// are required to decode identically).
	const width, height = 64, 256
	pix := jbig2TestPattern("bands", width, height)

	without := EncodeJBIG2GenericRegion(width, height, pix, false)
	with := EncodeJBIG2GenericRegion(width, height, pix, true)
	if len(with) >= len(without) {
		t.Errorf("typical prediction produced %d bytes, no smaller than the %d bytes without it", len(with), len(without))
	}
}

func TestJBIG2DecodeViaFilterDictionary(t *testing.T) {
	// The same round trip through this package's public entry point,
	// confirming "JBIG2Decode" is actually wired into decodeOne's filter
	// dispatch and not merely reachable from inside the package.
	const width, height = 24, 18
	want := jbig2TestPattern("diagonalsAndBlock", width, height)
	stream := EncodeJBIG2GenericRegion(width, height, want, false)

	out, err := Decode(syntax.Dictionary{"Filter": syntax.Name("JBIG2Decode")}, stream)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := unpackJBIG2Output(t, out, width, height); !bytes.Equal(got, want) {
		t.Errorf("Decode round trip did not reproduce the encoded bitmap")
	}
}

func TestJBIG2MultipleRegionsComposite(t *testing.T) {
	// A scanner emitting a page as horizontal strips produces several
	// generic region segments, each positioned at its own Y offset, in one
	// stream. The decoder has to size the page to hold all of them and
	// composite each at its declared position.
	const width, stripHeight, strips = 20, 6, 3

	var stream []byte
	want := make([]byte, width*stripHeight*strips)
	for s := 0; s < strips; s++ {
		// A distinct pattern per strip, so a strip landing at the wrong Y
		// offset (or overwriting another) cannot go unnoticed.
		strip := make([]byte, width*stripHeight)
		for i := range strip {
			if (i/width+i%width+s)%3 == 0 {
				strip[i] = 1
			}
		}
		copy(want[s*width*stripHeight:], strip)
		stream = append(stream, encodeGenericRegionSegment(width, stripHeight, strip, false, 0, s*stripHeight, combOpOr)...)
	}

	out, err := decodeJBIG2(stream)
	if err != nil {
		t.Fatalf("decodeJBIG2: %v", err)
	}
	got := unpackJBIG2Output(t, out, width, stripHeight*strips)
	if !bytes.Equal(got, want) {
		t.Errorf("compositing three stacked strips did not reproduce the whole page")
	}
}

func TestJBIG2IgnoresSegmentsItDoesNotNeed(t *testing.T) {
	// Page info, end-of-page and end-of-file segments carry nothing a
	// generic-region bitmap needs, so they must be skipped (using their
	// own declared data length) rather than rejected or misparsed. Real
	// PDF-embedded streams routinely contain them.
	const width, height = 16, 12
	want := jbig2TestPattern("border", width, height)

	const (
		segTypePageInfo   = 48
		segTypeEndOfPage  = 49
		segTypeEndOfFile  = 51
		pageInfoDataBytes = 19
	)

	var stream []byte
	stream = append(stream, buildSegmentHeader(0, segTypePageInfo, pageInfoDataBytes)...)
	stream = append(stream, make([]byte, pageInfoDataBytes)...)
	stream = append(stream, EncodeJBIG2GenericRegion(width, height, want, false)...)
	stream = append(stream, buildSegmentHeader(2, segTypeEndOfPage, 0)...)
	stream = append(stream, buildSegmentHeader(3, segTypeEndOfFile, 0)...)

	out, err := decodeJBIG2(stream)
	if err != nil {
		t.Fatalf("decodeJBIG2: %v", err)
	}
	if got := unpackJBIG2Output(t, out, width, height); !bytes.Equal(got, want) {
		t.Errorf("surrounding the region with ignorable segments changed the decoded bitmap")
	}
}

// TestJBIG2UnsupportedAndMalformedStreams describes each bad stream as "a
// valid one, except for this field", rather than hand-assembling every
// byte. The offsets the edits use are stable by construction:
// buildSegmentHeader emits 11 bytes, followed by the 17-byte region
// information field, followed by the generic region flags byte and then
// the AT pixel pairs.
func TestJBIG2UnsupportedAndMalformedStreams(t *testing.T) {
	const (
		headerLen     = 11
		regionInfoLen = 17
		flagsOffset   = headerLen + regionInfoLen
		atOffset      = flagsOffset + 1
		// Within the segment header: 4-byte number, then the flags byte
		// whose low 6 bits are the segment type.
		segmentTypeOffset = 4
		// Within the region information field: width, height, X, Y.
		regionWidthOffset = headerLen
	)

	tests := []struct {
		name    string
		wantErr error
		edit    func(stream []byte) []byte
	}{
		{
			name:    "MMR coding",
			wantErr: pdferror.ErrUnsupported,
			edit: func(s []byte) []byte {
				s[flagsOffset] |= 0x01 // MMR bit.
				return s
			},
		},
		{
			name:    "non-default AT pixel",
			wantErr: pdferror.ErrUnsupported,
			edit: func(s []byte) []byte {
				s[atOffset] = 0x7F // AT1's dx, normally +3.
				return s
			},
		},
		{
			name:    "symbol dictionary segment",
			wantErr: pdferror.ErrUnsupported,
			edit: func(s []byte) []byte {
				// Keep the type's other flag bits, replace the type itself.
				s[segmentTypeOffset] = s[segmentTypeOffset]&^0x3F | byte(segTypeSymbolDictionary)
				return s
			},
		},
		{
			name:    "text region segment",
			wantErr: pdferror.ErrUnsupported,
			edit: func(s []byte) []byte {
				s[segmentTypeOffset] = s[segmentTypeOffset]&^0x3F | byte(segTypeTextRegionImmediate)
				return s
			},
		},
		{
			name:    "refinement region segment",
			wantErr: pdferror.ErrUnsupported,
			edit: func(s []byte) []byte {
				s[segmentTypeOffset] = s[segmentTypeOffset]&^0x3F | byte(segTypeRefinementRegionImmediate)
				return s
			},
		},
		{
			name:    "unknown segment data length",
			wantErr: pdferror.ErrUnsupported,
			edit: func(s []byte) []byte {
				// The data length is the segment header's last 4 bytes.
				for i := headerLen - 4; i < headerLen; i++ {
					s[i] = 0xFF
				}
				return s
			},
		},
		{
			name:    "zero region width",
			wantErr: pdferror.ErrMalformed,
			edit: func(s []byte) []byte {
				for i := regionWidthOffset; i < regionWidthOffset+4; i++ {
					s[i] = 0
				}
				return s
			},
		},
		{
			name:    "region larger than the pixel limit",
			wantErr: pdferror.ErrUnsupported,
			edit: func(s []byte) []byte {
				// Width and height each just inside the per-axis limit, but
				// wildly over the total-pixel limit when multiplied.
				for i, b := range []byte{0x00, 0x08, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00} {
					s[regionWidthOffset+i] = b
				}
				return s
			},
		},
		{
			name:    "declared data length runs past the end",
			wantErr: pdferror.ErrMalformed,
			edit: func(s []byte) []byte {
				s[headerLen-1]++ // Data length one byte longer than reality.
				return s
			},
		},
		{
			name:    "truncated segment header",
			wantErr: pdferror.ErrMalformed,
			edit:    func(s []byte) []byte { return s[:3] },
		},
		{
			name:    "truncated region information field",
			wantErr: pdferror.ErrMalformed,
			edit: func(s []byte) []byte {
				// Keep the header, cut the region data short, and shrink the
				// declared length to match so the truncation is caught by
				// the region parser rather than the length check.
				s[headerLen-1] = 5
				return s[:headerLen+5]
			},
		},
		{
			name:    "no generic region segment at all",
			wantErr: pdferror.ErrMalformed,
			edit: func(s []byte) []byte {
				const segTypeEndOfFile = 51
				return buildSegmentHeader(0, segTypeEndOfFile, 0)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const width, height = 12, 9
			stream := tc.edit(EncodeJBIG2GenericRegion(width, height, jbig2TestPattern("border", width, height), false))
			_, err := decodeJBIG2(stream)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("decodeJBIG2: got %v, want an error wrapping %v", err, tc.wantErr)
			}
		})
	}
}

func TestJBIG2RegionPositionedPastPageLimit(t *testing.T) {
	// A small region declaring an enormous origin must be rejected rather
	// than allocating the page bitmap that would be needed to hold it: the
	// position is bounded per axis, and the page extent it implies is
	// bounded again before being used as an allocation size.
	stream := EncodeJBIG2GenericRegion(8, 8, jbig2TestPattern("solid", 8, 8), false)
	// The region information field's Y location is its bytes 12-15, which
	// follow the 11-byte segment header.
	const yOffset = 11 + 12
	for i, b := range []byte{0x7F, 0xFF, 0xFF, 0xF0} {
		stream[yOffset+i] = b
	}

	if _, err := decodeJBIG2(stream); err == nil {
		t.Fatal("decodeJBIG2 accepted a region positioned far outside any plausible page")
	}
}

func TestJBIG2DecodeTruncatedCodedData(t *testing.T) {
	// Arithmetic-coded data cut short must not panic or hang: the MQ
	// decoder pads a short stream with an endless run of 0xFF (see
	// mqDecoder.byteAt), so the pixels simply come out wrong from the
	// truncation point onward. The bitmap's declared size still bounds how
	// much work is done, so this returns a full-size (if wrong) result
	// rather than an error.
	const width, height = 32, 32
	stream := EncodeJBIG2GenericRegion(width, height, jbig2TestPattern("diagonalsAndBlock", width, height), false)

	for cut := 1; cut < 12; cut++ {
		if cut >= len(stream) {
			break
		}
		truncated := make([]byte, len(stream)-cut)
		copy(truncated, stream)
		// Shrink the declared segment data length to match, so this
		// exercises short *coded data* rather than the length check.
		truncated[10] -= byte(cut)

		out, err := decodeJBIG2(truncated)
		if err != nil {
			continue // A rejected stream is an acceptable outcome too.
		}
		if want := (width + 7) / 8 * height; len(out) != want {
			t.Fatalf("cut %d bytes: decoded %d bytes, want %d", cut, len(out), want)
		}
	}
}
