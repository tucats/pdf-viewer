package pdfviewer

import (
	"context"
	"image"
	"image/color"
	"math"

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
	// Bounds returns the page's box in PDF points (1/72 inch): its
	// /CropBox (resolving PDF's page-attribute inheritance rules if the
	// page itself does not specify one directly, and clipped to lie
	// within /MediaBox - see internal/model's Page.CropBox doc comment),
	// or /MediaBox itself if the page has no /CropBox anywhere in its
	// ancestry. This is deliberately CropBox rather than MediaBox: PDF
	// producers commonly set MediaBox to a whole physical press sheet
	// (crop marks, bleed, and other margin content included) and
	// CropBox to the smaller region actually meant to be seen, and every
	// mainstream PDF viewer displays that smaller region - Bounds (and
	// Render/Thumbnail, which use the same box) matches that.
	Bounds() Rect

	// Render draws the page and returns it as an image, per opts (see
	// RenderOptions). It interprets the page's content stream
	// (internal/content, driving internal/graphics) into a display list
	// and rasterizes it (internal/raster) - a deterministic, pure-Go
	// software pipeline with no CGO or native rendering dependency, per
	// the README's "Dependency and safety policy".
	//
	// Vector graphics, (Phase 3) images, and (Phase 4) text are painted -
	// see internal/content's package doc comment and
	// docs/capability-matrix.md for the current, phase-by-phase
	// breakdown (transparency and patterns are Phase 5).
	// A page using only unsupported features still renders (as a blank
	// page in its background color) rather than failing outright; Render
	// only returns an error for content this package can positively
	// detect as unsupported in a way that would otherwise silently
	// produce a materially wrong image (a pattern color space, or an
	// image using an unsupported color space or filter - see
	// internal/content and internal/image) or for content that is
	// malformed PDF syntax.
	Render(ctx context.Context, opts RenderOptions) (image.Image, error)

	// Thumbnail is Render's bounded-size counterpart, per opts (see
	// ThumbnailOptions): it renders the same page content Render does -
	// the exact same content-stream parse and interpret (internal/content)
	// producing the exact same graphics.DisplayList, per the README's
	// Draft Public API note that Thumbnail must not be "a second
	// interpretation of the PDF" - just rasterized (internal/raster) at a
	// smaller scale chosen so the longer of the page's two (possibly
	// /Rotate-swapped) dimensions fits within
	// ThumbnailOptions.MaxDimension, preserving aspect ratio.
	Thumbnail(ctx context.Context, opts ThumbnailOptions) (image.Image, error)

	// Text extracts this page's text content: every glyph shown by a
	// text-showing operator ("Tj"/"TJ"/"'"/"\"" - see
	// internal/content's text.go), decoded to Unicode via each glyph's
	// own font (its /ToUnicode CMap where one exists, or a simple
	// font's resolved /Encoding otherwise - see TextGlyph.Text's own doc
	// comment), together with its baseline position and advance width in
	// the same page-point coordinate system Bounds reports.
	//
	// Text is Phase 10's own capability (docs/PLAN2.md), deliberately
	// kept separate from Render/Thumbnail per the README's original
	// Phase 4 plan ("keep text extraction as a separate capability from
	// text painting"): it does not rasterize anything, interprets only
	// the operators that can affect what text exists or where it sits
	// (internal/content's ExtractText - see that file's own doc comment
	// for exactly which), and - unlike Render - never fails on account
	// of a feature Render itself does not support (an unrecognized
	// image filter, an unsupported shading type, a pattern color space,
	// and so on), since none of that affects a page's text at all. It
	// does still return an error for content that is genuinely
	// malformed among the operators it does interpret, and for the same
	// page-content-reading failures Render itself can report (a
	// corrupted content stream, for instance).
	//
	// Text does not currently recurse into a Form XObject's own content
	// stream ("Do" naming a /Form) - see ExtractText's own doc comment
	// for why this is a documented scope limitation, not an oversight.
	Text(ctx context.Context) ([]TextGlyph, error)
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
	b := p.page.CropBox
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
	return p.renderAtScale(ctx, scale, opts.Background, opts.HideAnnotations)
}

// defaultThumbnailMaxDimension is the maximum dimension (in pixels)
// Thumbnail uses when ThumbnailOptions.MaxDimension is not given (the
// zero value). 256 matches a common "thumbnail" size used by desktop
// file browsers and image galleries - large enough to recognize a page's
// layout at a glance, small enough that generating many of them (e.g.
// for a page-list sidebar) stays cheap.
const defaultThumbnailMaxDimension = 256

func (p *pageImpl) Thumbnail(ctx context.Context, opts ThumbnailOptions) (image.Image, error) {
	if p.doc.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	maxDimension := opts.MaxDimension
	if maxDimension <= 0 {
		maxDimension = defaultThumbnailMaxDimension
	}
	scale, err := p.thumbnailScale(maxDimension)
	if err != nil {
		return nil, err
	}
	return p.renderAtScale(ctx, scale, opts.Background, opts.HideAnnotations)
}

func (p *pageImpl) Text(ctx context.Context) ([]TextGlyph, error) {
	if p.doc.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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

	// Unlike renderAtScale's own initialCTM (pageDeviceGeometry's
	// device-pixel mapping, which depends on a chosen RenderOptions.Scale
	// - see this method's own doc comment on Page interface, above),
	// ExtractText is called with the identity matrix: extraction has no
	// inherent target resolution, so every TextGlyph's position comes
	// back in the page's own default user space, matching Bounds.
	glyphs, err := content.ExtractText(ops, graphics.Identity(), p.page.RawResources, p.doc.model, p.doc.fontCache)
	if err != nil {
		return nil, err
	}

	out := make([]TextGlyph, len(glyphs))
	for i, g := range glyphs {
		out[i] = TextGlyph{Text: g.Text, X: g.X, Y: g.Y, Width: g.Width, FontSize: g.FontSize}
	}
	return out, nil
}

// renderAtScale is the shared implementation behind both Render and
// Thumbnail: the only difference between "render a page" and "render a
// thumbnail of a page" is which scale (device pixels per PDF point) is
// used, so both compute their own scale (Render's straight from
// RenderOptions.Scale; Thumbnail's derived from ThumbnailOptions.
// MaxDimension - see thumbnailScale) and then share every remaining
// step: computing device geometry, checking the pixel-count bound,
// reading and parsing the page's content stream, interpreting it into a
// DisplayList, and rasterizing. This is what the README's Draft Public
// API means by Thumbnail sharing "the same page interpretation as full
// rendering" rather than being a second, separate implementation that
// could drift out of sync with Render's own behavior.
func (p *pageImpl) renderAtScale(ctx context.Context, scale float64, background color.Color, hideAnnotations bool) (image.Image, error) {
	bg := background
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
	list, err := content.InterpretCached(ops, ctm, p.page.RawResources, p.doc.model, p.doc.fontCache)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if !hideAnnotations {
		// Painted after the page's own content, matching how real-world
		// PDF viewers layer an annotation's appearance on top of
		// whatever the page itself already drew - see annotations.go.
		annotOps := annotationDrawOps(p.doc.model, p.page.Dict(), ctm, p.doc.fontCache)
		list = append(list, annotOps...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	img := raster.Render(list, width, height, colorToGraphics(bg))
	return img, nil
}

// thumbnailScale computes the RenderOptions-style scale (device pixels
// per PDF point) that makes the longer of the page's two rendered-pixel
// dimensions equal to (at most) maxDimension, preserving its aspect
// ratio - "rendered", here, already accounting for the page's own
// /Rotate the same way pageDeviceGeometry does, since a 90-or-270-degree
// rotated page's *visual* width and height are swapped from its
// MediaBox's own width and height.
//
// The obvious formula - maxDimension divided by the longer point
// dimension - is computed first as a starting estimate, then corrected
// at most once by actually calling pageDeviceGeometry and checking its
// result: floating-point rounding in that estimate could otherwise
// occasionally push pageDeviceGeometry's own ceiling-rounded pixel
// dimensions one pixel past maxDimension for a page size that lands
// exactly (or almost exactly) on an integer boundary, which would be a
// silent, easy-to-miss correctness bug for exactly the property
// (thumbnails are bounded in size) Thumbnail exists to guarantee.
func (p *pageImpl) thumbnailScale(maxDimension int) (float64, error) {
	box := p.page.CropBox
	w := math.Abs(box.URX - box.LLX)
	h := math.Abs(box.URY - box.LLY)
	if p.page.Rotate == 90 || p.page.Rotate == 270 {
		w, h = h, w
	}
	if w <= 0 || h <= 0 {
		return 0, MalformedErrorf("page has a degenerate box (%v x %v points)", w, h)
	}
	longest := w
	if h > longest {
		longest = h
	}
	scale := float64(maxDimension) / longest

	width, height, _, err := pageDeviceGeometry(p.page, scale)
	if err != nil {
		return 0, err
	}
	longestPixels := width
	if height > longestPixels {
		longestPixels = height
	}
	if longestPixels > maxDimension {
		scale *= float64(maxDimension) / float64(longestPixels)
	}
	return scale, nil
}

// pageDeviceGeometry computes the pixel dimensions of page's rendered
// output at the given scale (device pixels per PDF point) and the
// initial content-transformation matrix - mapping the page's default
// user space all the way to device pixel space, including both the
// standard PDF-to-raster axis flip (PDF's y-axis points up; image rows
// count down) and the page's own /Rotate attribute (see
// model.Page.Rotate).
//
// The output window is anchored to page's CropBox, not its MediaBox
// (see model.Page.CropBox and Page.Bounds' doc comments for why): a
// page's content stream still draws in the same MediaBox-relative user
// space it always did (nothing about the content's own coordinates
// changes), but only the CropBox-sized, CropBox-origin-anchored portion
// of that user space becomes visible in the rendered image - exactly
// like a physical printer's sheet being trimmed down to the finished
// page, everything outside the trim line simply does not appear.
func pageDeviceGeometry(page model.Page, scale float64) (width, height int, ctm graphics.Matrix, err error) {
	box := page.CropBox
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
		return 0, 0, graphics.Matrix{}, MalformedErrorf("page has a degenerate box (%v x %v points)", maxX-minX, maxY-minY)
	}

	// baseCTM maps a user-space point directly to this "unrotated"
	// device space: x shifts by the CropBox's own origin and scales; y
	// additionally flips, since PDF user space has y increasing upward
	// but image rows increase downward.
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
