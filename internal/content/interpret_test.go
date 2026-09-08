package content

import (
	"errors"
	"testing"

	"github.com/tucats/pdf-viewer/internal/graphics"
	"github.com/tucats/pdf-viewer/internal/pdferror"
)

// mustInterpret parses and interprets src under the identity initial
// CTM, failing the test on any error.
func mustInterpret(t *testing.T, src string) graphics.DisplayList {
	t.Helper()
	ops, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	// nil resources and resolver: none of the tests driven through this
	// helper exercise "Do" or "BI" (see image_test.go for those), so
	// there is nothing for Interpret to resolve.
	list, err := Interpret(ops, graphics.Identity(), nil, nil)
	if err != nil {
		t.Fatalf("Interpret(%q): %v", src, err)
	}
	return list
}

func TestInterpretFillRectangle(t *testing.T) {
	list := mustInterpret(t, "1 0 0 rg\n10 10 80 80 re\nf\n")
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	op := list[0]
	if op.Color != (graphics.Color{R: 1}) {
		t.Errorf("Color = %v, want {1,0,0}", op.Color)
	}
	if op.Rule != graphics.NonZero {
		t.Errorf("Rule = %v, want NonZero", op.Rule)
	}
	minX, minY, maxX, maxY, ok := op.Path.Bounds()
	if !ok || minX != 10 || minY != 10 || maxX != 90 || maxY != 90 {
		t.Errorf("bounds = (%v,%v,%v,%v ok=%v), want (10,10,90,90 true)", minX, minY, maxX, maxY, ok)
	}
}

func TestInterpretEvenOddFill(t *testing.T) {
	list := mustInterpret(t, "0 0 10 10 re f*\n")
	if len(list) != 1 || list[0].Rule != graphics.EvenOdd {
		t.Fatalf("list = %#v, want one EvenOdd fill", list)
	}
}

func TestInterpretStrokeProducesOutline(t *testing.T) {
	list := mustInterpret(t, "0 0 1 RG\n4 w\n0 0 m\n10 0 l\nS\n")
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].Color != (graphics.Color{B: 1}) {
		t.Errorf("Color = %v, want {0,0,1}", list[0].Color)
	}
	minX, minY, maxX, maxY, ok := list[0].Path.Bounds()
	if !ok {
		t.Fatal("stroke outline is empty")
	}
	// Line width 4 => half-width 2, so the outline should extend 2
	// units above and below the y=0 line.
	if minY != -2 || maxY != 2 {
		t.Errorf("outline y range = [%v,%v], want [-2,2]", minY, maxY)
	}
	if minX != 0 || maxX != 10 {
		t.Errorf("outline x range = [%v,%v], want [0,10]", minX, maxX)
	}
}

func TestInterpretFillThenStrokeProducesTwoOps(t *testing.T) {
	list := mustInterpret(t, "0 0 10 10 re B\n")
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2 (fill + stroke)", len(list))
	}
}

// TestInterpretCmConcatenatesOntoCTM confirms "cm" affects subsequent
// path construction correctly, including that a later "cm" concatenates
// with (rather than replaces) the existing CTM in the order the PDF
// specification requires: CTM_new = operand-matrix × CTM_old, meaning a
// point in the newest (innermost) user space is transformed by the
// latest "cm"'s operand matrix *first*, then by whatever CTM already
// existed (see graphics.Matrix.Mul's doc comment).
func TestInterpretCmConcatenatesOntoCTM(t *testing.T) {
	list := mustInterpret(t, "1 0 0 1 10 10 cm\n2 0 0 2 0 0 cm\n0 0 5 5 re f\n")
	minX, minY, maxX, maxY, ok := list[0].Path.Bounds()
	if !ok {
		t.Fatal("empty path")
	}
	// Point (0,0): scaled 2x by the second cm -> (0,0), then translated
	// by the first cm's (10,10) -> (10,10).
	// Point (5,5): scaled 2x -> (10,10), then translated -> (20,20).
	if minX != 10 || minY != 10 || maxX != 20 || maxY != 20 {
		t.Errorf("bounds = (%v,%v,%v,%v), want (10,10,20,20)", minX, minY, maxX, maxY)
	}
}

// TestInterpretQQRestoresState confirms a "q ... Q" sequence leaves the
// CTM (and therefore subsequent path coordinates) exactly as it was
// before the "q".
func TestInterpretQQRestoresState(t *testing.T) {
	list := mustInterpret(t, "q\n1 0 0 1 100 100 cm\nQ\n0 0 10 10 re f\n")
	minX, minY, _, _, ok := list[0].Path.Bounds()
	if !ok {
		t.Fatal("empty path")
	}
	if minX != 0 || minY != 0 {
		t.Errorf("bounds min = (%v,%v), want (0,0) - the cm inside q/Q leaked out", minX, minY)
	}
}

func TestInterpretClipAffectsSubsequentPaint(t *testing.T) {
	list := mustInterpret(t, "20 20 40 40 re\nW\nn\n0 0 100 100 re\nf\n")
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if len(list[0].Clips) != 1 {
		t.Fatalf("len(Clips) = %d, want 1", len(list[0].Clips))
	}
	minX, minY, maxX, maxY, ok := list[0].Clips[0].Path.Bounds()
	if !ok || minX != 20 || minY != 20 || maxX != 60 || maxY != 60 {
		t.Errorf("clip bounds = (%v,%v,%v,%v ok=%v), want (20,20,60,60 true)", minX, minY, maxX, maxY, ok)
	}
}

// TestInterpretClipDoesNotApplyToPathThatSetIt confirms the clip
// established by "W" only takes effect for painting *after* the "n"/"f"
// that ends the very path W applied to (per spec) - checked indirectly
// here by confirming a clip set via one path does not appear on a
// DrawOp painted earlier.
func TestInterpretClipAppliesOnlyAfterward(t *testing.T) {
	list := mustInterpret(t, "0 0 10 10 re f\n20 20 40 40 re W n\n0 0 100 100 re f\n")
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	if len(list[0].Clips) != 0 {
		t.Errorf("first fill has %d clips, want 0 (clip set afterward must not apply retroactively)", len(list[0].Clips))
	}
	if len(list[1].Clips) != 1 {
		t.Errorf("second fill has %d clips, want 1", len(list[1].Clips))
	}
}

func TestInterpretGrayColor(t *testing.T) {
	list := mustInterpret(t, "0.5 g\n0 0 1 1 re f\n")
	want := graphics.Color{R: 0.5, G: 0.5, B: 0.5}
	if list[0].Color != want {
		t.Errorf("Color = %v, want %v", list[0].Color, want)
	}
}

func TestInterpretCMYKColor(t *testing.T) {
	list := mustInterpret(t, "0 0 0 1 k\n0 0 1 1 re f\n") // pure black via CMYK
	want := graphics.Color{}
	if list[0].Color != want {
		t.Errorf("Color = %v, want %v", list[0].Color, want)
	}
}

func TestInterpretScnFallbackByComponentCount(t *testing.T) {
	cases := []struct {
		src  string
		want graphics.Color
	}{
		{"0.25 scn\n0 0 1 1 re f\n", graphics.Color{R: 0.25, G: 0.25, B: 0.25}},
		{"1 0 0 scn\n0 0 1 1 re f\n", graphics.Color{R: 1}},
		{"0 0 0 1 scn\n0 0 1 1 re f\n", graphics.Color{}},
	}
	for _, c := range cases {
		list := mustInterpret(t, c.src)
		if list[0].Color != c.want {
			t.Errorf("Interpret(%q) color = %v, want %v", c.src, list[0].Color, c.want)
		}
	}
}

func TestInterpretScnPatternNameIsUnsupported(t *testing.T) {
	ops, err := Parse([]byte("/P1 scn\n0 0 1 1 re f\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = Interpret(ops, graphics.Identity(), nil, nil)
	if !errors.Is(err, pdferror.ErrUnsupported) {
		t.Fatalf("Interpret with pattern scn: error = %v, want ErrUnsupported", err)
	}
}

// TestInterpretUnsupportedOperatorIsSkipped confirms an unrecognized
// operator (here, a text-showing operator) does not abort the whole
// render - content after it still produces a DrawOp.
func TestInterpretUnsupportedOperatorIsSkipped(t *testing.T) {
	list := mustInterpret(t, "(Hello) Tj\n0 0 10 10 re f\n")
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1 (the fill after the skipped Tj)", len(list))
	}
}

func TestInterpretWrongOperandCountIsMalformed(t *testing.T) {
	ops, err := Parse([]byte("1 2 3 cm\n")) // cm needs 6 operands
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Interpret(ops, graphics.Identity(), nil, nil); !errors.Is(err, pdferror.ErrMalformed) {
		t.Fatalf("Interpret: error = %v, want ErrMalformed", err)
	}
}

// TestInterpretQUnderflowIsTolerated confirms an excess "Q" does not
// abort rendering.
func TestInterpretQUnderflowIsTolerated(t *testing.T) {
	list := mustInterpret(t, "Q\n0 0 1 1 re f\n")
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
}

// TestInterpretCurveVShorthandUsesCurrentPointAsFirstControl confirms
// "v" (whose first control point is implicitly the current point)
// produces the same geometry as an equivalent explicit "c".
func TestInterpretCurveVShorthandUsesCurrentPointAsFirstControl(t *testing.T) {
	explicit := mustInterpret(t, "0 0 m\n0 0 10 10 10 0 c\nh f\n")
	shorthand := mustInterpret(t, "0 0 m\n10 10 10 0 v\nh f\n")

	e := explicit[0].Path.Subpaths[0].Points
	s := shorthand[0].Path.Subpaths[0].Points
	if len(e) != len(s) {
		t.Fatalf("point counts differ: %d vs %d", len(e), len(s))
	}
	for i := range e {
		if e[i] != s[i] {
			t.Errorf("point %d: explicit %v != shorthand %v", i, e[i], s[i])
		}
	}
}
