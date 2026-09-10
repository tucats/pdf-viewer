// Package model builds the document-level view of a PDF - primarily its
// page tree - on top of the object-level values internal/parser
// resolves. This is where PDF's tree-shaped, reference-heavy dictionary
// structure gets turned into a flat, ordinary Go slice of pages that the
// rest of this module (and eventually the public pdfviewer API) can use
// without understanding PDF's object model directly.
//
// # The page tree, briefly
//
// A PDF document's pages are not stored as a flat list. The trailer's
// /Root entry points at a "document catalog" dictionary, whose /Pages
// entry points at the root of a tree of "page tree nodes": each
// intermediate node has a /Kids array of further nodes (page tree nodes
// or actual pages) and a /Count giving the total number of pages
// beneath it; a leaf node is a page dictionary itself. Several page
// attributes - notably /Resources, /MediaBox, /CropBox, and /Rotate -
// are "inheritable": if a page dictionary does not have one of these
// entries directly, the value is taken from the nearest ancestor node
// in the tree that does have it. This package resolves that inheritance
// once, while flattening the tree, so nothing above this package needs
// to walk the tree again to answer "what is this page's MediaBox".
//
// This is Phase 1 work per the repository README's phased plan: only
// page-tree traversal, page count, and page boxes are implemented here
// so far (see the Page type below); Resources is threaded through the
// data model already (RawResources) because later phases (content
// streams, fonts, images) need it, but nothing in this package
// interprets it yet.
package model

import (
	"math"

	"github.com/tucats/pdf-viewer/internal/diag"
	"github.com/tucats/pdf-viewer/internal/parser"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// Page is one flattened, inheritance-resolved page from a document's
// page tree.
type Page struct {
	// MediaBox is the page's media box in PDF points (1/72 inch),
	// resolved from the page's own /MediaBox entry or, if absent,
	// inherited from the nearest ancestor page tree node that has one.
	// Per the PDF specification every page must have a MediaBox
	// available somewhere in its ancestry; a page tree with no MediaBox
	// anywhere along a page's ancestor chain is treated as malformed
	// (see Document.pages below).
	MediaBox Rect

	// CropBox is the page's box in PDF points, exactly as a real PDF
	// viewer (Preview, Acrobat, ...) would determine "the visible page":
	// the page's own (possibly inherited) /CropBox entry if it has one,
	// clipped to lie within MediaBox (a /CropBox is only ever meant to
	// select a region *within* the media, never extend past it - see
	// buildPage), or MediaBox itself when there is no /CropBox anywhere
	// in the page's ancestry. Printers commonly produce a MediaBox
	// spanning the whole physical press sheet - including bleed and
	// crop-mark margins outside the final trimmed page - with /CropBox
	// naming the smaller, actually-intended-to-be-seen region within it;
	// this field, not MediaBox, is what Bounds and Render use, so that
	// this package's output matches what other viewers show rather than
	// including that outer margin.
	CropBox Rect

	// BleedBox is the page's box in PDF points: the region to which page
	// content shall be clipped when output in a production (print)
	// environment, intended to accommodate the physical limitations of
	// cutting and folding equipment - a bit larger than TrimBox so that
	// color intended to run all the way to the trimmed edge ("full
	// bleed") still has margin for a slightly imprecise cut, but usually
	// smaller than MediaBox's full press sheet. Unlike MediaBox/CropBox/
	// Resources/Rotate, /BleedBox is NOT an inheritable page attribute
	// per the specification (ISO 32000-1 Table 30) - only the page's own
	// dictionary is consulted, never an ancestor Pages node - so a
	// document that sets /BleedBox on an intermediate node rather than
	// each leaf page (unusual, but not forbidden by the file format) has
	// that entry silently ignored, exactly as a real PDF-consuming
	// application would. When the page's own dictionary has no /BleedBox
	// entry at all, this defaults to CropBox (see buildPage), then - like
	// every page box - is clipped to lie within MediaBox if it does not
	// already.
	BleedBox Rect

	// TrimBox is the page's box in PDF points: the intended finished
	// dimensions of the page after trimming, i.e. what the reader
	// actually sees once a printed, bled sheet has been cut down - the
	// PDF/X print-production standards use this as "the" page size for
	// exactly that reason. Same inheritance (none - leaf page dictionary
	// only), defaulting (to CropBox), and clipping (to MediaBox) rules as
	// BleedBox above.
	TrimBox Rect

	// ArtBox is the page's box in PDF points: the extent of the page's
	// meaningful content as intended by the page's creator, excluding
	// any surrounding white space - used, for example, when placing a
	// whole page as a Form XObject into another document, so only its
	// actual artwork (not incidental margin) is positioned. Same
	// inheritance (none), defaulting (to CropBox), and clipping (to
	// MediaBox) rules as BleedBox above.
	ArtBox Rect

	// RawResources is the page's (possibly inherited) /Resources
	// dictionary, not yet interpreted. It is exposed now, ahead of the
	// fonts/images/content-stream work that will actually use it
	// (Phases 3-4 of the phased plan), so that model.Document's shape
	// does not need to change again once those phases add code that
	// consumes it.
	RawResources syntax.Dictionary

	// Rotate is the page's (possibly inherited) /Rotate entry, normalized
	// to one of 0, 90, 180, or 270: the number of degrees clockwise the
	// page shall be rotated when displayed or printed, per the PDF
	// specification. It defaults to 0 when /Rotate is absent anywhere in
	// the page's ancestry, or when a present value is not a multiple of
	// 90 (itself malformed, but not worth rejecting the whole page over -
	// see mergeInherited).
	Rotate int

	// dict is the page's own (unmerged) dictionary, kept for future use
	// by higher layers (for example, internal/content will need the
	// page's /Contents entry) without this package having to grow an
	// accessor for every field a later phase turns out to need.
	dict syntax.Dictionary
}

// Dict returns the page's own PDF dictionary, exactly as read from the
// file (not merged with any inherited attributes - use MediaBox and
// RawResources for those). This is an escape hatch for later packages
// (internal/content, in particular, which needs /Contents) rather than
// something Phase 1 code itself uses.
func (p Page) Dict() syntax.Dictionary {
	return p.dict
}

// Rect is an axis-aligned rectangle in PDF points (1/72 inch), matching
// the four-number arrays PDF uses for page boxes: [llx lly urx ury] -
// the lower-left and upper-right corners. PDF does not require llx<urx
// or lly<ury (a producer may list corners in either order), so this
// type's fields are named by position in that array, not by geometric
// meaning; a caller that needs a normalized (min, min)-(max, max) form
// should compute it from these.
type Rect struct {
	LLX, LLY, URX, URY float64
}

// Document is the page-tree-aware view of an open PDF, built by Open
// from an already-parsed *parser.Document.
type Document struct {
	parser *parser.Document
	pages  []Page

	// catalog is the document catalog dictionary (the trailer's /Root
	// entry, already resolved) - kept around, beyond what Open originally
	// needed it for (finding /Pages), so that AcroForm below can read its
	// /AcroForm entry without re-resolving the trailer's /Root itself.
	catalog syntax.Dictionary

	// diagnostics is nil unless the root package's WithDiagnostics option
	// attached one via SetDiagnostics - see that method's doc comment.
	diagnostics *diag.Recorder

	// fontSource is nil unless the root package's WithFontSubstitution
	// option attached one via SetFontSource - see that method's doc
	// comment for why this is typed as `any` rather than a concrete
	// internal/fonts type.
	fontSource any

	// cmapSource is nil unless the root package's WithPredefinedCMaps
	// option attached one via SetCMapSource - see that method's doc
	// comment, which mirrors SetFontSource's above exactly.
	cmapSource any
}

// SetDiagnostics attaches r to d, so that every subsequent call the rest
// of this module makes through d (as a fonts.Resolver, an
// internal/image.Resolver, or a content-package resolver - d implements
// all three, structurally) that calls diag.Note on it records into r.
// Passing nil detaches whatever Recorder was previously attached,
// restoring the package-wide default of recording nothing.
func (d *Document) SetDiagnostics(r *diag.Recorder) {
	d.diagnostics = r
}

// SetFontSource attaches src - expected to be a *internal/fonts.
// DirectorySource, or any other value satisfying that package's
// FontSource interface - to d, so that internal/fonts.Load (via
// FontSubstitutionSource below and that package's own
// substitutionSourceFor helper) can find it once d is used as a
// fonts.Resolver.
//
// src is typed as `any` here rather than internal/fonts.FontSource
// specifically so that this package does not need to import
// internal/fonts at all just to declare this method's parameter type -
// internal/model, internal/fonts, and internal/content are kept as
// independent peer packages within this module, none importing either
// of the others directly (see, for example, internal/fonts/resolver.go's
// doc comment on why internal/fonts declares its own Resolver interface
// rather than importing one), with the root package the only place that
// imports all of them and wires concrete values from one into another.
// Passing nil (the default for every Document that never has this
// method called) restores "no font substitution", exactly like
// SetDiagnostics(nil) restores "no diagnostics".
func (d *Document) SetFontSource(src any) {
	d.fontSource = src
}

// SetCMapSource attaches src - expected to be a *internal/fonts.
// DirectoryCMapSource, or any other value satisfying that package's
// CMapSource interface - to d, so that internal/fonts.Load (via
// PredefinedCMapSource below and that package's own predefined_cmap.go
// helpers) can find it once d is used as a fonts.Resolver. This is
// SetFontSource's exact counterpart for Phase 9's predefined-CMap
// resolution - see that method's doc comment for the full rationale
// behind the `any` parameter type, which applies here unchanged.
// Passing nil (the default for every Document that never has this
// method called) restores "no predefined-CMap resolution", exactly like
// SetFontSource(nil) restores "no font substitution".
func (d *Document) SetCMapSource(src any) {
	d.cmapSource = src
}

// FontSubstitutionSource implements internal/fonts.SubstitutionProvider
// structurally (see that interface's own doc comment for the full
// rationale), returning whatever SetFontSource last attached - nil if it
// was never called. internal/fonts.Load's callers type-assert this
// return value back to internal/fonts.FontSource on their own side (via
// that package's substitutionSourceFor), so this method itself needs no
// knowledge of that type.
func (d *Document) FontSubstitutionSource() any {
	return d.fontSource
}

// PredefinedCMapSource implements internal/fonts.CMapSourceProvider
// structurally, returning whatever SetCMapSource last attached - nil if
// it was never called. This is FontSubstitutionSource's exact
// counterpart for predefined-CMap resolution - see that method's doc
// comment for the full rationale, which applies here unchanged.
func (d *Document) PredefinedCMapSource() any {
	return d.cmapSource
}

// RecordDiagnostic implements diag.Recordable, so any package holding d
// as a resolver-shaped interface value can call diag.Note(resolverValue,
// ...) to record into whatever Recorder SetDiagnostics last attached -
// or, if none has been, do nothing at the cost of one nil check (see
// diag.Recorder.Record).
func (d *Document) RecordDiagnostic(format string, args ...any) {
	d.diagnostics.Record(format, args...)
}

// maxPageTreeDepth bounds how deep Open will recurse while walking a
// document's page tree. Combined with the cycle detection in
// collectPages (a page tree node whose /Kids somehow points back at one
// of its own ancestors), this keeps a hostile or corrupted page tree
// from causing unbounded recursion - see the repository README's
// "Dependency and safety policy". PDF page trees encountered in
// practice are rarely more than a few levels deep even for documents
// with tens of thousands of pages, since implementations balance the
// tree specifically to keep lookups fast.
const maxPageTreeDepth = 256

// Open builds a Document from an already-opened parser.Document: it
// resolves the trailer's /Root entry to find the document catalog,
// follows its /Pages entry to the root of the page tree, and flattens
// that tree into an ordered slice of pages, resolving inheritable
// attributes along the way.
//
// Open returns an error wrapping pdfviewer.ErrMalformed if the document
// catalog, page tree root, or any page is missing, is not the kind of
// object it is supposed to be, or if the page tree contains a cycle or
// exceeds maxPageTreeDepth.
func Open(p *parser.Document) (*Document, error) {
	catalog, err := resolveDictionary(p, p.Trailer["Root"], "document catalog (trailer /Root)")
	if err != nil {
		return nil, err
	}

	d := &Document{parser: p, catalog: catalog}
	visited := make(map[int]bool)
	if err := d.collectPages(catalog["Pages"], inheritable{}, 0, visited); err != nil {
		return nil, err
	}
	return d, nil
}

// AcroForm returns the document catalog's /AcroForm entry (the
// interactive form dictionary, ISO 32000-1 12.7.2), resolving it through
// an indirect reference if needed, and ok=false if the catalog has no
// /AcroForm entry at all or it does not resolve to a dictionary - both
// cases simply meaning "this document has no interactive form", not an
// error, matching how model.Document already treats "no /CropBox" or
// "no /Rotate" as ordinary, expected absences rather than malformed
// input.
//
// Phase 12 (AcroForm field appearance regeneration - see
// internal/acroform) is the first thing in this module that needs the
// catalog for anything beyond finding /Pages, which is why this method
// (and the catalog field it reads) was added alongside it rather than
// back when Open first resolved the catalog for Phase 1.
func (d *Document) AcroForm() (syntax.Dictionary, bool) {
	obj, ok := d.catalog["AcroForm"]
	if !ok {
		return nil, false
	}
	resolved, err := resolveObject(d.parser, obj)
	if err != nil {
		return nil, false
	}
	dict, ok := resolved.(syntax.Dictionary)
	return dict, ok
}

// PageCount returns the number of pages in the document.
func (d *Document) PageCount() int {
	return len(d.pages)
}

// Page returns the page at the given zero-based index. The caller is
// responsible for checking 0 <= index < PageCount(); Page does not
// itself return an error, matching how internal/model is used purely
// internally, by the root pdfviewer package, which is where the public,
// user-facing bounds-checked API (and its ErrPageIndex) lives.
func (d *Document) Page(index int) Page {
	return d.pages[index]
}

// inheritable carries the page attributes that PDF allows a page tree
// node to inherit from its ancestors, accumulated while walking down the
// tree from the root: each level's own values (if present) override
// what was inherited from further up.
type inheritable struct {
	mediaBox     *Rect
	cropBox      *Rect
	resources    syntax.Dictionary
	hasResources bool
	rotate       int
}

// collectPages recursively walks the page tree rooted at node
// (identified by its still-unresolved syntax.Object, typically a
// syntax.Reference), appending each leaf page it finds to d.pages in
// document order (the order /Kids arrays list them in, which is the
// order the specification defines for a document's pages).
//
// inherited carries whatever inheritable attributes have already been
// resolved from ancestors closer to the root; depth and visited
// implement the recursion-depth and cycle guards described on
// maxPageTreeDepth.
func (d *Document) collectPages(node syntax.Object, inherited inheritable, depth int, visited map[int]bool) error {
	if depth > maxPageTreeDepth {
		return pdferror.Malformedf("page tree exceeds %d levels", maxPageTreeDepth)
	}

	ref, isRef := node.(syntax.Reference)
	if isRef {
		if visited[ref.Number] {
			return pdferror.Malformedf("page tree contains a cycle at object %d", ref.Number)
		}
		visited[ref.Number] = true
	}

	dict, err := resolveDictionary(d.parser, node, "page tree node")
	if err != nil {
		return err
	}

	inherited = mergeInherited(d.parser, inherited, dict)

	nodeType, _ := dict["Type"].(syntax.Name)
	if kidsObj, hasKids := dict["Kids"]; hasKids && nodeType != "Page" {
		kids, ok := kidsObj.(syntax.Array)
		if !ok {
			return pdferror.Malformedf("page tree node /Kids is not an array")
		}
		for _, kid := range kids {
			if err := d.collectPages(kid, inherited, depth+1, visited); err != nil {
				return err
			}
		}
		return nil
	}

	// A leaf: either explicitly /Type /Page, or a node with no /Kids at
	// all (some real-world files omit /Type; a Kids-less node can only
	// sensibly be a page).
	page, err := buildPage(d.parser, dict, inherited)
	if err != nil {
		return err
	}
	d.pages = append(d.pages, page)
	return nil
}

// mergeInherited returns the inheritable attributes a child of dict
// should see: dict's own values where present, falling back to
// inherited's (i.e. an ancestor's) values otherwise.
//
// Each of /MediaBox, /Resources, and /Rotate is legal for a producer to
// write as an indirect reference rather than a direct value (nothing in
// the specification requires otherwise, and real-world producers -
// including whatever produced this package's own motivating real-world
// test file - routinely share one /Resources dictionary across many
// pages via exactly such a reference), so each is resolved through p
// first via resolveObject. An unresolvable reference, or a resolved
// value of the wrong type, is tolerated exactly like a malformed direct
// value always was: the field is simply left as whatever was already
// inherited from further up the tree, consistent with this package's
// general tolerance for a single bad field (see collectPages's doc
// comment on /Rotate below).
func mergeInherited(p *parser.Document, inherited inheritable, dict syntax.Dictionary) inheritable {
	if box, ok := dict["MediaBox"]; ok {
		if resolved, err := resolveObject(p, box); err == nil {
			if r, ok := parseRect(resolved); ok {
				inherited.mediaBox = &r
			}
		}
	}
	if box, ok := dict["CropBox"]; ok {
		if resolved, err := resolveObject(p, box); err == nil {
			if r, ok := parseRect(resolved); ok {
				inherited.cropBox = &r
			}
		}
	}
	if res, ok := dict["Resources"]; ok {
		if resolved, err := resolveObject(p, res); err == nil {
			if resDict, ok := resolved.(syntax.Dictionary); ok {
				inherited.resources = resDict
				inherited.hasResources = true
			}
		}
	}
	if rot, ok := dict["Rotate"]; ok {
		if resolved, err := resolveObject(p, rot); err == nil {
			if i, ok := resolved.(syntax.Integer); ok {
				if normalized, ok := normalizeRotate(int(i)); ok {
					inherited.rotate = normalized
				}
				// A /Rotate present but not a multiple of 90 is malformed
				// per the specification; rather than rejecting the whole
				// page over one bad inheritable attribute, this simply
				// keeps whatever rotation was inherited from further up
				// the tree (or the default 0).
			}
		}
	}
	return inherited
}

// normalizeRotate reduces deg to the equivalent value in [0,360) and
// reports ok=false if it is not a multiple of 90 - the only values the
// PDF specification permits for /Rotate.
func normalizeRotate(deg int) (int, bool) {
	if deg%90 != 0 {
		return 0, false
	}
	deg %= 360
	if deg < 0 {
		deg += 360
	}
	return deg, true
}

// buildPage constructs a Page from a leaf page dictionary and whatever
// inheritable attributes it inherited from its ancestors, requiring that
// a MediaBox was found somewhere along the way (directly or inherited) -
// per the PDF specification, every page must have one available. p is
// needed (only) to resolve BleedBox/TrimBox/ArtBox, which - unlike
// MediaBox/CropBox/Resources/Rotate above - are not inheritable, so they
// are read directly from dict (the leaf page's own dictionary) rather
// than from the inheritable accumulator collectPages already built while
// walking down the tree; an indirect reference in dict still needs p to
// follow, exactly as mergeInherited needs it for the inheritable
// attributes.
func buildPage(p *parser.Document, dict syntax.Dictionary, inherited inheritable) (Page, error) {
	if inherited.mediaBox == nil {
		return Page{}, pdferror.Malformedf("page has no /MediaBox, direct or inherited")
	}
	mediaBox := *inherited.mediaBox
	cropBox := resolveCropBox(mediaBox, inherited.cropBox)
	page := Page{
		MediaBox: mediaBox,
		CropBox:  cropBox,
		BleedBox: resolveNonInheritedBox(mediaBox, cropBox, resolveOwnBox(p, dict, "BleedBox")),
		TrimBox:  resolveNonInheritedBox(mediaBox, cropBox, resolveOwnBox(p, dict, "TrimBox")),
		ArtBox:   resolveNonInheritedBox(mediaBox, cropBox, resolveOwnBox(p, dict, "ArtBox")),
		Rotate:   inherited.rotate,
		dict:     dict,
	}
	if inherited.hasResources {
		page.RawResources = inherited.resources
	}
	return page, nil
}

// resolveOwnBox resolves dict's own key entry (following an indirect
// reference through p if needed) as a page-box rectangle, reporting nil
// if the entry is absent, unresolvable, or not shaped like a rectangle -
// BleedBox/TrimBox/ArtBox's own doc comments explain why only dict
// itself (never an ancestor) is consulted here.
func resolveOwnBox(p *parser.Document, dict syntax.Dictionary, key syntax.Name) *Rect {
	obj, ok := dict[key]
	if !ok {
		return nil
	}
	resolved, err := resolveObject(p, obj)
	if err != nil {
		return nil
	}
	r, ok := parseRect(resolved)
	if !ok {
		return nil
	}
	return &r
}

// resolveNonInheritedBox implements BleedBox/TrimBox/ArtBox's shared
// defaulting and clipping rule: own is nil when the page's own
// dictionary has no entry for that box at all, in which case the result
// is cropBox (itself already resolved and clipped) unchanged, per the
// specification's stated default; otherwise the result is *own clipped
// to lie within mediaBox, via the same clipToMediaBox rule /CropBox
// itself uses (see resolveCropBox).
func resolveNonInheritedBox(mediaBox, cropBox Rect, own *Rect) Rect {
	if own == nil {
		return cropBox
	}
	return clipToMediaBox(mediaBox, *own)
}

// resolveCropBox implements the /CropBox field's own doc comment: cropBox
// is nil when the page's ancestry had no /CropBox entry at all, in which
// case the result is simply mediaBox unchanged; otherwise the result is
// *cropBox clipped to lie within mediaBox, via clipToMediaBox.
func resolveCropBox(mediaBox Rect, cropBox *Rect) Rect {
	if cropBox == nil {
		return mediaBox
	}
	return clipToMediaBox(mediaBox, *cropBox)
}

// clipToMediaBox implements the specification's shared clipping rule for
// every page box beyond MediaBox itself ("the crop, bleed, trim, and art
// boxes shall not ordinarily extend beyond the boundaries of the media
// box... if they do, they shall be clipped to the media box"). Both
// rectangles are normalized (min, min)-(max, max) first, since PDF does
// not require a box's corners to be listed in any particular order (see
// Rect's own doc comment) and an intersection computed from
// un-normalized corners would be meaningless.
//
// If clipping produces a degenerate (zero or negative area) rectangle -
// a malformed box that does not actually overlap the media box at all -
// mediaBox is returned instead, on the theory that showing the whole
// page is a far more useful fallback than a blank or rejected one for
// what is, after all, only a viewing hint.
func clipToMediaBox(mediaBox, box Rect) Rect {
	media := normalizeRect(mediaBox)
	b := normalizeRect(box)

	clipped := Rect{
		LLX: math.Max(media.LLX, b.LLX),
		LLY: math.Max(media.LLY, b.LLY),
		URX: math.Min(media.URX, b.URX),
		URY: math.Min(media.URY, b.URY),
	}
	if clipped.URX <= clipped.LLX || clipped.URY <= clipped.LLY {
		return mediaBox
	}
	return clipped
}

// normalizeRect reorders r's corners, if needed, so LLX <= URX and
// LLY <= URY - the (min, min)-(max, max) form clipToMediaBox's
// intersection math (and callers elsewhere that assume this form)
// requires, but which PDF itself does not guarantee a page box array is
// already written in.
func normalizeRect(r Rect) Rect {
	if r.LLX > r.URX {
		r.LLX, r.URX = r.URX, r.LLX
	}
	if r.LLY > r.URY {
		r.LLY, r.URY = r.URY, r.LLY
	}
	return r
}

// parseRect interprets obj as a four-number PDF rectangle array
// ([llx lly urx ury]), reporting ok=false if it is not shaped that way.
// Both syntax.Integer and syntax.Real elements are accepted, matching
// how permissive real PDF producers are about which numeric PDF object
// type they use for a given value.
func parseRect(obj syntax.Object) (Rect, bool) {
	arr, ok := obj.(syntax.Array)
	if !ok || len(arr) != 4 {
		return Rect{}, false
	}
	nums := make([]float64, 4)
	for i, v := range arr {
		n, ok := numberValue(v)
		if !ok {
			return Rect{}, false
		}
		nums[i] = n
	}
	return Rect{LLX: nums[0], LLY: nums[1], URX: nums[2], URY: nums[3]}, true
}

// numberValue extracts a float64 from a syntax.Integer or syntax.Real,
// the two PDF object types that represent numbers.
func numberValue(obj syntax.Object) (float64, bool) {
	switch v := obj.(type) {
	case syntax.Integer:
		return float64(v), true
	case syntax.Real:
		return float64(v), true
	default:
		return 0, false
	}
}

// resolveDictionary resolves obj (following it through parser.Document
// if it is a syntax.Reference; using it directly if it is already a
// syntax.Dictionary) and requires the result to be a Dictionary,
// returning an error wrapping pdfviewer.ErrMalformed - naming what,
// which, is included in the error message - otherwise.
func resolveDictionary(p *parser.Document, obj syntax.Object, what string) (syntax.Dictionary, error) {
	resolved, err := resolveObject(p, obj)
	if err != nil {
		return nil, err
	}
	dict, ok := resolved.(syntax.Dictionary)
	if !ok {
		return nil, pdferror.Malformedf("%s is not a dictionary (found %T)", what, resolved)
	}
	return dict, nil
}

// resolveObject follows obj through parser.Document.Resolve if it is a
// syntax.Reference, or returns it unchanged otherwise (PDF allows a
// dictionary entry to be either a direct value or an indirect
// reference to one, and callers throughout this package need to accept
// both interchangeably).
func resolveObject(p *parser.Document, obj syntax.Object) (syntax.Object, error) {
	ref, ok := obj.(syntax.Reference)
	if !ok {
		return obj, nil
	}
	return p.Resolve(ref.Number)
}
