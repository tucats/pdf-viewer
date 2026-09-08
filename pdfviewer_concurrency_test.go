package pdfviewer_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file is the regression test for the concurrency guarantee
// decided in Phase 6 and documented on the Document type (document.go):
// a single *Document is not safe for concurrent use, but a program that
// opens a separate *Document per goroutine gets full concurrency, since
// separate Documents share no state with each other at all.
//
// Run this file with `go test -race` (as this project's CI does - see
// .github/workflows/ci.yml) so that the race detector would actually
// catch a future regression that introduced accidental shared state
// (for example, a package-level cache) between independently opened
// Documents - a bug this test's "each goroutine gets its own Document"
// pattern is specifically designed to expose if it were ever true.

// TestConcurrentRenderAcrossSeparateDocuments opens the same fixture
// file many times over - once per goroutine, each its own independent
// *Document, per the documented pattern - and renders every page of
// each one concurrently, some as a full Render and some as a
// Thumbnail. A failure here (an error, a wrong-sized image, or - most
// importantly, under `go test -race` - a reported data race) would mean
// this project's "separate Document per goroutine" concurrency
// guarantee does not actually hold.
func TestConcurrentRenderAcrossSeparateDocuments(t *testing.T) {
	const goroutines = 8
	path := fixturePath("two-pages.pdf")

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*4)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			doc, err := pdfviewer.OpenFile(path)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: OpenFile: %w", id, err)
				return
			}
			defer doc.Close()

			for pageIndex := 0; pageIndex < doc.PageCount(); pageIndex++ {
				page, err := doc.Page(pageIndex)
				if err != nil {
					errs <- fmt.Errorf("goroutine %d: Page(%d): %w", id, pageIndex, err)
					continue
				}

				img, err := page.Render(context.Background(), pdfviewer.RenderOptions{})
				if err != nil {
					errs <- fmt.Errorf("goroutine %d: Page(%d).Render: %w", id, pageIndex, err)
				} else if b := img.Bounds(); b.Dx() == 0 || b.Dy() == 0 {
					errs <- fmt.Errorf("goroutine %d: Page(%d).Render produced an empty image", id, pageIndex)
				}

				thumb, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{})
				if err != nil {
					errs <- fmt.Errorf("goroutine %d: Page(%d).Thumbnail: %w", id, pageIndex, err)
				} else if b := thumb.Bounds(); b.Dx() == 0 || b.Dy() == 0 {
					errs <- fmt.Errorf("goroutine %d: Page(%d).Thumbnail produced an empty image", id, pageIndex)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
