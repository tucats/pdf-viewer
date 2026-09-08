package pdfviewer

import (
	"context"
	"image"

	"github.com/tucats/pdf-viewer/internal/model"
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

	// Render draws the page and returns it as an image. It is not
	// implemented yet: rendering is Phase 2 and Phase 3 work per the
	// repository README's phased plan (a minimal raster backend for
	// vector content in Phase 2; images and color in Phase 3), so this
	// method currently always returns an error wrapping ErrUnsupported.
	// The method exists now, ahead of an implementation, so that the
	// Page interface's shape already matches the README's Draft Public
	// API and callers can be written against the final interface today.
	Render(ctx context.Context, opts RenderOptions) (image.Image, error)

	// Thumbnail is Render's bounded-size counterpart, sharing the same
	// "not implemented yet" status - see Render's doc comment. Per the
	// README's Draft Public API, once implemented (Phase 3) it is
	// expected to reuse the same page interpretation as Render rather
	// than being a second, separate rendering path.
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

func (p *pageImpl) Render(ctx context.Context, _ RenderOptions) (image.Image, error) {
	if p.doc.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, UnsupportedErrorf("Page.Render (rendering is planned for Phase 2 of the project's phased plan)")
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
