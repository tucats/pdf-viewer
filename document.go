package pdfviewer

import (
	"io"
	"os"

	"github.com/tucats/pdf-viewer/internal/content"
	"github.com/tucats/pdf-viewer/internal/fonts"
	"github.com/tucats/pdf-viewer/internal/model"
	"github.com/tucats/pdf-viewer/internal/parser"
	"github.com/tucats/pdf-viewer/internal/source"
)

// Document is an opened PDF file: its structure has been parsed and its
// page tree has been walked (see internal/parser and internal/model),
// but no page content has been rendered - that happens lazily, per
// page, via Page.Render (not yet implemented; see page.go), matching
// the "lazy" rendering model described in the README's "Pagination and
// resource ownership" section.
//
// A Document must be closed with Close when the caller is done with it.
// Pages obtained from a Document (via Page) must not be used after the
// Document is closed - see Close's doc comment.
//
// # Concurrency (Phase 6 decision)
//
// A *Document, and any Page obtained from it, is NOT safe for
// concurrent use by multiple goroutines. Every internal cache this
// package's layers build up as a document is read - internal/parser's
// resolved-object cache, internal/parser's decoded-object-stream cache,
// and this package's own per-Document font cache (see
// docs/capability-matrix.md and the Progress Log's Phase 6 entry) - is
// an ordinary Go map with no locking, and internal/parser's
// cross-reference recovery scan (see that package's doc comment)
// mutates the document's cross-reference table itself in place. Calling
// any method on the same Document (Page, PageCount, Page.Render,
// Page.Thumbnail, or Close) from more than one goroutine at a time,
// without the caller's own synchronization, is a data race.
//
// This is a deliberate choice, not an oversight: internal/parser's
// reference-cycle detection (see its Document.resolving field) is
// scoped to a single, possibly-reentrant call chain within one
// goroutine, which is what actually shapes this decision - correctly
// generalizing it to detect a cycle across concurrent goroutines would
// need a substantially different design for a benefit (parallel
// rendering *of the same open Document*) this project's own test corpus
// and use cases have not yet demonstrated a pressing need for, the same
// standard applied to every other deferred feature in this project.
//
// A program that wants to render many pages (or many documents) in
// parallel remains free to do so: open a separate *Document per
// goroutine (via Open or OpenFile - each call parses independently and
// the resulting Documents share no state at all with each other), or
// serialize all access to one shared Document with its own mutex. See
// pdfviewer_concurrency_test.go for a regression test covering the
// supported ("separate Document per goroutine") pattern under the race
// detector, and cmd/pdfthumbnails for a worked example.
type Document struct {
	model *model.Document

	// closer is non-nil only when this Document owns the underlying
	// file it is reading from - i.e. it was created by OpenFile, which
	// opened the file itself and is therefore responsible for closing
	// it again. A Document created by Open, from a caller-supplied
	// io.ReaderAt, never closes that reader: Open did not open it, and
	// an io.ReaderAt is not even guaranteed to implement io.Closer in
	// the first place (many, such as bytes.Reader, do not).
	closer io.Closer

	closed bool

	// fontCache remembers every font already loaded from this document's
	// embedded font programs, shared across every Page.Render/Thumbnail
	// call made against this Document for its whole lifetime - see
	// internal/content.FontCache's doc comment for why this is safe
	// (given this project's Phase 6 concurrency decision, above) and
	// what it actually saves. It is never reset or invalidated by Close,
	// since Close's own contract already forbids using anything derived
	// from a closed Document.
	fontCache *content.FontCache
}

// Open parses the PDF document read from r, which must contain size
// bytes total. Opening a document parses its structure (header,
// cross-reference table, trailer, and page tree) but does not render
// any page content - see the Document type's doc comment.
//
// Open does not take ownership of r: it never closes it, even when the
// returned Document's Close method is called, since Open has no way of
// knowing whether r should be closed at all (a caller-provided
// io.ReaderAt might not even implement io.Closer) or whether the caller
// still needs it for something else. A caller that opened r itself (a
// file, typically) is responsible for closing it once both the caller
// and the returned Document are done with it. OpenFile, below, is a
// convenience for the common case of reading directly from a named file
// and does take care of this.
func Open(r io.ReaderAt, size int64, opts ...OpenOption) (*Document, error) {
	cfg := &openConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	src, err := source.New(r, size)
	if err != nil {
		return nil, err
	}
	p, err := parser.Open(src, parser.WithPassword(cfg.password))
	if err != nil {
		return nil, err
	}
	m, err := model.Open(p)
	if err != nil {
		return nil, err
	}
	if cfg.diagnostics != nil {
		m.SetDiagnostics(&cfg.diagnostics.recorder)
	}
	if cfg.fontSubstitution != nil {
		m.SetFontSource(fonts.NewDirectorySource(
			cfg.fontSubstitution.Directories,
			!cfg.fontSubstitution.DisableSystemDefaults,
		))
	}
	return &Document{model: m, fontCache: content.NewFontCache()}, nil
}

// OpenFile opens the named file and parses it as a PDF document,
// exactly as Open does, but as a convenience that also takes care of
// opening (and, unlike Open, eventually closing) the underlying file
// itself: the returned Document's Close method will close the file.
//
// OpenFile is not required for embedding pdf-viewer in a larger
// program - Open, which accepts any io.ReaderAt, is the more general
// entry point (see the README's Draft Public API notes on why
// io.ReaderAt was chosen) - but is convenient for the common case of
// reading a PDF directly from disk.
func OpenFile(name string, opts ...OpenOption) (*Document, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	doc, err := Open(f, info.Size(), opts...)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	doc.closer = f
	return doc, nil
}

// Close releases the Document's caches and, if the Document was created
// by OpenFile, closes the underlying file. It is safe to call more than
// once: every call after the first is a no-op returning a nil error,
// which matches how Go programs conventionally use `defer doc.Close()`
// alongside an earlier explicit Close on a success path without needing
// to guard against a resulting double-close error.
//
// Every Page obtained from this Document (via Page) must not be used
// after Close is called - see the README's Draft Public API notes on
// this ownership rule. Phase 1 does not yet have any Page functionality
// that would actually observe a difference (Page.Render and
// Page.Thumbnail are not implemented yet; see page.go), but the rule is
// enforced starting now so that no caller depends on being able to
// violate it once rendering does land in a later phase.
func (d *Document) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	if d.closer != nil {
		return d.closer.Close()
	}
	return nil
}

// PageCount returns the number of pages in the document. Unlike Page
// below, PageCount remains valid to call after Close, since the page
// count is determined once, while opening the document, and is not
// itself a resource that Close releases.
func (d *Document) PageCount() int {
	return d.model.PageCount()
}

// Page returns the page at the given zero-based index. It returns an
// error wrapping ErrPageIndex if index is negative or greater than or
// equal to PageCount, and an error wrapping ErrClosed if the Document
// has already been closed.
func (d *Document) Page(index int) (Page, error) {
	if d.closed {
		return nil, ErrClosed
	}
	if index < 0 || index >= d.model.PageCount() {
		return nil, ErrPageIndex
	}
	return &pageImpl{doc: d, page: d.model.Page(index)}, nil
}
