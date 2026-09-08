package raster

import (
	"image"

	"github.com/tucats/pdf-viewer/internal/graphics"
)

// Render paints every DrawOp in list (in order - later DrawOps paint
// over earlier ones, matching a content stream's own top-to-bottom
// painting order) onto a fresh Canvas of the given pixel dimensions,
// starting from background, and returns the finished image. This is the
// single entry point the root package's Page.Render calls once a page's
// content stream has been fully interpreted (see internal/content) into
// a DisplayList.
func Render(list graphics.DisplayList, width, height int, background graphics.Color) *image.RGBA {
	canvas := NewCanvas(width, height, background)
	for _, op := range list {
		if op.Image != nil {
			// Phase 3: an image ("Do" or an inline "BI" image) paints
			// per-pixel sampled color/alpha rather than a single solid
			// Color - see DrawOp's doc comment and Canvas.DrawImage.
			canvas.DrawImage(op.Path, op.ImageToDevice, op.Image, op.Clips)
			continue
		}
		canvas.Fill(op.Path, op.Rule, op.Color, op.Clips)
	}
	return canvas.Image()
}
