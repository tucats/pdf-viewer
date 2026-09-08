package content

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/graphics"
	pdfimage "github.com/tucats/pdf-viewer/internal/image"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/raster"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file implements tiling patterns (/PatternType 1), the other half
// of shading.go's resolvePatternPaint: buildTilingPattern turns a
// pattern stream into a *graphics.TilingPattern by actually rendering
// one repetition of the pattern cell's own content stream to a small,
// reusable image (internal/raster.RenderTransparent - unlike
// internal/raster.Render, this preserves real per-pixel transparency,
// which a tiling pattern's own uncovered regions need so the page
// underneath shows through wherever the pattern's own content does not
// paint) and recording the matrix that maps that one repetition back to
// however large a shape the pattern actually fills (see
// graphics.DrawOp.Repeat).
//
// Only /PaintType 1 ("colored") tiling patterns are supported:
// /PaintType 2 ("uncolored") patterns carry a cell that paints no color
// of its own at all, relying entirely on whatever color operands
// preceded the pattern name in "scn"/"SCN" - correctly supporting that
// would mean forcing every paint operation inside the pattern's own
// content to use that externally supplied color regardless of what the
// content stream itself sets, which is a distinct, additional
// interpretation mode this package does not implement. A /PaintType 2
// pattern returns an error wrapping ErrUnsupported naming it explicitly.

// maxPatternTileDimension bounds the pixel width and height of the
// offscreen tile image buildTilingPattern renders - the same "bounded
// work even under a hostile or merely oversized request" policy the
// root package's maxRenderPixels and internal/image's maxImagePixels
// already enforce (see the repository README's "Dependency and safety
// policy"). A pattern whose /XStep or /YStep is extremely small relative
// to the current device scale could otherwise demand an arbitrarily
// large tile; 1024 comfortably covers any pattern cell that is actually
// meant to be visible at normal viewing scales without being so large
// that a single degenerate pattern could exhaust memory on its own.
const maxPatternTileDimension = 1024

// buildTilingPattern reads dict/stream (the pattern's own dictionary and
// content-stream bytes - see resolvePatternPaint, this function's only
// caller, for how the two were separated from one resolved pattern
// object) and patternToDevice (the pattern's /Matrix already combined
// with the content stream's default coordinate system, per
// resolvePatternPaint's own doc comment) into a *graphics.TilingPattern.
func (in *interpreter) buildTilingPattern(dict syntax.Dictionary, stream syntax.Stream, patternToDevice graphics.Matrix) (*graphics.TilingPattern, error) {
	if in.formDepth >= maxFormDepth {
		return nil, pdferror.Malformedf("tiling pattern nesting exceeds %d levels", maxFormDepth)
	}

	paintType := 1
	if v, ok := dict["PaintType"]; ok {
		resolved, err := resolveIfRef(in.resolver, v)
		if err != nil {
			return nil, err
		}
		if n, ok := numberValue(resolved); ok {
			paintType = int(n)
		}
	}
	if paintType != 1 {
		return nil, pdferror.Unsupportedf("uncolored tiling patterns (/PaintType %d) are not supported", paintType)
	}

	bbox, found, err := floatArrayEntry(in.resolver, dict, "BBox")
	if err != nil {
		return nil, err
	}
	if !found || len(bbox) != 4 {
		return nil, pdferror.Malformedf("tiling pattern has no valid /BBox")
	}
	llx, lly := bbox[0], bbox[1]

	xStep, err := patternNumberEntry(in.resolver, dict, "XStep")
	if err != nil {
		return nil, err
	}
	yStep, err := patternNumberEntry(in.resolver, dict, "YStep")
	if err != nil {
		return nil, err
	}
	// A zero or negative step has no sensible repetition direction under
	// this function's tile-image approach (and is vanishingly rare in
	// real-world content, which always steps in the positive direction
	// of its own pattern space) - treated as malformed rather than
	// guessing a sign convention.
	if xStep <= 0 || yStep <= 0 {
		return nil, pdferror.Malformedf("tiling pattern /XStep and /YStep must be positive, got %v and %v", xStep, yStep)
	}

	// The tile image's pixel dimensions are chosen so one rendered
	// repetition looks sharp at the pattern's actual on-page scale: the
	// device-space length of the XStep/YStep vectors under
	// patternToDevice's own linear part (ApplyVector ignores
	// translation, which is irrelevant to a pure length) - matching how
	// densely the pattern will actually be sampled once painted.
	vx, vy := patternToDevice.ApplyVector(xStep, 0)
	tileW := clampTileDimension(math.Hypot(vx, vy))
	vx, vy = patternToDevice.ApplyVector(0, yStep)
	tileH := clampTileDimension(math.Hypot(vx, vy))

	// cellToPatternSpace maps the tile image's own unit square [0,1]x[0,1]
	// (top-left origin, y increasing downward - graphics.Image's own
	// convention) onto exactly one XStep x YStep repetition of pattern
	// space, anchored at the /BBox's own origin: the same "shift by the
	// box's own origin, scale, flip y" shape as the root package's
	// pageDeviceGeometry uses for a page's own MediaBox, applied here to
	// a pattern cell instead of a whole page.
	cellToPatternSpace := graphics.Matrix{A: xStep, D: -yStep, E: llx, F: lly + yStep}
	imageToDevice := cellToPatternSpace.Mul(patternToDevice)

	// tileCTM is the initial CTM the pattern's own content stream is
	// interpreted with: it maps pattern space directly into the tile
	// image's own pixel space (as opposed to imageToDevice above, which
	// maps the tile's unit square into device space) - the "render at a
	// resolution matching tileW x tileH" counterpart to
	// cellToPatternSpace, inverted in the sense that this one produces
	// pattern-space -> tile-pixel-space rather than unit-square ->
	// pattern-space.
	tileCTM := graphics.Matrix{
		A: float64(tileW) / xStep, D: -float64(tileH) / yStep,
		E: -llx * float64(tileW) / xStep, F: (lly + yStep) * float64(tileH) / yStep,
	}

	resources := syntax.Dictionary{}
	if resObj, ok := dict["Resources"]; ok {
		resolved, err := resolveIfRef(in.resolver, resObj)
		if err != nil {
			return nil, err
		}
		if resDict, ok := resolved.(syntax.Dictionary); ok {
			resources = resDict
		}
	}

	samples, err := in.resolver.DecodeStream(stream)
	if err != nil {
		return nil, err
	}
	ops, err := Parse(samples)
	if err != nil {
		return nil, err
	}
	list, err := interpretAtDepth(ops, tileCTM, resources, in.resolver, in.fontCache, in.formDepth+1)
	if err != nil {
		return nil, err
	}

	tile := raster.RenderTransparent(list, tileW, tileH)
	return &graphics.TilingPattern{Tile: tile, ImageToDevice: imageToDevice}, nil
}

// clampTileDimension converts a device-space length (in pixels) to a
// tile pixel dimension of at least 1 (a zero or negative dimension
// cannot be rasterized at all) and at most maxPatternTileDimension - see
// that constant's own doc comment. A non-finite input (NaN or Inf,
// possible from a degenerate patternToDevice) falls back to 1 rather
// than propagating into an invalid slice allocation size.
func clampTileDimension(v float64) int {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 1 {
		return 1
	}
	n := int(v + 0.5)
	if n > maxPatternTileDimension {
		return maxPatternTileDimension
	}
	return n
}

// patternNumberEntry resolves dict[key] (following an indirect
// reference) into a float64, treating a missing or non-numeric entry as
// malformed - used for /XStep and /YStep, both required by the
// specification with no documented default (unlike, say, /Matrix, which
// defaults to identity).
func patternNumberEntry(r pdfimage.Resolver, dict syntax.Dictionary, key syntax.Name) (float64, error) {
	obj, ok := dict[key]
	if !ok {
		return 0, pdferror.Malformedf("tiling pattern has no /%s", key)
	}
	resolved, err := resolveIfRef(r, obj)
	if err != nil {
		return 0, err
	}
	v, ok := numberValue(resolved)
	if !ok {
		return 0, pdferror.Malformedf("tiling pattern /%s is not a number (found %T)", key, resolved)
	}
	return v, nil
}
