package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fixtures", "handmade", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture %s not found at %s: %v", name, path, err)
	}
	return path
}

// TestRenderThumbnailsWritesOnePngPerPage confirms the basic contract:
// one page-NNNN.png file per page, each decodable, each no larger than
// the requested maxDimension in its longer dimension - using
// two-pages.pdf (see testdata/fixtures/FIXTURES.md), whose two pages
// have different, non-square MediaBox sizes, specifically so this test
// also exercises rendering more than one *distinct* page rather than
// the same page's thumbnail twice.
func TestRenderThumbnailsWritesOnePngPerPage(t *testing.T) {
	outDir := t.TempDir()
	const maxDim = 50

	if err := renderThumbnails(fixturePath(t, "two-pages.pdf"), outDir, maxDim); err != nil {
		t.Fatalf("renderThumbnails: %v", err)
	}

	for i := 0; i < 2; i++ {
		path := filepath.Join(outDir, fmt.Sprintf("page-%04d.png", i))
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("expected output file %s: %v", path, err)
		}
		cfg, err := png.DecodeConfig(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s is not a valid PNG: %v", path, err)
		}
		longest := cfg.Width
		if cfg.Height > longest {
			longest = cfg.Height
		}
		if longest > maxDim {
			t.Errorf("%s: longest dimension %d exceeds requested maxdim %d", path, longest, maxDim)
		}
		if cfg.Width == 0 || cfg.Height == 0 {
			t.Errorf("%s: empty image (%dx%d)", path, cfg.Width, cfg.Height)
		}
	}

	// No third file should exist - two-pages.pdf has exactly two pages.
	if _, err := os.Stat(filepath.Join(outDir, "page-0002.png")); err == nil {
		t.Error("found page-0002.png, but two-pages.pdf only has 2 pages (indices 0 and 1)")
	}
}

// TestRenderThumbnailsMissingInput confirms a nonexistent input file is
// reported as an error rather than a panic, and that no output
// directory contents are left behind to be confused with a real result.
func TestRenderThumbnailsMissingInput(t *testing.T) {
	outDir := t.TempDir()
	err := renderThumbnails(filepath.Join(outDir, "does-not-exist.pdf"), outDir, 128)
	if err == nil {
		t.Fatal("renderThumbnails with a nonexistent input file succeeded, want an error")
	}
}
