package content

import (
	"testing"

	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// meshBitWriter is meshshading_test.go's own small, test-only inverse of
// bitReader: it packs successive most-significant-bit-first fields into a
// byte slice, letting a test build exactly the kind of hand-crafted
// packed vertex stream a real PDF producer would write, without needing
// a production encoder (this project only ever *reads* PDF content, so
// there is no non-test reason for a mesh-shading bit writer to exist).
type meshBitWriter struct {
	data   []byte
	bitPos int
}

func (w *meshBitWriter) write(value uint32, bits int) {
	for i := bits - 1; i >= 0; i-- {
		byteIdx := w.bitPos / 8
		for byteIdx >= len(w.data) {
			w.data = append(w.data, 0)
		}
		bit := (value >> uint(i)) & 1
		if bit != 0 {
			w.data[byteIdx] |= 1 << uint(7-w.bitPos%8)
		}
		w.bitPos++
	}
}

func (w *meshBitWriter) align() {
	if rem := w.bitPos % 8; rem != 0 {
		w.bitPos += 8 - rem
		for w.bitPos/8 > len(w.data) {
			w.data = append(w.data, 0)
		}
	}
}

func TestBitReaderReadsMostSignificantBitFirst(t *testing.T) {
	br := &bitReader{data: []byte{0b10110010}}
	if v := br.read(4); v != 0b1011 {
		t.Fatalf("read(4) = %b, want %b", v, 0b1011)
	}
	if v := br.read(4); v != 0b0010 {
		t.Fatalf("read(4) = %b, want %b", v, 0b0010)
	}
}

func TestBitReaderReadCrossesByteBoundary(t *testing.T) {
	br := &bitReader{data: []byte{0xFF, 0x00}}
	if v := br.read(12); v != 0xFF0 {
		t.Fatalf("read(12) = %#x, want %#x", v, 0xFF0)
	}
}

func TestBitReaderPastEndReturnsZero(t *testing.T) {
	br := &bitReader{data: []byte{0xFF}}
	br.read(8) // consume the only byte
	if v := br.read(8); v != 0 {
		t.Fatalf("read(8) past end = %d, want 0", v)
	}
}

func TestBitReaderAlign(t *testing.T) {
	br := &bitReader{data: []byte{0xFF, 0xAA}}
	br.read(3)
	br.align()
	if br.bitPos != 8 {
		t.Fatalf("bitPos after align = %d, want 8", br.bitPos)
	}
	if v := br.read(8); v != 0xAA {
		t.Fatalf("read(8) after align = %#x, want %#x", v, 0xAA)
	}
}

func TestBitReaderAlignAlreadyAlignedIsNoop(t *testing.T) {
	br := &bitReader{data: []byte{0xFF}}
	br.read(8)
	br.align()
	if br.bitPos != 8 {
		t.Fatalf("bitPos = %d, want 8 (align on an already-aligned position must not advance further)", br.bitPos)
	}
}

func TestBitReaderHasData(t *testing.T) {
	br := &bitReader{data: []byte{0xFF}}
	if !br.hasData() {
		t.Fatalf("hasData() = false before any read, want true")
	}
	br.read(8)
	if br.hasData() {
		t.Fatalf("hasData() = true after consuming the only byte, want false")
	}
}

func TestReadCoordinateMapsThroughDecode(t *testing.T) {
	w := &meshBitWriter{}
	w.write(0xFFFF, 16) // max raw x
	w.write(0, 16)      // min raw y
	br := &bitReader{data: w.data}
	decode := []float64{10, 20, 100, 200} // xmin xmax ymin ymax
	got := readCoordinate(br, 16, decode)
	if got.X != 20 {
		t.Fatalf("X = %v, want 20 (max raw value maps to decode's xmax)", got.X)
	}
	if got.Y != 100 {
		t.Fatalf("Y = %v, want 100 (zero raw value maps to decode's ymin)", got.Y)
	}
}

func TestReadColorMapsThroughDecodePerComponent(t *testing.T) {
	w := &meshBitWriter{}
	w.write(0, 8)   // component 0: min
	w.write(255, 8) // component 1: max
	br := &bitReader{data: w.data}
	decode := []float64{0, 1, 0, 1, 5, 6, 7, 9} // coords ignored here; components at indices 4..7
	got := readColor(br, 8, decode, 2)
	if len(got) != 2 || got[0] != 5 || got[1] != 9 {
		t.Fatalf("readColor = %v, want [5 9]", got)
	}
}

func deviceRGB(t *testing.T) pdfimage.ColorSpace {
	t.Helper()
	cs, err := pdfimage.ResolveColorSpace(&fakeResolver{}, syntax.Name("DeviceRGB"), nil)
	if err != nil {
		t.Fatalf("ResolveColorSpace(DeviceRGB): %v", err)
	}
	return cs
}

func TestMeshColorWithoutFunctionUsesRawComponents(t *testing.T) {
	cs := deviceRGB(t)
	col := meshColor([]float64{1, 0.5, 0}, nil, cs)
	if col.R != 1 || col.G != 0.5 || col.B != 0 {
		t.Fatalf("meshColor = %+v, want (1,0.5,0)", col)
	}
}

func TestLatticeToTrianglesTwoByTwoGridMakesTwoTriangles(t *testing.T) {
	rows := [][]meshVertex{
		{{X: 0, Y: 0}, {X: 10, Y: 0}},
		{{X: 0, Y: 10}, {X: 10, Y: 10}},
	}
	triangles := latticeToTriangles(rows)
	if len(triangles) != 2 {
		t.Fatalf("len(triangles) = %d, want 2", len(triangles))
	}
}

func TestLatticeToTrianglesSingleRowMakesNothing(t *testing.T) {
	rows := [][]meshVertex{{{X: 0, Y: 0}, {X: 10, Y: 0}}}
	if triangles := latticeToTriangles(rows); len(triangles) != 0 {
		t.Fatalf("len(triangles) = %d, want 0 (a single row has no adjacent row to pair with)", len(triangles))
	}
}

// type4Params returns a meshParams for an 8-bit-coordinate, 8-bit-
// component, function-less DeviceRGB mesh - the shape every
// decodeType4Mesh test below builds its packed bytes against.
func type4Params(t *testing.T) meshParams {
	return meshParams{
		bitsPerCoordinate: 8, bitsPerComponent: 8,
		decode:   []float64{0, 255, 0, 255, 0, 1, 0, 1, 0, 1},
		numComps: 3, cs: deviceRGB(t),
	}
}

// writeType4Vertex packs one Type 4 vertex record (flag, x, y, r, g, b -
// all 8 bits wide, matching type4Params) and pads to the next byte
// boundary, mirroring decodeType4Mesh's own per-vertex alignment rule.
func writeType4Vertex(w *meshBitWriter, flag, x, y, r, g, b byte) {
	w.write(uint32(flag), 8)
	w.write(uint32(x), 8)
	w.write(uint32(y), 8)
	w.write(uint32(r), 8)
	w.write(uint32(g), 8)
	w.write(uint32(b), 8)
	w.align()
}

func TestDecodeType4MeshSingleTriangle(t *testing.T) {
	w := &meshBitWriter{}
	writeType4Vertex(w, 0, 0, 0, 255, 0, 0)
	writeType4Vertex(w, 0, 100, 0, 0, 255, 0)
	writeType4Vertex(w, 0, 0, 100, 0, 0, 255)
	br := &bitReader{data: w.data}

	triangles, err := decodeType4Mesh(br, type4Params(t), 8)
	if err != nil {
		t.Fatalf("decodeType4Mesh: %v", err)
	}
	if len(triangles) != 1 {
		t.Fatalf("len(triangles) = %d, want 1", len(triangles))
	}
	tri := triangles[0]
	if tri.X0 != 0 || tri.Y0 != 0 || tri.C0.R != 1 {
		t.Fatalf("triangle vertex 0 = (%v,%v,%+v), want (0,0,red)", tri.X0, tri.Y0, tri.C0)
	}
	if tri.X1 != 100 || tri.Y1 != 0 || tri.C1.G != 1 {
		t.Fatalf("triangle vertex 1 = (%v,%v,%+v), want (100,0,green)", tri.X1, tri.Y1, tri.C1)
	}
	if tri.X2 != 0 || tri.Y2 != 100 || tri.C2.B != 1 {
		t.Fatalf("triangle vertex 2 = (%v,%v,%+v), want (0,100,blue)", tri.X2, tri.Y2, tri.C2)
	}
}

// TestDecodeType4MeshEdgeFlag1SharesLastTwoVertices confirms a strip of
// two triangles (flag 0 then flag 1) shares the *previous* triangle's 2nd
// and 3rd vertices as the new triangle's 1st and 2nd, per 8.7.4.5.5 - not
// some other, plausible-looking pairing (e.g. the previous triangle's 1st
// and 2nd), which is exactly the kind of mistake that would still produce
// a plausible-looking (but geometrically wrong) mesh.
func TestDecodeType4MeshEdgeFlag1SharesLastTwoVertices(t *testing.T) {
	w := &meshBitWriter{}
	writeType4Vertex(w, 0, 0, 0, 255, 0, 0)     // va: red
	writeType4Vertex(w, 0, 10, 0, 0, 255, 0)    // vb: green
	writeType4Vertex(w, 0, 0, 10, 0, 0, 255)    // vc: blue
	writeType4Vertex(w, 1, 10, 10, 255, 255, 0) // vd: yellow, flag 1
	br := &bitReader{data: w.data}

	triangles, err := decodeType4Mesh(br, type4Params(t), 8)
	if err != nil {
		t.Fatalf("decodeType4Mesh: %v", err)
	}
	if len(triangles) != 2 {
		t.Fatalf("len(triangles) = %d, want 2", len(triangles))
	}
	second := triangles[1]
	// Expect (vb, vc, vd): green, blue, yellow.
	if second.C0.G != 1 || second.C1.B != 1 || second.C2.R != 1 || second.C2.G != 1 {
		t.Fatalf("second triangle colors = (%+v,%+v,%+v), want (green,blue,yellow)", second.C0, second.C1, second.C2)
	}
	if second.X0 != 10 || second.Y0 != 0 || second.X1 != 0 || second.Y1 != 10 || second.X2 != 10 || second.Y2 != 10 {
		t.Fatalf("second triangle coords = (%v,%v)(%v,%v)(%v,%v), want (10,0)(0,10)(10,10)",
			second.X0, second.Y0, second.X1, second.Y1, second.X2, second.Y2)
	}
}

// TestDecodeType4MeshEdgeFlag2SharesFirstAndLastVertices is flag 1's
// sibling case: shares the previous triangle's 1st and 3rd vertices.
func TestDecodeType4MeshEdgeFlag2SharesFirstAndLastVertices(t *testing.T) {
	w := &meshBitWriter{}
	writeType4Vertex(w, 0, 0, 0, 255, 0, 0)     // va: red
	writeType4Vertex(w, 0, 10, 0, 0, 255, 0)    // vb: green
	writeType4Vertex(w, 0, 0, 10, 0, 0, 255)    // vc: blue
	writeType4Vertex(w, 2, 10, 10, 255, 255, 0) // vd: yellow, flag 2
	br := &bitReader{data: w.data}

	triangles, err := decodeType4Mesh(br, type4Params(t), 8)
	if err != nil {
		t.Fatalf("decodeType4Mesh: %v", err)
	}
	if len(triangles) != 2 {
		t.Fatalf("len(triangles) = %d, want 2", len(triangles))
	}
	second := triangles[1]
	// Expect (va, vc, vd): red, blue, yellow.
	if second.C0.R != 1 || second.C1.B != 1 || second.C2.R != 1 || second.C2.G != 1 {
		t.Fatalf("second triangle colors = (%+v,%+v,%+v), want (red,blue,yellow)", second.C0, second.C1, second.C2)
	}
}

func TestDecodeType4MeshInvalidFlagIsMalformed(t *testing.T) {
	w := &meshBitWriter{}
	writeType4Vertex(w, 3, 0, 0, 0, 0, 0) // 3 is not a valid edge flag
	br := &bitReader{data: w.data}
	if _, err := decodeType4Mesh(br, type4Params(t), 8); err == nil {
		t.Fatalf("decodeType4Mesh with edge flag 3: want an error, got nil")
	}
}

func TestDecodeType4MeshFlag1WithoutPriorTriangleIsMalformed(t *testing.T) {
	w := &meshBitWriter{}
	writeType4Vertex(w, 1, 0, 0, 0, 0, 0) // flag 1 with no earlier triangle to share from
	br := &bitReader{data: w.data}
	if _, err := decodeType4Mesh(br, type4Params(t), 8); err == nil {
		t.Fatalf("decodeType4Mesh with edge flag 1 and no prior triangle: want an error, got nil")
	}
}

func TestDecodeType4MeshTruncatedTrailingVerticesAreDropped(t *testing.T) {
	w := &meshBitWriter{}
	writeType4Vertex(w, 0, 0, 0, 255, 0, 0)
	writeType4Vertex(w, 0, 10, 0, 0, 255, 0)
	// Only 2 of the 3 vertices a fresh (flag 0) triangle needs - the
	// stream ends here, mid-triangle.
	br := &bitReader{data: w.data}
	triangles, err := decodeType4Mesh(br, type4Params(t), 8)
	if err != nil {
		t.Fatalf("decodeType4Mesh: %v", err)
	}
	if len(triangles) != 0 {
		t.Fatalf("len(triangles) = %d, want 0 (an incomplete trailing triangle must be dropped, not fabricated)", len(triangles))
	}
}

// writeType5Vertex packs one Type 5 vertex record (x, y, r, g, b - all 8
// bits wide) with *no* trailing align() call, unlike Type 4's vertices -
// Type 5 packs continuously with no per-vertex byte padding at all (see
// this file's own doc comment).
func writeType5Vertex(w *meshBitWriter, x, y, r, g, b byte) {
	w.write(uint32(x), 8)
	w.write(uint32(y), 8)
	w.write(uint32(r), 8)
	w.write(uint32(g), 8)
	w.write(uint32(b), 8)
}

func TestDecodeType5MeshTwoByTwoGrid(t *testing.T) {
	w := &meshBitWriter{}
	writeType5Vertex(w, 0, 0, 255, 0, 0)       // row 0, col 0: red
	writeType5Vertex(w, 100, 0, 0, 255, 0)     // row 0, col 1: green
	writeType5Vertex(w, 0, 100, 0, 0, 255)     // row 1, col 0: blue
	writeType5Vertex(w, 100, 100, 255, 255, 0) // row 1, col 1: yellow
	br := &bitReader{data: w.data}

	triangles := decodeType5Mesh(br, type4Params(t), 2)
	if len(triangles) != 2 {
		t.Fatalf("len(triangles) = %d, want 2 (one 2x2 grid cell splits into 2 triangles)", len(triangles))
	}
}

func TestDecodeType5MeshTruncatedTrailingRowIsDropped(t *testing.T) {
	w := &meshBitWriter{}
	writeType5Vertex(w, 0, 0, 255, 0, 0)
	writeType5Vertex(w, 100, 0, 0, 255, 0)
	writeType5Vertex(w, 0, 100, 0, 0, 255) // only 1 of 2 vertices for row 1
	br := &bitReader{data: w.data}

	triangles := decodeType5Mesh(br, type4Params(t), 2)
	if len(triangles) != 0 {
		t.Fatalf("len(triangles) = %d, want 0 (a single complete row alone has nothing to triangulate against, and the incomplete second row must be dropped)", len(triangles))
	}
}

// TestDecodeType5MeshNoByteAlignmentBetweenVertices confirms
// decodeType5Mesh packs vertices continuously with no per-vertex
// byte-alignment - unlike decodeType4Mesh's br.align() call (see this
// file's own doc comment on why the two types differ here). Using a bit
// width that does not divide evenly into a byte (5 bits, not 8) makes any
// accidental byte-alignment between vertices show up clearly: a wrongly
// inserted align() would shift every field after the first vertex to a
// different bit position, decoding it into an essentially unrelated
// value instead of the one actually encoded there.
func TestDecodeType5MeshNoByteAlignmentBetweenVertices(t *testing.T) {
	w := &meshBitWriter{}
	write5 := func(v uint32) { w.write(v, 5) }
	// 4 vertices x (x,y,r,g,b) = 20 fields x 5 bits = 100 bits = 12.5
	// bytes, so nothing about this stream's own length is byte-friendly
	// either. Colors are left at 0 - this test only checks coordinates.
	verts := [][2]uint32{{1, 2}, {3, 4}, {5, 6}, {7, 8}}
	for _, v := range verts {
		write5(v[0])
		write5(v[1])
		write5(0)
		write5(0)
		write5(0)
	}
	br := &bitReader{data: w.data}

	params := meshParams{
		bitsPerCoordinate: 5, bitsPerComponent: 5,
		decode: []float64{0, 31, 0, 31, 0, 1, 0, 1, 0, 1}, numComps: 3, cs: deviceRGB(t),
	}
	triangles := decodeType5Mesh(br, params, 2)
	if len(triangles) != 2 {
		t.Fatalf("len(triangles) = %d, want 2", len(triangles))
	}
	// rows = [[v1,v2],[v3,v4]] (verticesPerRow=2), so triangles[1] is
	// (v2, v4, v3) - see latticeToTriangles. Checking v4's coordinates
	// specifically (the 3rd vertex decoded) is what actually exercises
	// this test's point: a byte-alignment bug would have already thrown
	// off every field from v2 onward.
	const eps = 0.01
	if diff := triangles[1].X1 - 7; diff > eps || diff < -eps {
		t.Fatalf("4th vertex X = %v, want ~7 (a byte-alignment bug between vertices would desync this)", triangles[1].X1)
	}
	if diff := triangles[1].Y1 - 8; diff > eps || diff < -eps {
		t.Fatalf("4th vertex Y = %v, want ~8", triangles[1].Y1)
	}
}
