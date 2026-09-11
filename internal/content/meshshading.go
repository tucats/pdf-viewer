package content

import (
	"github.com/tucats/pdf-viewer/internal/function"
	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements PDF's four mesh shading types (8.7.4.5.5-.8):
// ShadingType 4 (free-form Gouraud-shaded triangle mesh), 5 (lattice-form
// Gouraud-shaded triangle mesh), 6 (Coons patch mesh), and 7
// (tensor-product patch mesh). Unlike axial/radial/function-based
// shadings (shading.go), which are each a small formula evaluated live
// per pixel, a mesh shading's data is a stream of packed binary vertex or
// patch records - closer in spirit to how internal/image decodes a
// sample row than to how shading.go builds a ColorAt closure. This file's
// job is exactly that decoding: turning a mesh shading's stream bytes
// into a flat []graphics.MeshTriangle (see internal/graphics/shading.go),
// after which internal/raster paints it exactly like any other shading -
// see MeshTriangle's own doc comment for how a pixel's color is found
// once decoding is done.
//
// The bit-packing rules (which fields are byte-aligned and which are
// packed continuously with no padding at all) differ, undocumented by
// any obvious pattern, between the mesh types - Type 4 pads every vertex
// out to a byte boundary; Types 5, 6, and 7 do not pad at all. This was
// confirmed against Mozilla's pdf.js (a mature, spec-conformant, MPL-2.0
// implementation - see its src/core/pattern.js MeshStreamReader and
// MeshShading classes) rather than derived from memory of the
// specification text alone, since getting this wrong produces a mesh
// that decodes without error but paints complete garbage - exactly the
// kind of mistake round-trip self-testing cannot catch (see 8a's own
// doc comment in docs/PLAN2.md for the general shape of that risk). Only
// the bit-layout rules and patch/flag arithmetic were consulted; no
// pdf.js source was copied, and this file's Go implementation, types, and
// structure are this project's own.

// bitReader walks an already-fully-decoded (filters applied) mesh
// shading stream's bytes, extracting successive most-significant-bit-
// first unsigned integers of a given bit width - the same packing
// convention internal/image's own (separately implemented, per that
// package's doc comment precedent of small duplicated bit readers rather
// than a shared package) bitReader and internal/function/type0.go's
// sampleAt use for image samples and Type 0 function samples
// respectively. Reading past the end of data returns zero bits rather
// than panicking, matching this project's general tolerance for
// malformed/truncated input.
type bitReader struct {
	data   []byte
	bitPos int
}

// hasData reports whether at least one more full byte remains unread -
// used as each mesh decoder's own loop condition ("keep reading records
// until the stream runs out"), since a mesh shading's stream has no
// explicit vertex/patch count of its own.
func (br *bitReader) hasData() bool {
	return br.bitPos/8 < len(br.data)
}

func (br *bitReader) read(bits int) uint32 {
	var v uint32
	for i := 0; i < bits; i++ {
		byteIdx := br.bitPos / 8
		bitIdx := 7 - (br.bitPos % 8)
		var bit uint32
		if byteIdx < len(br.data) {
			bit = uint32(br.data[byteIdx]>>uint(bitIdx)) & 1
		}
		v = v<<1 | bit
		br.bitPos++
	}
	return v
}

// align skips forward to the next byte boundary, discarding any unread
// bits in the current byte - Type 4's per-vertex padding rule (see this
// file's doc comment); Types 5-7 never call this.
func (br *bitReader) align() {
	if rem := br.bitPos % 8; rem != 0 {
		br.bitPos += 8 - rem
	}
}

// bitScale returns the multiplier that turns a raw bits-wide unsigned
// integer into a [0,1] fraction: 1/(2^bits - 1) for any width this
// project actually validates (see validateMeshBitWidth), except 32 bits
// itself, whose maximum value (2^32 - 1) does not fit back into the
// float64 arithmetic exactly the same way - 2^-32 is what the
// specification's own examples and other implementations use there, and
// the tiny resulting difference (on the order of 1 part in 4 billion) is
// far below anything a rendered pixel could show anyway.
func bitScale(bits int) float64 {
	if bits >= 32 {
		return 1.0 / 4294967296.0 // 2^-32
	}
	return 1.0 / float64((uint32(1)<<uint(bits))-1)
}

// point2D is a plain 2-D point in shading space - used throughout this
// file instead of two separate float64 locals purely for readability
// (a slice of these reads much more clearly than two parallel slices of
// x's and y's).
type point2D struct {
	X, Y float64
}

// meshVertex is one decoded mesh vertex: a position plus its own already
// fully color-space-converted color - the common currency every mesh
// type's decoder produces and latticeToTriangles/meshTriangleFrom
// consume, regardless of how differently each type's own stream format
// had to be parsed to produce it.
type meshVertex struct {
	X, Y float64
	C    graphics.Color
}

// meshParams bundles the handful of values every mesh type's per-vertex
// reads need, computed once by buildMeshShading before any decoding
// starts, so the per-type decode functions below (decodeType4Mesh and,
// in later sub-phases, Types 5-7) take one small struct instead of five
// separate parameters.
type meshParams struct {
	bitsPerCoordinate int
	bitsPerComponent  int
	decode            []float64 // [xmin xmax ymin ymax c0min c0max c1min c1max ...]
	numComps          int       // 1 if fn != nil (a single parametric input), else cs.Components()
	fn                function.Function
	cs                pdfimage.ColorSpace
}

// readCoordinate reads one (x, y) pair - 2*bitsPerCoordinate bits total -
// and maps each raw integer into real shading-space coordinates via
// decode[0:2] (x) and decode[2:4] (y), per 8.7.4.5.5's Table 84 (used
// identically, other than the number of trailing color entries, by every
// mesh type).
func readCoordinate(br *bitReader, bitsPerCoordinate int, decode []float64) point2D {
	scale := bitScale(bitsPerCoordinate)
	xi := br.read(bitsPerCoordinate)
	yi := br.read(bitsPerCoordinate)
	return point2D{
		X: float64(xi)*scale*(decode[1]-decode[0]) + decode[0],
		Y: float64(yi)*scale*(decode[3]-decode[2]) + decode[2],
	}
}

// readColor reads numComps raw color components - bitsPerComponent bits
// each - mapping every one through its own pair of the /Decode array
// (entries 4, 5 for component 0; 6, 7 for component 1; and so on),
// exactly mirroring readCoordinate's mapping formula for /Coords.
func readColor(br *bitReader, bitsPerComponent int, decode []float64, numComps int) []float64 {
	scale := bitScale(bitsPerComponent)
	out := make([]float64, numComps)
	for i := 0; i < numComps; i++ {
		ci := br.read(bitsPerComponent)
		j := 4 + 2*i
		out[i] = float64(ci)*scale*(decode[j+1]-decode[j]) + decode[j]
	}
	return out
}

// meshColor converts raw decoded color components into a graphics.Color,
// first passing them through the shading's /Function (when it has one -
// see meshParams.numComps's own doc comment) and then through the
// shading's color space, exactly mirroring buildAxialOrRadialShading's
// ColorAt closure. A validated function (buildMeshShading checks its
// input/output arity once, up front, before any vertex is ever decoded)
// cannot fail here in practice; the black fallback on error is the same
// defensive default used there.
func meshColor(raw []float64, fn function.Function, cs pdfimage.ColorSpace) graphics.Color {
	comps := raw
	if fn != nil {
		out, err := fn.Eval(raw)
		if err != nil {
			return graphics.Color{}
		}
		comps = out
	}
	r, g, b := cs.ToRGB(comps)
	return graphics.Color{R: r, G: g, B: b}
}

// meshTriangleFrom packages three vertices (in a, b, c winding order -
// meaningless for Gouraud shading itself, which only cares about the
// triangle's shape and each corner's color, not which way it winds) into
// a graphics.MeshTriangle.
func meshTriangleFrom(a, b, c meshVertex) graphics.MeshTriangle {
	return graphics.MeshTriangle{
		X0: a.X, Y0: a.Y, C0: a.C,
		X1: b.X, Y1: b.Y, C1: b.C,
		X2: c.X, Y2: c.Y, C2: c.C,
	}
}

// latticeToTriangles triangulates a regular grid of vertices - Type 5's
// entire vertex stream is already exactly this shape (see
// decodeType5Mesh, added in a later sub-phase), and Types 6/7's patches
// are reduced to this same shape by subdividing each patch's bicubic
// surface into a fine grid (see patchToTriangles, likewise a later
// sub-phase) before reaching here. Each 2x2 block of grid cells becomes
// two triangles, split along the same diagonal throughout - an arbitrary
// but consistent choice, since a bilinearly-varying quad has no single
// "more correct" diagonal.
func latticeToTriangles(rows [][]meshVertex) []graphics.MeshTriangle {
	var triangles []graphics.MeshTriangle
	for r := 0; r+1 < len(rows); r++ {
		top, bottom := rows[r], rows[r+1]
		n := len(top)
		if len(bottom) < n {
			n = len(bottom)
		}
		for c := 0; c+1 < n; c++ {
			v00, v01 := top[c], top[c+1]
			v10, v11 := bottom[c], bottom[c+1]
			triangles = append(triangles,
				meshTriangleFrom(v00, v01, v10),
				meshTriangleFrom(v01, v11, v10),
			)
		}
	}
	return triangles
}

// validMeshBitWidth reports whether bits is one of the specification's
// own enumerated values for the given field kind (8.7.4.5.5's Table 84):
// BitsPerCoordinate and BitsPerComponent share one set, BitsPerFlag has
// its own, smaller set. Mirrors this project's existing precedent of
// validating a PDF-defined enumeration explicitly (see
// internal/function/type0.go's identical treatment of /BitsPerSample)
// rather than accepting any bit count that happens to parse.
func validMeshBitWidth(bits int, isFlag bool) bool {
	if isFlag {
		switch bits {
		case 2, 4, 8:
			return true
		}
		return false
	}
	switch bits {
	case 1, 2, 4, 8, 12, 16, 24, 32:
		return true
	}
	return false
}

// requiredIntEntry resolves dict[key] into an int, reporting a
// pdferror.Malformedf error (naming key) if the entry is absent or not a
// number - the common shape every mesh shading dictionary's required
// /BitsPerCoordinate, /BitsPerComponent, /BitsPerFlag, and
// /VerticesPerRow entries share.
func requiredIntEntry(r pdfimage.Resolver, dict syntax.Dictionary, key syntax.Name) (int, error) {
	obj, ok := dict[key]
	if !ok {
		return 0, pdferror.Malformedf("shading dictionary has no required /%s entry", key)
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return 0, err
	}
	v, ok := numberValue(resolved)
	if !ok {
		return 0, pdferror.Malformedf("/%s is not a number (found %T)", key, resolved)
	}
	return int(v), nil
}

// buildMeshShading builds any of the four mesh shading types (4-7) from
// its already-identified stream, sharing every piece of parsing the
// types have in common (/BitsPerCoordinate, /BitsPerComponent, the
// optional /Function, /ColorSpace, /Decode) before handing off to
// whichever per-type decoder actually walks the packed vertex/patch
// bitstream.
func (in *interpreter) buildMeshShading(dict syntax.Dictionary, stream syntax.Stream, shadingType int, shadingToDevice graphics.Matrix) (*graphics.Shading, error) {
	bitsPerCoordinate, err := requiredIntEntry(in.resolver, dict, "BitsPerCoordinate")
	if err != nil {
		return nil, err
	}
	if !validMeshBitWidth(bitsPerCoordinate, false) {
		return nil, pdferror.Malformedf("mesh shading /BitsPerCoordinate %d is not one of 1, 2, 4, 8, 12, 16, 24, 32", bitsPerCoordinate)
	}

	bitsPerComponent, err := requiredIntEntry(in.resolver, dict, "BitsPerComponent")
	if err != nil {
		return nil, err
	}
	if !validMeshBitWidth(bitsPerComponent, false) {
		return nil, pdferror.Malformedf("mesh shading /BitsPerComponent %d is not one of 1, 2, 4, 8, 12, 16, 24, 32", bitsPerComponent)
	}

	var fn function.Function
	if fnObj, ok := dict["Function"]; ok {
		fn, err = function.Parse(in.resolver, fnObj)
		if err != nil {
			return nil, err
		}
	}

	csEntry, ok := dict["ColorSpace"]
	if !ok {
		return nil, pdferror.Malformedf("shading has no /ColorSpace")
	}
	// See shading.go's identical resolveIfRef call for why this step -
	// missing here until this fix - is necessary: /ColorSpace may itself
	// be an indirect reference, and pdfimage.ResolveColorSpace expects
	// its caller to have already resolved one level.
	csObj, err := resolveIfRef(in.resolver, csEntry)
	if err != nil {
		return nil, err
	}
	cs, err := pdfimage.ResolveColorSpace(in.resolver, csObj, in.resources)
	if err != nil {
		return nil, err
	}

	// Per 8.7.4.5.5: "If the shading dictionary includes a Function
	// entry, only a single parametric value shall be specified for each
	// vertex" - the function itself then maps that one value up to
	// however many components the color space needs, exactly like an
	// axial/radial shading's single t parameter.
	numComps := cs.Components()
	if fn != nil {
		if fn.NumInputs() != 1 {
			return nil, pdferror.Malformedf("mesh shading /Function must take 1 input, takes %d", fn.NumInputs())
		}
		if fn.NumOutputs() != cs.Components() {
			return nil, pdferror.Malformedf("mesh shading /Function produces %d output(s), /ColorSpace needs %d", fn.NumOutputs(), cs.Components())
		}
		numComps = 1
	}

	decodeVals, found, err := floatArrayEntry(in.resolver, dict, "Decode")
	if err != nil {
		return nil, err
	}
	wantDecode := 4 + 2*numComps
	if !found || len(decodeVals) != wantDecode {
		return nil, pdferror.Malformedf("mesh shading /Decode must have %d entries (4 for /Coords, 2 per color component), has %d", wantDecode, len(decodeVals))
	}

	data, err := in.resolver.DecodeStream(stream)
	if err != nil {
		return nil, err
	}
	br := &bitReader{data: data}
	params := meshParams{
		bitsPerCoordinate: bitsPerCoordinate, bitsPerComponent: bitsPerComponent,
		decode: decodeVals, numComps: numComps, fn: fn, cs: cs,
	}

	var triangles []graphics.MeshTriangle
	switch shadingType {
	case 4:
		bitsPerFlag, err := requiredIntEntry(in.resolver, dict, "BitsPerFlag")
		if err != nil {
			return nil, err
		}
		if !validMeshBitWidth(bitsPerFlag, true) {
			return nil, pdferror.Malformedf("mesh shading /BitsPerFlag %d is not one of 2, 4, 8", bitsPerFlag)
		}
		triangles, err = decodeType4Mesh(br, params, bitsPerFlag)
		if err != nil {
			return nil, err
		}
	case 5:
		verticesPerRow, err := requiredIntEntry(in.resolver, dict, "VerticesPerRow")
		if err != nil {
			return nil, err
		}
		if verticesPerRow < 2 {
			return nil, pdferror.Malformedf("mesh shading /VerticesPerRow must be at least 2, got %d", verticesPerRow)
		}
		triangles = decodeType5Mesh(br, params, verticesPerRow)
	case 6:
		bitsPerFlag, err := requiredIntEntry(in.resolver, dict, "BitsPerFlag")
		if err != nil {
			return nil, err
		}
		if !validMeshBitWidth(bitsPerFlag, true) {
			return nil, pdferror.Malformedf("mesh shading /BitsPerFlag %d is not one of 2, 4, 8", bitsPerFlag)
		}
		triangles, err = decodeType6Mesh(br, params, bitsPerFlag)
		if err != nil {
			return nil, err
		}
	case 7:
		bitsPerFlag, err := requiredIntEntry(in.resolver, dict, "BitsPerFlag")
		if err != nil {
			return nil, err
		}
		if !validMeshBitWidth(bitsPerFlag, true) {
			return nil, pdferror.Malformedf("mesh shading /BitsPerFlag %d is not one of 2, 4, 8", bitsPerFlag)
		}
		triangles, err = decodeType7Mesh(br, params, bitsPerFlag)
		if err != nil {
			return nil, err
		}
	default:
		return nil, pdferror.Unsupportedf("mesh shading type %d is not a recognized shading type", shadingType)
	}

	return &graphics.Shading{
		Kind:            graphics.ShadingKind(shadingType),
		Triangles:       triangles,
		ShadingToDevice: shadingToDevice,
	}, nil
}

// decodeType4Mesh implements 8.7.4.5.5 (free-form Gouraud-shaded
// triangle mesh): a stream of vertex records, each starting with an edge
// flag deciding how it combines with earlier vertices into triangles -
// 0 starts a brand new, independent triangle (so the *next* two vertices
// complete it); 1 shares the previous triangle's own 2nd and 3rd
// vertices as this new triangle's 1st and 2nd, needing only one more
// vertex; 2 shares the previous triangle's 1st and 3rd the same way. This
// is exactly how a triangle *strip* or *fan* is described one vertex at a
// time instead of three, and is why verts/idx below are built up
// incrementally rather than being read three-at-a-time.
//
// Every vertex record is padded out to its own byte boundary (see this
// file's doc comment) - the one mesh type that pads at all.
func decodeType4Mesh(br *bitReader, p meshParams, bitsPerFlag int) ([]graphics.MeshTriangle, error) {
	var verts []meshVertex
	var idx []int // flat list of indices into verts, 3 per completed triangle
	verticesLeft := 0
	for br.hasData() {
		flag := br.read(bitsPerFlag)
		coord := readCoordinate(br, p.bitsPerCoordinate, p.decode)
		raw := readColor(br, p.bitsPerComponent, p.decode, p.numComps)
		col := meshColor(raw, p.fn, p.cs)

		if verticesLeft == 0 {
			switch flag {
			case 0:
				verticesLeft = 3
			case 1:
				if len(idx) < 2 {
					return nil, pdferror.Malformedf("Type 4 mesh shading: edge flag 1 before any triangle exists")
				}
				idx = append(idx, idx[len(idx)-2], idx[len(idx)-1])
				verticesLeft = 1
			case 2:
				if len(idx) < 3 {
					return nil, pdferror.Malformedf("Type 4 mesh shading: edge flag 2 before any triangle exists")
				}
				idx = append(idx, idx[len(idx)-3], idx[len(idx)-1])
				verticesLeft = 1
			default:
				return nil, pdferror.Malformedf("Type 4 mesh shading: invalid edge flag %d", flag)
			}
		}
		idx = append(idx, len(verts))
		verts = append(verts, meshVertex{X: coord.X, Y: coord.Y, C: col})
		verticesLeft--
		br.align()
	}

	// A truncated final triangle (idx not a multiple of 3) is dropped
	// rather than erroring, matching this project's usual tolerance for
	// malformed trailing data.
	idx = idx[:len(idx)-len(idx)%3]
	triangles := make([]graphics.MeshTriangle, 0, len(idx)/3)
	for i := 0; i+2 < len(idx); i += 3 {
		triangles = append(triangles, meshTriangleFrom(verts[idx[i]], verts[idx[i+1]], verts[idx[i+2]]))
	}
	return triangles, nil
}

// decodeType5Mesh implements 8.7.4.5.6 (lattice-form Gouraud-shaded
// triangle mesh): unlike Type 4, there are no edge flags at all - every
// vertex is simply a (coordinate, color) pair, and the stream's implicit
// structure is a regular grid verticesPerRow wide, read one row at a
// time. Triangles are implied purely by grid adjacency (each 2x2 block of
// cells becomes two triangles - see latticeToTriangles) rather than
// anything the stream itself encodes. Vertex records are *not*
// byte-aligned here (see this file's doc comment) - no br.align() call,
// unlike decodeType4Mesh.
func decodeType5Mesh(br *bitReader, p meshParams, verticesPerRow int) []graphics.MeshTriangle {
	var rows [][]meshVertex
	var row []meshVertex
	for br.hasData() {
		coord := readCoordinate(br, p.bitsPerCoordinate, p.decode)
		raw := readColor(br, p.bitsPerComponent, p.decode, p.numComps)
		row = append(row, meshVertex{X: coord.X, Y: coord.Y, C: meshColor(raw, p.fn, p.cs)})
		if len(row) == verticesPerRow {
			rows = append(rows, row)
			row = nil
		}
	}
	// A trailing partial row (fewer than verticesPerRow vertices) is
	// simply never appended to rows at all, so it is silently dropped -
	// the same "tolerate truncated trailing data" policy
	// decodeType4Mesh's own final-triangle handling uses.
	return latticeToTriangles(rows)
}

// meshPatch is one decoded Coons (Type 6) or tensor-product (Type 7)
// patch's full 4x4 grid of bicubic Bezier control points, plus its four
// corner colors - the shape both patch types reduce to before rendering
// (see patchToTriangles), even though they arrive at it differently
// (Type 6 computes its 4 internal control points from the 12 boundary
// ones; Type 7 reads all 16 directly from the stream - see
// decodeType6Mesh/decodeType7Mesh).
//
// pts is indexed pts[row*4+col], row and col both running 0 (u=0 or v=0)
// to 3 (u=1 or v=1) - row is the "v" direction, col the "u" direction, an
// arbitrary but fixed choice matching patchToTriangles' own use of it.
// The four corners are pts[0] (u=0,v=0), pts[3] (u=1,v=0), pts[12]
// (u=0,v=1), and pts[15] (u=1,v=1) - colors follows that same order:
// colors[0]<->pts[0], colors[1]<->pts[3], colors[2]<->pts[12],
// colors[3]<->pts[15].
type meshPatch struct {
	pts    [16]point2D
	colors [4]graphics.Color
}

// applyPatchBoundary fills patch's 12 boundary control points (every
// index of pts except the 4 internal ones, 5, 6, 9, and 10) and all 4
// corner colors from newPts/newCols, per 8.7.4.5.7's edge-flag rule
// shared identically by Type 6 and Type 7 (only how many *internal*
// points a patch carries differs between the two - see
// decodeType6Mesh/decodeType7Mesh, which fill pts[5,6,9,10] themselves
// after calling this):
//
//   - flag 0: an independent patch. newPts holds all 12 boundary points
//     and newCols all 4 corner colors, read directly off the stream in
//     the specification's own boundary-traversal order.
//   - flags 1-3: this patch shares one edge (4 points, 2 colors) with the
//     *previous* patch, each flag selecting a different edge of that
//     previous patch and a different rotation - newPts holds only the
//     remaining 8 new boundary points and newCols only the 2 new corner
//     colors.
//
// This function's exact index arithmetic for flags 1-3 was cross-checked
// against pdf.js (see this file's own doc comment) rather than derived
// from the specification's prose alone, for the same reason 13b's own
// entry in docs/PLAN2.md gives: a transposition here would silently
// produce a plausible-but-wrong mesh, not an error.
func applyPatchBoundary(patch *meshPatch, prev *meshPatch, flag uint32, newPts []point2D, newCols []graphics.Color) error {
	switch flag {
	case 0:
		patch.pts[0], patch.pts[4], patch.pts[8], patch.pts[12] = newPts[0], newPts[1], newPts[2], newPts[3]
		patch.pts[13], patch.pts[14], patch.pts[15] = newPts[4], newPts[5], newPts[6]
		patch.pts[11], patch.pts[7], patch.pts[3], patch.pts[2], patch.pts[1] = newPts[7], newPts[8], newPts[9], newPts[10], newPts[11]
		patch.colors[0], patch.colors[2], patch.colors[3], patch.colors[1] = newCols[0], newCols[1], newCols[2], newCols[3]
	case 1:
		patch.pts[12], patch.pts[8], patch.pts[4], patch.pts[0] = prev.pts[15], prev.pts[14], prev.pts[13], prev.pts[12]
		patch.pts[13], patch.pts[14], patch.pts[15] = newPts[0], newPts[1], newPts[2]
		patch.pts[11], patch.pts[7], patch.pts[3], patch.pts[2], patch.pts[1] = newPts[3], newPts[4], newPts[5], newPts[6], newPts[7]
		patch.colors[2], patch.colors[0] = prev.colors[3], prev.colors[2]
		patch.colors[3], patch.colors[1] = newCols[0], newCols[1]
	case 2:
		patch.pts[12], patch.pts[8], patch.pts[4], patch.pts[0] = prev.pts[3], prev.pts[7], prev.pts[11], prev.pts[15]
		patch.pts[13], patch.pts[14], patch.pts[15] = newPts[0], newPts[1], newPts[2]
		patch.pts[11], patch.pts[7], patch.pts[3], patch.pts[2], patch.pts[1] = newPts[3], newPts[4], newPts[5], newPts[6], newPts[7]
		patch.colors[2], patch.colors[0] = prev.colors[1], prev.colors[3]
		patch.colors[3], patch.colors[1] = newCols[0], newCols[1]
	case 3:
		patch.pts[12], patch.pts[8], patch.pts[4], patch.pts[0] = prev.pts[0], prev.pts[1], prev.pts[2], prev.pts[3]
		patch.pts[13], patch.pts[14], patch.pts[15] = newPts[0], newPts[1], newPts[2]
		patch.pts[11], patch.pts[7], patch.pts[3], patch.pts[2], patch.pts[1] = newPts[3], newPts[4], newPts[5], newPts[6], newPts[7]
		patch.colors[2], patch.colors[0] = prev.colors[0], prev.colors[1]
		patch.colors[3], patch.colors[1] = newCols[0], newCols[1]
	default:
		return pdferror.Malformedf("mesh patch: invalid edge flag %d", flag)
	}
	return nil
}

// coonsInternal evaluates one of a Coons patch's four internal Bezier
// control points from its 8 nearest boundary points, via the standard
// Coons-patch-to-bicubic-Bezier conversion formula (an informative
// consequence of the Coons patch's own bilinear-blend definition, not
// something this project invented): point = (-4*a - b + 6*(c+d) -
// 2*(e+f) + 3*(g+h)) / 9. fillCoonsInternalPoints below supplies each of
// the four internal points' own particular 8-point argument order.
func coonsInternal(a, b, c, d, e, f, g, h point2D) point2D {
	return point2D{
		X: (-4*a.X - b.X + 6*(c.X+d.X) - 2*(e.X+f.X) + 3*(g.X+h.X)) / 9,
		Y: (-4*a.Y - b.Y + 6*(c.Y+d.Y) - 2*(e.Y+f.Y) + 3*(g.Y+h.Y)) / 9,
	}
}

// fillCoonsInternalPoints computes a Type 6 (Coons) patch's 4 internal
// control points (pts[5], pts[6], pts[9], pts[10]) from its 12 boundary
// points, already filled in by applyPatchBoundary - a Coons patch's own
// data never carries these directly (unlike Type 7's tensor-product
// patch, which reads them from the stream - see decodeType7Mesh, a later
// sub-phase).
func fillCoonsInternalPoints(patch *meshPatch) {
	p := &patch.pts
	p[5] = coonsInternal(p[0], p[15], p[4], p[1], p[12], p[3], p[13], p[7])
	p[6] = coonsInternal(p[3], p[12], p[2], p[7], p[0], p[15], p[4], p[14])
	p[9] = coonsInternal(p[12], p[3], p[8], p[13], p[0], p[15], p[11], p[1])
	p[10] = coonsInternal(p[15], p[0], p[11], p[14], p[12], p[3], p[2], p[8])
}

// bernstein returns the four cubic Bernstein basis polynomial values at
// parameter t in [0,1] - B0(t)=(1-t)^3, B1(t)=3t(1-t)^2, B2(t)=3t^2(1-t),
// B3(t)=t^3 - the weights a cubic Bezier curve (or, applied along both
// axes at once, a bicubic Bezier surface) blends its 4 (or 4x4) control
// points by.
func bernstein(t float64) [4]float64 {
	mt := 1 - t
	return [4]float64{mt * mt * mt, 3 * t * mt * mt, 3 * t * t * mt, t * t * t}
}

// lerpColor linearly interpolates between colors a (t=0) and b (t=1) -
// used by patchToTriangles for a patch's *color*, which the specification
// only ever blends bilinearly across a patch, never through the same
// cubic Bezier basis its geometry uses.
func lerpColor(a, b graphics.Color, t float64) graphics.Color {
	return graphics.Color{
		R: a.R + (b.R-a.R)*t,
		G: a.G + (b.G-a.G)*t,
		B: a.B + (b.B-a.B)*t,
	}
}

// meshPatchSubdivisions is how many equal steps patchToTriangles divides
// each of a patch's two parametric axes into - (meshPatchSubdivisions+1)^2
// sample points, triangulated into 2*meshPatchSubdivisions^2 triangles
// per patch. A fixed subdivision count, rather than pdf.js's own
// area-adaptive density, is a deliberate simplification: correctness (a
// patch's curved surface is approximated closely enough to look smooth
// at typical page-rendering resolutions) rather than pdf.js's specific
// performance tuning is this project's goal here. 16 is comfortably fine
// enough that a patch spanning a large fraction of a typical page does
// not show visible faceting, while keeping the triangle count per patch
// bounded and small regardless of the patch's own device-space size.
const meshPatchSubdivisions = 16

// patchToTriangles evaluates patch's bicubic Bezier surface S(u,v) =
// sum over r,c of B_r(v)*B_c(u)*pts[r*4+c] (see meshPatch's own doc
// comment for the row=v/col=u indexing convention) at a regular
// (meshPatchSubdivisions+1) x (meshPatchSubdivisions+1) grid of (u,v)
// parameter values, bilinearly interpolating color the same way
// buildLatticeFormTriangleMeshShading-style vertex grids do, and hands
// the resulting lattice to latticeToTriangles - the same function Type 5
// itself uses, since by this point a patch has been reduced to exactly
// that shape.
func patchToTriangles(patch meshPatch) []graphics.MeshTriangle {
	n := meshPatchSubdivisions
	rows := make([][]meshVertex, n+1)
	for i := 0; i <= n; i++ {
		v := float64(i) / float64(n)
		bv := bernstein(v)
		// The two color-interpolation edges at this row: u=0 (between the
		// two "v" corners colors[0] and colors[2]) and u=1 (between
		// colors[1] and colors[3]).
		left := lerpColor(patch.colors[0], patch.colors[2], v)
		right := lerpColor(patch.colors[1], patch.colors[3], v)

		row := make([]meshVertex, n+1)
		for j := 0; j <= n; j++ {
			u := float64(j) / float64(n)
			bu := bernstein(u)
			var x, y float64
			for r := 0; r < 4; r++ {
				for c := 0; c < 4; c++ {
					w := bv[r] * bu[c]
					pt := patch.pts[r*4+c]
					x += pt.X * w
					y += pt.Y * w
				}
			}
			row[j] = meshVertex{X: x, Y: y, C: lerpColor(left, right, u)}
		}
		rows[i] = row
	}
	return latticeToTriangles(rows)
}

// readPoints reads n consecutive (x, y) coordinate pairs - the shape a
// Type 6 or Type 7 patch's own boundary/internal control points come in.
func readPoints(br *bitReader, p meshParams, n int) []point2D {
	pts := make([]point2D, n)
	for i := range pts {
		pts[i] = readCoordinate(br, p.bitsPerCoordinate, p.decode)
	}
	return pts
}

// readColors reads n consecutive colors, each already converted through
// the optional /Function and the shading's color space via meshColor.
func readColors(br *bitReader, p meshParams, n int) []graphics.Color {
	cols := make([]graphics.Color, n)
	for i := range cols {
		cols[i] = meshColor(readColor(br, p.bitsPerComponent, p.decode, p.numComps), p.fn, p.cs)
	}
	return cols
}

// decodeType6Mesh implements 8.7.4.5.7 (Coons patch mesh): a stream of
// patch records, each an edge flag followed by either 12 boundary control
// points + 4 corner colors (flag 0, an independent patch) or 8 new
// boundary points + 2 new colors (flags 1-3, sharing one edge with the
// immediately preceding patch - see applyPatchBoundary). Patch records
// are packed with no byte alignment at all (like Types 5 and 7, unlike
// Type 4 - see this file's own doc comment). Each patch's 4 internal
// control points are always derived from its own boundary
// (fillCoonsInternalPoints) rather than read from the stream - the one
// structural difference from Type 7's tensor-product patch.
func decodeType6Mesh(br *bitReader, p meshParams, bitsPerFlag int) ([]graphics.MeshTriangle, error) {
	var triangles []graphics.MeshTriangle
	var prev *meshPatch
	for br.hasData() {
		flag := br.read(bitsPerFlag)
		if flag > 3 {
			return nil, pdferror.Malformedf("Type 6 mesh shading: invalid edge flag %d", flag)
		}
		if flag != 0 && prev == nil {
			return nil, pdferror.Malformedf("Type 6 mesh shading: edge flag %d before any patch exists", flag)
		}

		nPts, nCols := 12, 4
		if flag != 0 {
			nPts, nCols = 8, 2
		}
		newPts := readPoints(br, p, nPts)
		newCols := readColors(br, p, nCols)

		var patch meshPatch
		if err := applyPatchBoundary(&patch, prev, flag, newPts, newCols); err != nil {
			return nil, err
		}
		fillCoonsInternalPoints(&patch)

		triangles = append(triangles, patchToTriangles(patch)...)
		patchCopy := patch
		prev = &patchCopy
	}
	return triangles, nil
}

// decodeType7Mesh implements 8.7.4.5.8 (tensor-product patch mesh): the
// same boundary/edge-flag structure as Type 6 (reusing
// applyPatchBoundary unchanged), except a Type 7 patch also carries its
// own 4 internal control points directly in the stream rather than
// having them derived from the boundary - immediately after the boundary
// points, in a fixed relative order (pts[5], pts[9], pts[10], pts[6]),
// regardless of whether this is a fresh (flag 0, 16 total points) or
// edge-sharing (flags 1-3, 12 total points, the last 4 still being the
// internal ones) patch. Patch records are packed with no byte alignment,
// exactly like Type 6.
func decodeType7Mesh(br *bitReader, p meshParams, bitsPerFlag int) ([]graphics.MeshTriangle, error) {
	var triangles []graphics.MeshTriangle
	var prev *meshPatch
	for br.hasData() {
		flag := br.read(bitsPerFlag)
		if flag > 3 {
			return nil, pdferror.Malformedf("Type 7 mesh shading: invalid edge flag %d", flag)
		}
		if flag != 0 && prev == nil {
			return nil, pdferror.Malformedf("Type 7 mesh shading: edge flag %d before any patch exists", flag)
		}

		nBoundary, nCols := 12, 4
		if flag != 0 {
			nBoundary, nCols = 8, 2
		}
		newPts := readPoints(br, p, nBoundary+4) // boundary points, then 4 internal points
		newCols := readColors(br, p, nCols)

		var patch meshPatch
		if err := applyPatchBoundary(&patch, prev, flag, newPts[:nBoundary], newCols); err != nil {
			return nil, err
		}
		internal := newPts[nBoundary:]
		patch.pts[5], patch.pts[9], patch.pts[10], patch.pts[6] = internal[0], internal[1], internal[2], internal[3]

		triangles = append(triangles, patchToTriangles(patch)...)
		patchCopy := patch
		prev = &patchCopy
	}
	return triangles, nil
}
