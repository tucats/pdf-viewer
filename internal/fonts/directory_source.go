package fonts

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// This file implements DirectorySource, this package's only shipped
// FontSource (see substitute.go): a candidate font list built by
// scanning real directories on disk for ".ttf"/".ttc"/".otf" files and
// probing each one with Phase 2's ProbeFontFile. It is the part of
// docs/FONTS.md's design that actually touches a filesystem - see that
// document's "Scope decision: directory scanning, not a font-service
// API" section for why this is considered acceptable under this
// package's "no system font service" policy (doc.go): DirectorySource
// only ever calls os.ReadDir/os.ReadFile/filepath.WalkDir against
// directories a caller explicitly configured (either literally, via
// FontSubstitution.Directories, or by opting into this file's own
// GOOS-gated defaultFontScanDirs list below) - never a platform API call
// like fontconfig, Core Text, or DirectWrite that this project could not
// itself fully control or reproduce.

// maxScannedFontFiles bounds how many font files DirectorySource.build
// will ever probe across every configured directory combined, defending
// against a pathological directory tree (a huge flat directory, a deep
// recursive one, or a symlink cycle a recursive walk might otherwise
// follow indefinitely) costing an unbounded amount of work just to open
// a document - the same "bounded work against untrusted/unpredictable
// input" policy this package already applies to file-format parsing
// (see, for example, probe.go's maxTTCFaces) applied here to the
// filesystem itself, since a real machine's font directories are not
// under this project's control any more than a hostile PDF's internal
// structure is. No real-world system font directory tree comes anywhere
// near this many font files.
const maxScannedFontFiles = 20000

// scanDir is one directory DirectorySource scans, paired with whether to
// also look inside its own subdirectories - see defaultFontScanDirs'
// doc comment for why some platform-default directories need this and
// caller-supplied ones (via NewDirectorySource's directories parameter)
// do not.
type scanDir struct {
	path      string
	recursive bool
}

// DirectorySource is a FontSource that scans a fixed list of
// directories (see NewDirectorySource) for candidate font files.
//
// # Laziness and caching
//
// Scanning every configured directory and probing every font file found
// in them is real work (disk I/O plus, for each file, Phase 2's
// ProbeFontFile parsing its table directory and "name"/"OS/2" tables) -
// wasteful to repeat for every single glyph a document falls back to
// notdefGlyph for, or every time Candidates is called at all. A
// DirectorySource therefore only actually scans once, the first time
// Candidates is called (see the built field below), and reuses that same
// result for every later call - mirroring internal/content.FontCache's
// existing "build once per Document, reuse for its whole lifetime"
// precedent (see that type's own doc comment). Per this project's
// existing Phase 6 concurrency decision (a *Document, and everything
// reachable from it, is not safe for concurrent use by multiple
// goroutines - see the root package's Document type doc comment), this
// caching uses no locking of its own, exactly like FontCache.
type DirectorySource struct {
	dirs  []scanDir
	built bool
	faces []FontFace
}

// NewDirectorySource returns a DirectorySource that will scan
// directories (each treated as an explicit, non-recursive, top-priority
// location - see scanDir's doc comment) and, if includeSystemDefaults is
// true, this package's own GOOS-gated default platform font directories
// (see defaultFontScanDirs) after them. directories always take priority
// over the defaults regardless of includeSystemDefaults, matching
// docs/FONTS.md's "an explicit caller-supplied font should always win
// over one this package merely guessed at from the OS" rule - see
// FontSource.Candidates' doc comment for how that priority order is
// actually used once scanning is done (matchFace's tie-breaking).
//
// The returned DirectorySource does not touch the filesystem until its
// Candidates method is first called - see that type's own doc comment.
func NewDirectorySource(directories []string, includeSystemDefaults bool) *DirectorySource {
	dirs := make([]scanDir, 0, len(directories)+4)
	for _, d := range directories {
		dirs = append(dirs, scanDir{path: d})
	}
	if includeSystemDefaults {
		dirs = append(dirs, defaultFontScanDirs()...)
	}
	return &DirectorySource{dirs: dirs}
}

// Candidates implements FontSource: it triggers this DirectorySource's
// one-time directory scan (see the built field's doc comment) if it has
// not already happened, then returns the resulting candidate list, in
// the same most-local-first priority order its configured directories
// were scanned in.
func (s *DirectorySource) Candidates() []FontFace {
	if !s.built {
		s.build()
	}
	return s.faces
}

// build performs this DirectorySource's actual directory scan - see
// Candidates' doc comment for why this only ever happens once. It is
// deliberately tolerant of everything that can go wrong reading real
// directories it does not control (a directory that does not exist, is
// not readable, or contains a file that is not actually a usable font)
// - each such problem simply means fewer candidates are found, never a
// hard failure, matching this package's "Font never fails" philosophy
// (see font.go's doc comment) all the way down to this, its least
// predictable input source.
func (s *DirectorySource) build() {
	s.built = true
	scanned := 0
	for _, d := range s.dirs {
		if scanned >= maxScannedFontFiles {
			return
		}
		s.scanDirectory(d, &scanned)
	}
}

// scanDirectory lists (non-recursively) or walks (recursively, per d's
// own flag - see scanDir's doc comment) one directory, probing every
// file within it whose extension isFontFilename recognizes.
func (s *DirectorySource) scanDirectory(d scanDir, scanned *int) {
	if !d.recursive {
		entries, err := os.ReadDir(d.path)
		if err != nil {
			// Most commonly: this default/configured directory simply
			// does not exist on this machine (e.g. a Linux-only path on
			// a system that doesn't have it) - not an error worth
			// surfacing, since DirectorySource's whole contract is
			// "best effort, given whatever is actually on disk."
			return
		}
		for _, entry := range entries {
			if *scanned >= maxScannedFontFiles {
				return
			}
			if entry.IsDir() || !isFontFilename(entry.Name()) {
				continue
			}
			s.probeFile(filepath.Join(d.path, entry.Name()))
			*scanned++
		}
		return
	}

	// filepath.WalkDir visits d.path itself first and then every entry
	// beneath it in a deterministic (lexical) order; returning a non-nil
	// error from the callback below stops the walk early, which is how
	// this loop enforces maxScannedFontFiles for a recursive directory
	// (an ordinary directory-listing error for one subdirectory, by
	// contrast, is swallowed via the "return nil" below it, so one
	// unreadable subdirectory doesn't abort scanning its siblings).
	_ = filepath.WalkDir(d.path, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if *scanned >= maxScannedFontFiles {
			return filepath.SkipAll
		}
		if isFontFilename(entry.Name()) {
			s.probeFile(path)
			*scanned++
		}
		return nil
	})
}

// probeFile reads and probes one candidate font file at path, appending
// every face ProbeFontFile finds in it (tagging each with path, via
// FontFace's own Path field) to this DirectorySource's growing
// candidate list. A file that cannot be read, or that ProbeFontFile
// cannot make sense of at all (not a recognized font file), is silently
// skipped - the same tolerance scanDirectory already applies to an
// unreadable directory, extended to an unreadable/unrecognized file.
func (s *DirectorySource) probeFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	faces, err := ProbeFontFile(data)
	if err != nil {
		return
	}
	for i := range faces {
		faces[i].Path = path
	}
	s.faces = append(s.faces, faces...)
}

// isFontFilename reports whether name's extension is one DirectorySource
// probes as a candidate font file - the three container formats Phase 2
// and Phase 3's combined parsing actually understands (see probe.go's
// ProbeFontFile and cff.go): plain TrueType, TrueType Collection, and
// OpenType (either outline flavor - probeFace itself figures out whether
// an ".otf" file's outlines are "glyf" or "CFF "). The comparison is
// case-insensitive since real filesystems (particularly Windows' and
// macOS') commonly present font files with uppercase extensions.
func isFontFilename(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".ttc", ".otf":
		return true
	default:
		return false
	}
}

// defaultFontScanDirs returns this package's built-in, GOOS-gated list
// of common platform font directories, ordered most-local to
// least-local per docs/FONTS.md's "Configuration" section: a font a
// specific user installed for only themselves should be found - and
// preferred, via FontSource.Candidates' documented tie-breaking rule -
// over a same-named font installed machine-wide or shipped by the
// operating system itself. A directory that turns out not to exist on
// this particular machine (for example, no per-user Fonts directory has
// ever been created) is simply skipped once scanDirectory tries to read
// it - this function itself does no existence checking, since
// os.ReadDir/filepath.WalkDir already report that outcome the same way
// they report any other unreadable directory.
//
// Linux distributions conventionally nest font files inside
// subdirectories of their font directories (grouped by family or
// package, e.g. "/usr/share/fonts/truetype/dejavu/*.ttf") rather than
// storing them flat - unlike macOS and Windows, whose font directories
// are conventionally flat - which is why every Linux entry here is
// marked recursive while every macOS/Windows entry is not (see scanDir's
// own doc comment for what that flag controls, and maxScannedFontFiles
// for how a pathologically deep or wide tree is still bounded).
func defaultFontScanDirs() []scanDir {
	return defaultFontScanDirsFor(runtime.GOOS)
}

// defaultFontScanDirsFor is defaultFontScanDirs' testable core, taking
// the target GOOS as an explicit parameter (one of Go's "GOOS" build
// target names, e.g. "darwin"/"linux"/"windows" - see
// https://pkg.go.dev/internal/platform for the full list this project
// does not otherwise depend on) rather than always reading the running
// program's own runtime.GOOS constant, so directory_source_test.go can
// exercise every platform's directory list from a single test binary
// regardless of which platform actually built it.
func defaultFontScanDirsFor(goos string) []scanDir {
	switch goos {
	case "darwin":
		var dirs []scanDir
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, scanDir{path: filepath.Join(home, "Library", "Fonts")})
		}
		return append(dirs,
			scanDir{path: "/Library/Fonts"},
			scanDir{path: "/System/Library/Fonts"},
			// Fonts Apple ships but doesn't activate by default - Arial,
			// Arial Narrow, Georgia, Times New Roman, Verdana, and other
			// common Microsoft-metric-compatible fonts among them - live
			// in this flat subdirectory rather than directly under
			// /System/Library/Fonts (macOS itself, and apps like Preview,
			// resolve into it too). It is exactly the kind of font a PDF's
			// non-embedded "ArialMT" is asking to be substituted with, so
			// without this entry a real-world PDF built with Arial
			// substitutes nothing here even though the file is present on
			// disk and other viewers find it.
			scanDir{path: "/System/Library/Fonts/Supplemental"},
		)

	case "linux":
		var dirs []scanDir
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, scanDir{path: filepath.Join(home, ".local", "share", "fonts"), recursive: true})
		}
		return append(dirs,
			scanDir{path: "/usr/local/share/fonts", recursive: true},
			scanDir{path: "/usr/share/fonts", recursive: true},
		)

	case "windows":
		var dirs []scanDir
		// A per-user Fonts directory (fonts installed without admin
		// rights) exists on modern Windows alongside the machine-wide
		// one - see docs/FONTS.md's "Configuration" section - and, per
		// this function's own most-local-first ordering, is listed
		// first when present.
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, scanDir{path: filepath.Join(local, "Microsoft", "Windows", "Fonts")})
		}
		windir := os.Getenv("WINDIR")
		if windir == "" {
			windir = `C:\Windows`
		}
		return append(dirs, scanDir{path: filepath.Join(windir, "Fonts")})

	default:
		// An unrecognized/unresearched GOOS simply contributes no
		// default directories - not an error, since a caller can always
		// supply its own explicit FontSubstitution.Directories
		// regardless of platform, and includeSystemDefaults is opt-in
		// in the first place (see NewDirectorySource).
		return nil
	}
}
