package raster

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// RenderTransparent paints every DrawOp in list onto a fresh, fully
// transparent buffer of the given pixel dimensions and returns the
// result as a graphics.Image - the tiling-pattern counterpart to
// Render, which always starts from an opaque background (see that
// function's own doc comment) because every other rendered result this
// project ever produces is a flattened, standalone raster with nothing
// behind it. A tiling pattern's one rendered cell is different: it is
// itself going to be painted, repeatedly, on top of whatever the page
// underneath already has (see internal/content's tilingpattern.go and
// graphics.DrawOp.Repeat), so the cell's own uncovered regions must
// stay genuinely transparent - not the page's background color, which
// RenderTransparent's caller may not even know at the time the tile is
// built - or the pattern would paint an opaque rectangle instead of
// only its own painted shapes.
//
// Ordinary "over" alpha compositing (11.3.6, the same formula Canvas
// itself implements against an always-opaque backdrop) is used to
// combine each DrawOp with whatever is already in the buffer, tracking
// a real per-pixel alpha channel rather than Canvas's fixed 255 - this
// is genuinely different machinery from Canvas.paint, not a thin
// wrapper around it, which is why it lives in its own file rather than
// being bolted onto Canvas as another mode.
//
// Blend modes are not honored here - every DrawOp is composited as if
// its own BlendMode were graphics.BlendNormal, regardless of what it
// actually is. Correctly blending a source color against a
// partially-transparent (rather than opaque) backdrop needs the
// specification's fuller compositing formula (11.4.5), which accounts
// for the backdrop's own alpha in the blend itself; implementing that
// solely for the rare case of a blend-mode operator appearing inside a
// tiling pattern's own content stream (patterns are, in practice,
// almost always simple flat-colored motifs) was judged not worth the
// added complexity - see the package's own doc comment for this
// project's general preference for a documented simplification over
// speculative completeness.
func RenderTransparent(list graphics.DisplayList, width, height int) *graphics.Image {
	// buf holds one (r,g,b,a) float64 quadruple per pixel, each in
	// [0,1], starting fully transparent black (the Go zero value) -
	// exactly the "nothing painted here yet" state graphics.Image.At
	// already treats a fully out-of-bounds sample as returning.
	buf := make([]float64, width*height*4)
	for _, op := range list {
		compositeOp(buf, width, height, op)
	}

	out := &graphics.Image{Width: width, Height: height, Pix: make([]byte, width*height*4)}
	for i := 0; i < width*height; i++ {
		out.Pix[i*4+0] = to8(buf[i*4+0])
		out.Pix[i*4+1] = to8(buf[i*4+1])
		out.Pix[i*4+2] = to8(buf[i*4+2])
		out.Pix[i*4+3] = to8(buf[i*4+3])
	}
	return out
}

// compositeOp rasterizes op's coverage (exactly like Canvas.paint does -
// this is a deliberate, small duplication of that logic rather than a
// shared abstraction, since the two compositing targets, opaque-canvas
// "replace" versus transparent-buffer "over with a real alpha channel",
// differ in every line past the coverage computation itself) and
// composites it into buf (width*height*4 float64s, RGBA order, [0,1]
// per channel) using the standard non-premultiplied "over" operator.
func compositeOp(buf []float64, width, height int, op graphics.DrawOp) {
	if op.Path == nil {
		// Only ever possible for a "sh"-shaped DrawOp (no specific
		// shape - see graphics.DrawOp.Shading's doc comment); a tiling
		// pattern's own content stream painting "sh" directly (as
		// opposed to a shading *pattern* selected via "scn", which does
		// carry a Path) is a legal but vanishingly rare construction
		// this function simply does not support - silently skipping it
		// is consistent with this package's general tolerance for a
		// construction it cannot handle rather than failing the whole
		// tile.
		return
	}
	minXf, minYf, maxXf, maxYf, ok := op.Path.Bounds()
	if !ok {
		return
	}
	minCol := clampInt(int(math.Floor(minXf)), 0, width)
	maxCol := clampInt(int(math.Ceil(maxXf)), 0, width)
	minRow := clampInt(int(math.Floor(minYf)), 0, height)
	maxRow := clampInt(int(math.Ceil(maxYf)), 0, height)
	if minCol >= maxCol || minRow >= maxRow {
		return
	}

	var cov []float32
	if len(op.Clips) == 0 {
		cov = rasterizeCoverage(op.Path, op.Rule, minCol, minRow, maxCol, maxRow)
	} else {
		cov = rasterizeIntersectedCoverage(op.Path, op.Rule, op.Clips, minCol, minRow, maxCol, maxRow)
	}

	w := maxCol - minCol
	for row := minRow; row < maxRow; row++ {
		rowBuf := cov[(row-minRow)*w : (row-minRow)*w+w]
		for col := minCol; col < maxCol; col++ {
			shapeCov := rowBuf[col-minCol]
			if shapeCov <= 0 {
				continue
			}
			if shapeCov > 1 {
				shapeCov = 1
			}
			r, g, b, a := sampleDrawOp(op, col, row)
			if a <= 0 {
				continue
			}
			srcAlpha := float64(shapeCov) * a * op.Alpha
			if srcAlpha > 1 {
				srcAlpha = 1
			}
			if srcAlpha <= 0 {
				continue
			}
			overComposite(buf, (row*width+col)*4, r, g, b, srcAlpha)
		}
	}
}

// overComposite applies the standard "over" operator at buf's pixel
// starting at index i: the new source color (r,g,b), covering srcAlpha
// of the pixel, is composited on top of whatever (already
// non-premultiplied) color and alpha are already stored there.
func overComposite(buf []float64, i int, r, g, b, srcAlpha float64) {
	dstA := buf[i+3]
	outA := srcAlpha + dstA*(1-srcAlpha)
	if outA <= 0 {
		return
	}
	dstFactor := dstA * (1 - srcAlpha)
	buf[i+0] = (r*srcAlpha + buf[i+0]*dstFactor) / outA
	buf[i+1] = (g*srcAlpha + buf[i+1]*dstFactor) / outA
	buf[i+2] = (b*srcAlpha + buf[i+2]*dstFactor) / outA
	buf[i+3] = outA
}

// sampleDrawOp returns op's own color/alpha at device pixel (col, row) -
// a solid Color, an Image sample (repeating, if op.Repeat - see
// graphics.DrawOp.Repeat), or a Shading evaluation - mirroring Fill/
// DrawImage/FillShading's own per-pixel sample closures, duplicated here
// for the same reason compositeOp duplicates Canvas.paint's coverage
// loop (see this file's own doc comment).
func sampleDrawOp(op graphics.DrawOp, col, row int) (r, g, b, a float64) {
	switch {
	case op.Image != nil:
		deviceToImage, ok := op.ImageToDevice.Invert()
		if !ok {
			return 0, 0, 0, 0
		}
		ux, uy := deviceToImage.Apply(float64(col)+0.5, float64(row)+0.5)
		if op.Repeat {
			ux, uy = wrap01(ux), wrap01(uy)
		} else if ux < 0 || ux >= 1 || uy < 0 || uy >= 1 {
			return 0, 0, 0, 0
		}
		ix := clampInt(int(ux*float64(op.Image.Width)), 0, op.Image.Width-1)
		iy := clampInt(int(uy*float64(op.Image.Height)), 0, op.Image.Height-1)
		return op.Image.At(ix, iy)
	case op.Shading != nil:
		color, ok := op.Shading.At(float64(col)+0.5, float64(row)+0.5)
		if !ok {
			return 0, 0, 0, 0
		}
		return color.R, color.G, color.B, 1
	default:
		return op.Color.R, op.Color.G, op.Color.B, 1
	}
}
