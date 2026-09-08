package model

import "github.com/tucats/pdf-viewer/internal/syntax"

// This file lets *Document itself satisfy internal/image.Resolver (see
// that package's doc comment for the interface's contract) by simply
// forwarding each method to the *parser.Document it already wraps.
//
// Why this indirection exists: internal/content's "Do"/"BI" (Phase 3
// image) handling needs a Resolver, and the root package's Page.Render
// is the caller in a position to supply one - but Page.Render only ever
// holds a *model.Document (via Document.model, an unexported field - see
// document.go), not the *parser.Document inside it, matching this
// project's general layering where the root package composes
// internal/model rather than reaching past it into internal/parser
// directly (see the README's "Proposed Internal Layout" section). Rather
// than exposing the wrapped *parser.Document itself (which would leak an
// internal package's concrete type through model's own API), Document
// gains these three small forwarding methods so it can be passed
// anywhere an image.Resolver is expected without this package importing
// internal/image at all - Go's structural interface satisfaction means
// Document simply *is* an image.Resolver the moment its method set
// matches, with no explicit declaration of intent required on this
// package's side.
func (d *Document) Resolve(num int) (syntax.Object, error) {
	return d.parser.Resolve(num)
}

func (d *Document) ResolveDictionary(dict syntax.Dictionary) (syntax.Dictionary, error) {
	return d.parser.ResolveDictionary(dict)
}

func (d *Document) DecodeStream(s syntax.Stream) ([]byte, error) {
	return d.parser.DecodeStream(s)
}
