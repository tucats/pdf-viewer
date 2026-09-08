package pdfviewer

import (
	"fmt"

	"github.com/tucats/pdf-viewer/internal/annotation"
	"github.com/tucats/pdf-viewer/internal/content"
	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/model"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// This file bridges internal/annotation's page-level annotation
// resolution to internal/content's page-content-stream-oriented
// interpreter, letting Page.Render (page.go) paint each visible
// annotation's existing appearance on top of the page's own content
// without internal/content needing any annotation-specific code of its
// own - see annotationDrawOps's doc comment for how.

// syntheticAnnotationOps is a fixed, one-operator content stream
// ("/A Do") reused for every annotation this package paints: internal/
// content's "Do" operator, applied to a synthetic /Resources /XObject
// entry that *is* the annotation's own appearance stream (built fresh
// per annotation in annotationDrawOps below), is exactly what already
// knows how to apply a Form XObject's /Matrix and /BBox clip correctly
// (form.go's doForm) - reusing it here means this package needs no
// separate, annotation-specific "interpret a Form XObject" code path at
// all. Parsed once, at package init, since it is a fixed literal that
// can never itself fail to parse or need to vary per call.
var syntheticAnnotationOps = mustParseSyntheticOps("/A Do")

func mustParseSyntheticOps(src string) []content.Operator {
	ops, err := content.Parse([]byte(src))
	if err != nil {
		// A hand-written, fixed literal failing to parse could only
		// indicate a bug in this package itself (or in internal/content's
		// operator grammar) - not anything a caller-supplied PDF file did
		// wrong, so this is a genuine programming-error panic rather than
		// a returned error every caller of Render would otherwise need to
		// handle for a condition that can never actually occur.
		panic(fmt.Sprintf("pdfviewer: internal: parsing synthetic content stream %q: %v", src, err))
	}
	return ops
}

// annotationDrawOps resolves every currently-visible annotation
// appearance on the page described by pageDict (see
// internal/annotation.Resolve) and interprets each one's appearance
// stream, returning the combined DrawOps in /Annots array order.
//
// pageCTM is the same CTM the page's own content stream was interpreted
// with (see pageImpl.renderAtScale) - each appearance's own
// device-mapping CTM is annotation.Appearance.Matrix.Mul(pageCTM),
// exactly as that field's doc comment describes: Matrix maps the
// appearance into the page's default user space, and pageCTM finishes
// the job of mapping the page's default user space into device pixels.
//
// fontCache is passed straight through to content.InterpretCached (see
// its doc comment) so that an annotation appearance showing text shares
// the same cross-render font cache the page's own content does - it may
// be nil, in which case no cross-call caching happens for annotation
// text either.
//
// A single annotation whose appearance stream itself turns out to be
// malformed, or to use a feature this project cannot render (an
// unsupported image filter inside it, say), is skipped rather than
// failing the whole page's render - consistent with
// internal/annotation.Resolve's own "annotations are optional,
// decorative content" tolerance for a bad annotation *dictionary*,
// extended here to a bad annotation *content stream* for the same
// reason.
func annotationDrawOps(resolver *model.Document, pageDict syntax.Dictionary, pageCTM graphics.Matrix, fontCache *content.FontCache) graphics.DisplayList {
	appearances := annotation.Resolve(resolver, pageDict)
	if len(appearances) == 0 {
		return nil
	}

	var all graphics.DisplayList
	for _, app := range appearances {
		resources := syntax.Dictionary{"XObject": syntax.Dictionary{"A": app.Stream}}
		list, err := content.InterpretCached(syntheticAnnotationOps, app.Matrix.Mul(pageCTM), resources, resolver, fontCache)
		if err != nil {
			continue
		}
		all = append(all, list...)
	}
	return all
}
