package fonts

import (
	"io/fs"
	"os"
	"path/filepath"
)

// This file implements DirectoryCMapSource, this package's only shipped
// CMapSource (see predefined_cmap.go): a name-to-file index built by
// scanning real directories on disk for a file whose *base name* exactly
// matches a requested predefined CMap name (e.g. "UniGB-UCS2-H"), read
// only when actually asked for. This is the disk-touching half of
// Phase 9's predefined-CMap support, mirroring directory_source.go's
// DirectorySource (font substitution) closely enough that this doc
// comment mostly points at the differences:
//
//   - Adobe's own CMap resource files (https://github.com/adobe-type-tools/
//     cmap-resources, or the equivalent files bundled with a Ghostscript,
//     poppler, or TeX installation) have no file extension at all and are
//     conventionally named exactly after the CMap they contain -
//     "UniGB-UCS2-H" is both the file's name and the /CMapName a PDF's
//     /Encoding entry uses - so, unlike DirectorySource's extension-based
//     isFontFilename filter, every regular file this scan finds is a
//     candidate, indexed by its own base name.
//   - There is no GOOS-gated "common default directory" list here the
//     way defaultFontScanDirs provides for fonts: CMap resources are not
//     installed as part of any operating system's normal font or
//     resource stack, so NewDirectoryCMapSource takes only explicit
//     directories, always recursively scanned (Adobe's own resource
//     repository nests each file one directory level down, under
//     "<Registry>-<Ordering>/CMap/") - see predefined_cmap.go's own doc
//     comment for why this package ships no such data itself and treats
//     locating it as entirely the embedding application's job.

// maxScannedCMapFiles bounds how many files DirectoryCMapSource.build
// will index across every configured directory combined, for exactly
// the same reason directory_source.go's maxScannedFontFiles bounds its
// own scan (a pathological or symlink-cycling directory tree should
// never cost unbounded work just to open a document) - see that
// constant's doc comment. No real CMap resource directory tree comes
// anywhere near this many files.
const maxScannedCMapFiles = 20000

// DirectoryCMapSource is a CMapSource that scans a fixed list of
// directories (see NewDirectoryCMapSource) for candidate CMap resource
// files, indexed by file name.
//
// Like DirectorySource, this only actually scans the filesystem once,
// the first time CMapData is called (see the built field), and reuses
// that index for the lifetime of the Document it is attached to - see
// DirectorySource's own doc comment for the full "laziness and caching"
// rationale, which applies here unchanged (including this package's
// Phase 6 concurrency decision: no locking, since a *Document is not
// safe for concurrent use regardless).
type DirectoryCMapSource struct {
	dirs  []string
	built bool
	index map[string]string
}

// NewDirectoryCMapSource returns a DirectoryCMapSource that will
// recursively scan directories the first time its CMapData method is
// called - see that type's own doc comment. Every directory is treated
// equally (unlike DirectorySource's most-local-first priority order,
// there is no meaningful "which copy of a predefined CMap resource
// wins" question in practice: a given CMap name's data does not vary
// meaningfully by source the way a font's actual glyph outlines can),
// so the first directory to contain a matching file name wins simply by
// virtue of being scanned first.
func NewDirectoryCMapSource(directories []string) *DirectoryCMapSource {
	return &DirectoryCMapSource{dirs: append([]string(nil), directories...)}
}

// CMapData implements CMapSource: it triggers this DirectoryCMapSource's
// one-time directory scan (see the built field's doc comment) if it has
// not already happened, then reads and returns the matching file's
// bytes, if any. ok=false whenever name was never found by the scan, or
// (rarer - a file that existed during the scan but was removed or
// became unreadable since) the file can no longer be read - both are
// treated identically to "no predefined CMap data available for this
// name", the same tolerant fallback predefined_cmap.go's callers already
// expect.
func (s *DirectoryCMapSource) CMapData(name string) ([]byte, bool) {
	if !s.built {
		s.build()
	}
	path, ok := s.index[name]
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// build performs this DirectoryCMapSource's actual directory scan - see
// CMapData's doc comment for why this only ever happens once. Like
// DirectorySource.build, it is deliberately tolerant of everything that
// can go wrong reading real directories it does not control (a
// directory that does not exist or is not readable simply contributes
// no entries, never a hard failure).
func (s *DirectoryCMapSource) build() {
	s.built = true
	s.index = make(map[string]string)
	scanned := 0
	for _, dir := range s.dirs {
		if scanned >= maxScannedCMapFiles {
			return
		}
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if scanned >= maxScannedCMapFiles {
				return filepath.SkipAll
			}
			scanned++
			name := entry.Name()
			if _, exists := s.index[name]; !exists {
				s.index[name] = path
			}
			return nil
		})
	}
}
