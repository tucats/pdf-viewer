package fonts

import (
	"encoding/binary"
	"testing"

	"github.com/tucats/pdf-viewer/internal/syntax"
)

// TestParseCIDWidths_RangeShape exercises the "cFirst cLast w" packed
// width shape (parseCIDWidths' doc comment's second bullet) - the
// font_test.go Load-level tests only exercise the other shape ("c
// [w1 w2 ...]").
func TestParseCIDWidths_RangeShape(t *testing.T) {
	t.Parallel()
	w := syntax.Array{syntax.Integer(10), syntax.Integer(12), syntax.Integer(500)}
	widths := parseCIDWidths(w, fakeResolver{})
	for cid := 10; cid <= 12; cid++ {
		if widths[cid] != 500 {
			t.Errorf("widths[%d] = %v, want 500", cid, widths[cid])
		}
	}
	if _, ok := widths[13]; ok {
		t.Errorf("widths[13] should not be set (outside the range)")
	}
}

// TestParseCIDWidths_MixedShapes confirms both packed-width shapes can
// appear in the same /W array, one after another, as real-world files
// do.
func TestParseCIDWidths_MixedShapes(t *testing.T) {
	t.Parallel()
	w := syntax.Array{
		syntax.Integer(1), syntax.Array{syntax.Integer(100), syntax.Integer(200)},
		syntax.Integer(50), syntax.Integer(55), syntax.Integer(1000),
	}
	widths := parseCIDWidths(w, fakeResolver{})
	if widths[1] != 100 || widths[2] != 200 {
		t.Errorf("array-shape widths = %v, %v, want 100, 200", widths[1], widths[2])
	}
	if widths[50] != 1000 || widths[55] != 1000 {
		t.Errorf("range-shape widths = %v, %v, want 1000, 1000", widths[50], widths[55])
	}
}

// TestParseCIDWidths_RejectsAbsurdRange confirms a hostile "cFirst
// cLast w" entry with an enormous range does not cause parseCIDWidths
// to attempt to populate billions of map entries - see
// maxCIDRangeSpan's inline doc comment.
func TestParseCIDWidths_RejectsAbsurdRange(t *testing.T) {
	t.Parallel()
	w := syntax.Array{syntax.Integer(0), syntax.Integer(2_000_000_000), syntax.Integer(1)}
	widths := parseCIDWidths(w, fakeResolver{})
	if len(widths) != 0 {
		t.Errorf("len(widths) = %d, want 0 (absurd range should be rejected, not truncated)", len(widths))
	}
}

// TestCidGlyphLookup_ExplicitStream exercises /CIDToGIDMap as an
// explicit stream of 2-byte big-endian glyph indices (as opposed to the
// /Identity default, which font_test.go's TestLoad_Type0IdentityH
// already covers).
func TestCidGlyphLookup_ExplicitStream(t *testing.T) {
	t.Parallel()
	// CID 0 -> GID 0 (i.e. "no glyph" - font_test's convention), CID 1
	// -> GID 42, CID 2 -> GID 7.
	data := make([]byte, 6)
	binary.BigEndian.PutUint16(data[0:2], 0)
	binary.BigEndian.PutUint16(data[2:4], 42)
	binary.BigEndian.PutUint16(data[4:6], 7)

	resolver := fakeResolver{objects: map[int]syntax.Object{
		9: syntax.Stream{Raw: data},
	}}
	descendant := syntax.Dictionary{"CIDToGIDMap": syntax.Reference{Number: 9}}
	lookup := cidGlyphLookup(descendant, resolver)

	if gid, ok := lookup(1); !ok || gid != 42 {
		t.Errorf("lookup(1) = (%d,%v), want (42,true)", gid, ok)
	}
	if gid, ok := lookup(2); !ok || gid != 7 {
		t.Errorf("lookup(2) = (%d,%v), want (7,true)", gid, ok)
	}
	if _, ok := lookup(0); ok {
		t.Errorf("lookup(0) found a glyph, want not-found (GID 0 means \"no glyph\")")
	}
	if _, ok := lookup(100); ok {
		t.Errorf("lookup(100) found a glyph, want not-found (out of range)")
	}
}

func TestCidGlyphLookup_UnrecognizedNameFallsBackToIdentity(t *testing.T) {
	t.Parallel()
	descendant := syntax.Dictionary{"CIDToGIDMap": syntax.Name("SomeUnknownName")}
	lookup := cidGlyphLookup(descendant, fakeResolver{})
	if gid, ok := lookup(5); !ok || gid != 5 {
		t.Errorf("lookup(5) = (%d,%v), want (5,true) (Identity fallback)", gid, ok)
	}
}
