# pdf-viewer

An embeddable PDF renderer, written entirely in Go.

If you have ever needed to turn a PDF page into an image from inside a Go
program, you already know the usual answer: shell out to a system tool,
wrap a native library like MuPDF or PDFium through CGO, or pull in a
license-gated commercial SDK. Any of those choices drags a piece of your
build and deployment story outside of Go — a binary to install, a library
to link, a subprocess to sandbox, a license key to manage.

`pdf-viewer` takes a different bet: the whole renderer lives in this
module. `go get` it, `import` it, and you have a working PDF-to-image
pipeline in the same binary as the rest of your program. No CGO, no
`os/exec`, no runtime-loaded native library, no network access or
filesystem writes as a side effect of opening or rendering a document.
Cross-compiling your program cross-compiles this too.

## Why that might matter to you

Ask yourself where a PDF currently has to leave your Go process to become
a picture:

- A document management or intake service that needs a quick preview
  thumbnail the moment a file is uploaded.
- A CLI or desktop tool that wants to show users what's inside a PDF
  without shipping a separate viewer dependency.
- A pipeline running in a locked-down container or serverless environment
  where installing a native PDF library isn't an option — or isn't allowed.
- Anything that treats PDFs as untrusted input and would rather hand a
  malformed or hostile file to a parser with bounded work and typed
  errors than to a subprocess that might hang or crash.

If any of those sound familiar, it's worth a closer look: a page-oriented
`image.Image` API that fits naturally into the tools you already use
(`image/png`, `image/jpeg`, `image/draw`, and so on), with random access
to pages, lazy rendering, and per-document cancellation via
`context.Context`.

## A taste of the API

```go
doc, err := pdfviewer.OpenFile("input.pdf")
if err != nil {
    log.Fatal(err)
}
defer doc.Close()

page, err := doc.Page(0) // pages are zero-indexed
if err != nil {
    log.Fatal(err)
}

img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
if err != nil {
    log.Fatal(err)
}

out, _ := os.Create("page0.png")
defer out.Close()
png.Encode(out, img)
```

That's the whole shape: open a document, ask it how many pages it has,
grab one, render it. `Page.Thumbnail` offers the same interpretation of
the page bounded to a maximum dimension, for when you want a quick
preview rather than a full-resolution render.

## How far it goes

This isn't a toy renderer limited to flat text and lines. Vector
graphics, images, embedded fonts, transparency and blend modes, tiling
and shading patterns, Form XObjects, and annotation appearance streams
are all part of what gets painted onto the page — and where a document
uses something this package can't yet handle, it says so with a typed
error instead of silently producing a wrong picture.

## Where to go next

This file is the pitch; [docs/API.md](docs/API.md) is the reference —
installation, a full quick-start walkthrough, every option on `Open`,
`Render`, and `Thumbnail`, the error taxonomy, and this package's
cancellation and concurrency guarantees. If you want the design
reasoning behind those decisions and a phase-by-phase account of how the
renderer got built, see [docs/PLAN.md](docs/PLAN.md); for a row-by-row
answer to "does this support PDF feature X," see
[docs/capability-matrix.md](docs/capability-matrix.md).

Working example programs live under [cmd/](cmd/) — `pdfpreview`,
`pdfthumbnails`, and `pdfexport` — if you'd rather read runnable code
than prose.
