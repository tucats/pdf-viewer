package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"sync"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// webFS embeds the static frontend (HTML/CSS/JS) directly into the
// pdfbrowser binary, so "go run ./cmd/pdfbrowser" works from any
// directory without needing to locate the web/ files on disk at
// runtime.
//
//go:embed web
var webFS embed.FS

// maxUploadBytes caps how much a single /api/open request will read
// into memory, mainly so dropping some unrelated, huge file onto the
// page fails with a clear error instead of exhausting memory.
const maxUploadBytes = 256 << 20 // 256 MiB

// thumbnailMaxDimension is the longest side, in pixels, of every
// thumbnail image this server generates - see
// pdfviewer.ThumbnailOptions.MaxDimension.
const thumbnailMaxDimension = 200

// defaultPageScale is the device-pixels-per-PDF-point (see
// pdfviewer.RenderOptions.Scale) used for the full-size page image sent
// to the main viewer, unless overridden by main.go's -scale flag (see
// browserServer.pageScale) or the request's own "scale" query parameter
// (see handlePage).
const defaultPageScale = 1.5

// browserServer holds the single PDF document currently loaded into the
// browser tab. This tool serves one developer, one browser tab, one
// document at a time (see main.go's package doc comment), so -
// deliberately, to keep this simple - there is exactly one "current
// document" shared by every request rather than a per-upload document
// table keyed by an ID.
//
// mu guards every field below *and* serializes every call that touches
// doc: pdfviewer.Document is documented as unsafe for concurrent use
// (see the root package's Document doc comment), and a browser
// naturally fires off several thumbnail requests at once as a page's
// thumbnails scroll into view, so each handler below holds mu for the
// whole time it is using doc, not just while reading the pointer.
type browserServer struct {
	mu sync.Mutex

	// data is the raw bytes of the most recently uploaded PDF, kept
	// around (rather than discarded once doc is opened) so that
	// handleFontSubstitution can re-open the document with a different
	// pdfviewer.WithFontSubstitution setting - which, like every
	// OpenOption, can only be chosen at Open time - without asking the
	// browser to re-upload the file.
	data []byte

	doc        *pdfviewer.Document
	generation int
	pageCount  int

	// diagnostics collects whatever pdfviewer.WithDiagnostics recorded
	// while doc was opened and its pages rendered - see handleDiagnostics.
	// A fresh one is created every time doc is (re)opened, since a
	// pdfviewer.Diagnostics is documented as belonging to a single
	// Document at a time (see that type's own doc comment) and this
	// server always wants "diagnostics for the document currently
	// loaded", not a running log across every document ever dropped on
	// it.
	diagnostics *pdfviewer.Diagnostics

	// useSystemFonts mirrors the frontend's "Use system fonts" checkbox
	// (see handleFontSubstitution) - whether the *next* document opened
	// (including the current one, if any, re-opened right away) should
	// pass pdfviewer.WithFontSubstitution. It persists across documents
	// so that dropping a second file after checking the box does not
	// silently forget the choice. Its initial value comes from main.go's
	// -use-system-fonts flag (see newBrowserServer); handleConfig reports
	// that initial value to the frontend so the checkbox can start out
	// checked to match, without the frontend needing to guess or the
	// server needing to fake a change event.
	useSystemFonts bool

	// pageScale is the device-pixels-per-PDF-point (see
	// pdfviewer.RenderOptions.Scale) handlePage uses for the full-size
	// page image when the request's own "scale" query parameter is
	// absent - set once from main.go's -scale flag (see
	// newBrowserServer) and never changed afterward, so - unlike every
	// other field on this type - reading it needs no mu (nothing ever
	// writes it after construction).
	pageScale float64
}

// newBrowserServer builds a browserServer with its initial
// configuration - pageScale and useSystemFonts - taken from main.go's
// command-line flags. A zero-value browserServer would work just as
// well for pageScale (handlePage would just always fall through to
// defaultPageScale), but not for useSystemFonts, which needs to be
// seeded from -use-system-fonts before the very first document is
// opened.
func newBrowserServer(pageScale float64, useSystemFonts bool) *browserServer {
	return &browserServer{pageScale: pageScale, useSystemFonts: useSystemFonts}
}

// Sentinel errors returned by browserServer.withPage, translated to
// HTTP status codes by writeRenderError below.
var (
	errNoDocument         = errors.New("no document is loaded yet")
	errGenerationMismatch = errors.New("a different document has since been loaded")
	errPageRange          = errors.New("page index out of range")
)

// newMux builds the http.Handler for the whole pdfbrowser web app: the
// embedded static frontend at "/", plus the small JSON/image API srv's
// methods implement. triggerQuit is called once /api/quit is asked to
// stop the server - see main.go's run for what it does.
func newMux(srv *browserServer, triggerQuit func()) http.Handler {
	// fs.Sub rooted at "web" so the embedded files are served at "/",
	// "/app.js", "/style.css", ... rather than under a "/web/" prefix.
	staticFiles, err := fs.Sub(webFS, "web")
	if err != nil {
		// Only possible if the "web" directory referenced by the
		// go:embed directive above were missing at build time, which
		// would already have failed the build itself - not a runtime
		// condition this program needs to recover from.
		panic(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))
	mux.HandleFunc("/api/open", srv.handleOpen)
	mux.HandleFunc("/api/thumbnail", srv.handleThumbnail)
	mux.HandleFunc("/api/page", srv.handlePage)
	mux.HandleFunc("/api/font-substitution", srv.handleFontSubstitution)
	mux.HandleFunc("/api/config", srv.handleConfig)
	mux.HandleFunc("/api/diagnostics", srv.handleDiagnostics)
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		srv.handleQuit(w, r, triggerQuit)
	})
	return mux
}

// documentResponse is the JSON body handleOpen and handleFontSubstitution
// send back once a document is (re)loaded: enough for the frontend to
// know how many thumbnails to lay out and what "generation" to stamp
// onto every subsequent thumbnail/page image request for this document
// (see the generation field's doc comment above). HasDocument is false
// only for handleFontSubstitution's response when no document has been
// opened yet - see that handler.
type documentResponse struct {
	HasDocument bool `json:"hasDocument"`
	Generation  int  `json:"generation"`
	PageCount   int  `json:"pageCount"`
}

// openDocument opens data (the raw bytes of an uploaded PDF) with a
// fresh pdfviewer.Diagnostics attached, and - if useSystemFonts is true -
// pdfviewer.WithFontSubstitution's zero-value configuration, which (per
// that option's own doc comment) already means "scan this machine's
// usual platform font directories". This is the one place both
// handleOpen (a brand new document) and handleFontSubstitution (the
// same document's bytes, re-opened with the checkbox's new value)
// actually call pdfviewer.Open, so the two can never drift apart in
// which options they pass.
func openDocument(data []byte, useSystemFonts bool) (*pdfviewer.Document, *pdfviewer.Diagnostics, error) {
	diagnostics := &pdfviewer.Diagnostics{}
	opts := []pdfviewer.OpenOption{pdfviewer.WithDiagnostics(diagnostics)}
	if useSystemFonts {
		opts = append(opts, pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{}))
	}

	// bytes.NewReader's *bytes.Reader implements io.ReaderAt, which is
	// exactly what pdfviewer.Open wants - reading the whole upload into
	// memory first (rather than, say, spooling it to a temp file) keeps
	// this simple, which is fine for a local development tool handling
	// one document at a time (see maxUploadBytes for the resulting size
	// cap on how much is ever read in this way).
	doc, err := pdfviewer.Open(bytes.NewReader(data), int64(len(data)), opts...)
	if err != nil {
		return nil, nil, err
	}
	return doc, diagnostics, nil
}

// handleOpen accepts a PDF as the raw POST body, replacing whatever
// document was previously loaded (if any) with it.
func (s *browserServer) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading upload: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	useSystemFonts := s.useSystemFonts
	s.mu.Unlock()

	doc, diagnostics, err := openDocument(data, useSystemFonts)
	if err != nil {
		http.Error(w, fmt.Sprintf("opening PDF: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	if s.doc != nil {
		s.doc.Close()
	}
	s.data = data
	s.doc = doc
	s.diagnostics = diagnostics
	s.generation++
	s.pageCount = doc.PageCount()
	resp := documentResponse{HasDocument: true, Generation: s.generation, PageCount: s.pageCount}
	s.mu.Unlock()

	writeJSON(w, resp)
}

// handleFontSubstitution updates whether documents should be opened
// with pdfviewer.WithFontSubstitution (the "Use system fonts" checkbox -
// see docs/FONTS.md's Phase 4 for what that option actually does: find
// a real substitute outline, from a font installed on this machine, for
// a PDF font this package could not otherwise extract an embedded
// outline from). Since that choice can only be made at Open time, if a
// document is already loaded this re-opens it (from the bytes handleOpen
// kept in s.data) with the new setting, which is why this bumps
// s.generation exactly like loading a brand new document does - every
// thumbnail/page image URL the frontend is holding embeds the old
// generation and must be re-requested against the new one to reflect
// the change.
func (s *browserServer) handleFontSubstitution(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("decoding request body: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.useSystemFonts = body.Enabled
	data := s.data
	hasDoc := s.doc != nil
	s.mu.Unlock()

	if !hasDoc {
		// Nothing loaded yet - the preference above is recorded and
		// will apply to whatever is opened next; there is no document
		// to re-open right now.
		writeJSON(w, documentResponse{HasDocument: false})
		return
	}

	doc, diagnostics, err := openDocument(data, body.Enabled)
	if err != nil {
		http.Error(w, fmt.Sprintf("re-opening PDF: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.doc.Close()
	s.doc = doc
	s.diagnostics = diagnostics
	s.generation++
	s.pageCount = doc.PageCount()
	resp := documentResponse{HasDocument: true, Generation: s.generation, PageCount: s.pageCount}
	s.mu.Unlock()

	writeJSON(w, resp)
}

// configResponse is the JSON body handleConfig sends back.
type configResponse struct {
	UseSystemFonts bool `json:"useSystemFonts"`
}

// handleConfig reports this server's current "Use system fonts" state
// (see the useSystemFonts field's doc comment) so the frontend can set
// its checkbox to match on page load - initially seeded from main.go's
// -use-system-fonts flag, and kept in sync afterward by whatever the
// checkbox itself last POSTed to /api/font-substitution. This is a
// plain GET, not paired with a POST the way /api/font-substitution is:
// it only ever reports state, never changes it.
func (s *browserServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	useSystemFonts := s.useSystemFonts
	s.mu.Unlock()

	writeJSON(w, configResponse{UseSystemFonts: useSystemFonts})
}

// diagnosticsResponse is the JSON body handleDiagnostics sends back.
type diagnosticsResponse struct {
	Messages []string `json:"messages"`
}

// handleDiagnostics returns every diagnostic message recorded so far
// (docs/FONTS.md and pdfviewer.Diagnostics's own doc comment: things
// this package tolerated rather than rejected outright while opening
// the current document or rendering its pages - a missing font program
// substituted for a placeholder box, an unresolvable resource name, and
// so on) for the currently loaded document. The frontend polls this
// after every thumbnail/page render (see app.js's refreshDiagnostics)
// rather than this server pushing updates, since Diagnostics.Messages
// is cheap to call and a handful of extra GETs is simpler than adding a
// second transport (SSE/WebSocket) to a development tool.
func (s *browserServer) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	var messages []string
	if s.diagnostics != nil {
		messages = s.diagnostics.Messages()
	}
	s.mu.Unlock()

	writeJSON(w, diagnosticsResponse{Messages: messages})
}

// handleThumbnail renders one page's thumbnail (see
// thumbnailMaxDimension) as a PNG image.
func (s *browserServer) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	gen, page, err := parseGenAndPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	img, err := s.withPage(gen, page, func(p pdfviewer.Page) (image.Image, error) {
		return p.Thumbnail(r.Context(), pdfviewer.ThumbnailOptions{MaxDimension: thumbnailMaxDimension})
	})
	if err != nil {
		writeRenderError(w, err)
		return
	}
	writePNG(w, img)
}

// handlePage renders one page at full size (or at the "scale" query
// parameter, if given - see pdfviewer.RenderOptions.Scale) as a PNG
// image, for display in the main viewer.
func (s *browserServer) handlePage(w http.ResponseWriter, r *http.Request) {
	gen, page, err := parseGenAndPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	scale := s.pageScale
	if raw := r.URL.Query().Get("scale"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			http.Error(w, "invalid scale parameter", http.StatusBadRequest)
			return
		}
		scale = parsed
	}

	img, err := s.withPage(gen, page, func(p pdfviewer.Page) (image.Image, error) {
		return p.Render(r.Context(), pdfviewer.RenderOptions{Scale: scale})
	})
	if err != nil {
		writeRenderError(w, err)
		return
	}
	writePNG(w, img)
}

// handleQuit tells the running server to shut itself down after this
// request completes. triggerQuit is main.go's run's signal to begin
// that shutdown; see its doc comment there for why it is safe to call
// more than once.
func (s *browserServer) handleQuit(w http.ResponseWriter, r *http.Request, triggerQuit func()) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	triggerQuit()
}

// parseGenAndPage extracts and validates the "gen" and "page" query
// parameters shared by handleThumbnail and handlePage.
func parseGenAndPage(r *http.Request) (gen, page int, err error) {
	gen, err = strconv.Atoi(r.URL.Query().Get("gen"))
	if err != nil {
		return 0, 0, fmt.Errorf("missing or invalid gen parameter")
	}
	page, err = strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil {
		return 0, 0, fmt.Errorf("missing or invalid page parameter")
	}
	return gen, page, nil
}

// withPage locks s, validates that gen still names the currently loaded
// document and that page is in range, and then - still holding the
// lock, per browserServer's own doc comment on why - looks up that page
// and calls render with it, returning whatever image render produces.
func (s *browserServer) withPage(gen, page int, render func(pdfviewer.Page) (image.Image, error)) (image.Image, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.doc == nil {
		return nil, errNoDocument
	}
	if gen != s.generation {
		return nil, errGenerationMismatch
	}
	if page < 0 || page >= s.pageCount {
		return nil, errPageRange
	}

	p, err := s.doc.Page(page)
	if err != nil {
		return nil, err
	}
	return render(p)
}

// writeRenderError maps the sentinel errors withPage can return to
// appropriate HTTP status codes; anything else (a rendering failure
// from deep inside the PDF interpreter, say) is reported as a generic
// server error.
func writeRenderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoDocument):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, errGenerationMismatch):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, errPageRange):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// writeJSON writes v to w as a JSON response body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("pdfbrowser: encoding JSON response: %v", err)
	}
}

// writePNG writes img to w as a PNG image response body. The
// Cache-Control header tells the browser it may cache the result
// indefinitely: every thumbnail/page URL this server hands out embeds
// the document's generation number, so a URL's content can never change
// once served (a newly dropped document gets a new generation, and thus
// entirely new URLs - see browserServer's own doc comment).
func writePNG(w http.ResponseWriter, img image.Image) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if err := png.Encode(w, img); err != nil {
		log.Printf("pdfbrowser: encoding PNG response: %v", err)
	}
}
