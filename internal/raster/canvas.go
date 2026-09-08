package raster

import (
	"image"
	"math"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// Canvas wraps an *image.RGBA and knows how to composite a
// graphics.Path, filled under a graphics.FillRule and restricted to a
// set of clip Paths, onto it - see rasterizeCoverage for the actual
// rasterization algorithm this type drives. Every pixel Canvas ever
// writes ends up fully opaque (alpha 255): this project's rendered
// output is always a flattened raster over a solid background color
// (see NewCanvas), not a document with its own transparency to
// preserve - see docs/capability-matrix.md's transparency row for where
// true alpha/transparency-group support is scheduled (Phase 5).
type Canvas struct {
	img           *image.RGBA
	width, height int
}

// NewCanvas returns a Canvas of the given pixel dimensions, filled
// entirely with background.
func NewCanvas(width, height int, background graphics.Color) *Canvas {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	r, g, b := to8(background.R), to8(background.G), to8(background.B)
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = r
		img.Pix[i+1] = g
		img.Pix[i+2] = b
		img.Pix[i+3] = 255
	}
	return &Canvas{img: img, width: width, height: height}
}

// Image returns the Canvas's underlying image. The returned *image.RGBA
// is shared, not copied; callers should not mutate it further once
// rendering is complete and it is about to be handed to the caller of
// Page.Render.
func (c *Canvas) Image() *image.RGBA {
	return c.img
}

// Fill rasterizes path under rule and composites it onto the canvas with
// color, restricted to the intersection of every clip in clips (nil or
// empty means unclipped, besides the canvas's own bounds).
//
// path's coordinates are trusted to already be finite and within
// graphics.Path's own bounded range - every method that adds a point to
// a Path (MoveTo, LineTo, AppendRect) clamps it there, which is the
// single choke point that guarantees it regardless of which content
// stream operator or how much arithmetic produced the point; see
// graphics.Path's clampPoint.
func (c *Canvas) Fill(path *graphics.Path, rule graphics.FillRule, color graphics.Color, clips []graphics.ClipPath) {
	minXf, minYf, maxXf, maxYf, ok := path.Bounds()
	if !ok {
		return
	}
	minCol := clampInt(int(math.Floor(minXf)), 0, c.width)
	maxCol := clampInt(int(math.Ceil(maxXf)), 0, c.width)
	minRow := clampInt(int(math.Floor(minYf)), 0, c.height)
	maxRow := clampInt(int(math.Ceil(maxYf)), 0, c.height)
	if minCol >= maxCol || minRow >= maxRow {
		return
	}

	cov := rasterizeCoverage(path, rule, minCol, minRow, maxCol, maxRow)
	for _, clip := range clips {
		clipCov := rasterizeCoverage(clip.Path, clip.Rule, minCol, minRow, maxCol, maxRow)
		for i := range cov {
			cov[i] *= clipCov[i]
		}
	}

	w := maxCol - minCol
	r, g, b := color.R, color.G, color.B
	for row := minRow; row < maxRow; row++ {
		rowBuf := cov[(row-minRow)*w : (row-minRow)*w+w]
		for col := minCol; col < maxCol; col++ {
			a := rowBuf[col-minCol]
			if a <= 0 {
				continue
			}
			if a > 1 {
				a = 1
			}
			c.blend(col, row, r, g, b, float64(a))
		}
	}
}

func (c *Canvas) blend(x, y int, r, g, b, alpha float64) {
	i := c.img.PixOffset(x, y)
	pix := c.img.Pix
	pix[i+0] = blend8(pix[i+0], r, alpha)
	pix[i+1] = blend8(pix[i+1], g, alpha)
	pix[i+2] = blend8(pix[i+2], b, alpha)
	pix[i+3] = 255
}

// blend8 linearly interpolates between the existing 8-bit channel value
// dst and the new color component src (in [0,1]) by alpha (in [0,1]) -
// ordinary "over" alpha compositing onto an always-opaque destination.
func blend8(dst uint8, src, alpha float64) uint8 {
	d := float64(dst) / 255
	s := clamp01(src)
	out := d*(1-alpha) + s*alpha
	return to8(out)
}

func to8(v float64) uint8 {
	v = clamp01(v)
	return uint8(v*255 + 0.5)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
