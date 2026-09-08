package parser

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/tucats/pdf-viewer/internal/source"
)

// FuzzOpenAndResolveAll is this package's Phase 1 fuzz target (see the
// repository README's Progress Log entry for Phase 0, which deferred
// fuzzing to land alongside the parsing code it exercises). It feeds
// arbitrary bytes to Open, and - for anything that does open
// successfully - resolves every object number the fuzzer-mutated file's
// own cross-reference table claims to have, plus a handful of numbers
// just outside that range. Open and Resolve must never panic and must
// always return, regardless of how malformed the input is; this is the
// same "bounded work, no panics" property internal/syntax's fuzz targets
// check at the tokenizing/value layer, extended here to this package's
// added responsibilities: locating startxref, walking a /Prev chain, and
// the linear-scan recovery path.
//
// The seed corpus is the hand-authored fixture files themselves (see
// testdata/fixtures/FIXTURES.md) plus a few small hand-written
// variations, which gives the fuzzer a running start from inputs that
// are already close to (or exactly) valid PDF structure rather than
// having to discover that structure from nothing.
//
// Run with, for example:
//
//	go test ./internal/parser/ -fuzz=FuzzOpenAndResolveAll -fuzztime=60s
func FuzzOpenAndResolveAll(f *testing.F) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		f.Fatalf("reading %s: %v", fixturesDir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pdf" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(fixturesDir, e.Name()))
		if err != nil {
			f.Fatalf("reading %s: %v", e.Name(), err)
		}
		f.Add(data)
	}
	f.Add([]byte("%PDF-1.7\n"))
	f.Add([]byte(""))
	f.Add([]byte("not a pdf at all"))

	f.Fuzz(func(t *testing.T, data []byte) {
		src, err := source.New(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			// Only possible for a nil reader (never the case here) or a
			// negative size (impossible from len(data)); kept as a
			// defensive check rather than removed, since source.New's
			// contract could change independently of this test.
			return
		}
		d, err := Open(src)
		if err != nil {
			return
		}

		// Probe a bounded range of object numbers: whatever the
		// cross-reference table actually contains, plus a little past
		// its highest known number, so the fuzzer also exercises the
		// nonexistent-object-yields-null path (see
		// TestResolveNonexistentObjectYieldsNull) without probing an
		// unbounded range on a maliciously large object number the file
		// might claim to define.
		const maxProbes = 4096
		probed := 0
		for num := range d.xref {
			if probed >= maxProbes {
				break
			}
			_, _ = d.Resolve(num)
			probed++
		}
		for extra := 0; extra < 8 && probed < maxProbes; extra++ {
			_, _ = d.Resolve(len(d.xref) + extra)
			probed++
		}
	})
}
