package pdfviewer

// This file declares the option types sketched in the README's "Draft
// Public API" section: OpenOption for Open and OpenFile, and
// RenderOptions/ThumbnailOptions for Page.Render and Page.Thumbnail.
//
// None of these carry any fields yet. That is deliberate, not an
// oversight: Open and OpenFile currently have nothing to configure (see
// OpenOption below), and Render/Thumbnail are not implemented yet at all
// (they return an error wrapping ErrUnsupported - see page.go) since
// actual rendering is Phase 2 and Phase 3 work per the repository
// README's phased plan. The three types exist now, ahead of having
// anything to put in them, purely so that this package's exported
// function and method *signatures* already match the shape described in
// the README's Draft Public API and will not need to change (only grow)
// once rendering lands - adding a field to an existing options struct is
// backward compatible in Go; adding a previously-absent parameter to an
// existing function is not.

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

// RenderOptions configures Page.Render. Per the README's Draft Public
// API, once implemented (Phase 2) this is expected to cover DPI or an
// explicit pixel size, page-box selection, rotation, background color,
// and color mode - none of which exist yet, since Render itself is not
// implemented yet; see page.go.
type RenderOptions struct{}

// ThumbnailOptions configures Page.Thumbnail. Per the README's Draft
// Public API, once implemented (Phase 3) this is expected to be a
// convenience for a bounded maximum dimension, sharing the same page
// interpretation as full rendering - none of which exists yet, since
// Thumbnail itself is not implemented yet; see page.go.
type ThumbnailOptions struct{}
