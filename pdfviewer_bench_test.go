package pdfviewer_test

import (
	"context"
	"testing"

	pdfviewer "github.com/tucats/pdf-viewer"
)

// This file is Phase 5's "benchmark results for single-page, multi-page,
// and thumbnail workloads" exit criterion (see the repository README's
// Phase 5 entry): each Benchmark below opens a fixture once, then times
// only the repeated Render/Thumbnail calls (b.ResetTimer excludes the
// one-time Open cost, which is not what these benchmarks are meant to
// characterize). Run with:
//
//	go test . -bench . -benchmem -run '^$'
//
// -run '^$' skips the ordinary tests so only the benchmarks execute;
// -benchmem additionally reports allocations per operation, useful for
// spotting an accidental per-render allocation regression in a future
// change (this project's own "cache decoded resources" phase task -
// see the README's Phase 5 entry - is deliberately deferred to Phase 6,
// once that phase decides Page.Render's concurrency guarantees, since a
// cross-render cache's correctness depends on exactly that undecided
// policy; these benchmarks exist now specifically so a future caching
// change has a baseline to measure against).

// BenchmarkRenderSinglePage times repeated Render calls against one
// simple, single-page, vector-only fixture - the cheapest realistic
// workload, and the baseline every other benchmark in this file should
// be compared relative to.
func BenchmarkRenderSinglePage(b *testing.B) {
	doc, err := pdfviewer.OpenFile(fixturePath("filled-rect.pdf"))
	if err != nil {
		b.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		b.Fatalf("Page(0): %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := page.Render(context.Background(), pdfviewer.RenderOptions{}); err != nil {
			b.Fatalf("Render: %v", err)
		}
	}
}

// BenchmarkRenderComplexPage times repeated Render calls against a page
// exercising several of this project's more expensive Phase 5 code
// paths together in one render - a shading gradient, a tiling pattern,
// and constant alpha - giving a more representative "realistic,
// feature-rich page" data point alongside BenchmarkRenderSinglePage's
// deliberately minimal one.
func BenchmarkRenderComplexPage(b *testing.B) {
	doc, err := pdfviewer.OpenFile(fixturePath("tiling-pattern-fill.pdf"))
	if err != nil {
		b.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		b.Fatalf("Page(0): %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := page.Render(context.Background(), pdfviewer.RenderOptions{}); err != nil {
			b.Fatalf("Render: %v", err)
		}
	}
}

// BenchmarkRenderMultiPage times rendering every page of a multi-page
// document in sequence, once per b.N iteration - representative of a
// caller that renders (or re-renders) an entire document's worth of
// pages, as opposed to the single-page benchmarks above.
func BenchmarkRenderMultiPage(b *testing.B) {
	doc, err := pdfviewer.OpenFile(fixturePath("two-pages.pdf"))
	if err != nil {
		b.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	count := doc.PageCount()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for p := 0; p < count; p++ {
			page, err := doc.Page(p)
			if err != nil {
				b.Fatalf("Page(%d): %v", p, err)
			}
			if _, err := page.Render(context.Background(), pdfviewer.RenderOptions{}); err != nil {
				b.Fatalf("Render(%d): %v", p, err)
			}
		}
	}
}

// BenchmarkThumbnail times repeated Thumbnail calls at the default
// MaxDimension - Thumbnail shares Render's full content-stream
// interpretation (see page.go's renderAtScale), so this benchmark's
// main point of comparison against BenchmarkRenderSinglePage is the
// cost difference from rasterizing at a smaller pixel size rather than
// from interpreting different content.
func BenchmarkThumbnail(b *testing.B) {
	doc, err := pdfviewer.OpenFile(fixturePath("filled-rect.pdf"))
	if err != nil {
		b.Fatalf("OpenFile: %v", err)
	}
	defer doc.Close()
	page, err := doc.Page(0)
	if err != nil {
		b.Fatalf("Page(0): %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := page.Thumbnail(context.Background(), pdfviewer.ThumbnailOptions{}); err != nil {
			b.Fatalf("Thumbnail: %v", err)
		}
	}
}
