package annotation

import (
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

func numArray(vals ...float64) syntax.Array {
	arr := make(syntax.Array, len(vals))
	for i, v := range vals {
		arr[i] = syntax.Real(v)
	}
	return arr
}

func TestResolveNoAnnotsReturnsNil(t *testing.T) {
	got := Resolve(&fakeResolver{}, syntax.Dictionary{})
	if got != nil {
		t.Fatalf("Resolve with no /Annots = %v, want nil", got)
	}
}

// TestResolveSimpleAppearanceIdentityMapping exercises the common case:
// the appearance's own (already-untransformed) /BBox exactly matches the
// annotation's /Rect, so the resulting mapping matrix should be the
// identity (scale 1, no translation).
func TestResolveSimpleAppearanceIdentityMapping(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}}
	annot := syntax.Dictionary{
		"Rect": numArray(0, 0, 10, 10),
		"AP":   syntax.Dictionary{"N": apStream},
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}

	got := Resolve(&fakeResolver{}, pageDict)
	if len(got) != 1 {
		t.Fatalf("len(Resolve(...)) = %d, want 1", len(got))
	}
	if got[0].Matrix != graphics.Identity() {
		t.Fatalf("Matrix = %+v, want identity", got[0].Matrix)
	}
}

// TestResolveScalesAndTranslatesToRect exercises the general case: a
// 10x10 appearance /BBox mapped onto a /Rect at a different position and
// twice the size - the resulting matrix should scale by 2 and translate
// the BBox's own origin to the Rect's own origin.
func TestResolveScalesAndTranslatesToRect(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}}
	annot := syntax.Dictionary{
		"Rect": numArray(100, 200, 120, 220), // 20x20, offset by (100,200)
		"AP":   syntax.Dictionary{"N": apStream},
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}

	got := Resolve(&fakeResolver{}, pageDict)
	if len(got) != 1 {
		t.Fatalf("len(Resolve(...)) = %d, want 1", len(got))
	}
	x, y := got[0].Matrix.Apply(0, 0)
	if x != 100 || y != 200 {
		t.Fatalf("Matrix.Apply(0,0) = (%v,%v), want (100,200)", x, y)
	}
	x, y = got[0].Matrix.Apply(10, 10)
	if x != 120 || y != 220 {
		t.Fatalf("Matrix.Apply(10,10) = (%v,%v), want (120,220)", x, y)
	}
}

// TestResolveAppliesAppearanceMatrixBeforeComputingBBoxPrime confirms
// the appearance's own /Matrix is applied to /BBox (per the package doc
// comment's step 1) before the BBox'-to-Rect mapping is computed - here,
// a 90-degree rotation turns a 10x20 BBox into a 20x10 BBox', so mapping
// it onto a 20x10 Rect should need no scaling at all (unlike mapping the
// *original*, unrotated 10x20 BBox onto a 20x10 Rect, which would need
// non-uniform scaling).
func TestResolveAppliesAppearanceMatrixBeforeComputingBBoxPrime(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{
		"BBox":   numArray(0, 0, 10, 20),
		"Matrix": numArray(0, 1, -1, 0, 0, 0), // 90-degree rotation
	}}
	annot := syntax.Dictionary{
		"Rect": numArray(0, 0, 20, 10),
		"AP":   syntax.Dictionary{"N": apStream},
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}

	got := Resolve(&fakeResolver{}, pageDict)
	if len(got) != 1 {
		t.Fatalf("len(Resolve(...)) = %d, want 1", len(got))
	}
	// Rotating (0,0)-(10,20) by 90 degrees ((x,y)->(-y,x)) gives corners
	// spanning x in [-20,0], y in [0,10] - a 20x10 box already exactly
	// the Rect's own size, so A should have unit scale (up to sign/
	// translation) rather than a distorting non-uniform one.
	m := got[0].Matrix
	if m.A != 1 || m.D != 1 {
		t.Fatalf("Matrix = %+v, want unit scale (A=1,D=1) since BBox' already matches Rect's size", m)
	}
}

func TestResolveMultiStateAppearanceUsesAS(t *testing.T) {
	onStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}, Raw: []byte("on")}
	offStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}, Raw: []byte("off")}
	annot := syntax.Dictionary{
		"Rect": numArray(0, 0, 10, 10),
		"AS":   syntax.Name("On"),
		"AP":   syntax.Dictionary{"N": syntax.Dictionary{"On": onStream, "Off": offStream}},
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}

	got := Resolve(&fakeResolver{}, pageDict)
	if len(got) != 1 {
		t.Fatalf("len(Resolve(...)) = %d, want 1", len(got))
	}
	if string(got[0].Stream.Raw) != "on" {
		t.Fatalf("Stream.Raw = %q, want %q (the /AS-selected state)", got[0].Stream.Raw, "on")
	}
}

func TestResolveMultiStateAppearanceWithoutASIsSkipped(t *testing.T) {
	onStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}}
	annot := syntax.Dictionary{
		"Rect": numArray(0, 0, 10, 10),
		"AP":   syntax.Dictionary{"N": syntax.Dictionary{"On": onStream}},
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}

	got := Resolve(&fakeResolver{}, pageDict)
	if len(got) != 0 {
		t.Fatalf("len(Resolve(...)) = %d, want 0 (no /AS to select a state)", len(got))
	}
}

func TestResolveHiddenFlagIsSkipped(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}}
	annot := syntax.Dictionary{
		"Rect": numArray(0, 0, 10, 10), "AP": syntax.Dictionary{"N": apStream},
		"F": syntax.Integer(2), // bit 2: Hidden
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}

	if got := Resolve(&fakeResolver{}, pageDict); len(got) != 0 {
		t.Fatalf("len(Resolve(...)) = %d, want 0 (Hidden flag)", len(got))
	}
}

func TestResolveNoViewFlagIsSkipped(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}}
	annot := syntax.Dictionary{
		"Rect": numArray(0, 0, 10, 10), "AP": syntax.Dictionary{"N": apStream},
		"F": syntax.Integer(32), // bit 6: NoView
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}

	if got := Resolve(&fakeResolver{}, pageDict); len(got) != 0 {
		t.Fatalf("len(Resolve(...)) = %d, want 0 (NoView flag)", len(got))
	}
}

func TestResolveNoAPIsSkipped(t *testing.T) {
	annot := syntax.Dictionary{"Rect": numArray(0, 0, 10, 10)}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}
	if got := Resolve(&fakeResolver{}, pageDict); len(got) != 0 {
		t.Fatalf("len(Resolve(...)) = %d, want 0 (no /AP)", len(got))
	}
}

func TestResolveMalformedRectIsSkipped(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}}
	annot := syntax.Dictionary{
		"Rect": numArray(0, 0, 10), // only 3 entries
		"AP":   syntax.Dictionary{"N": apStream},
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}
	if got := Resolve(&fakeResolver{}, pageDict); len(got) != 0 {
		t.Fatalf("len(Resolve(...)) = %d, want 0 (malformed /Rect)", len(got))
	}
}

func TestResolveMissingAppearanceBBoxIsSkipped(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{}} // no /BBox at all
	annot := syntax.Dictionary{"Rect": numArray(0, 0, 10, 10), "AP": syntax.Dictionary{"N": apStream}}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{annot}}
	if got := Resolve(&fakeResolver{}, pageDict); len(got) != 0 {
		t.Fatalf("len(Resolve(...)) = %d, want 0 (appearance stream has no /BBox)", len(got))
	}
}

// TestResolveSkipsBadAnnotationButKeepsGoodOnes confirms one malformed
// or hidden annotation in /Annots does not prevent the rest of the array
// from being resolved, and that order is preserved.
func TestResolveSkipsBadAnnotationButKeepsGoodOnes(t *testing.T) {
	good1 := syntax.Dictionary{
		"Rect": numArray(0, 0, 10, 10),
		"AP":   syntax.Dictionary{"N": syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}, Raw: []byte("first")}},
	}
	bad := syntax.Dictionary{"Rect": numArray(0, 0, 10, 10)} // no /AP
	good2 := syntax.Dictionary{
		"Rect": numArray(0, 0, 10, 10),
		"AP":   syntax.Dictionary{"N": syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}, Raw: []byte("second")}},
	}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{good1, bad, good2}}

	got := Resolve(&fakeResolver{}, pageDict)
	if len(got) != 2 {
		t.Fatalf("len(Resolve(...)) = %d, want 2", len(got))
	}
	if string(got[0].Stream.Raw) != "first" || string(got[1].Stream.Raw) != "second" {
		t.Fatalf("Resolve(...) order = [%q %q], want [first second]", got[0].Stream.Raw, got[1].Stream.Raw)
	}
}

// TestResolveFollowsIndirectReferences confirms every level of
// indirection this package might plausibly encounter - the /Annots
// array's own entries, an annotation's /AP, and /AP's /N - is followed
// correctly.
func TestResolveFollowsIndirectReferences(t *testing.T) {
	apStream := syntax.Stream{Dict: syntax.Dictionary{"BBox": numArray(0, 0, 10, 10)}}
	r := &fakeResolver{objects: map[int]syntax.Object{
		1: syntax.Dictionary{"Rect": numArray(0, 0, 10, 10), "AP": syntax.Reference{Number: 2}},
		2: syntax.Dictionary{"N": syntax.Reference{Number: 3}},
		3: apStream,
	}}
	pageDict := syntax.Dictionary{"Annots": syntax.Array{syntax.Reference{Number: 1}}}

	got := Resolve(r, pageDict)
	if len(got) != 1 {
		t.Fatalf("len(Resolve(...)) = %d, want 1", len(got))
	}
}
