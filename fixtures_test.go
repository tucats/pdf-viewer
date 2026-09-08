package pdfviewer_test

import (
	"os"
	"path/filepath"
	"testing"
)

// handmadeFixturesDir is where tools/genfixtures writes the hand-authored
// PDF fixtures used across this module's tests. See
// testdata/fixtures/FIXTURES.md for what each file is for and how to
// regenerate them.
const handmadeFixturesDir = "testdata/fixtures/handmade"

// TestHandmadeFixturesArePresentAndWellFormedAtByteLevel is a Phase 0
// sanity check: it does not parse PDF structure (there is no parser yet
// — that starts in Phase 1) but it does confirm the fixture corpus
// described in testdata/fixtures/FIXTURES.md actually exists on disk and
// that each file at least starts with a PDF header, which is the one
// thing every fixture - including the deliberately malformed and
// truncated ones - must have in common, since a missing header would
// mean the file isn't recognizable as a PDF at all rather than testing
// the specific structural problem it's meant to test.
//
// Once internal/parser exists, this test should be superseded by (not
// necessarily replaced by - a byte-level smoke test is still cheap
// insurance) real parsing tests that open each fixture and assert on its
// page count, page boxes, and so on.
func TestHandmadeFixturesArePresentAndWellFormedAtByteLevel(t *testing.T) {
	entries, err := os.ReadDir(handmadeFixturesDir)
	if err != nil {
		t.Fatalf("reading %s: %v", handmadeFixturesDir, err)
	}

	var pdfCount int
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".pdf" {
			continue
		}
		pdfCount++

		path := filepath.Join(handmadeFixturesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading fixture %s: %v", path, err)
			continue
		}

		if len(data) == 0 {
			t.Errorf("fixture %s is empty", path)
			continue
		}

		const header = "%PDF-"
		if len(data) < len(header) || string(data[:len(header)]) != header {
			t.Errorf("fixture %s does not start with %q", path, header)
		}
	}

	if pdfCount == 0 {
		t.Fatalf("no .pdf files found in %s; run `go run ./tools/genfixtures`", handmadeFixturesDir)
	}
}
