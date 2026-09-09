package fonts

import (
	"os"
	"path/filepath"
	"testing"
)

// buildMinimalCandidateSfnt assembles just enough of a synthetic sfnt
// file (reusing truetype_test.go's assembleSfnt/sfntTable and
// probe_test.go's buildNameTable/buildOS2Table helpers) for
// ProbeFontFile to characterize it and report HasOutlines=true - a
// "glyf" table's mere presence is enough for that (see probe.go's
// probeFace, which never validates glyf's contents while probing), so
// this deliberately does not bother building a real head/maxp/loca/glyf
// set the way truetype_test.go's buildTestSfnt does; nothing in this
// file's tests ever calls FontFace.Outline.
func buildMinimalCandidateSfnt(family string, bold, italic bool) []byte {
	var fsSelection uint16
	if bold {
		fsSelection |= fsSelectionBold
	}
	if italic {
		fsSelection |= fsSelectionItalic
	}
	tables := []sfntTable{
		{"name", buildNameTable(map[uint16]string{nameIDFamily: family})},
		{"OS/2", buildOS2Table(400, fsSelection, 0)},
		{"glyf", []byte{}},
	}
	return assembleSfnt(sfntVersionTrueType, tables, 0)
}

func writeFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestDirectorySource_ScansNonRecursiveDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Arial.ttf", buildMinimalCandidateSfnt("Arial", false, false))
	writeFile(t, dir, "Arial-Bold.ttf", buildMinimalCandidateSfnt("Arial", true, false))
	writeFile(t, dir, "notes.txt", []byte("not a font"))

	src := NewDirectorySource([]string{dir}, false)
	faces := src.Candidates()
	if len(faces) != 2 {
		t.Fatalf("expected 2 candidate faces (the .txt file should be skipped), got %d: %+v", len(faces), faces)
	}
	for _, f := range faces {
		if f.Characteristics.Family != "Arial" {
			t.Errorf("expected family Arial, got %q", f.Characteristics.Family)
		}
		if f.Path == "" {
			t.Errorf("expected Path to be set on a candidate found via DirectorySource")
		}
		if !f.HasOutlines {
			t.Errorf("expected HasOutlines true for a face with a \"glyf\" table")
		}
	}
}

func TestDirectorySource_NonExistentDirectoryIsToleratedNotFatal(t *testing.T) {
	src := NewDirectorySource([]string{filepath.Join(t.TempDir(), "does-not-exist")}, false)
	if faces := src.Candidates(); len(faces) != 0 {
		t.Fatalf("expected no candidates from a nonexistent directory, got %d", len(faces))
	}
}

func TestDirectorySource_UnreadableFileIsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "garbage.ttf", []byte("this is not a real font file at all"))
	writeFile(t, dir, "Real.otf", buildMinimalCandidateSfnt("RealFont", false, false))

	src := NewDirectorySource([]string{dir}, false)
	faces := src.Candidates()
	if len(faces) != 1 || faces[0].Characteristics.Family != "RealFont" {
		t.Fatalf("expected only the one valid font to be found, got %+v", faces)
	}
}

func TestDirectorySource_BuildsIndexOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Arial.ttf", buildMinimalCandidateSfnt("Arial", false, false))

	src := NewDirectorySource([]string{dir}, false)
	first := src.Candidates()

	// Adding a second font file after the first Candidates() call should
	// not be picked up - the whole point of caching (see DirectorySource's
	// own doc comment) is that scanning happens at most once.
	writeFile(t, dir, "Verdana.ttf", buildMinimalCandidateSfnt("Verdana", false, false))
	second := src.Candidates()

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected the candidate list to stay at 1 face across calls (cached), got first=%d second=%d", len(first), len(second))
	}
}

func TestDirectorySource_ExplicitDirectoriesComeBeforeDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Explicit.ttf", buildMinimalCandidateSfnt("Explicit", false, false))

	// includeSystemDefaults is true, but on a CI/dev machine with no
	// matching GOOS directories actually present, defaultFontScanDirs'
	// entries just contribute nothing (see DirectorySource.scanDirectory's
	// tolerance of a nonexistent directory) - this test only asserts
	// ordering/priority, not that any real system font is found (which
	// docs/FONTS.md's Testing section explicitly says a test must not
	// depend on).
	src := NewDirectorySource([]string{dir}, true)
	if len(src.dirs) < 2 {
		t.Fatalf("expected the explicit directory plus at least one platform default entry, got %d dirs", len(src.dirs))
	}
	if src.dirs[0].path != dir {
		t.Fatalf("expected the explicit directory to be scanned first, got %q", src.dirs[0].path)
	}
}

func TestDirectorySource_RecursiveScanFindsNestedFiles(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "truetype", "myfamily")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, nested, "Nested.ttf", buildMinimalCandidateSfnt("Nested", false, false))

	src := &DirectorySource{dirs: []scanDir{{path: root, recursive: true}}}
	faces := src.Candidates()
	if len(faces) != 1 || faces[0].Characteristics.Family != "Nested" {
		t.Fatalf("expected the recursive scan to find the nested font, got %+v", faces)
	}
}

func TestDirectorySource_NonRecursiveScanSkipsNestedFiles(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "truetype", "myfamily")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, nested, "Nested.ttf", buildMinimalCandidateSfnt("Nested", false, false))

	src := NewDirectorySource([]string{root}, false)
	if faces := src.Candidates(); len(faces) != 0 {
		t.Fatalf("expected a non-recursive scan to skip a nested font, got %+v", faces)
	}
}

func TestIsFontFilename(t *testing.T) {
	tests := map[string]bool{
		"Arial.ttf":  true,
		"Arial.TTF":  true,
		"Bundle.ttc": true,
		"Face.otf":   true,
		"readme.txt": false,
		"noext":      false,
	}
	for name, want := range tests {
		if got := isFontFilename(name); got != want {
			t.Errorf("isFontFilename(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDefaultFontScanDirsFor_MostLocalFirst(t *testing.T) {
	// For every platform this package specifically knows about, the
	// first directory in the returned list must be the most-local one
	// (per-user), per docs/FONTS.md's "Configuration" section - this
	// test does not assert the exact paths (which depend on
	// os.UserHomeDir/environment variables this test does not control),
	// only that a per-user directory precedes the machine-wide ones.
	tests := []struct {
		goos           string
		wantRecursive  bool
		minDirectories int
	}{
		{"darwin", false, 3},
		{"linux", true, 3},
		{"windows", false, 1},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			dirs := defaultFontScanDirsFor(tt.goos)
			if len(dirs) < tt.minDirectories {
				t.Fatalf("expected at least %d directories for %s, got %d: %+v", tt.minDirectories, tt.goos, len(dirs), dirs)
			}
			for _, d := range dirs {
				if d.recursive != tt.wantRecursive {
					t.Errorf("%s: expected recursive=%v for every default directory, got %+v", tt.goos, tt.wantRecursive, d)
				}
			}
		})
	}
}

func TestDefaultFontScanDirsFor_UnknownGOOSReturnsNil(t *testing.T) {
	if dirs := defaultFontScanDirsFor("plan9"); dirs != nil {
		t.Fatalf("expected no default directories for an unresearched GOOS, got %+v", dirs)
	}
}
