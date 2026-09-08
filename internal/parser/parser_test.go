package parser

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/source"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// fixturesDir points at the hand-authored PDF corpus documented in
// testdata/fixtures/FIXTURES.md. Tests in this file exercise the parser
// against those same files rather than maintaining a second, parallel
// set of test-only PDFs, so that a change to the fixture generator
// (tools/genfixtures) and a change to what this package expects of them
// stay honest with each other.
const fixturesDir = "../../testdata/fixtures/handmade"

// openFixture opens the named file from fixturesDir as a Document,
// failing the test immediately if that does not succeed - most tests in
// this file expect a successful open and only care about what comes
// after, so this keeps them from having to repeat that boilerplate.
func openFixture(t *testing.T, name string) *Document {
	t.Helper()
	d, err := openFixtureErr(t, name)
	if err != nil {
		t.Fatalf("Open(%s): %v", name, err)
	}
	return d
}

// openFixtureErr is like openFixture but returns the error instead of
// failing the test, for tests that specifically want to inspect it.
func openFixtureErr(t *testing.T, name string) (*Document, error) {
	t.Helper()
	path := filepath.Join(fixturesDir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}
	src, err := source.New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("source.New(%s): %v", path, err)
	}
	return Open(src)
}

// resolveDict is a small test helper: it resolves ref (which must
// ultimately be a syntax.Dictionary) and fails the test if it is not.
func resolveDict(t *testing.T, d *Document, ref syntax.Reference) syntax.Dictionary {
	t.Helper()
	obj, err := d.Resolve(ref.Number)
	if err != nil {
		t.Fatalf("Resolve(%d): %v", ref.Number, err)
	}
	dict, ok := obj.(syntax.Dictionary)
	if !ok {
		t.Fatalf("Resolve(%d) = %#v (%T), want syntax.Dictionary", ref.Number, obj, obj)
	}
	return dict
}

func TestOpenMinimalBlankPage(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")

	rootRef, ok := d.Trailer["Root"].(syntax.Reference)
	if !ok {
		t.Fatalf("trailer /Root = %#v, want syntax.Reference", d.Trailer["Root"])
	}
	catalog := resolveDict(t, d, rootRef)
	if got := catalog["Type"]; got != syntax.Name("Catalog") {
		t.Errorf("catalog /Type = %#v, want /Catalog", got)
	}

	pagesRef, ok := catalog["Pages"].(syntax.Reference)
	if !ok {
		t.Fatalf("catalog /Pages = %#v, want syntax.Reference", catalog["Pages"])
	}
	pages := resolveDict(t, d, pagesRef)
	if got, want := pages["Count"], syntax.Integer(1); got != want {
		t.Errorf("pages /Count = %#v, want %#v", got, want)
	}

	kids, ok := pages["Kids"].(syntax.Array)
	if !ok || len(kids) != 1 {
		t.Fatalf("pages /Kids = %#v, want a 1-element Array", pages["Kids"])
	}
	pageRef, ok := kids[0].(syntax.Reference)
	if !ok {
		t.Fatalf("kid 0 = %#v, want syntax.Reference", kids[0])
	}
	page := resolveDict(t, d, pageRef)
	wantBox := syntax.Array{syntax.Integer(0), syntax.Integer(0), syntax.Integer(200), syntax.Integer(200)}
	if got := page["MediaBox"]; !equalArray(got, wantBox) {
		t.Errorf("page /MediaBox = %#v, want %#v", got, wantBox)
	}
}

func TestOpenTwoPages(t *testing.T) {
	d := openFixture(t, "two-pages.pdf")

	rootRef := d.Trailer["Root"].(syntax.Reference)
	catalog := resolveDict(t, d, rootRef)
	pagesRef := catalog["Pages"].(syntax.Reference)
	pages := resolveDict(t, d, pagesRef)

	kids, ok := pages["Kids"].(syntax.Array)
	if !ok || len(kids) != 2 {
		t.Fatalf("pages /Kids = %#v, want a 2-element Array", pages["Kids"])
	}

	wantBoxes := []syntax.Array{
		{syntax.Integer(0), syntax.Integer(0), syntax.Integer(100), syntax.Integer(200)},
		{syntax.Integer(0), syntax.Integer(0), syntax.Integer(300), syntax.Integer(400)},
	}
	for i, kid := range kids {
		ref, ok := kid.(syntax.Reference)
		if !ok {
			t.Fatalf("kid %d = %#v, want syntax.Reference", i, kid)
		}
		page := resolveDict(t, d, ref)
		if got := page["MediaBox"]; !equalArray(got, wantBoxes[i]) {
			t.Errorf("page %d /MediaBox = %#v, want %#v", i, got, wantBoxes[i])
		}
	}
}

// TestOpenIncrementalUpdate is the regression test described in
// tools/genfixtures/main.go's buildIncrementalUpdate doc comment: a
// reader that only looked at the newest revision's objects while
// ignoring the /Prev chain would fail to find objects 1, 3, and 4
// (defined only in the original revision); a reader that ignored the
// newest revision's redefinition of object 2 would see only the
// original single page instead of both.
func TestOpenIncrementalUpdate(t *testing.T) {
	d := openFixture(t, "incremental-update.pdf")

	rootRef := d.Trailer["Root"].(syntax.Reference)
	catalog := resolveDict(t, d, rootRef)
	pagesRef := catalog["Pages"].(syntax.Reference)
	pages := resolveDict(t, d, pagesRef)

	if got, want := pages["Count"], syntax.Integer(2); got != want {
		t.Fatalf("pages /Count = %#v, want %#v (the /Prev chain was not followed correctly)", got, want)
	}
	kids, ok := pages["Kids"].(syntax.Array)
	if !ok || len(kids) != 2 {
		t.Fatalf("pages /Kids = %#v, want a 2-element Array", pages["Kids"])
	}

	wantBoxes := []syntax.Array{
		{syntax.Integer(0), syntax.Integer(0), syntax.Integer(200), syntax.Integer(200)},
		{syntax.Integer(0), syntax.Integer(0), syntax.Integer(250), syntax.Integer(350)},
	}
	for i, kid := range kids {
		ref := kid.(syntax.Reference)
		page := resolveDict(t, d, ref)
		if got := page["MediaBox"]; !equalArray(got, wantBoxes[i]) {
			t.Errorf("page %d /MediaBox = %#v, want %#v", i, got, wantBoxes[i])
		}
	}
}

// TestOpenMalformedBadXrefOffsetRecovers exercises the linear-scan
// recovery path (see resolve.go's recoverByScanning) against the
// fixture built specifically to require it: opening the file succeeds
// (the xref table itself is syntactically fine), but resolving the one
// object whose recorded offset was deliberately corrupted must still
// succeed by falling back to a full-file scan.
func TestOpenMalformedBadXrefOffsetRecovers(t *testing.T) {
	d := openFixture(t, "malformed-bad-xref-offset.pdf")

	rootRef := d.Trailer["Root"].(syntax.Reference)
	catalog := resolveDict(t, d, rootRef)
	pagesRef := catalog["Pages"].(syntax.Reference)
	pages := resolveDict(t, d, pagesRef)
	kids := pages["Kids"].(syntax.Array)
	pageRef := kids[0].(syntax.Reference)

	page := resolveDict(t, d, pageRef)
	wantBox := syntax.Array{syntax.Integer(0), syntax.Integer(0), syntax.Integer(200), syntax.Integer(200)}
	if got := page["MediaBox"]; !equalArray(got, wantBox) {
		t.Errorf("page /MediaBox = %#v, want %#v (recovery scan did not find the right object)", got, wantBox)
	}
	if !d.recovered {
		t.Error("d.recovered = false, want true after resolving the corrupted object")
	}
}

// TestOpenTruncatedFileIsMalformed confirms that a file cut off before
// its cross-reference table and trailer even exist fails cleanly with a
// classified ErrMalformed error at Open time, rather than panicking or
// (worse) succeeding with a nonsensical empty document.
func TestOpenTruncatedFileIsMalformed(t *testing.T) {
	_, err := openFixtureErr(t, "truncated.pdf")
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Open(truncated.pdf) error = %v, want ErrMalformed", err)
	}
}

// TestResolveNonexistentObjectYieldsNull confirms the PDF specification
// rule that Resolve implements for a reference to an object number the
// cross-reference table has no entry for: it behaves like a reference to
// the null object, not like an error.
func TestResolveNonexistentObjectYieldsNull(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")

	obj, err := d.Resolve(9999)
	if err != nil {
		t.Fatalf("Resolve(9999): %v", err)
	}
	if _, ok := obj.(syntax.Null); !ok {
		t.Errorf("Resolve(9999) = %#v (%T), want syntax.Null", obj, obj)
	}
}

// TestResolveCachesResult confirms that resolving the same object number
// twice returns from Document.cache the second time rather than
// re-reading the file - a plain functional-equivalence check (the
// returned value must be identical) stands in for observing the cache
// directly, since Resolve's cache is an internal implementation detail
// callers should not need to know about.
func TestResolveCachesResult(t *testing.T) {
	d := openFixture(t, "minimal-blank-page.pdf")

	first, err := d.Resolve(1)
	if err != nil {
		t.Fatalf("Resolve(1) first call: %v", err)
	}
	if _, ok := d.cache[1]; !ok {
		t.Fatal("object 1 was not cached after Resolve")
	}
	second, err := d.Resolve(1)
	if err != nil {
		t.Fatalf("Resolve(1) second call: %v", err)
	}
	if !equalDict(first.(syntax.Dictionary), second.(syntax.Dictionary)) {
		t.Errorf("Resolve(1) returned different values across calls: %#v vs %#v", first, second)
	}
}

// TestOpenMissingHeaderIsMalformed and the other ad hoc byte-buffer
// tests below construct minimal, deliberately broken inputs inline
// rather than as checked-in fixtures, since they each test one narrow
// structural rule in isolation and do not need (or benefit from) the
// full weight of a hand-authored, provenance-tracked fixture file - see
// testdata/fixtures/FIXTURES.md for why that heavier process exists for
// the corpus under testdata/fixtures/handmade.
func TestOpenMissingHeaderIsMalformed(t *testing.T) {
	if _, err := openBytes(t, []byte("not a pdf file at all")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func TestOpenMissingStartxrefIsMalformed(t *testing.T) {
	if _, err := openBytes(t, []byte("%PDF-1.7\n1 0 obj\n<< >>\nendobj\n")); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

// TestOpenPrevCycleIsRejected constructs a trailer whose own /Prev
// points back at itself, which would otherwise send loadXref into an
// infinite loop; see loadXref's cycle guard.
func TestOpenPrevCycleIsRejected(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n")
	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 1\n0000000000 65535 f \n")
	// This trailer's /Prev points at xrefOffset itself, forming a
	// one-entry cycle.
	buf.WriteString("trailer\n<< /Size 1 /Root 1 0 R /Prev " + strconv.Itoa(xrefOffset) + " >>\n")
	buf.WriteString("startxref\n" + strconv.Itoa(xrefOffset) + "\n%%EOF\n")

	if _, err := openBytes(t, buf.Bytes()); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
}

func openBytes(t *testing.T, data []byte) (*Document, error) {
	t.Helper()
	src, err := source.New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("source.New: %v", err)
	}
	return Open(src)
}

func equalArray(obj syntax.Object, want syntax.Array) bool {
	arr, ok := obj.(syntax.Array)
	if !ok || len(arr) != len(want) {
		return false
	}
	for i := range arr {
		if arr[i] != want[i] {
			return false
		}
	}
	return true
}

func equalDict(a, b syntax.Dictionary) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			// Values that are themselves composite (Array, Dictionary)
			// are not comparable with ==; none of this test file's
			// fixtures currently need that depth of comparison, so a
			// simple mismatch is reported rather than recursing.
			return false
		}
	}
	return true
}
