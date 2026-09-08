package pdfviewer

import (
	"context"
	"image"
	"image/color"

	"github.com/tucats/pdf-viewer/internal/content"
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/model"
	"github.com/tucats/pdf-viewer/internal/raster"
)

// Page is one page of an open Document, obtained from Document.Page.
//
// A Page must not be used after the Document it came from has been
// closed - see Document.Close's doc comment.
type Page interface {
	// Bounds returns the page's box (its /MediaBox, resolving PDF's
	// page-attribute inheritance rules if the page itself does not
	// specify one directly - see internal/model's package doc comment)
	// in PDF points (1/72 inch).
	Bounds() Rect

	// Render draws the page and returns it as an image, per opts (see
	// RenderOptions). It interprets the page's content stream
	// (internal/content, driving internal/graphics) into a display list
	// and rasterizes it (internal/raster) - a deterministic, pure-Go
	// software pipeline with no CGO or native rendering dependency, per
	// the README's "Dependency and safety policy".
	//
	// Only the vector graphics operators internal/content implements are
	// actually painted - see its package doc comment and
	// docs/capability-matrix.md for the current, phase-by-phase
	// breakdown (images are Phase 3, text is Phase 4, transparency and
	// patterns are Phase 5). A page using only unsupported features
	// still renders (as a blank page in its background color) rather
	// than failing outright; Render only returns an error for content
	// this package can positively detect as unsupported in a way that
	// would otherwise silently produce a materially wrong image (an
	// inline image, or a pattern color space - see internal/content) or
	// for content that is malformed PDF syntax.
	Render(ctx context.Context, opts RenderOptions) (image.Image, error)

	// Thumbnail is Render's bounded-size counterpart. It is not
	// implemented yet: per the README's Draft Public API, once
	// implemented (Phase 3) it is expected to reuse the same page
	// interpretation as Render rather than being a second, separate
	// rendering path. It currently always returns an error wrapping
	// ErrUnsupported.
	Thumbnail(ctx context.Context, opts ThumbnailOptions) (image.Image, error)
}

// pageImpl is the concrete implementation of Page returned by
// Document.Page. It is unexported because callers are only ever meant
// to hold it through the Page interface - see the README's Draft Public
// API note that the implementation should compose internal layers
// "without exposing their concrete types."
type pageImpl struct {
	doc  *Document
	page model.Page
}

func (p *pageImpl) Bounds() Rect {
	b := p.page.MediaBox
	return Rect{LLX: b.LLX, LLY: b.LLY, URX: b.URX, URY: b.URY}
}

// maxRenderPixels bounds the total pixel count (width * height) Render
// will attempt to allocate and rasterize, so that an absurd combination
// of a huge /MediaBox and a high RenderOptions.Scale - either a
// hostile/corrupted file or simply an oversized request - cannot exhaust
// memory; see the repository README's "Dependency and safety policy".
// 64 million pixels comfortably covers a print-resolution (300 DPI)
// rendering of the largest common paper sizes (e.g. ANSI E, 34x44
// inches, is roughly 100 megapixels at 300 DPI - larger than this limit
// deliberately, since that is an unusual request; ordinary letter/A4
// pages at 300 DPI are only a few megapixels).
const maxRenderPixels = 64_000_000

func (p *pageImpl) Render(ctx context.Context, opts RenderOptions) (image.Image, error) {
	if p.doc.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	scale := opts.Scale
	if scale <= 0 {
		scale = 1
	}
	bg := opts.Background
	if bg == nil {
		bg = color.White
	}

	width, height, ctm, err := pageDeviceGeometry(p.page, scale)
	if err != nil {
		return nil, err
	}
	if width*height > maxRenderPixels {
		return nil, UnsupportedErrorf("rendering this page at scale %v would produce a %dx%d pixel image, exceeding this package's %d-pixel limit", scale, width, height, maxRenderPixels)
	}

	data, err := p.doc.model.PageContentBytes(p.page)
	if err != nil {
		return nil, err
	}
	ops, err := content.Parse(data)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	list, err := content.Interpret(ops, ctm)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	img := raster.Render(list, width, height, colorToGraphics(bg))
	return img, nil
}

func (p *pageImpl) Thumbnail(ctx context.Context, _ ThumbnailOptions) (image.Image, error) {
	if p.doc.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, UnsupportedErrorf("Page.Thumbnail (thumbnails are planned for Phase 3 of the project's phased plan)")
}

// pageDeviceGeometry computes the pixel dimensions of page's rendered
// output at the given scale (device pixels per PDF point) and the
// initial content-transformation matrix - mapping the page's default
// user space (as defined by its MediaBox) all the way to device pixel
// space, including both the standard PDF-to-raster axis flip (PDF's
// y-axis points up; image rows count down) and the page's own /Rotate
// attribute (see model.Page.Rotate).
func pageDeviceGeometry(page model.Page, scale float64) (width, height int, ctm graphics.Matrix, err error) {
	box := page.MediaBox
	minX, maxX := box.LLX, box.URX
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	minY, maxY := box.LLY, box.URY
	if minY > maxY {
		minY, maxY = maxY, minY
	}
	pageWidth := (maxX - minX) * scale
	pageHeight := (maxY - minY) * scale
	if pageWidth <= 0 || pageHeight <= 0 {
		return 0, 0, graphics.Matrix{}, MalformedErrorf("page has a degenerate MediaBox (%v x %v points)", maxX-minX, maxY-minY)
	}

	// baseCTM maps a user-space point directly to this "unrotated"
	// device space: x shifts by the MediaBox's own origin and scales;
	// y additionally flips, since PDF user space has y increasing
	// upward but image rows increase downward.
	baseCTM := graphics.Matrix{
		A: scale, D: -scale,
		E: -minX * scale, F: maxY * scale,
	}

	unrotatedW, unrotatedH := pageWidth, pageHeight
	rotation := rotationMatrix(page.Rotate, unrotatedW, unrotatedH)
	ctm = baseCTM.Mul(rotation)

	width, height = int(ceilPositive(unrotatedW)), int(ceilPositive(unrotatedH))
	if page.Rotate == 90 || page.Rotate == 270 {
		width, height = height, width
	}
	return width, height, ctm, nil
}

// rotationMatrix returns the device-space transform that rotates an
// unrotatedW x unrotatedH image clockwise by degrees (one of 0, 90, 180,
// 270 - any other value, which model.Page.Rotate never actually
// produces, is treated as 0), mapping "unrotated" device coordinates to
// final device coordinates in the (possibly dimension-swapped) rotated
// canvas.
func rotationMatrix(degrees int, unrotatedW, unrotatedH float64) graphics.Matrix {
	switch degrees {
	case 90:
		return graphics.Matrix{A: 0, B: 1, C: -1, D: 0, E: unrotatedH, F: 0}
	case 180:
		return graphics.Matrix{A: -1, B: 0, C: 0, D: -1, E: unrotatedW, F: unrotatedH}
	case 270:
		return graphics.Matrix{A: 0, B: -1, C: 1, D: 0, E: 0, F: unrotatedW}
	default:
		return graphics.Identity()
	}
}

func ceilPositive(v float64) float64 {
	i := float64(int(v))
	if i < v {
		i++
	}
	return i
}

// colorToGraphics converts a standard library color.Color (RenderOptions
// deliberately uses the standard library's own color type rather than a
// package-specific one - see RenderOptions.Background's doc comment) to
// this project's internal graphics.Color (0..1 per component).
func colorToGraphics(c color.Color) graphics.Color {
	r, g, b, _ := c.RGBA()
	return graphics.Color{R: float64(r) / 65535, G: float64(g) / 65535, B: float64(b) / 65535}
}
