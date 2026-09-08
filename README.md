# pdf-viewer

An embeddable PDF renderer written in Go.

The goal is a package that a Go program can import to inspect and render PDF
documents without CGO, without launching a local PDF utility, and without
requiring a separately installed native rendering engine. The renderer will be
implemented in this repository rather than wrapping MuPDF, PDFium, Poppler, or
another existing renderer.

This document is both the project plan and the beginning of the package
contract. As implementation proceeds, the API and internal design sections
should be updated with the behavior that the tests actually guarantee.

## Decisions So Far

### Primary output: `image.Image`

The primary output should be a raster image for one page at a time. This fits
the Go standard library, works for desktop viewers and image pipelines, and is
the interface used by the most relevant existing Go renderers:

| Project | Rendering interface | Dependency/runtime observation |
| --- | --- | --- |
| [go-pdfium](https://github.com/klippa-app/go-pdfium) | Renders selected pages to Go images, with DPI or pixel sizing; also exposes PNG/JPEG helpers | PDFium through CGO, subprocesses, or embedded WebAssembly depending on the chosen implementation |
| [go-fitz](https://github.com/gen2brain/go-fitz) | `Document.Image(page)` returns an image; also supports text, HTML, and SVG extraction | MuPDF wrapper; native libraries and CGO/purego loading are part of its deployment model |
| [pdfcpu](https://github.com/pdfcpu/pdfcpu) | Strong pure-Go PDF parsing and document operations | Not a page renderer; useful as a design reference for validation, object resolution, and dependency independence |
| [UniPDF](https://github.com/unidoc/unipdf) | Separate render package in a full PDF model, with page and content operations | Pure Go, but its current distribution requires a commercial license key |
| [benoitkugler/pdf](https://github.com/benoitkugler/pdf) | Typed PDF model and reader layers intended to be reused by higher-level libraries | Pure Go and MIT licensed; useful as a parser/model reference, not a finished renderer |

These projects point to a page-oriented API rather than returning one giant
document result. A caller can render only the visible page, retain only the
images it needs, and choose a separate low-resolution operation for a page
strip or thumbnail view. HTML and SVG are not the primary output: they require
another layout or browser-like interpretation and cannot faithfully represent
all PDF compositing, clipping, transparency, and print geometry.

### Pagination and resource ownership

Documents will expose a stable page count and allow random access to pages.
Rendering will be lazy: opening a document parses enough structure to inspect
it, while `Render` does the expensive work for one requested page. A future
streaming iterator may be added for sequential workloads, but it must not
replace random access or force every page into memory.

The proposed indexing convention is zero-based, matching Go slices and the
existing rendering APIs surveyed above. Page dimensions are expressed in PDF
points (1/72 inch); rendered dimensions are pixels selected by a render option.

### Dependency and safety policy

The core will use the Go standard library only unless a later decision records
a compelling reason otherwise. In particular, it must not use CGO, `os/exec`,
subprocess workers, or runtime loading of a system PDF library. Malformed and
hostile input is expected: parsers must have bounded work where practical,
return errors instead of panicking, and never write files or access the network
as a consequence of rendering.

## Draft Public API

Names and exact signatures are provisional until the first implementation and
tests land. The important contract is the ownership and granularity:

```go
package pdfviewer

type Document struct { /* opaque implementation */ }

func Open(r io.ReaderAt, size int64, opts ...OpenOption) (*Document, error)
func OpenFile(name string, opts ...OpenOption) (*Document, error)

func (d *Document) Close() error
func (d *Document) PageCount() int
func (d *Document) Page(index int) (Page, error)

type Page interface {
	Bounds() Rect // page box in PDF points
	Render(ctx context.Context, opts RenderOptions) (image.Image, error)
	Thumbnail(ctx context.Context, opts ThumbnailOptions) (image.Image, error)
}
```

The implementation should prefer `io.ReaderAt` plus a size because PDF files
use offsets and can be large. `OpenFile` is a convenience, not a requirement
for embedding. `Close` releases caches and any backing reader resources; pages
must not outlive their document. Rendered images belong to the caller and may
be retained after the document is closed.

`RenderOptions` should make the output predictable without tying callers to an
image file format. It is expected to cover DPI or an explicit pixel size,
page-box selection, rotation, background color, and color mode. Encoding PNG or
JPEG belongs to the caller through the standard `image/png` and `image/jpeg`
packages. `ThumbnailOptions` should be a convenience for a bounded maximum
dimension and should share the same page interpretation as full rendering.

The API still needs decisions for password-protected files, cancellation
granularity, annotations, and whether a document or a page may be rendered
concurrently. The initial contract should choose conservative behavior and
document it rather than silently promising full PDF compatibility.

## Phased Plan

### Phase 0: Scope, fixtures, and compatibility policy

- Establish the module path, supported Go versions, error conventions, and
	zero-based page indexing.
- Create small hand-authored PDFs for each feature under test, plus permitted
	real-world fixtures with recorded licenses and provenance.
- Define a capability matrix for PDF versions, encryption, filters, color
	spaces, fonts, transparency, images, annotations, and page boxes.
- Add fuzz targets for file parsing and content streams before accepting broad
	input compatibility claims.

**Exit criteria:** a reproducible test corpus, documented non-goals, and no
CGO, subprocess, network, or native-library dependency in the build.

**Status: done.** See the Progress Log at the end of this document for
what was actually built.

### Phase 1: File structure and safe object model

- Parse the header, body objects, cross-reference tables and streams, trailers,
	incremental updates, and object streams as their support is added.
- Resolve indirect references lazily and distinguish missing, null, malformed,
	and unsupported values.
- Implement limits for recursion, stream sizes, nesting, and decompression to
	keep resource use inspectable.
- Expose only document metadata, page count, page boxes, and basic page access
	at first; do not couple the public API to internal PDF dictionary types.

**Exit criteria:** valid documents can be opened and paginated; invalid input
returns classified errors; parser fuzzing finds no panics or unbounded loops.

**Status: done for classic (table-based) cross-reference structure**; PDF
1.5+ cross-reference streams and object streams were deliberately moved to
Phase 2, since both are normally Flate-compressed and Flate decoding is
Phase 2 work. See the Progress Log at the end of this document.

### Phase 2: Content streams and a minimal raster backend

- Decode ASCII85, ASCIIHex, Flate, RunLength, and LZW streams as needed by the
	fixture corpus, with explicit unsupported-filter errors for the remainder.
- Implement the PDF graphics state, coordinate transforms, paths, fills,
	strokes, clipping, line styles, and solid colors.
- Add a deterministic software rasterizer using standard-library image types.
- Render simple vector pages and compare output against checked-in reference
	images with a documented tolerance.

**Exit criteria:** `Page.Render` produces correct images for basic paths,
transforms, clipping, and page boxes on every supported platform.

### Phase 3: Images, color, and thumbnails

- Support inline and referenced images, image masks, soft masks, and the common
	PDF image color spaces and decode arrays.
- Add JPEG and other image decoding only where the license and standard-library
	support are clear; preserve a narrow unsupported path rather than guessing.
- Implement `Thumbnail` as a bounded render of the same page display list,
	with no second interpretation of the PDF.
- Test thumbnails for aspect ratio, orientation, transparent/background
	behavior, and memory bounds.

**Exit criteria:** image-heavy pages and thumbnails are correct, bounded, and
	visibly consistent with full-page rendering.

### Phase 4: Text and fonts

- Parse text operators, text matrices, character mappings, widths, and font
	encodings.
- Support embedded Type 1, TrueType, and Type 0/CID fonts incrementally.
- Define font fallback and missing-glyph behavior; do not assume that a system
	font is available or silently invoke a platform font service.
- Keep text extraction as a separate capability from text painting so it can be
	added without changing page rendering ownership.

**Exit criteria:** representative Latin text renders with embedded fonts and
	predictable fallback; text positioning tests cover rotation and scaling.

### Phase 5: Transparency, patterns, shadings, and advanced graphics

- Add transparency groups, blend modes, soft masks, tiling patterns, shading
	patterns, and the remaining color-space behavior needed by the corpus.
- Add annotations and form appearance streams only after ordinary page content
	is stable.
- Profile allocations and cache decoded resources without making cache
	lifetime observable through the public API.

**Exit criteria:** the renderer handles a broad compatibility corpus and has
	benchmark results for single-page, multi-page, and thumbnail workloads.

### Phase 6: API stabilization and viewer integration

- Decide and document concurrency guarantees, cancellation behavior, password
	handling, error taxonomy, and supported PDF versions.
- Add examples for a page preview, page list with thumbnails, and full-page
	export using standard-library image encoders.
- Run cross-platform CI with `CGO_ENABLED=0`, race tests, fuzzing, benchmarks,
	and dependency/license checks.
- Version the public package only after the API has passed the preceding
	compatibility and ownership review.

**Exit criteria:** an importing Go program can open, inspect, render, thumbnail,
	and close a document without knowing the PDF internals.

## Proposed Internal Layout

The package boundaries should follow the PDF pipeline and remain testable in
isolation:

```text
source       random-access input, bounds and read accounting
syntax       tokens, names, strings, arrays, dictionaries and streams
parser       xref/trailer resolution, object streams and lazy references
model        pages, resources, boxes, fonts, images and document metadata
content      content-stream operators and display-list construction
graphics     graphics state, paths, compositing and clipping
raster       software rasterization into image.Image
fonts        font decoding, metrics, mappings and fallback policy
```

The public package should compose these layers without exposing their concrete
types. A display list or equivalent intermediate representation is preferred
over drawing directly while parsing: it enables thumbnails, caching,
inspection, and later alternate backends without reparsing the file.

## Reference Material and Attribution

PDF is specified by ISO 32000. The freely available Adobe PDF 1.7 reference is
useful historical reading, while PDF 2.0 is maintained through ISO 32000-2 and
the PDF Association. The project will link to those sources and record the
edition used for each implementation decision, but will not copy a standard or
third-party manual into this repository unless its redistribution terms clearly
permit that copy. Code, test fixtures, fonts, and sample PDFs must each carry
their own attribution and license information.

Useful references:

- [Adobe PDF 1.7 reference materials](https://www.adobe.com/go/pdfreference)
- [PDF Association: ISO 32000-2 PDF 2.0](https://pdfa.org/resource/iso-32000-2-pdf-2-0/)
- [ISO 32000-2:2020 official record](https://www.iso.org/standard/75839.html)
- [pdfcpu documentation and source](https://github.com/pdfcpu/pdfcpu)
- [go-pdfium rendering API and runtime notes](https://github.com/klippa-app/go-pdfium)
- [go-fitz page image API](https://github.com/gen2brain/go-fitz)
- [benoitkugler/pdf model and reader](https://github.com/benoitkugler/pdf)

Survey performed September 8, 2026. The linked projects and their licenses can
change; verify current terms before copying code, fixtures, fonts, or prose.

## Non-goals for the Initial Release

- HTML as the canonical rendering output.
- A command-line wrapper around another PDF program.
- CGO, native shared libraries, runtime-loaded PDF engines, or forked workers.
- Guaranteed support for every PDF feature, malformed file, encryption scheme,
	or embedded scripting behavior.
- PDF creation or editing before the reader and renderer are dependable.

## License

This project is released under the MIT License; see [LICENSE](LICENSE).

## Progress Log

This section is updated at the end of each phase with what was actually
built, so this document stays an accurate record of implementation
status rather than only a forward-looking plan. Entries are appended in
phase order and are not rewritten later except to fix mistakes; if a
later phase changes an earlier decision, that belongs in the later
phase's entry with a note about what changed.

### Phase 0: Scope, fixtures, and compatibility policy — done (2026-09-08)

- **Module and error conventions.** Initialized the Go module as
	`github.com/tucats/pdf-viewer` targeting Go 1.23. Added
	[errors.go](errors.go), defining the sentinel errors every later
	package will wrap its failures in: `ErrMalformed` (structurally
	invalid input), `ErrUnsupported` (valid PDF, unimplemented feature),
	`ErrClosed`, and `ErrPageIndex`, plus `MalformedErrorf`/
	`UnsupportedErrorf` helper constructors that wrap them with `%w` so
	`errors.Is` keeps working through any amount of further wrapping.
	Covered by [errors_test.go](errors_test.go).
- **Package skeleton.** Created the eight internal packages from the
	"Proposed Internal Layout" section above
	(`internal/{source,syntax,parser,model,content,graphics,raster,fonts}`),
	each currently containing only a `doc.go` explaining that package's
	future responsibility and which phase is expected to fill it in. No
	parsing or rendering code exists yet — that starts in Phase 1.
- **Hand-authored fixture corpus.** Added
	[tools/genfixtures](tools/genfixtures/main.go), a small program that
	*generates* the PDF fixtures under `testdata/fixtures/handmade` rather
	than having them hand-edited byte-by-byte, so that their
	cross-reference offsets are always correct by construction and the
	corpus is reproducible (`go run ./tools/genfixtures` regenerates it
	identically). Five fixtures were added: a minimal one-page PDF, a
	two-page PDF (page-tree traversal), an incrementally-updated PDF (two
	chained trailers via `/Prev`), a PDF with a deliberately corrupted
	cross-reference offset, and a truncated PDF. Each fixture's purpose is
	documented in [testdata/fixtures/FIXTURES.md](testdata/fixtures/FIXTURES.md),
	along with provenance/license (original work, MIT, same as the rest of
	the repository). The fixtures were independently validated against
	`pypdf` during development to confirm they parse the way each was
	designed to (including the corrupted one being auto-repaired via a
	linear object scan, and the truncated one correctly failing).
	[tools/genfixtures/main_test.go](tools/genfixtures/main_test.go) keeps
	the checked-in `.pdf` files from silently drifting out of sync with
	the generator code that is supposed to produce them, and
	[fixtures_test.go](fixtures_test.go) adds a byte-level smoke test (the
	fixtures exist and start with a PDF header) that will be superseded by
	real structural parsing tests once Phase 1 lands.
- **Capability matrix.** Added
	[docs/capability-matrix.md](docs/capability-matrix.md), tracking
	support status per PDF version, encryption scheme, filter, color
	space, font type, transparency/graphics feature, image feature,
	annotation feature, and page box, each tagged with its target phase.
	Everything is currently "Not started," which is expected for Phase 0.
- **Fuzz targets deferred to Phase 1.** The README's Phase 0 bullet list
	calls for fuzz targets for "file parsing and content streams" — there
	is deliberately no parser yet to fuzz, so this was not done in Phase 0.
	It is carried forward as a Phase 1 deliverable instead: fuzz targets
	for `internal/source`/`internal/syntax`/`internal/parser` should land
	alongside the parsing code they exercise, not as an empty scaffold
	ahead of it.
- **CI.** Added [.github/workflows/ci.yml](.github/workflows/ci.yml),
	which builds, vets, and tests the module with `CGO_ENABLED=0` on
	Linux, macOS, and Windows, plus a `gofmt` check. This turns the "no
	CGO, subprocess, network, or native-library dependency" exit criterion
	into something enforced automatically rather than only stated in this
	document.

### Phase 1: File structure and safe object model — mostly done (2026-09-08)

- **Shared error sentinels.** Added `internal/pdferror`, a small leaf
	package holding the actual `ErrMalformed`/`ErrUnsupported` values;
	the root package's `errors.go` now re-exports them instead of
	declaring its own copies, so every internal package (which cannot
	import the root package without an import cycle) can still produce
	errors a caller's `errors.Is(err, pdfviewer.ErrMalformed)` finds. See
	the package doc comment on `internal/pdferror` for the full rationale.
- **`internal/source`.** Added [source.go](internal/source/source.go): a
	bounds-checked wrapper around `io.ReaderAt` that every byte read in
	this module ultimately goes through, so an out-of-range offset or
	length anywhere in the file always fails as a classified error
	instead of forwarding to whatever the caller's `io.ReaderAt` happens
	to do. Covered by
	[source_test.go](internal/source/source_test.go).
- **`internal/syntax`.** Added the PDF object grammar: a tokenizer
	([lexer.go](internal/syntax/lexer.go)) implementing PDF's whitespace/
	delimiter/regular-character lexical rules (including literal- and
	hex-string escapes, name `#xx` escapes, and comments), and a value
	parser ([value.go](internal/syntax/value.go)) that assembles tokens
	into booleans, numbers, strings, names, arrays, dictionaries,
	streams, indirect references, and null
	([object.go](internal/syntax/object.go)). Nesting depth is bounded
	(`maxNestingDepth`) to prevent stack exhaustion on adversarial input.
	A stream's raw bytes are read either by its dictionary's direct
	`/Length` or, when `/Length` is an indirect reference this package
	cannot resolve on its own, by a bounded scan for the `endstream`
	keyword. Covered by
	[lexer_test.go](internal/syntax/lexer_test.go) and
	[value_test.go](internal/syntax/value_test.go), plus the Phase 1 fuzz
	targets promised in the Phase 0 entry above:
	[fuzz_test.go](internal/syntax/fuzz_test.go) (`FuzzLexer`,
	`FuzzParseValue`) ran clean across several million executions each
	during development.
- **`internal/parser`.** Added
	[parser.go](internal/parser/parser.go) and
	[resolve.go](internal/parser/resolve.go): header validation, locating
	`startxref` and walking a classic cross-reference table's trailer,
	following any chain of incremental updates via each trailer's `/Prev`
	entry (newest revision's object definitions correctly shadow older
	ones), and lazy, cached indirect-object resolution. When the
	cross-reference table's recorded offset for an object turns out to be
	wrong, `Document.Resolve` falls back once to a whole-file linear scan
	for `"N G obj"` markers and retries — this is exactly what
	`malformed-bad-xref-offset.pdf` (see the Phase 0 entry above) is a
	regression test for. PDF 1.5+ cross-reference streams are not
	supported yet: both cross-reference streams and object streams are
	normally Flate-compressed, and decompression is Phase 2 work, so
	implementing them now would have meant half-implementing
	decompression ahead of schedule; opening such a file currently fails
	with a clear error wrapping `ErrUnsupported` rather than being
	silently misread. Covered by
	[parser_test.go](internal/parser/parser_test.go) (against both the
	fixture corpus and several small ad hoc malformed inputs built
	in-test) and the fuzz target in
	[fuzz_test.go](internal/parser/fuzz_test.go)
	(`FuzzOpenAndResolveAll`), which ran over 17 million executions
	clean during development.
- **`internal/model`.** Added [model.go](internal/model/model.go):
	page-tree traversal from the trailer's `/Root` through the document
	catalog's `/Pages`, flattening it into an ordered slice of pages and
	resolving PDF's page-attribute inheritance rules for `/MediaBox` and
	`/Resources` (a page's own value wins; otherwise the nearest
	ancestor's value is inherited). Bounded recursion depth
	(`maxPageTreeDepth`) plus a visited-object-number check reject a
	cyclic or absurdly deep page tree instead of recursing forever.
	Covered by [model_test.go](internal/model/model_test.go), including
	several page-tree shapes (multi-level trees, inheritance overridden
	at the page level, a deliberate cycle) built with a small in-test
	helper rather than as checked-in fixtures, since they each test one
	narrow structural rule in isolation.
- **Public API.** [document.go](document.go), [page.go](page.go),
	[rect.go](rect.go), and [options.go](options.go) implement the first
	real slice of the README's Draft Public API: `Open`, `OpenFile`,
	`Document.Close` (idempotent; closes the underlying file only if
	`OpenFile` opened it), `Document.PageCount`, `Document.Page`, and
	`Page.Bounds`. `Page.Render` and `Page.Thumbnail` exist with the
	signatures the README sketches but always return an error wrapping
	`ErrUnsupported` for now, since a rasterizer is Phase 2/3 work — they
	were added now, ahead of an implementation, so the `Page` interface's
	shape will not need to change (only gain a working body) once
	rendering lands. `OpenOption`/`RenderOptions`/`ThumbnailOptions` exist
	as currently-empty extension points for the same reason. Covered by
	[pdfviewer_test.go](pdfviewer_test.go), exercising the public API
	end-to-end against the fixture corpus (rather than any internal
	package directly), including the closed-document/page-index error
	paths and a canceled-context check on `Render`.
- **Capability matrix updated.** `docs/capability-matrix.md`'s "PDF
	1.4–1.7 core structure" row is now "Partial" (classic xref/trailer/
	incremental-update support exists; broader real-world corpus coverage
	is still growing) and its "PDF 1.5+ cross-reference streams" row now
	targets Phase 2 instead of Phase 1, matching the decision above.
	`MediaBox` is now "Done."
- **What's carried forward.** PDF 1.5+ cross-reference streams and
	object streams move to Phase 2, alongside Flate decoding. `CropBox`,
	page rotation, and everything content-stream-related remain
	unimplemented, as planned.
