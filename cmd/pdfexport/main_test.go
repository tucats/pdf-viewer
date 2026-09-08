package main

import (
	"fmt"
	"image/jpeg"
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

// TestExportPagesPNG confirms the default (PNG) path: one page-NNNN.png
// per page, full resolution (not thumbnail-bounded), matching
// two-pages.pdf's two distinct MediaBox sizes exactly at scale 1.0.
func TestExportPagesPNG(t *testing.T) {
	outDir := t.TempDir()
	if err := exportPages(fixturePath(t, "two-pages.pdf"), outDir, "png", 1.0); err != nil {
		t.Fatalf("exportPages: %v", err)
	}

	wantDims := [][2]int{{100, 200}, {300, 400}}
	for i, want := range wantDims {
		path := filepath.Join(outDir, pageFileName(i, "png"))
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("expected output file %s: %v", path, err)
		}
		cfg, err := png.DecodeConfig(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s is not a valid PNG: %v", path, err)
		}
		if cfg.Width != want[0] || cfg.Height != want[1] {
			t.Errorf("page %d: got %dx%d, want %dx%d (full resolution, not thumbnail-bounded)", i, cfg.Width, cfg.Height, want[0], want[1])
		}
	}
}

// TestExportPagesJPEG confirms the -format jpeg path uses the .jpg
// extension and produces a file image/jpeg can actually decode -
// exercising encoderFor's second branch, which TestExportPagesPNG above
// does not reach at all.
func TestExportPagesJPEG(t *testing.T) {
	outDir := t.TempDir()
	if err := exportPages(fixturePath(t, "filled-rect.pdf"), outDir, "jpeg", 1.0); err != nil {
		t.Fatalf("exportPages: %v", err)
	}

	path := filepath.Join(outDir, pageFileName(0, "jpg"))
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("expected output file %s: %v", path, err)
	}
	defer f.Close()
	if _, err := jpeg.Decode(f); err != nil {
		t.Errorf("%s did not decode as JPEG: %v", path, err)
	}
}

// TestExportPagesUnknownFormat confirms an unrecognized -format value is
// rejected up front (before any file is opened or created) rather than
// silently falling back to some default.
func TestExportPagesUnknownFormat(t *testing.T) {
	outDir := t.TempDir()
	err := exportPages(fixturePath(t, "filled-rect.pdf"), outDir, "bmp", 1.0)
	if err == nil {
		t.Fatal("exportPages with -format bmp succeeded, want an error")
	}
}

// pageFileName mirrors exportOnePage's own output naming exactly, so
// this test file has one place to change if that naming ever does.
func pageFileName(pageIndex int, ext string) string {
	return fmt.Sprintf("page-%04d.%s", pageIndex, ext)
}
