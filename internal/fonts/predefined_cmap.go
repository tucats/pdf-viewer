package fonts

// This file resolves a Type0 font's *predefined* CJK /Encoding name
// (e.g. "UniGB-UCS2-H", "90ms-RKSC-H") to a real *CMap - the one part of
// Phase 9 (docs/PLAN2.md) this package cannot do purely from the PDF
// file itself, because a predefined encoding's mapping data is not
// stored in the file at all: it is one of the several dozen CMap
// resource files Adobe publishes separately (historically bundled with
// Acrobat/Reader, and still distributed today at
// https://github.com/adobe-type-tools/cmap-resources), which every
// conforming PDF reader is expected to already have on hand.
//
// This package bundles none of that data itself, for the same reason
// internal/fonts ships no font files of its own (see docs/FONTS.md's
// "Scope decision" section): it is Adobe's own licensed resource, this
// project has no license/bundling decision to bundle it, and - unlike a
// missing embedded font program, where *some* usable substitute may
// exist on an ordinary system font directory - there is no equivalent
// "just scan a common directory" fallback for CMap resources, since they
// are not installed as part of any operating system's normal font
// stack. So, exactly like font substitution (substitute.go's FontSource/
// SubstitutionProvider), this is an opt-in mechanism: an embedding
// application that has its own copy of Adobe's CMap resources (or the
// equivalent files from a Ghostscript, poppler, or TeX installation,
// which all use the identical file format and naming convention) can
// point this package at them via pdfviewer.WithPredefinedCMaps, and
// nothing changes for every caller that doesn't.
//
// # CMapSource: the same structural-interface pattern as FontSource
//
// CMapSource, CMapSourceProvider, and predefinedCMapSourceFor below
// mirror substitute.go's FontSource/SubstitutionProvider/
// substitutionSourceFor exactly - see those types' own doc comments for
// the full rationale (internal/model.Document implements
// CMapSourceProvider structurally, without importing this package, the
// same way it already implements SubstitutionProvider). This package's
// only shipped CMapSource, DirectoryCMapSource, lives in
// directory_cmap_source.go.

// CMapSource supplies predefined CMap data by name - predefined_cmap.go
// asks it for whichever /Encoding name a Type0 font actually declares.
// DirectoryCMapSource (directory_cmap_source.go) is this package's only
// shipped implementation, but any type providing this one method can be
// wired in the same way (see CMapSourceProvider).
type CMapSource interface {
	// CMapData returns the raw, not-yet-parsed bytes of the CMap
	// resource named name (e.g. "UniGB-UCS2-H" - never including any
	// directory or file extension, matching the bare name a PDF's
	// /Encoding entry itself uses), and ok=false if this source has no
	// such resource.
	CMapData(name string) ([]byte, bool)
}

// CMapSourceProvider is implemented by a Resolver that also knows about
// a CMapSource configured for the document it belongs to - in practice,
// *internal/model.Document, once the root package's
// WithPredefinedCMaps option has attached one via that package's
// SetCMapSource method. loadType0Encoding (cid.go) reaches this through
// predefinedCMapFor/predefinedCMapResolverFor below, the same
// "type-assert the resolver, no new parameter threaded through every
// call site" pattern substitute.go's SubstitutionProvider already uses.
type CMapSourceProvider interface {
	PredefinedCMapSource() any
}

// cmapSourceFor returns the CMapSource resolver has been configured
// with, if any - ok is false in the overwhelmingly common case that
// predefined-CMap resolution was never enabled at all (mirrors
// substitute.go's substitutionSourceFor exactly).
func cmapSourceFor(resolver Resolver) (CMapSource, bool) {
	provider, ok := resolver.(CMapSourceProvider)
	if !ok {
		return nil, false
	}
	src, ok := provider.PredefinedCMapSource().(CMapSource)
	return src, ok
}

// predefinedCMapFor resolves name (a Type0 font's predefined /Encoding
// name) to a parsed *CMap via resolver's configured CMapSource, if any.
// ok=false whenever no source is configured, or the configured source
// has no data for this particular name - both are the ordinary,
// expected case for the vast majority of documents and callers, per
// this file's own doc comment.
//
// A predefined CMap resource can itself declare "usecmap" (most
// commonly, a "...-H" horizontal-vertical companion, or a UCS2 variant
// building on a simpler base encoding), so parsing here is given the
// same source to resolve any further names against - see
// predefinedCMapResolverFor, which every recursive call reaches through.
func predefinedCMapFor(resolver Resolver, name string) (*CMap, bool) {
	src, ok := cmapSourceFor(resolver)
	if !ok {
		return nil, false
	}
	data, ok := src.CMapData(name)
	if !ok {
		return nil, false
	}
	return parseCMap(data, newPredefinedCMapResolver(src, maxUseCMapDepth)), true
}

// predefinedCMapResolverFor adapts resolver's configured CMapSource (if
// any) into the plain `func(name string) (*CMap, bool)` shape parseCMap
// expects for resolving a "usecmap" operator - used both for an embedded
// CMap stream's own usecmap (cid.go's loadType0Encoding) and for a
// predefined CMap's (predefinedCMapFor, above), so a resource chain like
// "a custom encoding built on a predefined UCS2 base" resolves all the
// way through regardless of which layer introduced the chain. Returns
// nil (parseCMap treats a nil resolver as "usecmap has no effect") when
// no CMapSource is configured at all, so the overwhelmingly common
// "predefined CMaps not configured" case costs nothing beyond this one
// type assertion.
func predefinedCMapResolverFor(resolver Resolver) func(name string) (*CMap, bool) {
	src, ok := cmapSourceFor(resolver)
	if !ok {
		return nil
	}
	return newPredefinedCMapResolver(src, maxUseCMapDepth)
}

// newPredefinedCMapResolver builds a usecmap resolver function bound to
// src, that will itself only chain to further usecmap resolutions
// remaining times before giving up (returning ok=false rather than
// resolving further) - the cycle guard parseCMap's own doc comment
// defers to this file, since a cycle here spans multiple independent
// calls to parseCMap (one per link in the chain) that parseCMap itself
// has no way to see across. remaining is decremented by one at each
// link, so a resolver configuration that somehow chains predefined
// CMaps into a cycle (A usecmap B, B usecmap A) still terminates after
// maxUseCMapDepth hops instead of recursing forever - no real predefined
// CMap resource chain is anywhere near that deep.
func newPredefinedCMapResolver(src CMapSource, remaining int) func(name string) (*CMap, bool) {
	if remaining <= 0 {
		return nil
	}
	return func(name string) (*CMap, bool) {
		data, ok := src.CMapData(name)
		if !ok {
			return nil, false
		}
		return parseCMap(data, newPredefinedCMapResolver(src, remaining-1)), true
	}
}
