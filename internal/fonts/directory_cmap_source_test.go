package fonts

import (
	"os"
	"path/filepath"
	"testing"
)

// This file tests DirectoryCMapSource against a real (temporary)
// directory tree - see directory_source_test.go for the equivalent
// coverage of DirectorySource, whose "writeFile" helper this file
// reuses.

func TestDirectoryCMapSource_FindsFileByExactName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "UniGB-UCS2-H", []byte("1 begincidrange\n<0041> <005A> 1\nendcidrange\nendcmap\n"))
	writeFile(t, dir, "notes.txt", []byte("not a cmap"))

	src := NewDirectoryCMapSource([]string{dir})
	data, ok := src.CMapData("UniGB-UCS2-H")
	if !ok {
		t.Fatal("expected to find UniGB-UCS2-H")
	}
	cm := parseCMap(data, nil)
	if cid, ok := cm.CIDForCode(0x0041); !ok || cid != 1 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (1,true)", cid, ok)
	}

	if _, ok := src.CMapData("notes.txt"); !ok {
		// Every regular file is a candidate regardless of name (see this
		// type's doc comment) - "notes.txt" is a legitimate, if useless,
		// match for a request that happens to ask for exactly that name.
		t.Error("expected notes.txt to be indexed too (any regular file is a candidate)")
	}

	if _, ok := src.CMapData("Some-Other-Encoding"); ok {
		t.Error("expected ok=false for a name with no matching file")
	}
}

func TestDirectoryCMapSource_ScansNestedDirectories(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "Adobe-Japan1", "CMap")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeFile(t, nested, "UniJIS-UCS2-H", []byte("1 begincidchar\n<0041> 65\nendcidchar\nendcmap\n"))

	src := NewDirectoryCMapSource([]string{dir})
	data, ok := src.CMapData("UniJIS-UCS2-H")
	if !ok {
		t.Fatal("expected to find UniJIS-UCS2-H nested one directory level down, matching Adobe's own resource repository layout")
	}
	cm := parseCMap(data, nil)
	if cid, ok := cm.CIDForCode(0x0041); !ok || cid != 65 {
		t.Errorf("CIDForCode(0x41) = (%d,%v), want (65,true)", cid, ok)
	}
}

func TestDirectoryCMapSource_MissingDirectoryIsHarmless(t *testing.T) {
	src := NewDirectoryCMapSource([]string{filepath.Join(t.TempDir(), "does-not-exist")})
	if _, ok := src.CMapData("Anything"); ok {
		t.Error("expected ok=false when the configured directory does not exist")
	}
}

func TestDirectoryCMapSource_ScansOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	src := NewDirectoryCMapSource([]string{dir})
	src.CMapData("Anything") // triggers the first (and, per this test, only) scan

	// Adding a file after the first CMapData call must not be picked up -
	// build only ever runs once, exactly like DirectorySource.Candidates
	// (see that type's own doc comment on caching).
	writeFile(t, dir, "LateArrival", []byte("endcmap\n"))
	if _, ok := src.CMapData("LateArrival"); ok {
		t.Error("expected ok=false for a file added after the one-time scan already ran")
	}
}
