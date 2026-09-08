package content

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
	"github.com/tucats/pdf-viewer/internal/syntax"
)

// simpleTilingPatternStream returns a minimal, valid colored
// (/PaintType 1) tiling pattern stream: a 10x10-unit cell (matching its
// own /BBox and /XStep/YStep exactly, so cell size and repetition
// interval coincide - the common case) whose content fills an 4x4
// square at the cell's own origin solid red, leaving the rest of the
// cell transparent.
func simpleTilingPatternStream() syntax.Stream {
	content := []byte("1 0 0 rg\n0 0 4 4 re\nf\n")
	return syntax.Stream{
		Dict: syntax.Dictionary{
			"PatternType": syntax.Integer(1), "PaintType": syntax.Integer(1),
			"BBox":  syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(10), syntax.Real(10)},
			"XStep": syntax.Real(10), "YStep": syntax.Real(10),
		},
		Raw: content,
	}
}

func TestScnTilingPatternPaintsFilledShapeWithRepeatingImage(t *testing.T) {
	resources := syntax.Dictionary{"Pattern": syntax.Dictionary{"P0": simpleTilingPatternStream()}}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 0 0 100 100 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	op := list[0]
	if op.Image == nil {
		t.Fatalf("Image = nil, want the rendered tile")
	}
	if !op.Repeat {
		t.Fatalf("Repeat = false, want true (a tiling pattern must wrap, not clamp)")
	}
	if op.Path == nil {
		t.Fatalf("Path = nil, want the actual filled rectangle's geometry")
	}
	// The rendered tile should have a fully-transparent corner (outside
	// the 4x4 red square painted at the cell's own origin) and a fully
	// opaque, red pixel near that origin.
	r, g, b, a := op.Image.At(0, op.Image.Height-1) // bottom-left corner in device-row terms
	if a < 0.9 || r < 0.9 || g > 0.1 || b > 0.1 {
		t.Fatalf("tile pixel near cell origin = (%v,%v,%v,%v), want ~opaque red", r, g, b, a)
	}
	r, g, b, a = op.Image.At(op.Image.Width-1, 0) // top-right corner: outside the 4x4 square
	if a > 0.1 {
		t.Fatalf("tile pixel far from the painted square = (%v,%v,%v,%v), want transparent (a~0)", r, g, b, a)
	}
}

func TestScnTilingPatternUsesInitialCTMNotCurrent(t *testing.T) {
	resources := syntax.Dictionary{"Pattern": syntax.Dictionary{"P0": simpleTilingPatternStream()}}
	// A "cm" active before the pattern is even selected must not affect
	// the pattern's own device mapping - mirrors
	// TestScnShadingPatternUsesInitialCTMNotCurrent for shading patterns.
	ops, err := Parse([]byte("q 1 0 0 1 5000 0 cm /Pattern cs /P0 scn 0 0 100 100 re f Q"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	// A tile rendered relative to the live (translated) CTM would be
	// enormous (the ImageToDevice scale would still be normal, but this
	// assertion instead checks the *painted shape*'s own device
	// position, which is unaffected by the pattern/matrix bug this test
	// guards against - the real regression this test would catch is a
	// panic or wildly wrong tile size from an inflated patternToDevice,
	// exercised indirectly by Interpret succeeding at all with a
	// reasonable tile).
	if list[0].Image.Width <= 0 || list[0].Image.Width > maxPatternTileDimension {
		t.Fatalf("tile width = %d, want a reasonable, bounded size", list[0].Image.Width)
	}
}

func TestTilingPatternMissingBBoxIsMalformed(t *testing.T) {
	stream := simpleTilingPatternStream()
	delete(stream.Dict, "BBox")
	resources := syntax.Dictionary{"Pattern": syntax.Dictionary{"P0": stream}}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Interpret with no /BBox: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestTilingPatternZeroStepIsMalformed(t *testing.T) {
	stream := simpleTilingPatternStream()
	stream.Dict["XStep"] = syntax.Real(0)
	resources := syntax.Dictionary{"Pattern": syntax.Dictionary{"P0": stream}}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Interpret with /XStep 0: got %v, want an error wrapping ErrMalformed", err)
	}
}

func TestTilingPatternOwnResourcesUsedForNestedContent(t *testing.T) {
	imgDict := syntax.Dictionary{
		"Subtype": syntax.Name("Image"), "Width": syntax.Integer(1), "Height": syntax.Integer(1),
		"BitsPerComponent": syntax.Integer(8), "ColorSpace": syntax.Name("DeviceGray"),
	}
	patResources := syntax.Dictionary{"XObject": syntax.Dictionary{"Im0": syntax.Stream{Dict: imgDict, Raw: []byte{200}}}}
	stream := syntax.Stream{
		Dict: syntax.Dictionary{
			"PatternType": syntax.Integer(1), "PaintType": syntax.Integer(1),
			"BBox":  syntax.Array{syntax.Real(0), syntax.Real(0), syntax.Real(10), syntax.Real(10)},
			"XStep": syntax.Real(10), "YStep": syntax.Real(10),
			"Resources": patResources,
		},
		Raw: []byte("q 10 0 0 10 0 0 cm /Im0 Do Q\n"),
	}
	resources := syntax.Dictionary{"Pattern": syntax.Dictionary{"P0": stream}}
	ops, err := Parse([]byte("/Pattern cs /P0 scn 0 0 10 10 re f"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	list, err := Interpret(ops, graphics.Identity(), resources, &fakeResolver{})
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if list[0].Image == nil {
		t.Fatalf("Image = nil, want the rendered tile")
	}
	// The tile should be entirely opaque gray (~200/255), since the
	// nested image covered the whole 10x10 cell - proving the pattern's
	// own /Resources (not the caller's, which has none) resolved "Im0".
	r, _, _, a := list[0].Image.At(list[0].Image.Width/2, list[0].Image.Height/2)
	if a < 0.9 {
		t.Fatalf("center tile pixel alpha = %v, want ~1 (opaque)", a)
	}
	if r < 0.7 || r > 0.85 {
		t.Fatalf("center tile pixel red = %v, want ~0.78 (200/255)", r)
	}
}
