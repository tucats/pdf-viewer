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
// empty means unclipped, besides the canvas's own bounds), attenuated by
// the constant alpha and combined with the existing canvas contents per
// mode (graphics.BlendNormal - ordinary "paint over" compositing, with
// no dependence on what is already there - reproduces this method's
// pre-Phase-5 behavior exactly).
//
// path's coordinates are trusted to already be finite and within
// graphics.Path's own bounded range - every method that adds a point to
// a Path (MoveTo, LineTo, AppendRect) clamps it there, which is the
// single choke point that guarantees it regardless of which content
// stream operator or how much arithmetic produced the point; see
// graphics.Path's clampPoint.
func (c *Canvas) Fill(path *graphics.Path, rule graphics.FillRule, color graphics.Color, alpha float64, mode graphics.BlendMode, clips []graphics.ClipPath) {
	r, g, b := color.R, color.G, color.B
	c.paint(path, rule, clips, alpha, mode, func(_, _ int) (float64, float64, float64, float64) {
		return r, g, b, 1
	})
}

// DrawImage rasterizes quad (an image's device-space quadrilateral -
// image space's unit square [0,1]x[0,1] mapped through imageToDevice,
// see graphics.DrawOp.ImageToDevice) exactly like Fill under the
// NonZero rule, but instead of a single solid color, samples img once
// per covered device pixel: imageToDevice is inverted (see
// graphics.Matrix.Invert) to map each covered pixel's center back into
// image space, which is then scaled by img's own dimensions to pick the
// nearest source pixel.
//
// imageToDevice is expected to already map image space with the same
// axis convention graphics.Image itself uses - (0,0) at its top-left
// sample, both axes increasing the "normal" (screen-like) direction, no
// further flip needed here - per DrawOp.ImageToDevice's own doc comment.
// PDF's content-stream CTM does not natively have that convention (its
// unit square is y-up, matching PDF user space generally); reconciling
// the two is internal/content's job when it builds ImageToDevice from a
// graphics state's CTM (see that package's imageSpaceToDevice), kept
// deliberately out of this package so that internal/raster (per its own
// package doc comment) carries no PDF-specific axis-convention knowledge
// at all - only "map this unit square to device space, in whatever
// convention the caller's matrix already establishes."
//
// Nearest-neighbor sampling (rather than bilinear or another smoother
// resampling filter) is a deliberate simplification, consistent with
// internal/image's own resampling of a mismatched-size /SMask or /Mask -
// see that package's doc comment; a scaled-up image will show visibly
// blocky pixels rather than a smooth gradient between samples. A
// degenerate (non-invertible) imageToDevice - possible from malformed
// content, e.g. "0 0 0 0 0 0 cm" active when "Do" runs - simply paints
// nothing, matching Fill's own "nothing to paint" handling for a path
// with no area.
//
// repeat, when true (only ever for a tiling pattern's DrawOp - see
// graphics.DrawOp.Repeat's doc comment), wraps an out-of-[0,1)
// image-space coordinate back into range along each axis independently,
// rather than painting nothing - the standard "tile" texture-wrapping
// mode, which is exactly what makes a single pre-rendered pattern cell
// repeat correctly across however large a shape it fills.
func (c *Canvas) DrawImage(quad *graphics.Path, imageToDevice graphics.Matrix, img *graphics.Image, repeat bool, alpha float64, mode graphics.BlendMode, clips []graphics.ClipPath) {
	if img == nil || img.Width <= 0 || img.Height <= 0 {
		return
	}
	deviceToImage, ok := imageToDevice.Invert()
	if !ok {
		return
	}

	c.paint(quad, graphics.NonZero, clips, alpha, mode, func(col, row int) (float64, float64, float64, float64) {
		// Sample at the pixel's center (col+0.5, row+0.5), not its
		// integer corner, so a pixel is colored by whatever image sample
		// its middle actually falls under.
		ux, uy := deviceToImage.Apply(float64(col)+0.5, float64(row)+0.5)
		if repeat {
			ux = wrap01(ux)
			uy = wrap01(uy)
		} else if ux < 0 || ux >= 1 || uy < 0 || uy >= 1 {
			// Outside the image's own unit square: this can happen for a
			// pixel the shape coverage above still counted as partially
			// covered, right at quad's anti-aliased edge. Painting
			// nothing here (rather than clamping to the nearest edge
			// pixel) avoids smearing the image's edge pixels outward.
			return 0, 0, 0, 0
		}
		ix := clampInt(int(ux*float64(img.Width)), 0, img.Width-1)
		iy := clampInt(int(uy*float64(img.Height)), 0, img.Height-1)
		return img.At(ix, iy)
	})
}

// wrap01 reduces v to [0,1) as if the unit interval repeated
// infinitely in both directions - math.Mod alone leaves a negative v
// negative (Go's % and math.Mod both take the sign of the dividend), so
// a second adjustment is needed to fold that case back into [0,1).
func wrap01(v float64) float64 {
	v = math.Mod(v, 1)
	if v < 0 {
		v += 1
	}
	return v
}

// FillShading rasterizes path under rule exactly like Fill, but instead
// of a single solid color, samples sh (a resolved gradient - see
// graphics.Shading) once per covered device pixel, at that pixel's own
// center - used for a fill or stroke painted with a shading pattern (see
// graphics.DrawOp.Shading's doc comment). A pixel sh.At reports as
// uncovered (outside the gradient's own geometry, with no applicable
// /Extend) paints nothing there, leaving whatever was already
// underneath - the same "partial coverage" tolerance DrawImage already
// has for a device pixel outside an image's own unit square.
func (c *Canvas) FillShading(path *graphics.Path, rule graphics.FillRule, sh *graphics.Shading, alpha float64, mode graphics.BlendMode, clips []graphics.ClipPath) {
	c.paint(path, rule, clips, alpha, mode, func(col, row int) (r, g, b, a float64) {
		color, ok := sh.At(float64(col)+0.5, float64(row)+0.5)
		if !ok {
			return 0, 0, 0, 0
		}
		return color.R, color.G, color.B, 1
	})
}

// PaintShading implements the "sh" operator's whole-region painting: sh
// is composited across every pixel of the entire canvas, restricted only
// by clips (empty/nil clips means the entire page, matching the
// specification's "sh paints the whole current clipping region, which is
// the whole page when unclipped") - see graphics.DrawOp.Shading's doc
// comment for why internal/content hands this a nil Path rather than
// building a covering rectangle itself.
func (c *Canvas) PaintShading(sh *graphics.Shading, alpha float64, mode graphics.BlendMode, clips []graphics.ClipPath) {
	var full graphics.Path
	full.AppendRect([4]graphics.Point{
		{X: 0, Y: 0}, {X: float64(c.width), Y: 0},
		{X: float64(c.width), Y: float64(c.height)}, {X: 0, Y: float64(c.height)},
	})
	c.FillShading(&full, graphics.NonZero, sh, alpha, mode, clips)
}

// paint is the shared core of Fill and DrawImage: it rasterizes path's
// coverage under rule (intersected with every clip in clips, exactly as
// Fill's own doc comment describes), then for every pixel with nonzero
// combined coverage calls sample to ask what color and alpha to
// composite there - sample receives the pixel's own (col, row) device
// coordinates so DrawImage's image-space mapping (or, for Fill, nothing
// at all - a constant color) can be computed per pixel without paint
// itself needing to know which case it is serving. constantAlpha (PDF's
// "ca"/"CA") further attenuates sample's own per-pixel alpha, and mode
// selects how the result combines with whatever is already on the
// canvas (see blendChannel).
func (c *Canvas) paint(path *graphics.Path, rule graphics.FillRule, clips []graphics.ClipPath, constantAlpha float64, mode graphics.BlendMode, sample func(col, row int) (r, g, b, a float64)) {
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
			r, g, b, a := sample(col, row)
			if a <= 0 {
				continue
			}
			alpha := float64(shapeCov) * a * constantAlpha
			if alpha > 1 {
				alpha = 1
			}
			if alpha <= 0 {
				continue
			}
			c.blend(col, row, r, g, b, alpha, mode)
		}
	}
}

func (c *Canvas) blend(x, y int, r, g, b, alpha float64, mode graphics.BlendMode) {
	i := c.img.PixOffset(x, y)
	pix := c.img.Pix
	pix[i+0] = blendChannel(pix[i+0], r, alpha, mode)
	pix[i+1] = blendChannel(pix[i+1], g, alpha, mode)
	pix[i+2] = blendChannel(pix[i+2], b, alpha, mode)
	pix[i+3] = 255
}

// blendChannel implements one channel of PDF's compositing formula
// (11.3.6): Cr = (1-alpha)*Cb + alpha*B(Cb,Cs), where Cb is the existing
// backdrop channel (dst, normalized to [0,1]), Cs is the newly painted
// source channel (src), B is mode's blend function (graphics.Blend -
// BlendNormal's B(Cb,Cs) is simply Cs, reducing this to ordinary linear
// "over" alpha compositing, exactly this method's pre-Phase-5 behavior
// under its old name, blend8), and alpha is the combined shape-coverage/
// image-alpha/constant-alpha value paint has already computed.
func blendChannel(dst uint8, src, alpha float64, mode graphics.BlendMode) uint8 {
	cb := float64(dst) / 255
	cs := clamp01(src)
	blended := graphics.Blend(mode, cb, cs)
	out := cb*(1-alpha) + blended*alpha
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
