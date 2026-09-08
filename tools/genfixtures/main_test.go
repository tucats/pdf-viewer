package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedFixturesMatchCheckedInFiles regenerates every fixture in
// memory (by calling the same buildXxx functions main() calls) and
// compares the result byte-for-byte against what is actually checked
// into testdata/fixtures/handmade.
//
// Why this test exists: the whole point of generating fixtures with Go
// code (see the package doc comment in main.go) instead of hand-editing
// PDF bytes is that the checked-in files are supposed to always be
// exactly what the generator produces. Without this test, someone could
// change a buildXxx function without re-running `go run ./tools/genfixtures`,
// and the checked-in .pdf files would silently drift out of sync with
// the source that supposedly produces them - defeating the entire
// reproducibility point. This test fails loudly in that situation and
// tells the developer to re-run the generator.
func TestGeneratedFixturesMatchCheckedInFiles(t *testing.T) {
	fixtures := map[string][]byte{
		"minimal-blank-page.pdf":        buildMinimalBlankPage(),
		"two-pages.pdf":                 buildTwoPages(),
		"incremental-update.pdf":        buildIncrementalUpdate(),
		"malformed-bad-xref-offset.pdf": buildMalformedBadXrefOffset(),
		"truncated.pdf":                 buildTruncated(),
		"xref-stream.pdf":               buildXrefStream(),
		"object-stream.pdf":             buildObjectStream(),
		"filled-rect.pdf":               buildFilledRect(),
		"stroked-line.pdf":              buildStrokedLine(),
		"clipped-rect.pdf":              buildClippedRect(),
		"transformed-rect.pdf":          buildTransformedRect(),
		"flate-content-rect.pdf":        buildFlateContentRect(),
		"image-rgb.pdf":                 buildImageRGB(),
		"image-mask.pdf":                buildImageMask(),
		"image-smask.pdf":               buildImageSMask(),
		"image-jpeg.pdf":                buildImageJPEG(),
		"inline-image.pdf":              buildInlineImage(),
		"rotated-page.pdf":              buildRotatedPage(),
	}

	for name, want := range fixtures {
		name, want := name, want
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(outputDir, name)
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading checked-in fixture %s: %v (did you forget to run `go run ./tools/genfixtures`?)", path, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("checked-in %s does not match what the generator currently produces; run `go run ./tools/genfixtures` and commit the result", path)
			}
		})
	}
}

// TestFixtureSetIsComplete makes sure every file actually present in
// testdata/fixtures/handmade is one this test (and therefore main.go's
// fixtures list) knows about, catching the opposite drift: a fixture
// file added or renamed without updating main.go and this test to match.
func TestFixtureSetIsComplete(t *testing.T) {
	known := map[string]bool{
		"minimal-blank-page.pdf":        true,
		"two-pages.pdf":                 true,
		"incremental-update.pdf":        true,
		"malformed-bad-xref-offset.pdf": true,
		"truncated.pdf":                 true,
		"xref-stream.pdf":               true,
		"object-stream.pdf":             true,
		"filled-rect.pdf":               true,
		"stroked-line.pdf":              true,
		"clipped-rect.pdf":              true,
		"transformed-rect.pdf":          true,
		"flate-content-rect.pdf":        true,
		"image-rgb.pdf":                 true,
		"image-mask.pdf":                true,
		"image-smask.pdf":               true,
		"image-jpeg.pdf":                true,
		"inline-image.pdf":              true,
		"rotated-page.pdf":              true,
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("reading %s: %v", outputDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pdf" {
			continue
		}
		if !known[e.Name()] {
			t.Errorf("found fixture file %s with no corresponding entry in this test (and likely none in main.go's fixtures list)", e.Name())
		}
	}
}
