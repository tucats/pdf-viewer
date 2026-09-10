package fonts

import (
	"testing"
)

// This file's helpers build entirely synthetic Type 1 font programs via
// EncodeType1FontProgram (type1_encode.go) and small charstring-building
// helpers below, the same "construct exactly the bytes a test needs, by
// code" approach cff_test.go's own helpers use (see that file's doc
// comment) - not a real font file, for the reasons type1_encode.go's own
// doc comment explains.

// t1 is a small fluent Type 1 charstring assembler, the Type 1
// counterpart to cff_test.go's csBuilder.
type t1 struct{ buf []byte }

func (b *t1) num(v int) *t1  { b.buf = append(b.buf, EncodeType1Int(v)...); return b }
func (b *t1) op(op byte) *t1 { b.buf = append(b.buf, op); return b }
func (b *t1) esc(sub byte) *t1 {
	b.buf = append(b.buf, 12, sub)
	return b
}
func (b *t1) bytes() []byte { return b.buf }

// Type 1 Charstring operator byte values, named for readability at each
// t1.op call site below (see type1.go's exec for the authoritative
// operator table these mirror).
const (
	t1Hstem        = 1
	t1Vmoveto      = 4
	t1Rlineto      = 5
	t1Hlineto      = 6
	t1Vlineto      = 7
	t1Rrcurveto    = 8
	t1Closepath    = 9
	t1Callsubr     = 10
	t1Return       = 11
	t1Hsbw         = 13
	t1Endchar      = 14
	t1Rmoveto      = 21
	t1Hmoveto      = 22
	t1Vhcurveto    = 30
	t1Hvcurveto    = 31
	t1EscSbw       = 7
	t1EscDiv       = 12
	t1EscCallother = 16
	t1EscPop       = 17
	t1EscSetCur    = 33
	t1EscSeac      = 6
)

// squareType1Charstring draws the same 700x700-unit square (offset so
// its lower-left corner sits at (150,150), matching this project's
// other font-test fixtures' glyphSquareMin/Max convention - see
// truetype.go's buildTestFontProgram) via hsbw (left sidebearing 150,
// arbitrary nonzero width so this exercises hsbw's sidebearing-not-
// width behavior) plus four rlineto edges from an rmoveto start.
func squareType1Charstring() []byte {
	b := new(t1)
	b.num(150).num(600).op(t1Hsbw) // sbx=150, wx=600 (width unused by this package)
	b.num(0).num(150).op(t1Rmoveto)
	b.num(700).num(0).op(t1Rlineto)
	b.num(0).num(700).op(t1Rlineto)
	b.num(-700).num(0).op(t1Rlineto)
	b.op(t1Closepath)
	b.op(t1Endchar)
	return b.bytes()
}

func buildTestType1Font(t *testing.T, glyphs []Type1TestGlyph, subrs [][]byte) type1Font {
	t.Helper()
	data, length1, length2 := EncodeType1FontProgram(glyphs, subrs, 0)
	font, ok := parseType1Font(data, length1, length2)
	if !ok {
		t.Fatalf("parseType1Font failed on a font this test just built")
	}
	return font
}

func TestType1_SimpleSquareGlyph(t *testing.T) {
	font := buildTestType1Font(t, []Type1TestGlyph{
		{Name: ".notdef", Charstring: new(t1).num(0).num(0).op(t1Hsbw).op(t1Endchar).bytes()},
		{Name: "A", Charstring: squareType1Charstring()},
	}, nil)

	if font.UnitsPerEm() != 1000 {
		t.Errorf("UnitsPerEm() = %d, want 1000 (FontMatrix default)", font.UnitsPerEm())
	}

	gid, ok := font.GIDForRune('A')
	if !ok {
		t.Fatalf("GIDForRune('A') failed")
	}
	path, ok := font.GlyphOutline(gid)
	if !ok {
		t.Fatalf("GlyphOutline(%d) failed", gid)
	}
	minX, minY, maxX, maxY := pathBounds(t, path)
	if minX != 150 || minY != 150 || maxX != 850 || maxY != 850 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (150,150)-(850,850)", minX, minY, maxX, maxY)
	}
}

// TestType1_CallsubrRoundTrip confirms a glyph that calls a local
// subroutine (rather than drawing every edge inline) produces the same
// outline as one that does not - exercising callSubr/return, Type 1's
// own unbiased subroutine indexing (contrast cff.go's subrBias).
func TestType1_CallsubrRoundTrip(t *testing.T) {
	// Subr 0 draws one relative edge: "dx dy rlineto return".
	edge := new(t1).num(700).num(0).op(t1Rlineto).op(t1Return).bytes()

	glyph := new(t1)
	glyph.num(150).num(600).op(t1Hsbw)
	glyph.num(0).num(150).op(t1Rmoveto)
	glyph.num(0).op(t1Callsubr) // calls subr 0: +700,+0
	glyph.num(0).num(700).op(t1Rlineto)
	glyph.num(-700).num(0).op(t1Rlineto)
	glyph.op(t1Closepath)
	glyph.op(t1Endchar)

	font := buildTestType1Font(t, []Type1TestGlyph{
		{Name: "A", Charstring: glyph.bytes()},
	}, [][]byte{edge})

	gid, ok := font.GIDForRune('A')
	if !ok {
		t.Fatalf("GIDForRune('A') failed")
	}
	path, ok := font.GlyphOutline(gid)
	if !ok {
		t.Fatalf("GlyphOutline(%d) failed", gid)
	}
	minX, minY, maxX, maxY := pathBounds(t, path)
	if minX != 150 || minY != 150 || maxX != 850 || maxY != 850 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (150,150)-(850,850)", minX, minY, maxX, maxY)
	}
}

// TestType1_Flex exercises the flex OtherSubrs idiom (opCallothersubr's
// doc comment spells out the exact bytecode sequence this builds): two
// curves, drawn via seven rmoveto calls instead of ordinary curveto
// operators, should still produce two real curve segments landing at
// the expected final point.
func TestType1_Flex(t *testing.T) {
	b := new(t1)
	b.num(0).num(0).op(t1Hsbw)
	b.num(100).num(100).op(t1Rmoveto) // starting point (100,100)

	// begin flex: "0 1 callothersubr" (0 args, othersubr 1)
	b.num(0).num(1).esc(t1EscCallother)

	// Seven flex points: a small reference nudge, then two curves' worth
	// of three points each, all deltas chosen so the running absolute
	// position is easy to verify by hand - starting from (100,100):
	// (105,105) reference, (120,120), (135,105), (150,120) [curve 1's
	// control points and end], (165,135), (180,120), (195,135) [curve
	// 2's control points and end].
	points := [][2]int{
		{5, 5},
		{15, 15},
		{15, -15},
		{15, 15},
		{15, 15},
		{15, -15},
		{15, 15},
	}
	for _, p := range points {
		b.num(p[0]).num(p[1]).op(t1Rmoveto)
		b.num(0).num(2).esc(t1EscCallother) // "0 2 callothersubr" after each point
	}

	// end flex: "flex-height finalX finalY 3 0 callothersubr", finalX/Y
	// matching the geometric end point worked out above.
	b.num(50).num(195).num(135).num(3).num(0).esc(t1EscCallother)
	b.esc(t1EscPop)
	b.esc(t1EscPop)
	b.esc(t1EscSetCur)

	b.op(t1Endchar)

	font := buildTestType1Font(t, []Type1TestGlyph{{Name: "A", Charstring: b.bytes()}}, nil)
	gid, ok := font.GIDForRune('A')
	if !ok {
		t.Fatalf("GIDForRune('A') failed")
	}
	path, ok := font.GlyphOutline(gid)
	if !ok {
		t.Fatalf("GlyphOutline(%d) failed", gid)
	}

	if len(path.Subpaths) != 1 {
		t.Fatalf("got %d subpaths, want 1", len(path.Subpaths))
	}
	pts := path.Subpaths[0].Points
	// One point for the initial (non-flexing) rmoveto, plus
	// bezierSegments (16) flattened points per curve for both of the
	// two curves flex should have drawn - a wrong implementation that
	// fell back to two straight lines (or dropped the curves entirely)
	// would produce far fewer points here.
	const wantPoints = 1 + 16 + 16
	if len(pts) != wantPoints {
		t.Fatalf("got %d points in the subpath, want %d (flex should draw two real 16-segment curves)", len(pts), wantPoints)
	}

	end := pts[len(pts)-1]
	if end.X != 195 || end.Y != 135 {
		t.Errorf("final point = (%v,%v), want (195,135)", end.X, end.Y)
	}
}

// TestType1_Seac confirms a seac-using glyph is reported unavailable
// (falls back to notdefGlyph at the Font layer) rather than either
// failing the whole font or misinterpreting seac's operands as path
// data - the same documented non-goal cff.go's own implicit-seac case
// has (see type1.go's doc comment on scope).
func TestType1_Seac(t *testing.T) {
	seac := new(t1)
	seac.num(0).num(0).op(t1Hsbw)
	seac.num(0).num(0).num(0).num(65).num(66).esc(t1EscSeac) // asb adx ady bchar achar seac

	font := buildTestType1Font(t, []Type1TestGlyph{
		{Name: "A", Charstring: new(t1).num(0).num(0).op(t1Hsbw).op(t1Endchar).bytes()},
		{Name: "Aacute", Charstring: seac.bytes()},
	}, nil)

	// "Aacute" is not in glyphNameToRune's small table, so it is only
	// reachable by its parse-order GID here (1, the second glyph given
	// above) - this test cares about GlyphOutline's behavior on a seac
	// charstring, not name resolution, so that is enough.
	if _, ok := font.GlyphOutline(1); ok {
		t.Errorf("GlyphOutline for a seac-composed glyph = ok, want ok=false (unsupported, not composed)")
	}
}

// TestType1_HexArmoredEexec confirms a PFA-style, hex-encoded eexec
// section (see looksLikeType1PFAHex/decodeType1PFAHex) parses
// identically to the ordinary raw-binary form - real-world producers
// occasionally embed a font this way even inside a PDF, despite the PDF
// specification's own preference for the raw-binary form.
func TestType1_HexArmoredEexec(t *testing.T) {
	glyphs := []Type1TestGlyph{{Name: "A", Charstring: squareType1Charstring()}}
	data, length1, length2 := EncodeType1FontProgram(glyphs, nil, 0)

	cleartext := data[:length1]
	cipher := data[length1 : length1+length2]

	hex := make([]byte, 0, len(cipher)*2)
	const digits = "0123456789ABCDEF"
	for _, b := range cipher {
		hex = append(hex, digits[b>>4], digits[b&0xf])
	}

	hexData := append(append([]byte{}, cleartext...), hex...)
	font, ok := parseType1Font(hexData, length1, len(hex))
	if !ok {
		t.Fatalf("parseType1Font failed on a hex-armored eexec section")
	}
	gid, ok := font.GIDForRune('A')
	if !ok {
		t.Fatalf("GIDForRune('A') failed")
	}
	if _, ok := font.GlyphOutline(gid); !ok {
		t.Fatalf("GlyphOutline failed")
	}
}

// TestType1_MissingLength1Length2 confirms this package's fallback path
// (searching for the literal "eexec" keyword, and using all remaining
// bytes as the encrypted section) works when a /FontFile stream's
// /Length1/Length2 are unavailable - a real producer omitting them
// would be unusual but not impossible, and this package's other
// embedded-font parsers apply the same "recover rather than refuse"
// policy for their own untrustworthy structural hints.
func TestType1_MissingLength1Length2(t *testing.T) {
	glyphs := []Type1TestGlyph{{Name: "A", Charstring: squareType1Charstring()}}
	data, _, _ := EncodeType1FontProgram(glyphs, nil, 0)

	font, ok := parseType1Font(data, 0, 0)
	if !ok {
		t.Fatalf("parseType1Font failed without explicit Length1/Length2")
	}
	if _, ok := font.GIDForRune('A'); !ok {
		t.Fatalf("GIDForRune('A') failed")
	}
}

func TestType1_CustomFontMatrix(t *testing.T) {
	glyphs := []Type1TestGlyph{{Name: "A", Charstring: squareType1Charstring()}}
	data, length1, length2 := EncodeType1FontProgram(glyphs, nil, 2048)

	font, ok := parseType1Font(data, length1, length2)
	if !ok {
		t.Fatalf("parseType1Font failed")
	}
	if font.UnitsPerEm() != 2048 {
		t.Errorf("UnitsPerEm() = %d, want 2048", font.UnitsPerEm())
	}
}

func TestDecodeType1Number(t *testing.T) {
	cases := []struct {
		instr []byte
		want  float64
		next  int
	}{
		{[]byte{139}, 0, 1},
		{[]byte{140}, 1, 1},
		{[]byte{247, 0}, 108, 2},
		{[]byte{250, 255}, 1131, 2},
		{[]byte{251, 0}, -108, 2},
		{[]byte{254, 255}, -1131, 2},
		{[]byte{255, 0, 0, 4, 0}, 1024, 5},
		{[]byte{255, 0xff, 0xff, 0xff, 0xff}, -1, 5},
	}
	for _, c := range cases {
		v, next, ok := decodeType1Number(c.instr, 0)
		if !ok || v != c.want || next != c.next {
			t.Errorf("decodeType1Number(%v) = (%v,%v,%v), want (%v,%v,true)", c.instr, v, next, ok, c.want, c.next)
		}
	}
	if _, _, ok := decodeType1Number([]byte{247}, 0); ok {
		t.Errorf("decodeType1Number of a truncated 2-byte form should fail")
	}
	if _, _, ok := decodeType1Number([]byte{255, 0, 0}, 0); ok {
		t.Errorf("decodeType1Number of a truncated 5-byte form should fail")
	}
}
