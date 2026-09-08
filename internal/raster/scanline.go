package raster

import (
	"math"
	"sort"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// This file implements rasterizeCoverage, the one core algorithm this
// package's Canvas builds every Fill (and, since a stroke is converted
// to a fill outline before it ever reaches this package - see
// graphics.StrokeToFill - every Stroke and every clip evaluation too) on
// top of: a scanline coverage-accumulation rasterizer that anti-aliases
// both vertically (by sampling several sub-scanlines per pixel row) and
// horizontally (by computing exact fractional pixel coverage at each
// span's edges), for both of PDF's fill rules.
//
// # Why this algorithm
//
// A path is a set of Subpaths, each a closed polygon (PDF fills always
// treat every subpath as implicitly closed - see buildEdges). For one
// horizontal scan line at a fixed y, every polygon edge that crosses
// that y contributes one x-intersection; sorting those intersections and
// walking them left to right, tracking a running "inside" state
// (nonzero winding number, or even/odd parity), yields the exact set of
// x-spans that are inside the shape at that y. This is the standard
// scanline polygon fill algorithm. Anti-aliasing comes from doing this
// several times per pixel row at sub-pixel y offsets (subSamplesY) and
// accumulating fractional coverage - see addSpanCoverage - rather than
// only testing pixel centers.
//
// # Determinism
//
// subSamplesY and the fixed sub-scanline offsets below are constants,
// not derived from anything that could vary run to run (viewport size,
// timing, map iteration order - crossings are always explicitly sorted
// before being walked) - see internal/raster's package doc comment on
// why rendering the same input twice must produce the same pixels.

// subSamplesY is the number of sub-scanlines sampled per pixel row for
// vertical anti-aliasing. 4 gives a reasonable smoothness/cost tradeoff
// for this project's "minimal raster backend" scope (see the README's
// Phase 2 entry); horizontal anti-aliasing (see addSpanCoverage) is
// exact regardless of this constant, so only vertical edges benefit from
// increasing it further.
const subSamplesY = 4

// subOffsets are the fixed, evenly-spaced sub-scanline y offsets within
// one pixel row (a pixel row [row, row+1) is sampled at row+0.125,
// row+0.375, row+0.625, row+0.875).
var subOffsets = [subSamplesY]float64{0.125, 0.375, 0.625, 0.875}

// rasterizeCoverage computes path's fill coverage (0 = fully outside, 1
// = fully inside/covered, values between at anti-aliased edges) under
// rule, restricted to the pixel window [minCol,maxCol) x [minRow,maxRow)
// - the caller's responsibility to have already clamped to the canvas's
// actual bounds. The result is a flat, row-major buffer of size
// (maxRow-minRow)*(maxCol-minCol).
func rasterizeCoverage(path *graphics.Path, rule graphics.FillRule, minCol, minRow, maxCol, maxRow int) []float32 {
	w := maxCol - minCol
	h := maxRow - minRow
	if w <= 0 || h <= 0 {
		return nil
	}
	cov := make([]float32, w*h)

	edges := buildEdges(path)
	if len(edges) == 0 {
		return cov
	}

	const weight = 1.0 / subSamplesY
	var crossings []xing
	for row := minRow; row < maxRow; row++ {
		rowBuf := cov[(row-minRow)*w : (row-minRow)*w+w]
		for _, dy := range subOffsets {
			ys := float64(row) + dy
			crossings = crossings[:0]
			for _, e := range edges {
				if ys < e.loY || ys >= e.hiY {
					continue
				}
				t := (ys - e.y0) / (e.y1 - e.y0)
				x := e.x0 + t*(e.x1-e.x0)
				crossings = append(crossings, xing{x: x, dir: e.dir})
			}
			if len(crossings) == 0 {
				continue
			}
			sort.Slice(crossings, func(i, j int) bool { return crossings[i].x < crossings[j].x })
			accumulateSpans(rowBuf, crossings, rule, minCol, maxCol, weight)
		}
	}
	return cov
}

// edge is one polygon edge, pre-oriented so y0 is the endpoint with the
// smaller y and y1 the larger, with loY/hiY caching min/max for the
// scanline test and dir recording the edge's original vertical
// direction (+1 if it originally went downward in increasing y, -1 if
// upward) for the nonzero winding rule.
type edge struct {
	x0, y0, x1, y1 float64
	loY, hiY       float64
	dir            int
}

// buildEdges flattens every Subpath of path into edges, implicitly
// closing each one (connecting its last point back to its first)
// regardless of Subpath.Closed - per the PDF specification, "any subpath
// that is open shall be closed by adding a straight line segment
// connecting the subpath's endpoints" before it is filled, and this
// project treats every Subpath this uniformly at fill/clip-evaluation
// time (a Subpath's Closed flag matters for how internal/graphics
// strokes it, not for how internal/raster fills it - see
// graphics.Path's doc comment). Horizontal edges (which never
// contribute a crossing) are skipped entirely.
func buildEdges(path *graphics.Path) []edge {
	var edges []edge
	for _, sp := range path.Subpaths {
		n := len(sp.Points)
		if n < 2 {
			continue
		}
		for i := 0; i < n; i++ {
			p0 := sp.Points[i]
			p1 := sp.Points[(i+1)%n]
			if p0.Y == p1.Y {
				continue
			}
			dir := 1
			loY, hiY := p0.Y, p1.Y
			if p1.Y < p0.Y {
				dir = -1
				loY, hiY = p1.Y, p0.Y
			}
			edges = append(edges, edge{x0: p0.X, y0: p0.Y, x1: p1.X, y1: p1.Y, loY: loY, hiY: hiY, dir: dir})
		}
	}
	return edges
}

// xing is one scanline/edge x-intersection, with the edge's winding
// direction (see edge.dir).
type xing struct {
	x   float64
	dir int
}

// accumulateSpans walks crossings (already sorted by x) left to right,
// tracking the running winding state under rule, and adds weight
// coverage to rowBuf for every x-span found to be "inside".
func accumulateSpans(rowBuf []float32, crossings []xing, rule graphics.FillRule, minCol, maxCol int, weight float64) {
	winding := 0
	inside := false
	spanStart := 0.0

	for _, c := range crossings {
		wasInside := inside
		if rule == graphics.EvenOdd {
			winding++
			inside = winding%2 != 0
		} else {
			winding += c.dir
			inside = winding != 0
		}
		if !wasInside && inside {
			spanStart = c.x
		} else if wasInside && !inside {
			addSpanCoverage(rowBuf, minCol, maxCol, spanStart, c.x, weight)
		}
	}
}

// addSpanCoverage adds weight coverage to rowBuf for the horizontal span
// [x0,x1) (in absolute device-space x, not yet offset by minCol),
// clamped to [minCol,maxCol), with exact fractional coverage at the
// span's two boundary pixels and full weight for every pixel strictly
// between them - this is what gives horizontal edges anti-aliasing
// without needing horizontal supersampling.
func addSpanCoverage(rowBuf []float32, minCol, maxCol int, x0, x1, weight float64) {
	if x1 <= x0 {
		return
	}
	if x0 < float64(minCol) {
		x0 = float64(minCol)
	}
	if x1 > float64(maxCol) {
		x1 = float64(maxCol)
	}
	if x1 <= x0 {
		return
	}

	startPix := int(math.Floor(x0))
	endPix := int(math.Floor(x1))

	if startPix == endPix {
		rowBuf[startPix-minCol] += float32((x1 - x0) * weight)
		return
	}

	rowBuf[startPix-minCol] += float32((float64(startPix+1) - x0) * weight)
	for px := startPix + 1; px < endPix; px++ {
		rowBuf[px-minCol] += float32(weight)
	}
	if endPix < maxCol && x1 > float64(endPix) {
		rowBuf[endPix-minCol] += float32((x1 - float64(endPix)) * weight)
	}
}
