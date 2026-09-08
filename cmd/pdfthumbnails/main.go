// Command pdfthumbnails is a small, runnable example of embedding the
// pdf-viewer package for a second common viewer feature: a page list
// with a thumbnail image for every page, the way a sidebar in a desktop
// PDF viewer (or a grid of page previews in a web viewer) works. See the
// repository README's Phase 6 entry ("page list with thumbnails") for
// why this program exists.
//
// This example also doubles as a worked demonstration of this project's
// Phase 6 concurrency decision, documented on pdfviewer.Document: one
// *Document is not safe for concurrent use, but a program that wants to
// render many pages in parallel remains free to open a separate
// *Document per goroutine, since independently opened Documents share no
// state. See renderThumbnails below for how a small worker pool puts
// that pattern to use instead of thumbnailing every page one at a time.
//
// # Usage
//
//	go run ./cmd/pdfthumbnails [-maxdim N] -out thumbsdir input.pdf
//
// Each page's thumbnail is written to outdir as page-0000.png,
// page-0001.png, and so on (zero-padded so the files sort in page
// order in a plain directory listing).
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	pdfviewer "github.com/tucats/pdf-viewer"
)

func main() {
	maxDim := flag.Int("maxdim", 256, "maximum thumbnail dimension in pixels (see pdfviewer.ThumbnailOptions.MaxDimension)")
	out := flag.String("out", "", "directory to write page-NNNN.png thumbnail files into (required)")
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 || *out == "" {
		fmt.Fprintf(os.Stderr, "usage: pdfthumbnails [-maxdim N] -out thumbsdir input.pdf\n")
		os.Exit(2)
	}

	if err := renderThumbnails(args[0], *out, *maxDim); err != nil {
		fmt.Fprintf(os.Stderr, "pdfthumbnails: %v\n", err)
		os.Exit(1)
	}
}

// renderThumbnails writes one thumbnail PNG per page of inputPath into
// outDir, using up to runtime.NumCPU() worker goroutines - each with its
// own independently opened *pdfviewer.Document, per the concurrency
// pattern documented on the Document type - to render more than one
// page's thumbnail at a time on a multi-core machine.
//
// # Why a worker pool, and why each worker opens its own Document
//
// A single pdfviewer.Document is documented as unsafe for concurrent
// use (see pdfviewer.Document's doc comment): its internal caches
// (resolved objects, decoded object streams, loaded fonts) are ordinary,
// unsynchronized Go maps. Opening the same file again is cheap relative
// to rendering (it just re-parses the file's structure, not its page
// content), so giving each worker its own Document - rather than trying
// to share one Document across goroutines, which this package does not
// support - is both correct and simple: no locks of any kind are needed
// anywhere in this function.
//
// A worker pool (a small, fixed number of goroutines each pulling page
// indices from a shared channel) is used here rather than "one goroutine
// per page", which would be simpler to write but would also mean
// opening the file - and holding open file descriptors, and the
// resulting parsed structure in memory - once per page, all at once,
// for however many pages the document has. For a document with a
// realistic page count that is a small, bounded amount of duplicated
// work; for a document with an unusually large page count, a worker
// pool bounds both how many file descriptors and how many parsed
// documents ever exist in memory at once to runtime.NumCPU(), which is
// the more defensive default for example code intended to be copied
// into a real program.
func renderThumbnails(inputPath, outDir string, maxDimension int) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory %s: %w", outDir, err)
	}

	// Open the document once up front just to learn how many pages it
	// has, then close it immediately - each worker below opens its own
	// copy for the actual rendering work, per this function's own doc
	// comment above.
	doc, err := pdfviewer.OpenFile(inputPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", inputPath, err)
	}
	pageCount := doc.PageCount()
	doc.Close()

	if pageCount == 0 {
		return nil
	}

	// workerCount is the smaller of "one worker per available CPU" and
	// "one worker per page" - never start more workers than there is
	// work for.
	workerCount := runtime.NumCPU()
	if workerCount > pageCount {
		workerCount = pageCount
	}

	// pages is a buffered channel pre-loaded with every page index; each
	// worker goroutine below reads from it until it is empty (and
	// closed), which is Go's standard "fan out fixed work across a fixed
	// number of workers" pattern. Buffering it to hold every index up
	// front means the sending side (this function) never blocks trying
	// to hand out work, so it can simply close the channel right after
	// the loop that fills it.
	pages := make(chan int, pageCount)
	for i := 0; i < pageCount; i++ {
		pages <- i
	}
	close(pages)

	// errs collects the first error from each worker, if any - a
	// buffered channel sized to the number of workers means every
	// worker can send its result without blocking on a reader, even if
	// this function only ever looks at the first one.
	errs := make(chan error, workerCount)

	var wg sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- thumbnailWorker(inputPath, outDir, maxDimension, pages)
		}()
	}
	// wg.Wait blocks until every worker goroutine above has called
	// wg.Done (via the deferred call), i.e. until all workers have
	// drained the pages channel and finished. Only after that is it
	// safe to close(errs) and range over it below - closing a channel
	// while something might still send on it is itself a bug, which is
	// exactly what waiting for wg.Wait first avoids.
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// thumbnailWorker opens its own *pdfviewer.Document for inputPath and
// renders a thumbnail for every page index it reads from pages, until
// that channel is closed and drained (Go's "for range over a channel"
// loop exits automatically once the channel is both closed and empty).
// It returns the first error encountered, if any, stopping early rather
// than continuing to process further pages once something has already
// failed.
func thumbnailWorker(inputPath, outDir string, maxDimension int, pages <-chan int) error {
	doc, err := pdfviewer.OpenFile(inputPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", inputPath, err)
	}
	defer doc.Close()

	for pageIndex := range pages {
		if err := renderOneThumbnail(doc, pageIndex, outDir, maxDimension); err != nil {
			return err
		}
	}
	return nil
}

// renderOneThumbnail renders pageIndex's thumbnail and writes it to
// outDir/page-NNNN.png.
func renderOneThumbnail(doc *pdfviewer.Document, pageIndex int, outDir string, maxDimension int) error {
	page, err := doc.Page(pageIndex)
	if err != nil {
		return fmt.Errorf("page %d: %w", pageIndex, err)
	}

	img, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{MaxDimension: maxDimension})
	if err != nil {
		return fmt.Errorf("thumbnailing page %d: %w", pageIndex, err)
	}

	outPath := filepath.Join(outDir, fmt.Sprintf("page-%04d.png", pageIndex))
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outPath, err)
	}
	encErr := png.Encode(f, img)
	closeErr := f.Close()
	if encErr != nil {
		return fmt.Errorf("encoding PNG to %s: %w", outPath, encErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing %s: %w", outPath, closeErr)
	}
	return nil
}
