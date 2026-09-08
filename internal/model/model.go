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

	// RawResources is the page's (possibly inherited) /Resources
	// dictionary, not yet interpreted. It is exposed now, ahead of the
	// fonts/images/content-stream work that will actually use it
	// (Phases 2-4 of the phased plan), so that model.Document's shape
	// does not need to change again once those phases add code that
	// consumes it.
	RawResources syntax.Dictionary

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

	d := &Document{parser: p}
	visited := make(map[int]bool)
	if err := d.collectPages(catalog["Pages"], inheritable{}, 0, visited); err != nil {
		return nil, err
	}
	return d, nil
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
	resources    syntax.Dictionary
	hasResources bool
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

	inherited = mergeInherited(inherited, dict)

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
	page, err := buildPage(dict, inherited)
	if err != nil {
		return err
	}
	d.pages = append(d.pages, page)
	return nil
}

// mergeInherited returns the inheritable attributes a child of dict
// should see: dict's own values where present, falling back to
// inherited's (i.e. an ancestor's) values otherwise.
func mergeInherited(inherited inheritable, dict syntax.Dictionary) inheritable {
	if box, ok := dict["MediaBox"]; ok {
		if r, ok := parseRect(box); ok {
			inherited.mediaBox = &r
		}
	}
	if res, ok := dict["Resources"].(syntax.Dictionary); ok {
		inherited.resources = res
		inherited.hasResources = true
	}
	return inherited
}

// buildPage constructs a Page from a leaf page dictionary and whatever
// inheritable attributes it inherited from its ancestors, requiring that
// a MediaBox was found somewhere along the way (directly or inherited) -
// per the PDF specification, every page must have one available.
func buildPage(dict syntax.Dictionary, inherited inheritable) (Page, error) {
	if inherited.mediaBox == nil {
		return Page{}, pdferror.Malformedf("page has no /MediaBox, direct or inherited")
	}
	page := Page{
		MediaBox: *inherited.mediaBox,
		dict:     dict,
	}
	if inherited.hasResources {
		page.RawResources = inherited.resources
	}
	return page, nil
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
