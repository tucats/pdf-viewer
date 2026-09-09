package fonts

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// This file's helpers (buildTestCFF, encodeCFFIndex, ...) hand-build
// minimal, entirely synthetic CFF font byte streams, the same "construct
// exactly the bytes a test needs, byte-counted by code instead of
// pulled in from a real font file" approach truetype_test.go's own
// helpers use (see that file's doc comment for the full rationale).

// encodeCFFIndex builds a CFF INDEX (CFF specification section 5) from
// items, always using a 2-byte offset size - simpler than picking the
// smallest size that would fit (which real encoders do to save space),
// and parseCFFIndex accepts any valid size, so these test fixtures never
// need to bother.
func encodeCFFIndex(items [][]byte) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(items)))
	if len(items) == 0 {
		return buf.Bytes()
	}
	buf.WriteByte(2) // offSize
	offsets := make([]int, len(items)+1)
	offsets[0] = 1
	for i, it := range items {
		offsets[i+1] = offsets[i] + len(it)
	}
	for _, off := range offsets {
		_ = binary.Write(&buf, binary.BigEndian, uint16(off))
	}
	for _, it := range items {
		buf.Write(it)
	}
	return buf.Bytes()
}

// encodeCFFInt encodes v using CFF's compact variable-length integer
// operand forms (the same forms both a DICT and a Type 2 Charstring use
// for an ordinary integer - see parseCFFDict and decodeCharstringNumber)
// - whichever of the four forms is shortest for v. Only used by these
// tests for values within the 2-byte form's range (-1131..1131), which
// is every operand this file's tests need.
func encodeCFFInt(v int) []byte {
	switch {
	case v >= -107 && v <= 107:
		return []byte{byte(v + 139)}
	case v >= 108 && v <= 1131:
		v -= 108
		return []byte{byte(v/256 + 247), byte(v % 256)}
	case v >= -1131 && v <= -108:
		v = -v - 108
		return []byte{byte(v/256 + 251), byte(v % 256)}
	default:
		panic("encodeCFFInt: value out of range for this test helper")
	}
}

// encodeCFFInt32 encodes v using the DICT/charstring 5-byte, lead-byte-29
// fixed-width 32-bit integer form both a DICT and a charstring
// understand. Deliberately always 5 bytes
// regardless of v's magnitude (unlike encodeCFFInt above), which
// buildTestCFF relies on: it lets a DICT entry's encoded byte length be
// computed *before* the final offset/size values it will hold are known
// (every such value fits in an int32 either way), breaking what would
// otherwise be a chicken-and-egg layout problem - see buildTestCFF.
func encodeCFFInt32(v int) []byte {
	return []byte{29, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

// encodeCFFDictEntry encodes one DICT operator (op, or - for
// op >= cffEscapeOperator - the corresponding two-byte 12-prefixed
// escape operator) together with its operands, each written via
// encodeCFFInt32 (see that function's doc comment on why a fixed-width
// form is what buildTestCFF needs here).
func encodeCFFDictEntry(op int, operands ...int) []byte {
	var buf bytes.Buffer
	for _, v := range operands {
		buf.Write(encodeCFFInt32(v))
	}
	if op >= cffEscapeOperator {
		buf.WriteByte(12)
		buf.WriteByte(byte(op - cffEscapeOperator))
	} else {
		buf.WriteByte(byte(op))
	}
	return buf.Bytes()
}

// testSIDForName looks name up in the standard strings table (every
// glyph name this file's tests use is a standard one - see
// cffStandardStrings), for encoding a format-0 charset's raw SID values.
func testSIDForName(t *testing.T, name string) uint16 {
	t.Helper()
	for i, s := range cffStandardStrings {
		if s == name {
			return uint16(i)
		}
	}
	t.Fatalf("testSIDForName: %q is not a standard string", name)
	return 0
}

// buildTestCFF assembles a complete, minimal, entirely synthetic
// non-CID CFF font program. charsetNames, if non-nil, names glyphs 1..N
// (glyph 0 is always .notdef and is never itself named) via an explicit
// format-0 charset; if nil, the font uses CFF's predefined "ISOAdobe"
// charset (offset 0 - see identityCharset) instead. globalSubrs and
// localSubrs may be nil for a font that uses neither.
func buildTestCFF(t *testing.T, charStrings [][]byte, charsetNames []string, globalSubrs, localSubrs [][]byte) []byte {
	t.Helper()

	header := []byte{1, 0, 4, 4} // major, minor, hdrSize, offSize (this field is not read by any parser in this package)
	nameIndex := encodeCFFIndex([][]byte{[]byte("Test")})
	stringIndex := encodeCFFIndex(nil)
	gsubrIndex := encodeCFFIndex(globalSubrs)
	csIndex := encodeCFFIndex(charStrings)

	var charset []byte
	if charsetNames != nil {
		var buf bytes.Buffer
		buf.WriteByte(0) // charset format 0
		for _, name := range charsetNames {
			_ = binary.Write(&buf, binary.BigEndian, testSIDForName(t, name))
		}
		charset = buf.Bytes()
	}

	var privateDict, lsubrIndex []byte
	if localSubrs != nil {
		lsubrIndex = encodeCFFIndex(localSubrs)
		// Subrs (op 19) is a fixed 6 bytes here (5-byte int32 + 1 op
		// byte) regardless of its value, so this Private DICT's own
		// length is already final before its one operand (the offset of
		// the local subr INDEX, relative to this dict's own start - see
		// parsePrivateSubrs) is actually known.
		privateDict = encodeCFFDictEntry(19, 0)
		privateDict = encodeCFFDictEntry(19, len(privateDict))
	}

	// Top DICT: built first with every offset-bearing operand as a
	// placeholder 0, purely to learn its own encoded byte length (fixed
	// regardless of the placeholder values, since every operand here
	// uses encodeCFFInt32's constant-width form) - see
	// encodeCFFInt32's doc comment for why this sidesteps needing two
	// real passes.
	buildTopDict := func(charsetOffset, charStringsOffset, privateOffset int) []byte {
		var buf bytes.Buffer
		if charsetNames != nil {
			buf.Write(encodeCFFDictEntry(15, charsetOffset))
		}
		buf.Write(encodeCFFDictEntry(17, charStringsOffset))
		if privateDict != nil {
			buf.Write(encodeCFFDictEntry(18, len(privateDict), privateOffset))
		}
		return buf.Bytes()
	}
	topDictIndex := encodeCFFIndex([][]byte{buildTopDict(0, 0, 0)})

	pos := len(header) + len(nameIndex) + len(topDictIndex) + len(stringIndex) + len(gsubrIndex)
	charsetOffset := 0
	if charset != nil {
		charsetOffset = pos
		pos += len(charset)
	}
	charStringsOffset := pos
	pos += len(csIndex)
	privateOffset := pos

	topDictIndex = encodeCFFIndex([][]byte{buildTopDict(charsetOffset, charStringsOffset, privateOffset)})

	var out bytes.Buffer
	out.Write(header)
	out.Write(nameIndex)
	out.Write(topDictIndex)
	out.Write(stringIndex)
	out.Write(gsubrIndex)
	out.Write(charset)
	out.Write(csIndex)
	out.Write(privateDict)
	out.Write(lsubrIndex)
	return out.Bytes()
}

// csBuilder assembles a Type 2 Charstring's raw bytes fluently - a
// small test-only "assembler" so each test's charstring reads as a
// sequence of pushed numbers and operators, rather than raw byte
// literals.
type csBuilder struct{ buf bytes.Buffer }

func (b *csBuilder) num(v int) *csBuilder { b.buf.Write(encodeCFFInt(v)); return b }
func (b *csBuilder) op(op byte) *csBuilder {
	b.buf.WriteByte(op)
	return b
}
func (b *csBuilder) esc(sub byte) *csBuilder {
	b.buf.WriteByte(12)
	b.buf.WriteByte(sub)
	return b
}
func (b *csBuilder) bytes() []byte { return b.buf.Bytes() }

// Type 2 Charstring operator byte values, named for readability at each
// csBuilder.op call site in the tests below (see cff.go's exec for the
// authoritative operator table these mirror).
const (
	csHstem     = 1
	csVstem     = 3
	csVmoveto   = 4
	csRlineto   = 5
	csHlineto   = 6
	csVlineto   = 7
	csRrcurveto = 8
	csCallsubr  = 10
	csReturn    = 11
	csEndchar   = 14
	csHstemhm   = 18
	csHintmask  = 19
	csCntrmask  = 20
	csRmoveto   = 21
	csHmoveto   = 22
	csVstemhm   = 23
	csCallgsubr = 29
	csVhcurveto = 30
	csHvcurveto = 31
)

// squareCharstring builds a Type 2 Charstring drawing the same 700x700
// unit square unitSquareGlyph (truetype_test.go) draws for TrueType,
// letting TestCFF_SimpleSquareGlyph assert on the exact same bounds -
// this time via rmoveto plus four rlineto edges (a charstring never
// needs an explicit "close" operator; see charstringInterp.moveTo's doc
// comment on why the next moveto, or endchar, closes the contour
// implicitly).
func squareCharstring() []byte {
	b := new(csBuilder)
	b.num(100).num(100).op(csRmoveto)
	b.num(700).num(0).op(csRlineto)
	b.num(0).num(700).op(csRlineto)
	b.num(-700).num(0).op(csRlineto)
	b.op(csEndchar)
	return b.bytes()
}

func TestCFF_SimpleSquareGlyph(t *testing.T) {
	data := buildTestCFF(t, [][]byte{{}, squareCharstring()}, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	if font.UnitsPerEm() != 1000 {
		t.Errorf("UnitsPerEm() = %d, want 1000 (FontMatrix default)", font.UnitsPerEm())
	}
	path, ok := font.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline(1) failed")
	}
	minX, minY, maxX, maxY := pathBounds(t, path)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

func TestCFF_EmptyGlyphIsBlankNotMissing(t *testing.T) {
	b := new(csBuilder)
	b.op(csEndchar)
	data := buildTestCFF(t, [][]byte{b.bytes()}, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	path, ok := font.GlyphOutline(0)
	if !ok {
		t.Fatalf("GlyphOutline(0) failed")
	}
	if len(path.Subpaths) != 0 {
		t.Errorf("got %d subpaths for a blank glyph, want 0", len(path.Subpaths))
	}
}

func TestCFF_GIDForRuneViaCharset(t *testing.T) {
	glyphs := [][]byte{
		{},                 // 0: .notdef
		squareCharstring(), // 1: "A"
	}
	data := buildTestCFF(t, glyphs, []string{"A"}, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	gid, ok := font.GIDForRune('A')
	if !ok || gid != 1 {
		t.Errorf("GIDForRune('A') = (%d, %v), want (1, true)", gid, ok)
	}
	if _, ok := font.GIDForRune('Z'); ok {
		t.Errorf("GIDForRune('Z') unexpectedly found a glyph")
	}
}

// TestCFF_IdentityCharsetMatchesStandardStrings exercises the predefined
// ISOAdobe charset path (offset 0, no explicit charset table - see
// identityCharset): glyph 1 in ISOAdobe order is "space" (SID 1), so a
// font with no charset table at all should still resolve rune ' ' to
// GID 1 by way of the standard strings table alone.
func TestCFF_IdentityCharsetMatchesStandardStrings(t *testing.T) {
	glyphs := [][]byte{{}, new(csBuilder).op(csEndchar).bytes()}
	data := buildTestCFF(t, glyphs, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	if gid, ok := font.GIDForRune(' '); !ok || gid != 1 {
		t.Errorf("GIDForRune(' ') = (%d, %v), want (1, true)", gid, ok)
	}
}

func TestCFF_RunawayCallsubrDepthIsBounded(t *testing.T) {
	// Subroutine 0 (bias 107, so callsubr operand -107 selects it) calls
	// itself forever - must terminate via maxCharstringCallDepth rather
	// than overflowing the Go call stack.
	selfCall := new(csBuilder).num(-107).op(csCallsubr).bytes()
	top := new(csBuilder).num(-107).op(csCallsubr).op(csEndchar).bytes()
	data := buildTestCFF(t, [][]byte{{}, top}, nil, nil, [][]byte{selfCall})
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	if _, ok := font.GlyphOutline(1); ok {
		t.Errorf("GlyphOutline succeeded for infinitely-recursive subroutine, want ok=false")
	}
}

func TestCFF_CallsubrCallgsubrBias(t *testing.T) {
	// Local subroutine 0 (1 subroutine total -> bias 107, operand -107)
	// draws the second and third edge; global subroutine 0 (also bias
	// 107) draws the fourth and returns to the start.
	localSubr := new(csBuilder).num(0).num(700).op(csRlineto).num(-700).num(0).op(csRlineto).op(csReturn).bytes()
	globalSubr := new(csBuilder).op(csReturn).bytes() // exercised for its bias/dispatch path, not for drawing.

	top := new(csBuilder).
		num(100).num(100).op(csRmoveto).
		num(700).num(0).op(csRlineto).
		num(-107).op(csCallsubr).
		num(-107).op(csCallgsubr).
		op(csEndchar).bytes()

	data := buildTestCFF(t, [][]byte{{}, top}, nil, [][]byte{globalSubr}, [][]byte{localSubr})
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	path, ok := font.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline(1) failed")
	}
	minX, minY, maxX, maxY := pathBounds(t, path)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}

func TestCFF_HintmaskSkipsMaskBytesAndContinues(t *testing.T) {
	// Two vstemhm pairs (declares 2 stems) then a hintmask - its 1 mask
	// byte ((2 stems + 7)/8 == 1) must be skipped so the following
	// rmoveto/rlineto operands are read correctly, not misinterpreted as
	// path data or an operator.
	b := new(csBuilder)
	b.num(100).num(50).op(csVstemhm)
	b.buf.WriteByte(csHintmask)
	b.buf.WriteByte(0xFF) // 1 mask byte
	b.num(100).num(100).op(csRmoveto)
	b.num(700).num(0).op(csHlineto)
	b.op(csEndchar)

	data := buildTestCFF(t, [][]byte{{}, b.bytes()}, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	path, ok := font.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline(1) failed")
	}
	minX, minY, maxX, maxY := pathBounds(t, path)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 100 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (100,100)-(800,100)", minX, minY, maxX, maxY)
	}
}

func TestCFF_LeadingWidthOperandIsDiscarded(t *testing.T) {
	// rmoveto normally takes exactly 2 operands; a 3rd (leading) operand
	// is this glyph's width delta, which must be discarded rather than
	// treated as dx.
	b := new(csBuilder)
	b.num(999).num(100).num(100).op(csRmoveto) // 999 is a width value, not a coordinate
	b.num(700).num(0).op(csRlineto)
	b.op(csEndchar)

	data := buildTestCFF(t, [][]byte{{}, b.bytes()}, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	path, ok := font.GlyphOutline(1)
	if !ok {
		t.Fatalf("GlyphOutline(1) failed")
	}
	minX, _, maxX, _ := pathBounds(t, path)
	if minX != 100 || maxX != 800 {
		t.Errorf("bounds X = [%v,%v], want [100,800] - width operand was not discarded correctly", minX, maxX)
	}
}

func TestCFF_FlexOperatorsDrawCurves(t *testing.T) {
	tests := []struct {
		name string
		ops  func(b *csBuilder)
	}{
		{"hflex", func(b *csBuilder) {
			b.num(100).num(100).op(csRmoveto)
			b.num(50).num(20).num(10).num(30).num(50).num(-20).num(50).esc(34) // hflex
			b.op(csEndchar)
		}},
		{"flex", func(b *csBuilder) {
			b.num(100).num(100).op(csRmoveto)
			b.num(20).num(20).num(20).num(-10).num(20).num(20).num(20).num(20).num(20).num(-10).num(20).num(20).num(50).esc(35) // flex, 12 real + fd
			b.op(csEndchar)
		}},
		{"hflex1", func(b *csBuilder) {
			b.num(100).num(100).op(csRmoveto)
			b.num(20).num(10).num(20).num(-10).num(20).num(20).num(20).num(-20).num(20).esc(36) // hflex1
			b.op(csEndchar)
		}},
		{"flex1", func(b *csBuilder) {
			b.num(100).num(100).op(csRmoveto)
			b.num(20).num(10).num(20).num(-10).num(20).num(10).num(20).num(-10).num(20).num(10).num(40).esc(37) // flex1
			b.op(csEndchar)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := new(csBuilder)
			tc.ops(b)
			data := buildTestCFF(t, [][]byte{{}, b.bytes()}, nil, nil, nil)
			font, ok := parseCFFFont(data)
			if !ok {
				t.Fatalf("parseCFFFont failed")
			}
			path, ok := font.GlyphOutline(1)
			if !ok {
				t.Fatalf("GlyphOutline(1) failed for %s", tc.name)
			}
			if len(path.Subpaths) != 1 || len(path.Subpaths[0].Points) == 0 {
				t.Fatalf("%s produced no drawn path", tc.name)
			}
		})
	}
}

func TestCFF_MalformedCurveOperandCountFails(t *testing.T) {
	b := new(csBuilder)
	b.num(100).num(100).op(csRmoveto)
	b.num(1).num(2).num(3).op(csVhcurveto) // 3 is not 4k or 4k+1
	b.op(csEndchar)

	data := buildTestCFF(t, [][]byte{{}, b.bytes()}, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	if _, ok := font.GlyphOutline(1); ok {
		t.Errorf("GlyphOutline succeeded for a malformed vhcurveto operand count, want ok=false")
	}
}

func TestCFF_EndcharSeacIsUnsupportedButFailsGracefully(t *testing.T) {
	b := new(csBuilder)
	b.num(10).num(20).num(30).num(40).op(csEndchar) // 4 leftover operands: implicit seac
	data := buildTestCFF(t, [][]byte{{}, b.bytes()}, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	if _, ok := font.GlyphOutline(1); ok {
		t.Errorf("GlyphOutline succeeded for an implicit-seac endchar, want ok=false (documented non-goal)")
	}
}

func TestCFF_GlyphOutlineRejectsOutOfRangeGID(t *testing.T) {
	data := buildTestCFF(t, [][]byte{{}}, nil, nil, nil)
	font, ok := parseCFFFont(data)
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	if _, ok := font.GlyphOutline(1); ok {
		t.Errorf("GlyphOutline(1) succeeded for a 1-glyph font, want ok=false")
	}
}

func TestParseCFFIndex_Empty(t *testing.T) {
	data := []byte{0, 0, 0xAA} // count=0, then unrelated trailing bytes
	items, next, ok := parseCFFIndex(data, 0)
	if !ok || len(items) != 0 || next != 2 {
		t.Errorf("got (%v, %d, %v), want (empty, 2, true)", items, next, ok)
	}
}

func TestParseCFFIndex_RoundTrip(t *testing.T) {
	want := [][]byte{[]byte("a"), []byte("bcd"), {}}
	encoded := encodeCFFIndex(want)
	items, next, ok := parseCFFIndex(encoded, 0)
	if !ok {
		t.Fatalf("parseCFFIndex failed")
	}
	if next != len(encoded) {
		t.Errorf("next = %d, want %d (end of input)", next, len(encoded))
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d", len(items), len(want))
	}
	for i := range want {
		if !bytes.Equal(items[i], want[i]) {
			t.Errorf("item %d = %q, want %q", i, items[i], want[i])
		}
	}
}

func TestParseCFFIndex_RejectsTruncated(t *testing.T) {
	full := encodeCFFIndex([][]byte{[]byte("hello")})
	if _, _, ok := parseCFFIndex(full[:len(full)-2], 0); ok {
		t.Errorf("parseCFFIndex succeeded on truncated input")
	}
}

func TestParseCFFDict_IntegerForms(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(encodeCFFInt(0))    // op 0's first operand
	buf.Write(encodeCFFInt(1000)) // op 0's second operand
	buf.Write(encodeCFFInt(-1000))
	buf.WriteByte(0) // operator 0 ("version" in a real Top DICT; the value doesn't matter to this test)

	dict, ok := parseCFFDict(buf.Bytes())
	if !ok {
		t.Fatalf("parseCFFDict failed")
	}
	got := dict[0]
	want := []float64{0, 1000, -1000}
	if len(got) != len(want) {
		t.Fatalf("got %v operands, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("operand %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestParseCFFDict_EscapeOperator(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(encodeCFFInt(2))
	buf.WriteByte(12)
	buf.WriteByte(7) // FontMatrix's operator number
	dict, ok := parseCFFDict(buf.Bytes())
	if !ok {
		t.Fatalf("parseCFFDict failed")
	}
	if got := dict[cffEscapeOperator+7]; len(got) != 1 || got[0] != 2 {
		t.Errorf("dict[escape 7] = %v, want [2]", got)
	}
}

func TestParseCFFDict_RejectsLeadByte255(t *testing.T) {
	if _, ok := parseCFFDict([]byte{255, 0, 0, 0, 0}); ok {
		t.Errorf("parseCFFDict succeeded on reserved lead byte 255")
	}
}

func TestParseCFFReal(t *testing.T) {
	tests := []struct {
		nibbles []byte // one nibble per slice element, 0x0-0xf
		want    float64
	}{
		{[]byte{1, 0xa, 5, 0xf}, 1.5},  // "1.5"
		{[]byte{0xe, 2, 0, 0xf}, -20},  // "-20"
		{[]byte{1, 0xb, 2, 0xf}, 100},  // "1E2"
		{[]byte{1, 0xc, 2, 0xf}, 0.01}, // "1E-2"
	}
	for _, tc := range tests {
		var packed []byte
		for i := 0; i < len(tc.nibbles); i += 2 {
			hi := tc.nibbles[i]
			lo := byte(0xf)
			if i+1 < len(tc.nibbles) {
				lo = tc.nibbles[i+1]
			}
			packed = append(packed, hi<<4|lo)
		}
		v, _, ok := parseCFFReal(packed, 0)
		if !ok {
			t.Fatalf("parseCFFReal(%v) failed", tc.nibbles)
		}
		if v != tc.want {
			t.Errorf("parseCFFReal(%v) = %v, want %v", tc.nibbles, v, tc.want)
		}
	}
}

func TestDecodeCharstringNumber(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want float64
		next int
	}{
		{"small positive", []byte{139 + 5}, 5, 1},
		{"small negative", []byte{139 - 5}, -5, 1},
		{"medium positive", []byte{247, 0}, 108, 2},
		{"medium negative", []byte{251, 0}, -108, 2},
		{"shortint", []byte{28, 0x7F, 0xFF}, 32767, 3},
		{"16.16 fixed", []byte{255, 0, 2, 0x80, 0}, 2.5, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, next, ok := decodeCharstringNumber(tc.data, 0)
			if !ok {
				t.Fatalf("decodeCharstringNumber failed")
			}
			if v != tc.want || next != tc.next {
				t.Errorf("got (%v, %d), want (%v, %d)", v, next, tc.want, tc.next)
			}
		})
	}
}

func TestParseCustomCFFCharset_Format0(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0)
	_ = binary.Write(&buf, binary.BigEndian, uint16(10)) // gid 1
	_ = binary.Write(&buf, binary.BigEndian, uint16(11)) // gid 2
	ids, ok := parseCustomCFFCharset(buf.Bytes(), 0, 3)
	if !ok {
		t.Fatalf("parseCustomCFFCharset failed")
	}
	want := []uint16{0, 10, 11}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %d, want %d", i, ids[i], want[i])
		}
	}
}

func TestParseCustomCFFCharset_Format1AndFormat2(t *testing.T) {
	// A single range covering GIDs 1..5, starting at SID 100 - format 1
	// uses a 1-byte nLeft, format 2 a 2-byte one; both should produce the
	// same result.
	format1 := []byte{1, 0, 100, 4} // first=100, nLeft=4 -> SIDs 100..104 for GIDs 1..5
	format2 := []byte{2, 0, 100, 0, 4}

	for _, data := range [][]byte{format1, format2} {
		ids, ok := parseCustomCFFCharset(data, 0, 6)
		if !ok {
			t.Fatalf("parseCustomCFFCharset failed for %v", data)
		}
		for gid := 1; gid <= 5; gid++ {
			want := uint16(100 + gid - 1)
			if ids[gid] != want {
				t.Errorf("ids[%d] = %d, want %d", gid, ids[gid], want)
			}
		}
	}
}

func TestParseCFFFDSelect_Format0(t *testing.T) {
	data := []byte{0, 0, 1, 2, 0, 1}
	fds, ok := parseCFFFDSelect(data, 0, 5)
	if !ok {
		t.Fatalf("parseCFFFDSelect failed")
	}
	want := []byte{0, 1, 2, 0, 1}
	for i := 0; i < 5; i++ {
		if fds[i] != want[i] {
			t.Errorf("fds[%d] = %d, want %d", i, fds[i], want[i])
		}
	}
}

func TestParseCFFFDSelect_Format3(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(3)
	_ = binary.Write(&buf, binary.BigEndian, uint16(2)) // numRanges
	_ = binary.Write(&buf, binary.BigEndian, uint16(0)) // range 0: first GID 0
	buf.WriteByte(0)                                    // FD 0
	_ = binary.Write(&buf, binary.BigEndian, uint16(3)) // range 1: first GID 3
	buf.WriteByte(1)                                    // FD 1
	_ = binary.Write(&buf, binary.BigEndian, uint16(5)) // sentinel: end at GID 5

	fds, ok := parseCFFFDSelect(buf.Bytes(), 0, 5)
	if !ok {
		t.Fatalf("parseCFFFDSelect failed")
	}
	want := []byte{0, 0, 0, 1, 1}
	for i := range want {
		if fds[i] != want[i] {
			t.Errorf("fds[%d] = %d, want %d", i, fds[i], want[i])
		}
	}
}

func TestSubrBias(t *testing.T) {
	tests := []struct {
		n    int
		want int32
	}{
		{0, 107}, {1239, 107}, {1240, 1131}, {33899, 1131}, {33900, 32768},
	}
	for _, tc := range tests {
		if got := subrBias(tc.n); got != tc.want {
			t.Errorf("subrBias(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

// TestCFF_CIDKeyedCharsetAndFDSelect builds a small CID-keyed CFF font
// (a Top DICT carrying a ROS operator, an FDArray of one Font DICT, an
// FDSelect assigning every glyph to it, and a charset mapping GID 1 to
// CID 500) directly - not through cid.go, which is wired up in a later
// phase - to verify this file's own CID-keyed parsing end to end.
func TestCFF_CIDKeyedCharsetAndFDSelect(t *testing.T) {
	glyph := squareCharstring()

	header := []byte{1, 0, 4, 4}
	nameIndex := encodeCFFIndex([][]byte{[]byte("Test")})
	stringIndex := encodeCFFIndex(nil)
	gsubrIndex := encodeCFFIndex(nil)
	csIndex := encodeCFFIndex([][]byte{{}, glyph})

	var charset bytes.Buffer
	charset.WriteByte(0) // format 0
	_ = binary.Write(&charset, binary.BigEndian, uint16(500))

	fdPrivate := []byte{} // no Private DICT entries needed for this Font DICT; local subrs are absent (nil) for this test.
	fontDict := encodeCFFDictEntry(18, len(fdPrivate), 0)
	fdArray := encodeCFFIndex([][]byte{fontDict})
	fdSelect := []byte{0, 0} // format 0: 2 glyphs, both FD 0

	buildTopDict := func(charsetOff, csOff, fdaOff, fdsOff int) []byte {
		var buf bytes.Buffer
		buf.Write(encodeCFFDictEntry(cffEscapeOperator+30, 0, 0, 0)) // ROS: registry/ordering/supplement SIDs - values unused by this package, see parseCFFFont.
		buf.Write(encodeCFFDictEntry(15, charsetOff))
		buf.Write(encodeCFFDictEntry(17, csOff))
		buf.Write(encodeCFFDictEntry(cffEscapeOperator+36, fdaOff))
		buf.Write(encodeCFFDictEntry(cffEscapeOperator+37, fdsOff))
		return buf.Bytes()
	}
	topDictIndex := encodeCFFIndex([][]byte{buildTopDict(0, 0, 0, 0)})

	pos := len(header) + len(nameIndex) + len(topDictIndex) + len(stringIndex) + len(gsubrIndex)
	charsetOff := pos
	pos += charset.Len()
	csOff := pos
	pos += len(csIndex)
	fdaOff := pos
	pos += len(fdArray)
	fdsOff := pos

	topDictIndex = encodeCFFIndex([][]byte{buildTopDict(charsetOff, csOff, fdaOff, fdsOff)})

	var out bytes.Buffer
	out.Write(header)
	out.Write(nameIndex)
	out.Write(topDictIndex)
	out.Write(stringIndex)
	out.Write(gsubrIndex)
	out.Write(charset.Bytes())
	out.Write(csIndex)
	out.Write(fdArray)
	out.Write(fdSelect)

	font, ok := parseCFFFont(out.Bytes())
	if !ok {
		t.Fatalf("parseCFFFont failed")
	}
	if !font.isCID {
		t.Fatalf("isCID = false, want true")
	}
	gid, ok := font.GIDForCID(500)
	if !ok || gid != 1 {
		t.Fatalf("GIDForCID(500) = (%d, %v), want (1, true)", gid, ok)
	}
	path, ok := font.GlyphOutline(gid)
	if !ok {
		t.Fatalf("GlyphOutline(%d) failed", gid)
	}
	minX, minY, maxX, maxY := pathBounds(t, path)
	if minX != 100 || minY != 100 || maxX != 800 || maxY != 800 {
		t.Errorf("bounds = (%v,%v)-(%v,%v), want (100,100)-(800,800)", minX, minY, maxX, maxY)
	}
}
