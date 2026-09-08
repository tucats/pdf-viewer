package pdfviewer_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// FuzzOpenAndRender is the root package's Phase 2 fuzz target: the
// full, public-API pipeline (Open, then Render every page) exercised
// against arbitrary bytes, extending internal/parser's Phase 1
// FuzzOpenAndResolveAll (which stops at object resolution) all the way
// through content-stream interpretation (internal/content) and
// rasterization (internal/raster). Open and Render must never panic and
// must always return, regardless of how malformed or adversarial the
// input is - the same "bounded work, no panics" property every fuzz
// target in this module checks at its own layer.
//
// The seed corpus is the full hand-authored fixture corpus (see
// testdata/fixtures/FIXTURES.md), which gives the fuzzer a running start
// from inputs that already exercise every structural and rendering
// feature this package implements, rather than having to discover valid
// PDF structure and content stream syntax from nothing.
//
// Run with, for example:
//
//	go test . -fuzz=FuzzOpenAndRender -fuzztime=60s
func FuzzOpenAndRender(f *testing.F) {
	entries, err := os.ReadDir(handmadeFixturesDir)
	if err != nil {
		f.Fatalf("reading %s: %v", handmadeFixturesDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pdf" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(handmadeFixturesDir, e.Name()))
		if err != nil {
			f.Fatalf("reading %s: %v", e.Name(), err)
		}
		f.Add(data)
	}
	f.Add([]byte(""))
	f.Add([]byte("not a pdf at all"))

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := pdfviewer.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		defer doc.Close()

		// Bounded: render at most a handful of pages, regardless of how
		// many the fuzzer-mutated file's page tree claims to have, so
		// one input cannot dominate the fuzzing budget - Render itself
		// (via maxRenderPixels) already bounds the cost of any single
		// page.
		const maxPages = 8
		n := doc.PageCount()
		if n > maxPages {
			n = maxPages
		}
		for i := 0; i < n; i++ {
			page, err := doc.Page(i)
			if err != nil {
				continue
			}
			_, _ = page.Render(context.Background(), pdfviewer.RenderOptions{})
		}
	})
}
