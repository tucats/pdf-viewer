package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// fixturePath resolves a fixture file name to its path in the shared
// testdata/fixtures/handmade corpus (see testdata/fixtures/FIXTURES.md
// at the repository root) - reused by every fixture-driven test in this
// project, but reimplemented here (rather than imported) since this is
// a separate `main` package (a command, not a library) with its own,
// unrelated module-relative path back to the repository root.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fixtures", "handmade", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fixture %s not found at %s (did testdata/fixtures/handmade move?): %v", name, path, err)
	}
	return path
}

// TestRenderPreviewToFile is a straightforward "does the whole pipeline
// work" test: render a known-good fixture to a temporary PNG file and
// confirm the result decodes back as a PNG with the pixel dimensions
// this fixture's own 100x100-point MediaBox implies at the default
// scale (1 device pixel per PDF point - see pdfviewer.RenderOptions.
// Scale's doc comment).
func TestRenderPreviewToFile(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "preview.png")

	if err := renderPreviewToFile(fixturePath(t, "filled-rect.pdf"), 0, 1.0, outPath); err != nil {
		t.Fatalf("renderPreviewToFile: %v", err)
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("opening output file: %v", err)
	}
	defer f.Close()

	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("output file is not a valid PNG: %v", err)
	}
	if cfg.Width != 100 || cfg.Height != 100 {
		t.Errorf("output PNG is %dx%d, want 100x100 (filled-rect.pdf's MediaBox at scale 1.0)", cfg.Width, cfg.Height)
	}
}

// TestRenderPreviewToFileScale confirms the -scale flag (renderPreviewToFile's
// scale parameter) actually changes the rendered pixel dimensions, not
// just something cosmetic - at scale 2.0 a 100x100-point page should
// render at 200x200 pixels.
func TestRenderPreviewToFileScale(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "preview.png")

	if err := renderPreviewToFile(fixturePath(t, "filled-rect.pdf"), 0, 2.0, outPath); err != nil {
		t.Fatalf("renderPreviewToFile: %v", err)
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("opening output file: %v", err)
	}
	defer f.Close()

	cfg, err := png.DecodeConfig(f)
	if err != nil {
		t.Fatalf("output file is not a valid PNG: %v", err)
	}
	if cfg.Width != 200 || cfg.Height != 200 {
		t.Errorf("output PNG is %dx%d, want 200x200 (filled-rect.pdf's MediaBox at scale 2.0)", cfg.Width, cfg.Height)
	}
}

// TestRenderPreviewToFilePageOutOfRange confirms a bad page index is
// reported as an error (wrapping pdfviewer.ErrPageIndex, though this
// test only checks that some error comes back) rather than a crash -
// exactly the kind of caller mistake (a stale page count, an off-by-one)
// a real embedder's own error handling depends on this returning
// cleanly.
func TestRenderPreviewToFilePageOutOfRange(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "preview.png")

	err := renderPreviewToFile(fixturePath(t, "filled-rect.pdf"), 5, 1.0, outPath)
	if err == nil {
		t.Fatal("renderPreviewToFile with an out-of-range page index succeeded, want an error")
	}
}

// TestRenderPreviewToFileMissingInput confirms a nonexistent input path
// is reported as an error rather than a panic.
func TestRenderPreviewToFileMissingInput(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "preview.png")

	err := renderPreviewToFile(filepath.Join(t.TempDir(), "does-not-exist.pdf"), 0, 1.0, outPath)
	if err == nil {
		t.Fatal("renderPreviewToFile with a nonexistent input file succeeded, want an error")
	}
}
