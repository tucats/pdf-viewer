# pdf-viewer API guide

This document describes the public API of `github.com/tucats/pdf-viewer`
for a Go developer who wants to use the package - opening PDF files,
inspecting them, and rendering pages to images. It does not cover why
the package is built the way it is or what is planned for later; for
that, see [PLAN.md](PLAN.md) (the project's design rationale, phased
implementation plan, and progress log) and
[capability-matrix.md](capability-matrix.md) (a row-by-row answer to
"does this support PDF feature X").

Everything described here is also documented as Go doc comments on the
actual exported symbols - run `go doc github.com/tucats/pdf-viewer` or
browse [pkg.go.dev](https://pkg.go.dev/github.com/tucats/pdf-viewer) (once
published) for that reference in the usual Go tooling. This guide's job
is to tie those symbols together into a single, task-oriented
walkthrough with worked examples.

## Contents

- [Installation](#installation)
- [Quick start](#quick-start)
- [Core concepts](#core-concepts)
- [Opening a document](#opening-a-document)
- [Diagnostics](#diagnostics)
- [Font substitution](#font-substitution)
- [Inspecting a document](#inspecting-a-document)
- [Rendering a page](#rendering-a-page)
- [Thumbnails](#thumbnails)
- [Error handling](#error-handling)
- [Cancellation](#cancellation)
- [Concurrency](#concurrency)
- [What gets rendered](#what-gets-rendered)
- [Example programs](#example-programs)
- [Stability](#stability)

## Installation

```sh
go get github.com/tucats/pdf-viewer
```

The module requires Go 1.23 or later (see `go.mod`) and has zero
third-party dependencies - it is pure Go, with no CGO, no subprocesses,
and no runtime-loaded native libraries, so it cross-compiles and embeds
the same way any other Go package does.

## Quick start

Render the first page of a PDF file to a PNG:

```go
package main

import (
	"context"
	"image/png"
	"log"
	"os"

	pdfviewer "github.com/tucats/pdf-viewer"
)

func main() {
	doc, err := pdfviewer.OpenFile("input.pdf")
	if err != nil {
		log.Fatal(err)
	}
	defer doc.Close()

	page, err := doc.Page(0) // pages are zero-indexed: 0 is the first page
	if err != nil {
		log.Fatal(err)
	}

	img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
	if err != nil {
		log.Fatal(err)
	}

	out, err := os.Create("page0.png")
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()

	if err := png.Encode(out, img); err != nil {
		log.Fatal(err)
	}
}
```

`page.Render` returns an ordinary `image.Image` (in practice an
`*image.RGBA`), so it works with any encoder or image-processing code in
the standard library or the wider Go ecosystem - this package has no
opinion about file formats. See `cmd/pdfpreview` for this same example
as a complete, runnable command-line program.

## Core concepts

- **`Document`** represents one opened PDF file. Opening a document
  parses its structure (header, cross-reference table, trailer, page
  tree) but does not render anything yet - rendering is lazy, per page.
- **`Page`** represents one page of an open `Document`, obtained via
  `Document.Page(index)`. A `Page` is a thin, read-only view; all of its
  methods can be called as many times as you like.
- **Page indexing is zero-based**, matching Go slice conventions:
  `doc.Page(0)` is the document's first page, and valid indices run from
  `0` to `doc.PageCount()-1`.
- **Ownership**: a `Page` must not be used after the `Document` it came
  from has been closed (`Document.Close`). A `Document` and its `Page`s
  are otherwise ordinary Go values with no special lifetime rules beyond
  that.
- **Units**: page geometry (`Page.Bounds`) is expressed in PDF points
  (1/72 inch), matching how PDF itself describes page size. Rendered
  image dimensions are pixels, controlled by `RenderOptions.Scale` (see
  [Rendering a page](#rendering-a-page)).

## Opening a document

```go
func Open(r io.ReaderAt, size int64, opts ...OpenOption) (*Document, error)
func OpenFile(name string, opts ...OpenOption) (*Document, error)
```

`OpenFile` is the convenient entry point for reading a named file from
disk: it opens the file itself and arranges for `Document.Close` to
close it again.

`Open` is the more general entry point: it accepts any `io.ReaderAt` plus
the total size of the data behind it, so a document can come from
anywhere that satisfies that interface - an already-open `*os.File`, an
`io.SectionReader` into a larger blob, an `*bytes.Reader` over data
fetched some other way, and so on. `Open` never closes `r`, since it did
not open it and has no way to know whether the caller still needs it -
that remains the caller's responsibility.

```go
data, err := fetchPDFBytes(ctx, someID) // however your program gets the bytes
if err != nil {
	return err
}
doc, err := pdfviewer.Open(bytes.NewReader(data), int64(len(data)))
if err != nil {
	return err
}
defer doc.Close()
```

`opts ...OpenOption` is an extension point for open-time configuration.
Most calls omit it entirely, as in the examples above; the values
currently available are `WithDiagnostics` (see
[Diagnostics](#diagnostics), directly below), `WithFontSubstitution`
(see [Font substitution](#font-substitution)), and `WithPassword` (see
[Error handling](#error-handling)'s `ErrEncrypted` entry) for a document
protected by a non-empty password.

Always call `Close` on a successfully opened `Document` once you are
done with it (typically via `defer`) - see
[Concurrency](#concurrency) for what `Close` does and does not
guarantee when other goroutines might be involved.

## Diagnostics

```go
func WithDiagnostics(d *Diagnostics) OpenOption

type Diagnostics struct{ /* ... */ }
func (d *Diagnostics) Messages() []string
```

By default, this package tolerates a lot silently: an unsupported font
program that falls back to the placeholder box, an unresolvable
resource name, a malformed field it substituted a default for, and
similar recoverable situations are simply not surfaced anywhere - only
content this package cannot tolerate *at all* causes `Render` or
`Thumbnail` to return an error (see [Error handling](#error-handling)).

`WithDiagnostics` opts a `Document` into recording each of those
tolerated situations as a human-readable message, in the order they
occur, for later inspection - a debugging aid, not a substitute for the
error return:

```go
diag := &pdfviewer.Diagnostics{}
doc, err := pdfviewer.OpenFile("input.pdf", pdfviewer.WithDiagnostics(diag))
if err != nil {
	log.Fatal(err)
}
defer doc.Close()

page, err := doc.Page(0)
if err != nil {
	log.Fatal(err)
}
img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
if err != nil {
	log.Fatal(err)
}

for _, msg := range diag.Messages() {
	fmt.Println(msg)
}
```

A single `Diagnostics` is meant for one `Document` at a time - attaching
it to a second `Document` does not clear what the first already
recorded, and both go on appending to the same collection. `Messages`
is safe to call at any time, including before any page has been
rendered (an empty slice, not `nil`, if nothing has been recorded yet).
Without this option, none of this bookkeeping happens at all: this
package behaves exactly as it always has. See `cmd/pdfpreview
-diagnostics` for this option wired up in a runnable example.

## Font substitution

```go
func WithFontSubstitution(cfg FontSubstitution) OpenOption

type FontSubstitution struct {
	Directories           []string
	DisableSystemDefaults bool
}
```

By default, a `Document` never reads anything outside the PDF file
itself: a font with no usable embedded glyph program (no `/FontFile`,
`/FontFile2`, or `/FontFile3`, or one that fails to parse) renders every
glyph of that font as a small hollow placeholder box sized to the
glyph's own advance width.

Passing `WithFontSubstitution` opts a `Document` into finding a real
substitute outline for such fonts from a font file on disk, matched
against the PDF font's own declared family/weight/style:

```go
doc, err := pdfviewer.OpenFile("input.pdf",
	pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{}),
)
```

`FontSubstitution{}` (the zero value) scans this package's own
GOOS-gated list of common per-platform font directories.
`Directories` adds specific paths to search first, ahead of those
defaults (each scanned non-recursively - list every directory that
should actually be searched if candidate fonts are nested);
`DisableSystemDefaults` turns off the built-in platform list entirely,
leaving only `Directories`:

```go
doc, err := pdfviewer.OpenFile("input.pdf", pdfviewer.WithFontSubstitution(pdfviewer.FontSubstitution{
	Directories:           []string{"/opt/company-fonts"},
	DisableSystemDefaults: true, // only ever search /opt/company-fonts
}))
```

Substitution never invokes a platform font service (Core Text,
DirectWrite, fontconfig, or similar) - it only reads ordinary
`.ttf`/`.ttc`/`.otf` files via `os.ReadDir`/`os.ReadFile`, the same
approach this package already uses to read the PDF file itself. It is
purely additive: a font with a usable embedded program is completely
unaffected, and a font substitution finds no candidate for still falls
back to the placeholder box exactly as it would without this option.

**Scope**: substitution currently applies only to simple fonts (Type 1,
TrueType, MMType1), not to Type 0/CID fonts. Matching a font's
character code to a candidate's own glyph requires resolving that code
to a Unicode rune, which is only possible from a simple font's
`/Encoding`; a Type 0 font's CIDs have no known Unicode meaning without
a `/ToUnicode` CMap, which this package does not currently parse. A
non-embedded Type 0 font falls back to the placeholder box regardless of
this option. See [capability-matrix.md](capability-matrix.md)'s "Font
substitution" row for the exact current status, and `cmd/pdfpreview
-substitute-fonts` for this option wired up in a runnable example.

## Inspecting a document

```go
func (d *Document) PageCount() int
func (d *Document) Page(index int) (Page, error)

type Page interface {
	Bounds() Rect
	// Render and Thumbnail: see below
}

type Rect struct {
	LLX, LLY, URX, URY float64
}
func (r Rect) Width() float64  // URX - LLX
func (r Rect) Height() float64 // URY - LLY
```

`PageCount` remains valid to call even after `Close` (the count is
determined once, up front, and is not itself a resource `Close`
releases) - `Page` is not: it returns `ErrClosed` once the document has
been closed.

`Page.Bounds` returns the page's box in PDF points: its `/CropBox` (with
PDF's page-attribute inheritance already resolved if the page itself
doesn't specify one directly, and clipped to lie within `/MediaBox`), or
`/MediaBox` itself if the page has no `/CropBox` anywhere in its
ancestry. This is deliberately `/CropBox` rather than `/MediaBox`: PDF
producers commonly set `/MediaBox` to a whole physical press sheet (crop
marks, bleed, and other margin content included) and `/CropBox` to the
smaller region actually meant to be seen, and every mainstream PDF
viewer displays that smaller region - `Bounds`, `Render`, and
`Thumbnail` all agree on this same box. `Rect`'s field names follow
PDF's own convention (`LLX`/`LLY` lower-left, `URX`/`URY` upper-right);
PDF does not guarantee the lower-left corner is actually the smaller
corner, so `Width`/`Height` can be negative for an unusual file - take
their absolute value if you need a normalized non-negative size.

```go
for i := 0; i < doc.PageCount(); i++ {
	page, err := doc.Page(i)
	if err != nil {
		return err
	}
	b := page.Bounds()
	fmt.Printf("page %d: %.0fx%.0f pt\n", i, b.Width(), b.Height())
}
```

## Rendering a page

```go
func (p Page) Render(ctx context.Context, opts RenderOptions) (image.Image, error)

type RenderOptions struct {
	Scale           float64     // device pixels per PDF point; 0 means 1.0
	Background      color.Color // painted behind the page; nil means opaque white
	HideAnnotations bool        // suppress annotation appearances; default false (shown)
}
```

`Render` interprets the page's content and rasterizes it into an image,
honoring `RenderOptions`:

- **`Scale`** is device pixels per PDF point. The zero value means the
  default, `1.0`, so a page's rendered pixel dimensions exactly match
  `Page.Bounds`' dimensions in points (before `/Rotate` is applied - a
  90 or 270 degree rotated page swaps width and height in the output).
  To render at a specific DPI, compute `Scale` as `dpi/72` - for example,
  `RenderOptions{Scale: 300.0 / 72.0}` for 300 DPI.
- **`Background`** is the solid color shown wherever the page's own
  content doesn't fully cover the image (most pages don't paint their
  own full-page background). `nil` means opaque white, matching typical
  PDF viewer behavior. The rendered image is always fully opaque - only
  full-coverage opacity exists, not a transparent output.
- **`HideAnnotations`**, when true, suppresses painting annotation
  appearances (highlights, stamps, form field widgets, and similar) that
  are shown by default (`false`), matching how most PDF viewers render a
  page.

```go
img, err := page.Render(ctx, pdfviewer.RenderOptions{
	Scale:      300.0 / 72.0, // 300 DPI
	Background: color.White,
})
```

A page that uses only a feature this package doesn't implement still
renders (as whatever it can paint, on the requested background) rather
than failing outright; `Render` only returns an error for content this
package can positively detect would otherwise silently produce a
materially wrong image, or for content that is malformed PDF syntax. See
[What gets rendered](#what-gets-rendered).

## Thumbnails

```go
func (p Page) Thumbnail(ctx context.Context, opts ThumbnailOptions) (image.Image, error)

type ThumbnailOptions struct {
	MaxDimension    int         // longer side, in pixels; 0 means 256
	Background      color.Color // same as RenderOptions.Background
	HideAnnotations bool        // same as RenderOptions.HideAnnotations
}
```

`Thumbnail` renders the exact same page content `Render` does - it is
not a second, separate interpretation of the PDF - just scaled so the
longer of the page's two (possibly `/Rotate`-swapped) pixel dimensions
fits within `MaxDimension`, preserving aspect ratio. The zero value for
`MaxDimension` means the default, 256 pixels - a typical thumbnail size
for a file browser or a page-list sidebar.

```go
thumb, err := page.Thumbnail(ctx, pdfviewer.ThumbnailOptions{MaxDimension: 128})
```

See `cmd/pdfthumbnails` for a complete program that writes a thumbnail
for every page of a document, rendering pages concurrently (see
[Concurrency](#concurrency)).

## Error handling

Every error this package returns classifies as exactly one of five
sentinel values, meant to be checked with the standard library's
`errors.Is`, never by comparing error message strings:

| Sentinel | Meaning |
| --- | --- |
| `pdfviewer.ErrMalformed` | The input is not well-formed PDF (a bad cross-reference table, a truncated stream, a syntax error, ...) - a property of the bytes given to this package. |
| `pdfviewer.ErrUnsupported` | The input is well-formed PDF but uses a feature this package doesn't implement (see [What gets rendered](#what-gets-rendered)). |
| `pdfviewer.ErrEncrypted` | The document's trailer declares an `/Encrypt` dictionary that this package could not open. A document encrypted with the Standard security handler now opens and decrypts transparently whether its user password is empty (the common "permissions-only, opens freely" case - many bank statements, invoices, and print-to-PDF output - no option needed) or non-empty (supply the correct one via `WithPassword`); `ErrEncrypted` is returned when the password supplied (or, if none was, the empty string that is tried by default) does not validate, or when the document uses a security handler other than Standard (public-key handlers, no known demand). A wrong password and a missing one are distinguished only in the error's message text, not by a separate sentinel - check the message if your application needs to show a different message for "this file needs a password" versus "that password was wrong". `ErrEncrypted` always also satisfies `errors.Is(err, pdfviewer.ErrUnsupported)` - it is a specific case of that broader category - so existing code that only checks for `ErrUnsupported` keeps working. |
| `pdfviewer.ErrClosed` | A method was called on a `Document` (or a `Page` obtained from one) after `Close` had already been called on that `Document`. |
| `pdfviewer.ErrPageIndex` | `Document.Page` was called with a negative index, or one greater than or equal to `PageCount()`. |

```go
doc, err := pdfviewer.OpenFile(path)
switch {
case errors.Is(err, pdfviewer.ErrEncrypted):
	// This file needs the correct password - retry with
	// pdfviewer.WithPassword(thePassword), prompting the user for one
	// first if you don't already have it. Note: ErrEncrypted also
	// matches ErrUnsupported below, so check for it first if you want
	// to react to it specifically.
case errors.Is(err, pdfviewer.ErrMalformed):
	// The file itself is broken or corrupt.
case errors.Is(err, pdfviewer.ErrUnsupported):
	// A well-formed PDF using a feature this package doesn't render.
case err != nil:
	// Something else went wrong (the file doesn't exist, a read
	// error, ...).
default:
	defer doc.Close()
	// doc opened successfully.
}
```

`Render` and `Thumbnail` can return `ErrUnsupported` for the same
reason, or `ErrClosed` if the document was closed first; `Document.Page`
can return `ErrClosed` or `ErrPageIndex`.

Two helper functions, `pdfviewer.MalformedErrorf` and
`pdfviewer.UnsupportedErrorf`, build errors that wrap `ErrMalformed`/
`ErrUnsupported` with a formatted message. Most callers using this
package as a library will never need them; they exist for code that
wants to produce errors compatible with this package's own
`errors.Is`-based conventions (for example, a caller layering its own
validation on top of a successfully opened document).

## Cancellation

`Page.Render` and `Page.Thumbnail` both take a `context.Context` and
check `ctx.Err()` between pipeline stages (before parsing the page's
content stream, after parsing, after interpreting it, and before
rasterizing). `context.Background()` is the right choice for a
short-lived command-line tool that runs to completion; a longer-lived
program - a server handling many requests, or a GUI viewer where the
user might navigate away mid-render - should pass a context tied to that
request or that navigation instead, so a render already in flight for
something no one is waiting on can stop promptly:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

img, err := page.Render(ctx, pdfviewer.RenderOptions{})
if errors.Is(err, context.DeadlineExceeded) {
	// rendering did not finish in time
}
```

Cancellation is checked between pipeline stages, not within one (for
example, not partway through interpreting one very long content stream),
since every stage's worst-case work is already bounded independently by
internal limits - a canceled context is honored promptly in practice
without needing finer-grained checks.

## Concurrency

**A `*Document`, and any `Page` obtained from it, is not safe for
concurrent use by multiple goroutines.** Calling any method on the same
`Document` - `Page`, `PageCount`, `Page.Render`, `Page.Thumbnail`, or
`Close` - from more than one goroutine at a time, without your own
synchronization, is a data race.

This is a deliberate design choice (see PLAN.md's Phase 6 entry for the
full rationale), not a temporary limitation, so plan around it rather
than expecting it to change. A program that wants to render pages in
parallel has two supported options:

1. **Open a separate `*Document` per goroutine.** Independently opened
   Documents share no state with each other at all, so this needs no
   locking of any kind. Opening the same file again is cheap relative to
   rendering (it only re-parses the file's structure, not page content),
   which makes a small worker pool - each worker owning its own
   `*Document` for the same input file - a practical pattern for
   rendering many pages of one document concurrently:

   ```go
   func renderAllThumbnails(path string, maxDim int) ([]image.Image, error) {
   	doc, err := pdfviewer.OpenFile(path)
   	if err != nil {
   		return nil, err
   	}
   	n := doc.PageCount()
   	doc.Close() // only needed to learn the page count

   	results := make([]image.Image, n)
   	errs := make([]error, n)
   	pages := make(chan int, n)
   	for i := 0; i < n; i++ {
   		pages <- i
   	}
   	close(pages)

   	var wg sync.WaitGroup
   	for w := 0; w < runtime.NumCPU(); w++ {
   		wg.Add(1)
   		go func() {
   			defer wg.Done()
   			doc, err := pdfviewer.OpenFile(path) // one Document per worker
   			if err != nil {
   				return
   			}
   			defer doc.Close()
   			for i := range pages {
   				page, err := doc.Page(i)
   				if err != nil {
   					errs[i] = err
   					continue
   				}
   				results[i], errs[i] = page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{MaxDimension: maxDim})
   			}
   		}()
   	}
   	wg.Wait()
   	// ... check errs ...
   	return results, nil
   }
   ```

   See `cmd/pdfthumbnails` for this exact pattern as a complete, runnable,
   race-detector-tested program.

2. **Serialize access to one shared `Document` yourself** (your own
   `sync.Mutex` around every call into it), if you specifically want to
   avoid the memory and file-descriptor cost of opening the file more
   than once. This gives up the parallelism option 1 provides.

## What gets rendered

This package renders vector graphics (paths, fills, strokes, clipping),
images (referenced and inline, including JPEG, CCITT Group 3/4 fax,
image masks, and soft masks), text with embedded TrueType and
OpenType/CFF outlines plus Identity-encoded CID fonts, transparency
(constant alpha and separable blend modes), shading and tiling
patterns, Form XObjects, and annotation appearance streams.

It implements the Standard PDF security handler, both the empty- and
non-empty-user-password cases (RC4 and AES, revisions 2-6; the latter
via the `WithPassword` `OpenOption` - see [Error handling](#error-handling)'s
`ErrEncrypted` entry), but not recovering a user password from a
supplied owner password, nor any public-key security handler. It also
does not implement querying a system font service for a non-embedded
font (opt in to
finding a substitute outline from files on disk instead - see
[Font substitution](#font-substitution) - or a missing/unsupported font
falls back to a small placeholder box), transparency group isolation,
or a handful of narrower, explicitly out-of-scope features (Type 1's
own charstring outline format, non-Identity CID encodings,
JBIG2/JPEG2000 images, true ICC color management, and others).

For the complete, current, row-by-row breakdown of exactly what is and
is not supported - which this guide deliberately does not duplicate, so
it cannot silently go stale the way a restated copy would - see
[capability-matrix.md](capability-matrix.md).

## Example programs

The module's `cmd/` directory contains three complete, runnable example
programs, each demonstrating one task this guide covers and each usable
as a starting point for your own code. Clone the repository to run them
directly:

```sh
git clone https://github.com/tucats/pdf-viewer
cd pdf-viewer

# Render one page to a PNG.
go run ./cmd/pdfpreview -page 0 -out preview.png input.pdf

# Render a thumbnail for every page into a directory (concurrently -
# see cmd/pdfthumbnails's own doc comment for the worker-pool pattern
# this uses, matching the Concurrency section above).
go run ./cmd/pdfthumbnails -maxdim 128 -out thumbs/ input.pdf

# Export every page at full resolution as PNG or JPEG.
go run ./cmd/pdfexport -format png -out pages/ input.pdf
```

Or install one as a standalone binary with `go install`:

```sh
go install github.com/tucats/pdf-viewer/cmd/pdfpreview@latest
```

Each program's source is written to double as a Go-language explainer
(flags, `defer`, channels, worker pools, `io.Writer`) as well as an
example of this package's API - see each `main.go`'s own doc comment.

## Stability

This package has not yet been tagged with a version - see PLAN.md's
Phase 6 entry for what that decision depends on. The public API
described in this guide (`Open`, `OpenFile`, `Document`, `Page`,
`OpenOption` and its values (`WithDiagnostics`, `WithFontSubstitution`,
`WithPassword`),
`RenderOptions`, `ThumbnailOptions`, `Rect`, and the sentinel errors) is
considered stable in shape following the Phase 6 API-stabilization
review, but until an actual release is tagged, treat it the way you
would any pre-1.0 Go module: pin a specific commit if you need to
guarantee nothing changes under you.
