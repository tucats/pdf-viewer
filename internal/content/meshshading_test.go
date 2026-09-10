package content

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
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

func approxEqualPoint(t *testing.T, label string, got, want point2D) {
	t.Helper()
	const eps = 1e-9
	if got.X-want.X > eps || got.X-want.X < -eps || got.Y-want.Y > eps || got.Y-want.Y < -eps {
		t.Errorf("%s = %+v, want %+v", label, got, want)
	}
}

// flatRectanglePatch returns a meshPatch whose 12 boundary points trace
// the boundary of an axis-aligned 9x9 square with each edge's own 4
// Bezier control points evenly spaced along that edge (0, 3, 6, 9) - the
// specific shape that makes a Coons patch bilinear (perfectly flat, no
// curvature), so its own well-known bilinear grid positions
// (3,3)/(6,3)/(3,6)/(6,6) become an easy, independently-derivable
// expected value for fillCoonsInternalPoints to be checked against.
// Corner colors are red/green/blue/yellow at pts[0]/pts[3]/pts[12]/pts[15]
// respectively.
func flatRectanglePatch() meshPatch {
	var patch meshPatch
	patch.pts[0], patch.pts[1], patch.pts[2], patch.pts[3] = point2D{0, 0}, point2D{3, 0}, point2D{6, 0}, point2D{9, 0}
	patch.pts[4], patch.pts[7] = point2D{0, 3}, point2D{9, 3}
	patch.pts[8], patch.pts[11] = point2D{0, 6}, point2D{9, 6}
	patch.pts[12], patch.pts[13], patch.pts[14], patch.pts[15] = point2D{0, 9}, point2D{3, 9}, point2D{6, 9}, point2D{9, 9}
	patch.colors = [4]graphics.Color{{R: 1}, {G: 1}, {B: 1}, {R: 1, G: 1}}
	return patch
}

func TestFillCoonsInternalPointsFlatRectangle(t *testing.T) {
	patch := flatRectanglePatch()
	fillCoonsInternalPoints(&patch)
	approxEqualPoint(t, "pts[5]", patch.pts[5], point2D{3, 3})
	approxEqualPoint(t, "pts[6]", patch.pts[6], point2D{6, 3})
	approxEqualPoint(t, "pts[9]", patch.pts[9], point2D{3, 6})
	approxEqualPoint(t, "pts[10]", patch.pts[10], point2D{6, 6})
}

// TestPatchToTrianglesCornersMatchControlPoints exercises a property of
// *any* Bezier surface, flat or not: it interpolates its own 4 corner
// control points exactly, since the Bernstein basis at u,v in {0,1}
// reduces to a single weight of 1 (all others 0) - so the resulting
// lattice's own 4 corners must equal patch.pts[0]/[3]/[12]/[15] and
// patch.colors[0..3] exactly, regardless of internal-point placement or
// meshPatchSubdivisions' own value.
func TestPatchToTrianglesCornersMatchControlPoints(t *testing.T) {
	patch := flatRectanglePatch()
	fillCoonsInternalPoints(&patch)
	triangles := patchToTriangles(patch)

	wantTriangleCount := 2 * meshPatchSubdivisions * meshPatchSubdivisions
	if len(triangles) != wantTriangleCount {
		t.Fatalf("len(triangles) = %d, want %d", len(triangles), wantTriangleCount)
	}

	// The lattice's own first vertex (row 0, col 0) is triangles[0]'s
	// first corner: u=0,v=0.
	first := triangles[0]
	approxEqualPoint(t, "(u=0,v=0) position", point2D{first.X0, first.Y0}, patch.pts[0])
	if first.C0 != patch.colors[0] {
		t.Fatalf("(u=0,v=0) color = %+v, want %+v", first.C0, patch.colors[0])
	}
}

func TestApplyPatchBoundaryFlag0(t *testing.T) {
	var patch meshPatch
	newPts := []point2D{{0, 0}, {0, 3}, {0, 6}, {0, 9}, {3, 9}, {6, 9}, {9, 9}, {9, 6}, {9, 3}, {9, 0}, {6, 0}, {3, 0}}
	newCols := []graphics.Color{{R: 1}, {B: 1}, {R: 1, G: 1}, {G: 1}} // red, blue, yellow, green
	if err := applyPatchBoundary(&patch, nil, 0, newPts, newCols); err != nil {
		t.Fatalf("applyPatchBoundary: %v", err)
	}
	approxEqualPoint(t, "pts[0]", patch.pts[0], point2D{0, 0})
	approxEqualPoint(t, "pts[3]", patch.pts[3], point2D{9, 0})
	approxEqualPoint(t, "pts[12]", patch.pts[12], point2D{0, 9})
	approxEqualPoint(t, "pts[15]", patch.pts[15], point2D{9, 9})
	if patch.colors[0] != (graphics.Color{R: 1}) || patch.colors[1] != (graphics.Color{G: 1}) ||
		patch.colors[2] != (graphics.Color{B: 1}) || patch.colors[3] != (graphics.Color{R: 1, G: 1}) {
		t.Fatalf("colors = %+v, want [red green blue yellow]", patch.colors)
	}
}

// TestApplyPatchBoundaryFlag1SharesRightEdgeAsNewLeftEdge confirms flag
// 1's specific rotation: the previous patch's pts[12,13,14,15] (its own
// v=1 edge, u=0..1) becomes the new patch's pts[12,8,4,0] (v=1 down to
// v=0, at u=0) - reversed order, not the same order, which is exactly the
// kind of subtle transposition a hand-derivation could get backwards.
func TestApplyPatchBoundaryFlag1SharesRightEdgeAsNewLeftEdge(t *testing.T) {
	prev := flatRectanglePatch()
	fillCoonsInternalPoints(&prev)

	var patch meshPatch
	newPts := []point2D{{20, 9}, {20, 6}, {20, 3}, {20, 0}, {17, 0}, {14, 0}, {11, 0}, {11, 3}}
	newCols := []graphics.Color{{R: 1, B: 1}, {G: 1, B: 1}} // magenta, cyan
	if err := applyPatchBoundary(&patch, &prev, 1, newPts, newCols); err != nil {
		t.Fatalf("applyPatchBoundary: %v", err)
	}
	// New pts[12] (u=0,v=1) must equal prev's pts[15] (its own u=1,v=1
	// corner); new pts[0] (u=0,v=0) must equal prev's pts[12] (u=0,v=1).
	approxEqualPoint(t, "pts[12]", patch.pts[12], prev.pts[15])
	approxEqualPoint(t, "pts[8]", patch.pts[8], prev.pts[14])
	approxEqualPoint(t, "pts[4]", patch.pts[4], prev.pts[13])
	approxEqualPoint(t, "pts[0]", patch.pts[0], prev.pts[12])
	// The shared corners' colors carry over the same way: new colors[2]
	// (pts[12]) = prev colors[3] (pts[15]); new colors[0] (pts[0]) = prev
	// colors[2] (pts[12]).
	if patch.colors[2] != prev.colors[3] {
		t.Fatalf("colors[2] = %+v, want prev.colors[3] = %+v", patch.colors[2], prev.colors[3])
	}
	if patch.colors[0] != prev.colors[2] {
		t.Fatalf("colors[0] = %+v, want prev.colors[2] = %+v", patch.colors[0], prev.colors[2])
	}
}

func TestApplyPatchBoundaryInvalidFlagIsMalformed(t *testing.T) {
	var patch meshPatch
	if err := applyPatchBoundary(&patch, nil, 9, nil, nil); err == nil {
		t.Fatalf("applyPatchBoundary with flag 9: want an error, got nil")
	}
}

// writeType6Patch packs one Type 6 patch record (flag, nPts coordinate
// pairs, nCols colors - all 8 bits wide) with no trailing byte alignment,
// matching decodeType6Mesh's own continuous packing.
func writeType6Patch(w *meshBitWriter, flag byte, pts []point2D, cols [][3]byte) {
	w.write(uint32(flag), 8)
	for _, p := range pts {
		w.write(uint32(p.X), 8)
		w.write(uint32(p.Y), 8)
	}
	for _, c := range cols {
		w.write(uint32(c[0]), 8)
		w.write(uint32(c[1]), 8)
		w.write(uint32(c[2]), 8)
	}
}

func TestDecodeType6MeshSinglePatch(t *testing.T) {
	w := &meshBitWriter{}
	// The same flat-rectangle boundary as flatRectanglePatch, scaled to
	// byte-friendly values (0, 85, 170, 255 rather than 0, 3, 6, 9) so an
	// 8-bit-per-coordinate stream represents it exactly.
	pts := []point2D{
		{0, 0}, {0, 85}, {0, 170}, {0, 255}, {85, 255}, {170, 255},
		{255, 255}, {255, 170}, {255, 85}, {255, 0}, {170, 0}, {85, 0},
	}
	cols := [][3]byte{{255, 0, 0}, {0, 0, 255}, {255, 255, 0}, {0, 255, 0}} // red, blue, yellow, green
	writeType6Patch(w, 0, pts, cols)
	br := &bitReader{data: w.data}

	params := meshParams{
		bitsPerCoordinate: 8, bitsPerComponent: 8,
		decode: []float64{0, 255, 0, 255, 0, 1, 0, 1, 0, 1}, numComps: 3, cs: deviceRGB(t),
	}
	triangles, err := decodeType6Mesh(br, params, 8)
	if err != nil {
		t.Fatalf("decodeType6Mesh: %v", err)
	}
	wantTriangleCount := 2 * meshPatchSubdivisions * meshPatchSubdivisions
	if len(triangles) != wantTriangleCount {
		t.Fatalf("len(triangles) = %d, want %d", len(triangles), wantTriangleCount)
	}
	first := triangles[0]
	if diff := first.X0 - 0; diff > 0.01 || diff < -0.01 {
		t.Fatalf("first vertex X = %v, want ~0", first.X0)
	}
	if first.C0.R < 0.99 {
		t.Fatalf("first vertex color = %+v, want ~red", first.C0)
	}
}

func TestDecodeType6MeshInvalidFlagIsMalformed(t *testing.T) {
	w := &meshBitWriter{}
	w.write(9, 8) // invalid flag
	br := &bitReader{data: w.data}
	params := meshParams{bitsPerCoordinate: 8, bitsPerComponent: 8, decode: []float64{0, 1, 0, 1, 0, 1, 0, 1, 0, 1}, numComps: 3, cs: deviceRGB(t)}
	if _, err := decodeType6Mesh(br, params, 8); err == nil {
		t.Fatalf("decodeType6Mesh with edge flag 9: want an error, got nil")
	}
}

func TestDecodeType6MeshFlag1WithoutPriorPatchIsMalformed(t *testing.T) {
	w := &meshBitWriter{}
	w.write(1, 8) // flag 1 with no earlier patch to share from
	br := &bitReader{data: w.data}
	params := meshParams{bitsPerCoordinate: 8, bitsPerComponent: 8, decode: []float64{0, 1, 0, 1, 0, 1, 0, 1, 0, 1}, numComps: 3, cs: deviceRGB(t)}
	if _, err := decodeType6Mesh(br, params, 8); err == nil {
		t.Fatalf("decodeType6Mesh with edge flag 1 and no prior patch: want an error, got nil")
	}
}

// writeType7Patch packs one Type 7 patch record (flag, boundary point
// pairs, internal point pairs, colors - all 8 bits wide) with no trailing
// byte alignment, matching decodeType7Mesh's own continuous packing.
func writeType7Patch(w *meshBitWriter, flag byte, boundary, internal []point2D, cols [][3]byte) {
	w.write(uint32(flag), 8)
	for _, p := range append(append([]point2D{}, boundary...), internal...) {
		w.write(uint32(p.X), 8)
		w.write(uint32(p.Y), 8)
	}
	for _, c := range cols {
		w.write(uint32(c[0]), 8)
		w.write(uint32(c[1]), 8)
		w.write(uint32(c[2]), 8)
	}
}

// flatRectangleBoundary8Bit is the same flat, straight-edged 0..255
// square boundary TestDecodeType6MeshSinglePatch uses, factored out so
// the Type 7 tests below can build a comparable patch.
func flatRectangleBoundary8Bit() []point2D {
	return []point2D{
		{0, 0}, {0, 85}, {0, 170}, {0, 255}, {85, 255}, {170, 255},
		{255, 255}, {255, 170}, {255, 85}, {255, 0}, {170, 0}, {85, 0},
	}
}

func type7Params(t *testing.T) meshParams {
	return meshParams{
		bitsPerCoordinate: 8, bitsPerComponent: 8,
		decode: []float64{0, 255, 0, 255, 0, 1, 0, 1, 0, 1}, numComps: 3, cs: deviceRGB(t),
	}
}

func TestDecodeType7MeshSinglePatch(t *testing.T) {
	w := &meshBitWriter{}
	// Internal points at the same bilinear "thirds" positions
	// fillCoonsInternalPoints would derive for this flat boundary (see
	// TestFillCoonsInternalPointsFlatRectangle, scaled to 0-255), read in
	// pts[5], pts[9], pts[10], pts[6] order.
	internal := []point2D{{85, 85}, {85, 170}, {170, 170}, {170, 85}}
	cols := [][3]byte{{255, 0, 0}, {0, 0, 255}, {255, 255, 0}, {0, 255, 0}} // red, blue, yellow, green
	writeType7Patch(w, 0, flatRectangleBoundary8Bit(), internal, cols)
	br := &bitReader{data: w.data}

	triangles, err := decodeType7Mesh(br, type7Params(t), 8)
	if err != nil {
		t.Fatalf("decodeType7Mesh: %v", err)
	}
	wantTriangleCount := 2 * meshPatchSubdivisions * meshPatchSubdivisions
	if len(triangles) != wantTriangleCount {
		t.Fatalf("len(triangles) = %d, want %d", len(triangles), wantTriangleCount)
	}
	first := triangles[0]
	if diff := first.X0 - 0; diff > 0.01 || diff < -0.01 {
		t.Fatalf("first vertex X = %v, want ~0", first.X0)
	}
	if first.C0.R < 0.99 {
		t.Fatalf("first vertex color = %+v, want ~red", first.C0)
	}
}

// TestDecodeType7MeshUsesInternalPointsFromStream confirms Type 7's
// internal control points actually come from the stream's own extra 4
// points, rather than being silently ignored or (incorrectly) derived
// via Type 6's Coons formula: decoding the same boundary/colors twice
// with two very different sets of internal points must produce two
// measurably different surfaces, since the internal control points
// directly affect the interior geometry a Bezier surface interpolates.
func TestDecodeType7MeshUsesInternalPointsFromStream(t *testing.T) {
	cols := [][3]byte{{255, 0, 0}, {0, 0, 255}, {255, 255, 0}, {0, 255, 0}}
	boundary := flatRectangleBoundary8Bit()

	decodeWith := func(internal []point2D) []graphics.MeshTriangle {
		w := &meshBitWriter{}
		writeType7Patch(w, 0, boundary, internal, cols)
		triangles, err := decodeType7Mesh(&bitReader{data: w.data}, type7Params(t), 8)
		if err != nil {
			t.Fatalf("decodeType7Mesh: %v", err)
		}
		return triangles
	}

	bilinear := decodeWith([]point2D{{85, 85}, {85, 170}, {170, 170}, {170, 85}})
	collapsed := decodeWith([]point2D{{0, 0}, {0, 0}, {0, 0}, {0, 0}})

	if len(bilinear) != len(collapsed) {
		t.Fatalf("triangle counts differ: %d vs %d, want equal (same subdivision grid either way)", len(bilinear), len(collapsed))
	}
	// A cell strictly in the interior of the (row, col) subdivision grid
	// - not on any boundary row/column - since a Bezier surface's own
	// *edges* are determined entirely by the boundary control points in
	// that row/column and never touched by the internal ones at all;
	// picking a boundary-adjacent cell here would test nothing (see
	// latticeToTriangles for how (row, col) maps to a flat triangle
	// index: 2 triangles per cell, n cells per row).
	n := meshPatchSubdivisions
	interior := (n/2)*2*n + (n/2)*2
	a, b := bilinear[interior], collapsed[interior]
	if a.X0 == b.X0 && a.Y0 == b.Y0 {
		t.Fatalf("interior vertex identical (%v,%v) between two very different internal-point sets - internal points do not appear to affect the decoded surface", a.X0, a.Y0)
	}
}

func TestDecodeType7MeshInvalidFlagIsMalformed(t *testing.T) {
	w := &meshBitWriter{}
	w.write(9, 8) // invalid flag
	br := &bitReader{data: w.data}
	if _, err := decodeType7Mesh(br, type7Params(t), 8); err == nil {
		t.Fatalf("decodeType7Mesh with edge flag 9: want an error, got nil")
	}
}

func TestDecodeType7MeshFlag1WithoutPriorPatchIsMalformed(t *testing.T) {
	w := &meshBitWriter{}
	w.write(1, 8) // flag 1 with no earlier patch to share from
	br := &bitReader{data: w.data}
	if _, err := decodeType7Mesh(br, type7Params(t), 8); err == nil {
		t.Fatalf("decodeType7Mesh with edge flag 1 and no prior patch: want an error, got nil")
	}
}
