package pdfviewer

import "image/color"

// This file declares the option types sketched in the README's "Draft
// Public API" section: OpenOption for Open and OpenFile, and
// RenderOptions/ThumbnailOptions for Page.Render and Page.Thumbnail.
//
// Open and OpenFile still have nothing to configure (see OpenOption
// below) - that extension point exists purely so this package's
// exported function *signatures* already match the shape described in
// the README's Draft Public API and will not need to change (only grow)
// once there is something to put in it, since adding a field to an
// existing options struct is backward compatible in Go, but adding a
// previously-absent parameter to an existing function is not.
//
// RenderOptions carries the minimal set of fields Render's implementation
// (see page.go) actually uses: Scale and Background. Per the README's
// Draft Public API notes, a fuller RenderOptions is expected to also
// cover an explicit pixel size, page-box selection beyond MediaBox
// (CropBox/BleedBox/TrimBox/ArtBox - Phase 2/3 per
// docs/capability-matrix.md), and color mode; those are not implemented
// yet and are left for a later change now that there is a real Render to
// extend. ThumbnailOptions (Phase 3) mirrors it with MaxDimension in
// place of Scale, matching the README's own description of Thumbnail as
// "a convenience for a bounded maximum dimension" sharing full
// rendering's page interpretation.

// OpenOption configures how Open or OpenFile parses a document. No
// options are defined yet - this type exists as an extension point for
// when one is needed (for example, a future limit on how large a file
// the cross-reference recovery scan documented in internal/parser will
// attempt, exposed for callers who want stricter or looser bounds than
// this module's current internal defaults).
type OpenOption func(*openConfig)

// openConfig holds the settings OpenOption values would mutate. It has
// no fields yet, matching OpenOption above.
type openConfig struct{}

// RenderOptions configures Page.Render.
type RenderOptions struct {
	// Scale is the number of device pixels per PDF point (1/72 inch).
	// The zero value means the default, 1.0 - a page's rendered pixel
	// dimensions then exactly match its MediaBox dimensions in points
	// (before accounting for Rotate; see Page.Render). A caller wanting
	// a specific DPI can compute Scale as dpi/72.
	Scale float64

	// Background is the solid color painted behind the page before any
	// content is drawn, visible wherever the page's own content does not
	// fully cover it - most pages do not paint their own full-page
	// background. A nil Background means the default, opaque white,
	// matching how most real-world PDF viewers render a page with no
	// explicit background. Only opacity implied by full coverage exists;
	// Background's own alpha channel (if any) is otherwise ignored - the
	// rendered image is always fully opaque, since this project does not
	// yet track a page's transparency beyond what is opaquely painted
	// (see docs/capability-matrix.md's transparency row).
	Background color.Color
}

// ThumbnailOptions configures Page.Thumbnail.
type ThumbnailOptions struct {
	// MaxDimension bounds the longer of the thumbnail's two pixel
	// dimensions; the other dimension is scaled to preserve the page's
	// own aspect ratio (after accounting for /Rotate - see
	// pageImpl.thumbnailScale in page.go). The zero value means the
	// default, 256 pixels (see defaultThumbnailMaxDimension in page.go).
	MaxDimension int

	// Background is exactly RenderOptions.Background: the solid color
	// painted behind the page before any content is drawn. A nil
	// Background means the default, opaque white - see RenderOptions.
	// Background's doc comment for the full rationale, which applies
	// here unchanged.
	Background color.Color
}
