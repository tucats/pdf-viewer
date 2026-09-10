package annotation

import (
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// flagHidden and flagNoView are bit positions within an annotation
// dictionary's /F (flags) integer entry (12.5.3, Table 165): an
// annotation with either bit set must not be displayed on screen (bit 2,
// "Hidden", excludes it from *any* rendering; bit 6, "NoView", excludes
// it from on-screen display specifically, e.g. a print-only watermark) -
// this project's Page.Render only ever produces an on-screen-style
// raster, so both are treated identically. PDF numbers flag bits
// 1-based from the least significant bit, so bit 2 is 1<<1 and bit 6 is
// 1<<5.
const (
	flagHidden = 1 << 1
	flagNoView = 1 << 5
)

// Appearance is one annotation's resolved, paintable "normal" appearance
// - a Form XObject stream (see internal/content's form.go for how a Form
// XObject's own content is executed; an appearance stream has exactly
// that same structure) together with the matrix a caller must apply on
// top of the appearance's own /Matrix (which internal/content's Form
// execution already applies) to make it land correctly on the page.
type Appearance struct {
	// Stream is the appearance's Form XObject stream, exactly as found
	// (its own /BBox, /Matrix, /Resources, and content bytes untouched) -
	// a caller paints it precisely as it would any other Form XObject
	// (see the root package's annotations.go), just supplying Matrix
	// below as the "current CTM" the Form's own /Matrix should compose
	// onto, rather than whatever CTM an ordinary "Do" operator happens to
	// be running under.
	Stream syntax.Stream

	// Matrix is the "A" matrix the package doc comment describes: it
	// maps the appearance's own /BBox (after that /BBox has already been
	// transformed by the appearance's own /Matrix - see the package doc
	// comment's algorithm) onto the annotation's /Rect, both expressed in
	// the page's default user space. A caller composes this with the
	// page's own device-mapping CTM (Matrix.Mul(pageCTM), in this
	// package's row-vector convention - see graphics.Matrix.Mul's doc
	// comment) to get the CTM the appearance stream should actually be
	// interpreted with.
	Matrix graphics.Matrix
}

// Resolve reads pageDict's /Annots array (if any) and returns one
// Appearance per annotation that has a usable, currently-visible normal
// appearance - in page order, skipping (with no error) every annotation
// that does not: hidden or excluded from view via /F, no /AP, an /AP /N
// that cannot be resolved to a single appearance stream (including a
// multi-state /AP /N subdictionary with no matching /AS), or a malformed
// /Rect or appearance /BBox. Annotations are optional, decorative
// content layered on top of a page's own content stream - a single bad
// or unusual annotation dictionary (real-world PDF producers vary widely
// in how carefully they write these) degrades to "that one annotation is
// not painted", never to failing the whole page's render, so this
// function has no error return at all.
func Resolve(r Resolver, pageDict syntax.Dictionary) []Appearance {
	var out []Appearance
	for _, dict := range ResolveAnnots(r, pageDict) {
		if app, ok := ResolveOne(r, dict); ok {
			out = append(out, app)
		}
	}
	return out
}

// ResolveAnnots resolves pageDict's /Annots array (if any) into the
// dictionaries it names, in page order, following each array element's
// own indirect reference and silently dropping any element that does
// not resolve to a syntax.Dictionary - the same "annotations are
// optional, decorative content" tolerance Resolve's own doc comment
// describes, extended here to a bad *array element* rather than a bad
// appearance.
//
// This is split out from Resolve so that a caller needing to inspect
// annotation dictionaries directly - the root package's Phase 12
// AcroForm appearance-regeneration path (see annotations.go), which
// must tell "no usable existing appearance" apart from "not a widget
// annotation at all" for the *same* dictionary Resolve already looked
// at - does not need to re-walk /Annots with its own copy of this
// resolution logic.
func ResolveAnnots(r Resolver, pageDict syntax.Dictionary) []syntax.Dictionary {
	annotsObj, ok := pageDict["Annots"]
	if !ok {
		return nil
	}
	resolved, err := resolveIfRef(r, annotsObj)
	if err != nil {
		return nil
	}
	arr, ok := resolved.(syntax.Array)
	if !ok {
		return nil
	}

	var out []syntax.Dictionary
	for _, entry := range arr {
		resolvedEntry, err := resolveIfRef(r, entry)
		if err != nil {
			continue
		}
		if dict, ok := resolvedEntry.(syntax.Dictionary); ok {
			out = append(out, dict)
		}
	}
	return out
}

// ResolveOne resolves a single annotation dictionary into an Appearance,
// reporting ok=false for every way that can come up empty - see
// Resolve's doc comment for the full list.
func ResolveOne(r Resolver, dict syntax.Dictionary) (Appearance, bool) {
	if flags, ok := intEntry(r, dict, "F"); ok {
		if flags&(flagHidden|flagNoView) != 0 {
			return Appearance{}, false
		}
	}

	apStream, ok := resolveNormalAppearance(r, dict)
	if !ok {
		return Appearance{}, false
	}

	rect, ok := floatArrayEntry(r, dict, "Rect")
	if !ok || len(rect) != 4 {
		return Appearance{}, false
	}

	return FromStream(r, rect, apStream)
}

// FromStream builds an Appearance from apStream, an already-resolved
// Form XObject stream, mapped onto rect ([llx lly urx ury], in the
// page's default user space) via this package's doc comment's BBox-to-
// Rect algorithm. ResolveOne (above) is simply this preceded by finding
// apStream and rect from an annotation dictionary's own /AP/AS and
// /Rect; a caller that already has both in hand - the root package's
// Phase 12 AcroForm appearance-generation path, which builds apStream
// itself rather than finding one already in the file - calls this
// directly instead, so the BBox/Matrix mapping math is written and
// tested in exactly one place regardless of where the appearance stream
// came from.
func FromStream(r Resolver, rect []float64, apStream syntax.Stream) (Appearance, bool) {
	apDict, err := r.ResolveDictionary(apStream.Dict)
	if err != nil {
		return Appearance{}, false
	}
	bbox, ok := floatArrayEntry(r, apDict, "BBox")
	if !ok || len(bbox) != 4 {
		return Appearance{}, false
	}
	apMatrix := graphics.Identity()
	if mv, ok := floatArrayEntry(r, apDict, "Matrix"); ok && len(mv) == 6 {
		apMatrix = graphics.Matrix{A: mv[0], B: mv[1], C: mv[2], D: mv[3], E: mv[4], F: mv[5]}
	}

	if len(rect) != 4 {
		return Appearance{}, false
	}
	bboxPrime := transformedBounds(bbox, apMatrix)
	rectNorm := normalizeRect(rect)
	return Appearance{Stream: apStream, Matrix: rectMappingMatrix(bboxPrime, rectNorm)}, true
}

// resolveNormalAppearance resolves dict's /AP /N entry into a single
// appearance stream: either directly (when /N is itself a stream - the
// common case, an annotation with only one possible appearance) or via
// dict's own /AS (appearance state) name indexing into /N when /N is a
// subdictionary of several named states (a checkbox's "On"/"Off", for
// instance) - per 12.5.5, /AS is required whenever /N takes this second
// form, so a missing or non-matching /AS here means no usable appearance
// rather than an arbitrary guess at which state to show.
func resolveNormalAppearance(r Resolver, dict syntax.Dictionary) (syntax.Stream, bool) {
	apObj, ok := dict["AP"]
	if !ok {
		return syntax.Stream{}, false
	}
	resolvedAP, err := resolveIfRef(r, apObj)
	if err != nil {
		return syntax.Stream{}, false
	}
	apDict, ok := resolvedAP.(syntax.Dictionary)
	if !ok {
		return syntax.Stream{}, false
	}
	nObj, ok := apDict["N"]
	if !ok {
		return syntax.Stream{}, false
	}
	resolvedN, err := resolveIfRef(r, nObj)
	if err != nil {
		return syntax.Stream{}, false
	}

	switch v := resolvedN.(type) {
	case syntax.Stream:
		return v, true
	case syntax.Dictionary:
		asName, ok := dict["AS"].(syntax.Name)
		if !ok {
			return syntax.Stream{}, false
		}
		entry, ok := v[asName]
		if !ok {
			return syntax.Stream{}, false
		}
		resolvedEntry, err := resolveIfRef(r, entry)
		if err != nil {
			return syntax.Stream{}, false
		}
		s, ok := resolvedEntry.(syntax.Stream)
		return s, ok
	default:
		return syntax.Stream{}, false
	}
}

// transformedBounds applies m to arr's four corners (interpreted as
// [llx lly urx ury], per PDF's own rectangle-array convention - see
// internal/model.Rect's identical convention) and returns the smallest
// upright rectangle enclosing the four transformed points, as
// [minX minY maxX maxY] - step 1 of the package doc comment's algorithm.
func transformedBounds(arr []float64, m graphics.Matrix) [4]float64 {
	corners := [4][2]float64{
		{arr[0], arr[1]}, {arr[2], arr[1]}, {arr[2], arr[3]}, {arr[0], arr[3]},
	}
	minX, minY := transformX(m, corners[0]), transformY(m, corners[0])
	maxX, maxY := minX, minY
	for _, c := range corners[1:] {
		x, y := transformX(m, c), transformY(m, c)
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	return [4]float64{minX, minY, maxX, maxY}
}

func transformX(m graphics.Matrix, p [2]float64) float64 { x, _ := m.Apply(p[0], p[1]); return x }
func transformY(m graphics.Matrix, p [2]float64) float64 { _, y := m.Apply(p[0], p[1]); return y }

// normalizeRect returns arr (a [llx lly urx ury] rectangle array) as
// [minX minY maxX maxY] - PDF does not require llx<urx or lly<ury (a
// producer may list either pair of corners in either order), matching
// internal/model.Rect's identical tolerance.
func normalizeRect(arr []float64) [4]float64 {
	minX, maxX := arr[0], arr[2]
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	minY, maxY := arr[1], arr[3]
	if minY > maxY {
		minY, maxY = maxY, minY
	}
	return [4]float64{minX, minY, maxX, maxY}
}

// rectMappingMatrix computes the "A" matrix from the package doc
// comment's step 2: translate and independently scale x and y so that
// bboxPrime (as computed by transformedBounds) maps exactly onto rect
// (as computed by normalizeRect). A zero-width or zero-height bboxPrime
// (a degenerate appearance /BBox, once transformed) cannot be
// meaningfully scaled to fit rect's corresponding dimension - that axis
// falls back to scale 1 with no translation adjustment beyond aligning
// the low corner, rather than dividing by zero.
func rectMappingMatrix(bboxPrime, rect [4]float64) graphics.Matrix {
	bMinX, bMinY, bMaxX, bMaxY := bboxPrime[0], bboxPrime[1], bboxPrime[2], bboxPrime[3]
	rMinX, rMinY, rMaxX, rMaxY := rect[0], rect[1], rect[2], rect[3]

	sx := 1.0
	if w := bMaxX - bMinX; w != 0 {
		sx = (rMaxX - rMinX) / w
	}
	sy := 1.0
	if h := bMaxY - bMinY; h != 0 {
		sy = (rMaxY - rMinY) / h
	}

	return graphics.Matrix{
		A: sx, D: sy,
		E: rMinX - bMinX*sx,
		F: rMinY - bMinY*sy,
	}
}

// intEntry resolves dict[key] and reports it as an int, following a
// top-level indirect reference first - used only for /F, whose value the
// specification defines as a plain integer.
func intEntry(r Resolver, dict syntax.Dictionary, key syntax.Name) (int, bool) {
	obj, ok := dict[key]
	if !ok {
		return 0, false
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return 0, false
	}
	switch v := resolved.(type) {
	case syntax.Integer:
		return int(v), true
	case syntax.Real:
		return int(v), true
	default:
		return 0, false
	}
}

// floatArrayEntry resolves dict[key] (following a top-level reference,
// then each element's own reference) into a []float64, reporting
// ok=false for a missing key or any element that is not a number -
// used for /BBox, /Matrix, and /Rect, each of which the specification
// defines as a numeric array.
func floatArrayEntry(r Resolver, dict syntax.Dictionary, key syntax.Name) ([]float64, bool) {
	obj, ok := dict[key]
	if !ok {
		return nil, false
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return nil, false
	}
	arr, ok := resolved.(syntax.Array)
	if !ok {
		return nil, false
	}
	out := make([]float64, len(arr))
	for i, e := range arr {
		re, err := resolveIfRef(r, e)
		if err != nil {
			return nil, false
		}
		switch v := re.(type) {
		case syntax.Integer:
			out[i] = float64(v)
		case syntax.Real:
			out[i] = float64(v)
		default:
			return nil, false
		}
	}
	return out, true
}
